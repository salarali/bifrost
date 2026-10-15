package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/v2/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// GithubCopilotSignInHandler serves "Sign in with GitHub" for GitHub Copilot keys. The
// browser cannot run GitHub's device flow itself (GitHub sends no CORS headers), so the
// gateway calls GitHub and the UI only shows the code and polls.
type GithubCopilotSignInHandler struct {
	config *lib.Config
	models keyModelsRefresher // optional; re-checks the key's status after its sign-in changes
}

// keyModelsRefresher re-runs model discovery for one key, which also records its status.
type keyModelsRefresher interface {
	RefreshLiveModelsForKey(ctx context.Context, provider schemas.ModelProvider, keyID string) error
}

// NewGithubCopilotSignInHandler creates the sign-in handler.
func NewGithubCopilotSignInHandler(config *lib.Config, models keyModelsRefresher) *GithubCopilotSignInHandler {
	return &GithubCopilotSignInHandler{config: config, models: models}
}

// refreshKeyStatus re-checks a key once its sign-in changes. The key is saved before the
// operator signs in, so the check that ran on save always saw no credentials.
func (h *GithubCopilotSignInHandler) refreshKeyStatus(ctx context.Context, keyID string) {
	if h.models == nil {
		return
	}
	if err := h.models.RefreshLiveModelsForKey(ctx, schemas.GithubCopilot, keyID); err != nil && !errors.Is(err, ErrRefreshInProgress) {
		logger.Warn("github copilot: could not re-check key %s after sign-in change: %v", keyID, err)
	}
}

// RegisterRoutes registers the sign-in routes under the provider key they belong to.
func (h *GithubCopilotSignInHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/providers/{provider}/keys/{key_id}/sign-in", lib.ChainMiddlewares(h.status, middlewares...))
	r.POST("/api/providers/{provider}/keys/{key_id}/sign-in", lib.ChainMiddlewares(h.start, middlewares...))
	r.POST("/api/providers/{provider}/keys/{key_id}/sign-in/poll", lib.ChainMiddlewares(h.poll, middlewares...))
	r.DELETE("/api/providers/{provider}/keys/{key_id}/sign-in", lib.ChainMiddlewares(h.signOut, middlewares...))
}

// githubCopilotSignInRequest is the body of the start and poll calls. client_id may be the
// unsaved form value, an env reference, or empty or masked to use the key's saved client ID.
type githubCopilotSignInRequest struct {
	ClientID   string `json:"client_id"`
	DeviceCode string `json:"device_code"`
}

// target validates the route and returns the key ID. It writes the error response itself.
func (h *GithubCopilotSignInHandler) target(ctx *fasthttp.RequestCtx) (string, bool) {
	provider, _ := ctx.UserValue("provider").(string)
	if schemas.ModelProvider(provider) != schemas.GithubCopilot {
		SendError(ctx, fasthttp.StatusBadRequest, "sign-in is only available for github-copilot keys")
		return "", false
	}
	if h.config.GithubCopilotSignIn == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "sign in with GitHub needs a config store to keep the token")
		return "", false
	}
	keyID, err := getKeyIDFromCtx(ctx)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return "", false
	}
	return keyID, true
}

// clientID picks the client ID for a sign-in: the supplied value when it is a real one,
// otherwise the one saved on the key.
func (h *GithubCopilotSignInHandler) clientID(keyID, supplied string) (string, error) {
	if sv := schemas.NewSecretVar(strings.TrimSpace(supplied)); sv.IsSet() && !sv.IsMaskedPlaceholder() {
		if value := strings.TrimSpace(sv.GetValue()); value != "" {
			return value, nil
		}
		return "", fmt.Errorf("client_id refers to a secret that is not set")
	}
	provider, err := h.config.GetProviderConfigRaw(schemas.GithubCopilot)
	if err == nil {
		for i := range provider.Keys {
			if provider.Keys[i].ID == keyID && provider.Keys[i].GithubCopilotKeyConfig.UsesSignIn() {
				return strings.TrimSpace(provider.Keys[i].GithubCopilotKeyConfig.OAuthClientID.GetValue()), nil
			}
		}
	}
	return "", fmt.Errorf("client_id is required")
}

// parse decodes the request body and resolves the client ID.
func (h *GithubCopilotSignInHandler) parse(ctx *fasthttp.RequestCtx, keyID string) (githubCopilotSignInRequest, bool) {
	var req githubCopilotSignInRequest
	if body := ctx.PostBody(); len(body) > 0 {
		if err := sonic.Unmarshal(body, &req); err != nil {
			SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
			return req, false
		}
	}
	clientID, err := h.clientID(keyID, req.ClientID)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return req, false
	}
	req.ClientID = clientID
	return req, true
}

// start asks GitHub for a device code.
//
// POST /api/providers/github-copilot/keys/{key_id}/sign-in
func (h *GithubCopilotSignInHandler) start(ctx *fasthttp.RequestCtx) {
	keyID, ok := h.target(ctx)
	if !ok {
		return
	}
	req, ok := h.parse(ctx, keyID)
	if !ok {
		return
	}
	code, err := h.config.GithubCopilotSignIn.StartSignIn(ctx, req.ClientID)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	SendJSON(ctx, code)
}

// poll checks once whether the operator finished on github.com, and stores the token if so.
//
// POST /api/providers/github-copilot/keys/{key_id}/sign-in/poll
func (h *GithubCopilotSignInHandler) poll(ctx *fasthttp.RequestCtx) {
	keyID, ok := h.target(ctx)
	if !ok {
		return
	}
	req, ok := h.parse(ctx, keyID)
	if !ok {
		return
	}
	result, err := h.config.GithubCopilotSignIn.PollSignIn(ctx, keyID, req.ClientID, req.DeviceCode)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	if result.Status == "complete" {
		h.refreshKeyStatus(ctx, keyID)
	}
	SendJSON(ctx, result)
}

// status reports whether the key is signed in, and as whom.
//
// GET /api/providers/github-copilot/keys/{key_id}/sign-in
func (h *GithubCopilotSignInHandler) status(ctx *fasthttp.RequestCtx) {
	keyID, ok := h.target(ctx)
	if !ok {
		return
	}
	status, err := h.config.GithubCopilotSignIn.Status(ctx, keyID)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	SendJSON(ctx, status)
}

// signOut forgets the key's token.
//
// DELETE /api/providers/github-copilot/keys/{key_id}/sign-in
func (h *GithubCopilotSignInHandler) signOut(ctx *fasthttp.RequestCtx) {
	keyID, ok := h.target(ctx)
	if !ok {
		return
	}
	if err := h.config.GithubCopilotSignIn.SignOut(ctx, keyID); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	h.refreshKeyStatus(ctx, keyID)
	SendJSON(ctx, map[string]bool{"signed_in": false})
}
