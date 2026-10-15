package githubcopilot

import (
	"strings"

	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

// defaultCopilotAPIBaseURL is the public Copilot inference host. Paid plans are served from
// tier-specific subdomains (api.individual / api.business / api.enterprise), which arrive in
// the token exchange response rather than being knowable at config time.
const defaultCopilotAPIBaseURL = "https://api.githubcopilot.com"

// copilotCredentials is what one inference call needs: the bearer token and the host to send
// it to. The host is a property of the credential, not of the configuration.
type copilotCredentials struct {
	Token   string
	BaseURL string // validated, without a trailing slash
}

// resolveCredentials produces the credentials for one request from Key.Value:
//
//   - A GitHub user token (fine-grained PAT, ghu_ or gho_) is exchanged for a Copilot token,
//     refreshed before expiry. "Sign in with GitHub" keys arrive this way too: the HTTP gateway
//     puts the stored user token in Key.Value, so an empty value means there is no sign-in.
//   - Anything else is a pre-minted Copilot API token, used verbatim. base_url is required
//     with it because the token does not carry its host, and guessing the public host would
//     turn a Business token's 401 into what reads like a bad credential.
//
// See https://docs.github.com/en/copilot/how-tos/copilot-sdk/auth/authenticate
func resolveCredentials(
	ctx *schemas.BifrostContext,
	key schemas.Key,
	client *fasthttp.Client,
	configuredBaseURL string,
	logger schemas.Logger,
) (*copilotCredentials, *schemas.BifrostError) {
	token := strings.TrimSpace(key.Value.GetValue())
	switch {
	case token == "" && key.GithubCopilotKeyConfig.UsesSignIn():
		return nil, configurationError(
			"github copilot: this key uses Sign in with GitHub but has no active sign-in. " +
				"Open the key in the Bifrost UI and sign in with GitHub again.")
	case token == "":
		return nil, configurationError(
			"github copilot: no credentials on this key. Paste a GitHub token (a fine-grained personal " +
				"access token with the Copilot Requests permission) or sign in with GitHub.")
	case isGithubUserToken(token):
		return mintFromUserToken(ctx, token, client, configuredBaseURL, logger)
	}

	baseURL := strings.TrimRight(strings.TrimSpace(configuredBaseURL), "/")
	if baseURL == "" {
		return nil, configurationError(
			"github copilot: a Copilot API token needs network_config.base_url set to the host it " +
				"was issued for, because the token does not carry one. Paid plans use " +
				"api.individual, api.business or api.enterprise.githubcopilot.com.")
	}
	return &copilotCredentials{Token: token, BaseURL: baseURL}, nil
}
