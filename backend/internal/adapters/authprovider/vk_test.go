package authprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func fakeVK(t *testing.T, userInfo string) (*VK, *url.Values) {
	t.Helper()
	var tokenForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/token":
			tokenForm = r.PostForm
			_, _ = w.Write([]byte(`{"access_token":"at-1","user_id":42}`))
		case "/info":
			if r.PostForm.Get("access_token") != "at-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(userInfo))
		}
	}))
	t.Cleanup(srv.Close)
	vk := NewVK("app-1", "secret-1")
	vk.TokenURL, vk.UserInfoURL, vk.Client = srv.URL+"/token", srv.URL+"/info", srv.Client()
	return vk, &tokenForm
}

func TestVKExchange(t *testing.T) {
	vk, form := fakeVK(t, `{"user":{"user_id":42,"first_name":"Ann","last_name":"Lee","email":"ann@example.com"}}`)
	id, err := vk.Exchange(context.Background(), "code-1", "verifier-1", "http://app/auth/vk/callback", url.Values{"device_id": {"dev-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if id.Provider != "vk" || id.Subject != "42" || id.Email != "ann@example.com" || id.Name != "Ann Lee" {
		t.Fatalf("%+v", id)
	}
	f := *form
	if f.Get("code_verifier") != "verifier-1" || f.Get("device_id") != "dev-1" || f.Get("client_secret") != "secret-1" || f.Get("grant_type") != "authorization_code" {
		t.Fatalf("token form %v", f)
	}
}

func TestVKExchangeWithoutUser(t *testing.T) {
	vk, _ := fakeVK(t, `{"error":"invalid_token"}`)
	if _, err := vk.Exchange(context.Background(), "c", "v", "r", nil); err == nil || !strings.Contains(err.Error(), "invalid_token") {
		t.Fatalf("got %v", err)
	}
}

func TestVKAuthURL(t *testing.T) {
	raw := NewVK("app-1", "s").AuthURL("st", Challenge("verifier"), "http://app/cb")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "id.vk.com" || q.Get("code_challenge_method") != "S256" || q.Get("state") != "st" ||
		q.Get("code_challenge") != Challenge("verifier") || q.Get("redirect_uri") != "http://app/cb" {
		t.Fatalf("%s", raw)
	}
}
