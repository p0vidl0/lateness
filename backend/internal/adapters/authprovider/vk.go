// Package authprovider implements external sign-in providers (app.AuthProvider).
package authprovider

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/p0vidl0/lateness/internal/domain"
)

// VK is VK ID: OAuth 2.1 authorization code flow with PKCE.
type VK struct {
	ClientID     string
	ClientSecret string
	// Endpoints default to id.vk.com; tests point them at a fake.
	AuthorizeURL string
	TokenURL     string
	UserInfoURL  string
	Client       *http.Client
}

func NewVK(clientID, clientSecret string) *VK {
	return &VK{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		AuthorizeURL: "https://id.vk.com/authorize",
		TokenURL:     "https://id.vk.com/oauth2/auth",
		UserInfoURL:  "https://id.vk.com/oauth2/user_info",
		Client:       &http.Client{Timeout: 10 * time.Second},
	}
}

func (v *VK) ID() string    { return "vk" }
func (v *VK) Label() string { return "VK ID" }

func (v *VK) AuthURL(state, challenge, redirectURI string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {v.ClientID},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"scope":                 {"email"},
	}
	return v.AuthorizeURL + "?" + q.Encode()
}

func (v *VK) Exchange(ctx context.Context, code, verifier, redirectURI string, extra url.Values) (domain.Identity, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
		"client_id":     {v.ClientID},
		"client_secret": {v.ClientSecret},
	}
	if deviceID := extra.Get("device_id"); deviceID != "" {
		form.Set("device_id", deviceID)
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := v.postForm(ctx, v.TokenURL, form, &token); err != nil {
		return domain.Identity{}, fmt.Errorf("vk token: %w", err)
	}
	if token.AccessToken == "" {
		return domain.Identity{}, fmt.Errorf("vk token: no access_token")
	}
	var info struct {
		User struct {
			UserID    json.Number `json:"user_id"`
			FirstName string      `json:"first_name"`
			LastName  string      `json:"last_name"`
			Email     string      `json:"email"`
		} `json:"user"`
	}
	if err := v.postForm(ctx, v.UserInfoURL, url.Values{"access_token": {token.AccessToken}, "client_id": {v.ClientID}}, &info); err != nil {
		return domain.Identity{}, fmt.Errorf("vk user info: %w", err)
	}
	subject := info.User.UserID.String()
	if subject == "" {
		return domain.Identity{}, fmt.Errorf("vk user info: no user_id")
	}
	return domain.Identity{
		Provider: v.ID(),
		Subject:  subject,
		Email:    strings.TrimSpace(info.User.Email),
		Name:     strings.TrimSpace(info.User.FirstName + " " + info.User.LastName),
	}, nil
}

func (v *VK) postForm(ctx context.Context, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := v.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	var apiErr struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Error != "" {
		return fmt.Errorf("%s", apiErr.Error)
	}
	return json.Unmarshal(body, out)
}

// Challenge is the PKCE S256 code challenge for a verifier.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
