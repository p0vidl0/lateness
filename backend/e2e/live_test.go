//go:build e2e

// Package e2e drives a live serve and sweep (scripts/e2e-host-services.sh) through
// the pages, the intake endpoint, the invite CLI, the receiver and the test SMTP server.
package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type session struct {
	t      *testing.T
	client *http.Client
	base   string // http://host:port
	proj   string // /p/<id>
}

func env(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("%s is required", key)
	}
	return v
}

// signUp issues an invite through the CLI and registers through the invite page.
func signUp(t *testing.T) *session {
	t.Helper()
	if os.Getenv("LATENESS_E2E") != "1" {
		t.Skip("set LATENESS_E2E=1")
	}
	out, err := exec.Command(env(t, "LATENESS_BIN"), "invite", "-note", "e2e").CombinedOutput()
	if err != nil {
		t.Fatalf("invite: %v\n%s", err, out)
	}
	link := regexp.MustCompile(`/invite/[0-9a-f]+`).FindString(string(out))
	if link == "" {
		t.Fatalf("no invite link in %s", out)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &session{t: t, client: &http.Client{Jar: jar, Timeout: 10 * time.Second}, base: env(t, "LATENESS_HTTP_URL")}
	final, _ := s.post(link, url.Values{"email": {uuid.NewString() + "@example.com"}, "password": {"password1"}})
	s.proj = regexp.MustCompile(`^/p/[0-9a-f-]+`).FindString(final)
	if s.proj == "" {
		t.Fatalf("registration landed on %s", final)
	}
	return s
}

// post submits a form, follows redirects and returns the final path and body.
func (s *session) post(path string, form url.Values) (string, string) {
	s.t.Helper()
	resp, err := s.client.PostForm(s.base+path, form)
	if err != nil {
		s.t.Fatal(err)
	}
	return s.read(resp)
}

func (s *session) get(path string) string {
	s.t.Helper()
	resp, err := s.client.Get(s.base + path)
	if err != nil {
		s.t.Fatal(err)
	}
	_, body := s.read(resp)
	return body
}

func (s *session) read(resp *http.Response) (string, string) {
	s.t.Helper()
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		s.t.Fatalf("%s: %d\n%s", resp.Request.URL, resp.StatusCode, body)
	}
	return resp.Request.URL.Path, string(body)
}

// outlet creates an outlet and returns its id.
func (s *session) outlet(form url.Values) string {
	s.t.Helper()
	form.Set("name", form.Get("kind")+"-"+uuid.NewString()[:6])
	final, _ := s.post(s.proj+"/outlets", form)
	return final[strings.LastIndex(final, "/")+1:]
}

// expectation creates an expectation that is due every second without slack.
func (s *session) expectation(name string, extra url.Values, outletIDs ...string) (cardPath, body string) {
	s.t.Helper()
	form := url.Values{
		"name": {name}, "source": {"intake"}, "kind": {"interval"},
		"period_value": {"1"}, "period_unit": {"1"}, "slack_value": {"0"}, "slack_unit": {"60"},
		"outlet_ids": outletIDs,
	}
	for k, v := range extra {
		form[k] = v
	}
	return s.post(s.proj+"/expectations", form)
}

func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func receiverSaw(t *testing.T, path, text string) bool {
	resp, err := http.Get(env(t, "LATENESS_RECEIVER_URL") + "/calls")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Calls []struct{ Path, Body string } `json:"calls"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return false
	}
	for _, c := range body.Calls {
		if strings.HasPrefix(c.Path, path) && strings.Contains(c.Body, text) {
			return true
		}
	}
	return false
}

func TestSignalKeepsExpectationOnTime(t *testing.T) {
	s := signUp(t)
	card, body := s.expectation("steady", url.Values{"period_value": {"1"}, "period_unit": {"3600"}})
	intake := regexp.MustCompile(`/intake/[0-9a-f]{64}`).FindString(body)
	if intake == "" {
		t.Fatalf("no intake url on the card:\n%s", body)
	}
	resp, err := http.Post(s.base+intake, "application/json", strings.NewReader(`{"outcome":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("intake %d", resp.StatusCode)
	}
	if !strings.Contains(s.get(card), `data-status="on_time"`) {
		t.Fatal("card is not on time after ok")
	}
}

func TestSilenceReachesWebhook(t *testing.T) {
	s := signUp(t)
	hook := s.outlet(url.Values{"kind": {"http"}, "http_url": {env(t, "LATENESS_RECEIVER_URL") + "/hook"}})
	name := "silent-" + uuid.NewString()[:8]
	s.expectation(name, nil, hook)
	eventually(t, "webhook delivery", func() bool { return receiverSaw(t, "/hook", name) })
}

func TestTelegramOutlet(t *testing.T) {
	s := signUp(t)
	tg := s.outlet(url.Values{"kind": {"telegram"}, "telegram_token": {"42:e2e"}, "telegram_chat": {"-100500"}})
	name := "tg-" + uuid.NewString()[:8]
	s.expectation(name, nil, tg)
	eventually(t, "telegram message", func() bool { return receiverSaw(t, "/bot42:e2e/sendMessage", name) })
}

func TestProbeFailureNotifiesAndSuccessKeepsOnTime(t *testing.T) {
	s := signUp(t)
	receiver := env(t, "LATENESS_RECEIVER_URL")
	hook := s.outlet(url.Values{"kind": {"http"}, "http_url": {receiver + "/hook"}})
	probe := func(target string) url.Values {
		return url.Values{"source": {"probe"}, "probe_method": {"GET"}, "probe_url": {receiver + target}, "probe_timeout": {"5"},
			"period_value": {"1"}, "period_unit": {"3600"}}
	}
	healthy, _ := s.expectation("healthy", probe("/healthz"))
	eventually(t, "successful probe", func() bool { return strings.Contains(s.get(healthy), `data-status="on_time"`) })

	failing := probe("/status/503")
	failing.Set("period_unit", "1")
	name := "down-" + uuid.NewString()[:8]
	card, _ := s.expectation(name, failing, hook)
	eventually(t, "probe failure notice", func() bool { return receiverSaw(t, "/hook", name) })
	if !strings.Contains(s.get(card), "503") {
		t.Fatal("probe note is not on the card")
	}
}

func TestSMTPThroughTestServer(t *testing.T) {
	s := signUp(t)
	to := uuid.NewString()[:8] + "@example.com"
	mail := s.outlet(url.Values{
		"kind": {"smtp"}, "smtp_host": {"127.0.0.1"}, "smtp_port": {env(t, "LATENESS_SMTP_PORT")}, "smtp_security": {"tls"},
		"smtp_from": {"lateness@example.com"}, "smtp_to": {to},
	})
	name := "mail-" + uuid.NewString()[:8]
	s.expectation(name, nil, mail)
	api := env(t, "LATENESS_SMTP_API_URL")
	eventually(t, "letter in the test SMTP server", func() bool {
		resp, err := http.Get(api + "/mail?to=" + url.QueryEscape(to))
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		return strings.Contains(string(raw), name+" became late")
	})
}
