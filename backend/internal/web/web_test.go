package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/p0vidl0/lateness/internal/app"
	"github.com/p0vidl0/lateness/internal/domain"
)

type fakeProvider struct{}

func (fakeProvider) ID() string    { return "vk" }
func (fakeProvider) Label() string { return "VK ID" }
func (fakeProvider) AuthURL(state, challenge, redirectURI string) string {
	return "https://id.example/authorize?" + url.Values{"state": {state}, "code_challenge": {challenge}, "redirect_uri": {redirectURI}}.Encode()
}
func (fakeProvider) Exchange(context.Context, string, string, string, url.Values) (domain.Identity, error) {
	return domain.Identity{}, nil
}

func routes(t *testing.T) http.Handler {
	t.Helper()
	h, err := New(nil, "http://localhost:18080", time.Hour, fakeProvider{})
	if err != nil {
		t.Fatal(err)
	}
	return h.Routes(nil)
}

func serve(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestLoginPageOffersProviders(t *testing.T) {
	rec := serve(routes(t), httptest.NewRequest(http.MethodGet, "/login", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `action="/login"`) || !strings.Contains(body, `action="/auth/vk"`) {
		t.Fatalf("%d %s", rec.Code, body)
	}
	if strings.Contains(body, "/register") {
		t.Fatal("public registration link must not exist")
	}
}

func TestPagesRequireSession(t *testing.T) {
	for _, path := range []string{"/", "/admin/invites", "/projects/new", "/p/00000000-0000-0000-0000-000000000000/"} {
		rec := serve(routes(t), httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestCrossOriginPostRefused(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=a&password=b"))
	req.Header.Set("Origin", "https://evil.example")
	if rec := serve(routes(t), req); rec.Code != http.StatusForbidden {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestAuthStartSetsStateCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/auth/vk", strings.NewReader("invite=inv-1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := serve(routes(t), req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]
	if cookie.Name != oauthCookie || !cookie.HttpOnly || cookie.Path != "/auth/" {
		t.Fatalf("cookie %+v", cookie)
	}
	cb := httptest.NewRequest(http.MethodGet, "/", nil)
	cb.AddCookie(cookie)
	st, ok := readOAuthState(cb)
	if !ok || st.State != loc.Query().Get("state") || st.Invite != "inv-1" || loc.Query().Get("redirect_uri") != "http://localhost:18080/auth/vk/callback" {
		t.Fatalf("state %+v location %s", st, loc)
	}
}

func TestAuthCallbackRejectsForeignState(t *testing.T) {
	start := httptest.NewRequest(http.MethodPost, "/auth/vk", strings.NewReader("invite=inv-1"))
	start.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	cookie := serve(routes(t), start).Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodGet, "/auth/vk/callback?code=c&state=forged", nil)
	req.AddCookie(cookie)
	rec := serve(routes(t), req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/invite/inv-1?error=provider" {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestAssetsAndHealth(t *testing.T) {
	for _, path := range []string{"/assets/app.css", "/assets/app.js", "/healthz", "/readyz"} {
		rec := serve(routes(t), httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Fatalf("%s: %d", path, rec.Code)
		}
	}
}

func TestLinkNeedsSession(t *testing.T) {
	start := httptest.NewRequest(http.MethodPost, "/auth/vk", strings.NewReader("link=1"))
	start.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := serve(routes(t), start)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLinkCallbackFailureReturnsToProfile(t *testing.T) {
	raw, _ := json.Marshal(oauthState{Provider: "vk", State: "s", Verifier: "v", Link: true})
	req := httptest.NewRequest(http.MethodGet, "/auth/vk/callback?code=c&state=forged", nil)
	req.AddCookie(&http.Cookie{Name: oauthCookie, Value: base64.RawURLEncoding.EncodeToString(raw)})
	rec := serve(routes(t), req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/profile?error=provider" {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestMethodsKeepUnconfiguredLinks(t *testing.T) {
	p := app.Profile{Identities: []domain.Identity{{Provider: "vk", Subject: "1"}, {Provider: "old", Subject: "2"}}}
	rows := methods(p, []app.AuthProvider{fakeProvider{}})
	if len(rows) != 2 || rows[0].ID != "vk" || rows[0].Linked == nil || !rows[0].CanLink || rows[1].ID != "old" || rows[1].CanLink || rows[0].LastOnly {
		t.Fatalf("%+v", rows)
	}
	last := methods(app.Profile{Identities: p.Identities[:1]}, []app.AuthProvider{fakeProvider{}})
	if !last[0].LastOnly {
		t.Fatal("the only method must not be unlinkable")
	}
}
