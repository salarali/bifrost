package handlers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/copilotsignin"
	"github.com/maximhq/bifrost/transports/v2/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

func signInConfig(clientID string) *lib.Config {
	return &lib.Config{
		Providers: map[schemas.ModelProvider]configstore.ProviderConfig{
			schemas.GithubCopilot: {Keys: []schemas.Key{{
				ID:                     "key-1",
				GithubCopilotKeyConfig: &schemas.GithubCopilotKeyConfig{OAuthClientID: *schemas.NewSecretVar(clientID)},
			}}},
		},
	}
}

func signInRequest(provider, keyID string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.SetUserValue("provider", provider)
	ctx.SetUserValue("key_id", keyID)
	return ctx
}

// TestGithubCopilotSignInRoutesCoexistWithKeyRoutes pins that the sign-in routes register
// beside the provider key routes; a conflicting pattern panics at startup.
func TestGithubCopilotSignInRoutesCoexistWithKeyRoutes(t *testing.T) {
	r := router.New()
	NewProviderHandler(nil, &lib.Config{}, nil).RegisterRoutes(r)
	NewGithubCopilotSignInHandler(&lib.Config{}, nil).RegisterRoutes(r)
}

func TestGithubCopilotSignInRejectsOtherProviders(t *testing.T) {
	cfg := signInConfig("Iv23liAbC")
	cfg.GithubCopilotSignIn = copilotsignin.New(nil, nil, func(string) bool { return true })
	h := NewGithubCopilotSignInHandler(cfg, nil)

	ctx := signInRequest("openai", "key-1")
	h.status(ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("status %d, want 400 for a non-Copilot provider: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}

func TestGithubCopilotSignInWithoutConfigStore(t *testing.T) {
	h := NewGithubCopilotSignInHandler(signInConfig("Iv23liAbC"), nil)

	ctx := signInRequest("github-copilot", "key-1")
	h.start(ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 when the token cannot be stored: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}

// TestGithubCopilotSignInClientID pins which client ID a sign-in uses. The form may hold an
// unsaved value, or the masked one a key read returns, which must map back to the saved ID.
func TestGithubCopilotSignInClientID(t *testing.T) {
	h := NewGithubCopilotSignInHandler(signInConfig("Iv23liStored"), nil)
	t.Setenv("COPILOT_SIGN_IN_CLIENT_ID", "Iv23liFromEnv")

	cases := []struct {
		name     string
		keyID    string
		supplied string
		want     string
		wantErr  string
	}{
		{name: "an unsaved form value wins", keyID: "key-1", supplied: "Iv23liTyped", want: "Iv23liTyped"},
		{name: "empty uses the saved client ID", keyID: "key-1", want: "Iv23liStored"},
		{name: "a masked value uses the saved client ID", keyID: "key-1", supplied: "Iv23" + strings.Repeat("*", 24) + "ored", want: "Iv23liStored"},
		{name: "an env reference resolves", keyID: "key-1", supplied: "env.COPILOT_SIGN_IN_CLIENT_ID", want: "Iv23liFromEnv"},
		{name: "an unset env reference is refused", keyID: "key-1", supplied: "env.COPILOT_SIGN_IN_UNSET", wantErr: "not set"},
		{name: "an unsaved key needs a client ID", keyID: "key-new", wantErr: "client_id is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := h.clientID(tc.keyID, tc.supplied)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got %q, %v; want an error containing %q", got, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

type recordingRefresher struct{ calls []string }

func (r *recordingRefresher) RefreshLiveModelsForKey(_ context.Context, provider schemas.ModelProvider, keyID string) error {
	r.calls = append(r.calls, string(provider)+"/"+keyID)
	return nil
}

// signOutStore is the part of the sign-in store that a sign-out touches.
type signOutStore struct{ copilotsignin.Store }

func (signOutStore) ReplaceGithubCopilotSignIn(context.Context, *tables.TableGithubCopilotSignIn) error {
	return nil
}

func (signOutStore) GetGithubCopilotSignIn(context.Context, string) (*tables.TableGithubCopilotSignIn, error) {
	return nil, nil
}

func (signOutStore) ClaimGithubCopilotSignInRefresh(context.Context, string, int64, time.Time) (bool, error) {
	return true, nil
}

// TestGithubCopilotSignOutRechecksKey pins that a sign-out re-runs the key's model discovery,
// so its status stops showing healthy once the token is gone.
func TestGithubCopilotSignOutRechecksKey(t *testing.T) {
	cfg := signInConfig("Iv23liAbC")
	cfg.GithubCopilotSignIn = copilotsignin.New(signOutStore{}, nil, func(string) bool { return true })
	refresher := &recordingRefresher{}
	h := NewGithubCopilotSignInHandler(cfg, refresher)

	ctx := signInRequest("github-copilot", "key-1")
	h.signOut(ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if len(refresher.calls) != 1 || refresher.calls[0] != "github-copilot/key-1" {
		t.Fatalf("re-checks %v, want one for github-copilot/key-1", refresher.calls)
	}
}
