package authprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/p0vidl0/lateness/internal/domain"
)

// OIDC is an OpenID Connect provider: authorization code flow with PKCE, the
// id_token is checked against the provider's JWKS, issuer and client id.
// Telegram is one configured instance; others differ only in endpoints and claims.
type OIDC struct {
	ProviderID   string
	ProviderName string
	ClientID     string
	ClientSecret string
	AuthorizeURL string
	TokenURL     string
	Issuer       string
	Scope        string
	// SubjectClaim names the claim with the stable user id; empty uses "sub".
	SubjectClaim string
	Client       *http.Client
	verifier     *oidc.IDTokenVerifier
}

// NewOIDC builds a provider and its id_token verifier backed by the JWKS at jwksURL.
func NewOIDC(p OIDC, jwksURL string) *OIDC {
	if p.Client == nil {
		p.Client = &http.Client{Timeout: 10 * time.Second}
	}
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), p.Client), jwksURL)
	p.verifier = oidc.NewVerifier(p.Issuer, keys, &oidc.Config{ClientID: p.ClientID})
	return &p
}

// NewTelegram is Telegram Login over OIDC (oauth.telegram.org). The client id and
// secret come from @BotFather; the user id is in the "id" claim.
func NewTelegram(clientID, clientSecret string) *OIDC {
	const root = "https://oauth.telegram.org"
	return NewOIDC(OIDC{
		ProviderID: "telegram", ProviderName: "Telegram",
		ClientID: clientID, ClientSecret: clientSecret,
		AuthorizeURL: root + "/auth", TokenURL: root + "/token", Issuer: root,
		Scope: "openid profile", SubjectClaim: "id",
	}, root+"/.well-known/jwks.json")
}

func (p *OIDC) ID() string    { return p.ProviderID }
func (p *OIDC) Label() string { return p.ProviderName }

func (p *OIDC) AuthURL(state, challenge, redirectURI string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {p.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {p.Scope},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return p.AuthorizeURL + "?" + q.Encode()
}

func (p *OIDC) Exchange(ctx context.Context, code, verifier, redirectURI string, _ url.Values) (domain.Identity, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {p.ClientID},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return domain.Identity{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(p.ClientID), url.QueryEscape(p.ClientSecret))
	resp, err := p.Client.Do(req)
	if err != nil {
		return domain.Identity{}, fmt.Errorf("%s token: %w", p.ProviderID, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return domain.Identity{}, fmt.Errorf("%s token: status %d", p.ProviderID, resp.StatusCode)
	}
	var tokens struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tokens); err != nil || tokens.IDToken == "" {
		return domain.Identity{}, fmt.Errorf("%s token: no id_token", p.ProviderID)
	}
	token, err := p.verifier.Verify(oidc.ClientContext(ctx, p.Client), tokens.IDToken)
	if err != nil {
		return domain.Identity{}, fmt.Errorf("%s id_token: %w", p.ProviderID, err)
	}
	var claims map[string]any
	if err := token.Claims(&claims); err != nil {
		return domain.Identity{}, err
	}
	return p.identity(token.Subject, claims)
}

func (p *OIDC) identity(sub string, claims map[string]any) (domain.Identity, error) {
	subject := sub
	if p.SubjectClaim != "" {
		subject = claimString(claims[p.SubjectClaim])
	}
	if subject == "" {
		return domain.Identity{}, fmt.Errorf("%s id_token: no %q claim", p.ProviderID, p.SubjectClaim)
	}
	id := domain.Identity{Provider: p.ProviderID, Subject: subject, Name: claimString(claims["name"])}
	if id.Name == "" {
		id.Name = strings.TrimSpace(claimString(claims["given_name"]) + " " + claimString(claims["family_name"]))
	}
	if id.Name == "" {
		if u := claimString(claims["preferred_username"]); u != "" {
			id.Name = "@" + u
		}
	}
	// An address the provider has not verified must not link accounts.
	if verified, _ := claims["email_verified"].(bool); verified {
		id.Email = claimString(claims["email"])
	}
	return id, nil
}

// claimString reads a string or a number claim. JSON numbers decode as float64,
// which is exact for ids below 2^53 (Telegram user ids are).
func claimString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return fmt.Sprintf("%.0f", x)
	case json.Number:
		return x.String()
	default:
		return ""
	}
}
