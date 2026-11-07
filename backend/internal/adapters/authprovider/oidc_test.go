package authprovider

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// fakeOIDC serves /token and /jwks; claims go into the id_token it signs.
func fakeOIDC(t *testing.T, claims map[string]any) (*OIDC, *url.Values, *string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "k1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var form url.Values
	var basic string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jwks":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
		case "/token":
			_ = r.ParseForm()
			form = r.PostForm
			user, pass, _ := r.BasicAuth()
			basic = user + ":" + pass
			payload, _ := json.Marshal(claims)
			sig, err := signer.Sign(payload)
			if err != nil {
				t.Error(err)
				return
			}
			raw, _ := sig.CompactSerialize()
			_ = json.NewEncoder(w).Encode(map[string]string{"id_token": raw})
		}
	}))
	t.Cleanup(srv.Close)
	p := NewOIDC(OIDC{
		ProviderID: "telegram", ProviderName: "Telegram", ClientID: "8001", ClientSecret: "s3cret",
		AuthorizeURL: srv.URL + "/auth", TokenURL: srv.URL + "/token", Issuer: "https://oauth.telegram.org",
		Scope: "openid profile", SubjectClaim: "id", Client: srv.Client(),
	}, srv.URL+"/jwks")
	return p, &form, &basic
}

func claims(extra map[string]any) map[string]any {
	c := map[string]any{
		"iss": "https://oauth.telegram.org", "aud": "8001", "sub": "opaque-sub",
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		"id": 123456789, "name": "Ann Lee", "preferred_username": "ann",
	}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func TestOIDCExchange(t *testing.T) {
	p, form, basic := fakeOIDC(t, claims(nil))
	id, err := p.Exchange(context.Background(), "code-1", "verifier-1", "http://app/auth/telegram/callback", nil)
	if err != nil {
		t.Fatal(err)
	}
	if id.Provider != "telegram" || id.Subject != "123456789" || id.Name != "Ann Lee" || id.Email != "" {
		t.Fatalf("%+v", id)
	}
	if (*form).Get("code_verifier") != "verifier-1" || (*form).Get("redirect_uri") != "http://app/auth/telegram/callback" || *basic != "8001:s3cret" {
		t.Fatalf("token request %v %s", *form, *basic)
	}
}

func TestOIDCRejectsBadTokens(t *testing.T) {
	for name, extra := range map[string]map[string]any{
		"audience": {"aud": "someone-else"},
		"issuer":   {"iss": "https://evil.example"},
		"expired":  {"exp": time.Now().Add(-time.Hour).Unix()},
		"no id":    {"id": nil},
	} {
		p, _, _ := fakeOIDC(t, claims(extra))
		if _, err := p.Exchange(context.Background(), "c", "v", "r", nil); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestOIDCEmailOnlyWhenVerified(t *testing.T) {
	p, _, _ := fakeOIDC(t, claims(map[string]any{"email": "ann@example.com"}))
	id, err := p.Exchange(context.Background(), "c", "v", "r", nil)
	if err != nil || id.Email != "" {
		t.Fatalf("unverified email kept: %+v %v", id, err)
	}
	p, _, _ = fakeOIDC(t, claims(map[string]any{"email": "ann@example.com", "email_verified": true}))
	id, err = p.Exchange(context.Background(), "c", "v", "r", nil)
	if err != nil || id.Email != "ann@example.com" {
		t.Fatalf("%+v %v", id, err)
	}
}

func TestTelegramAuthURL(t *testing.T) {
	u, err := url.Parse(NewTelegram("8001", "s").AuthURL("st", "ch", "http://app/cb"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "oauth.telegram.org" || q.Get("scope") != "openid profile" || q.Get("code_challenge_method") != "S256" ||
		!strings.HasPrefix(u.Path, "/auth") || q.Get("client_id") != "8001" {
		t.Fatalf("%s", u)
	}
}
