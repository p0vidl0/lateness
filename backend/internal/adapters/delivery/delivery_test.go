package delivery

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/p0vidl0/lateness/internal/domain"
)

func sampleNotice() domain.Notice {
	return domain.Notice{
		ExpectationID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Name:          "nightly import",
		From:          domain.StatusQuiet,
		To:            domain.StatusLate,
		Reason:        domain.ReasonWindowClosed,
		At:            time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
	}
}

func TestLetterVocabulary(t *testing.T) {
	smtp := domain.SMTPSettings{From: "lateness@localhost", To: "operator@localhost"}
	raw := string(Letter(smtp, sampleNotice()))
	onTime := string(Letter(smtp, domain.Notice{Name: "job", To: domain.StatusOnTime, From: domain.StatusLate, Reason: domain.ReasonSignalOK}))
	if !strings.Contains(onTime, "is on time") {
		t.Fatalf("on_time letter %q", onTime)
	}
	lower := strings.ToLower(raw)
	for _, word := range []string{"ping", "grace", "nag", "badge", "flip"} {
		if strings.Contains(lower, word) {
			t.Fatalf("letter contains %q:\n%s", word, raw)
		}
	}
	if !strings.Contains(raw, "became late") || !strings.Contains(raw, "nightly import") || !strings.Contains(raw, "\r\nReason: window_closed\r\n") {
		t.Fatalf("letter missing facts:\n%s", raw)
	}
}

func TestSendHTTP(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = string(body)
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type %s", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	hook := func(url string) domain.Outlet {
		return domain.Outlet{Kind: domain.OutletHTTP, Settings: domain.OutletSettings{HTTP: &domain.HTTPSettings{URL: url}}}
	}
	if err := (Router{Client: srv.Client()}).Send(context.Background(), hook(srv.URL), sampleNotice()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"reason":"window_closed"`) || !strings.Contains(got, "nightly import") {
		t.Fatalf("body %s", got)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	t.Cleanup(bad.Close)
	if err := (Router{Client: bad.Client()}).Send(context.Background(), hook(bad.URL), sampleNotice()); err == nil {
		t.Fatal("expected http failure")
	}
}

func TestSendTelegram(t *testing.T) {
	var path, body string
	reply := `{"ok":true}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		path, body = r.URL.Path, string(raw)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	outlet := domain.Outlet{Kind: domain.OutletTelegram, Settings: domain.OutletSettings{
		Telegram: &domain.TelegramSettings{BotToken: "123:secret", ChatID: "-100500"},
	}}
	r := Router{Client: srv.Client(), TelegramAPI: srv.URL}
	if err := r.Send(context.Background(), outlet, sampleNotice()); err != nil {
		t.Fatal(err)
	}
	if path != "/bot123:secret/sendMessage" || !strings.Contains(body, `"chat_id":"-100500"`) || !strings.Contains(body, "became late") {
		t.Fatalf("%s %s", path, body)
	}

	reply = `{"ok":false,"description":"chat not found"}`
	if err := r.Send(context.Background(), outlet, sampleNotice()); err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("expected api error, got %v", err)
	}

	srv.Close()
	err := r.Send(context.Background(), outlet, sampleNotice())
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("token must be redacted: %v", err)
	}
}

func TestSendSMTP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := fakeSMTP(t, ln)
	if err := (Router{}).Send(context.Background(), smtpOutlet(ln, domain.SMTPPlain), sampleNotice()); err != nil {
		t.Fatal(err)
	}
	expectLetter(t, got)
}

func TestSendSMTPImplicitTLS(t *testing.T) {
	// Borrow the self-signed localhost certificate of an httptest TLS server.
	certSrv := httptest.NewTLSServer(http.NotFoundHandler())
	cert := certSrv.TLS.Certificates
	certSrv.Close()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: cert, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	got := fakeSMTP(t, ln)
	if err := (Router{}).Send(context.Background(), smtpOutlet(ln, domain.SMTPTLS), sampleNotice()); err == nil {
		t.Fatal("self-signed certificate must be refused by default")
	}
	if err := (Router{SMTPInsecureTLS: true}).Send(context.Background(), smtpOutlet(ln, domain.SMTPTLS), sampleNotice()); err != nil {
		t.Fatal(err)
	}
	expectLetter(t, got)
}

func smtpOutlet(ln net.Listener, security domain.SMTPSecurity) domain.Outlet {
	host, portText, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portText)
	return domain.Outlet{Kind: domain.OutletSMTP, Settings: domain.OutletSettings{SMTP: &domain.SMTPSettings{
		Host: host, Port: port, Security: security, From: "lateness@localhost", To: "operator@localhost",
	}}}
}

func expectLetter(t *testing.T, got <-chan string) {
	t.Helper()
	select {
	case body := <-got:
		if !strings.Contains(body, "Subject: Lateness: nightly import became late") {
			t.Fatalf("smtp body:\n%s", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("smtp server got nothing")
	}
}

// fakeSMTP accepts sessions on ln and reports each DATA body.
func fakeSMTP(t *testing.T, ln net.Listener) <-chan string {
	t.Helper()
	t.Cleanup(func() { _ = ln.Close() })
	got := make(chan string, 1)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSMTP(conn, got)
		}
	}()
	return got
}

func serveSMTP(conn net.Conn, got chan<- string) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	if _, err := conn.Write([]byte("220 local\r\n")); err != nil {
		return
	}
	var data strings.Builder
	inData := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		if inData {
			if line == ".\r\n" {
				_, _ = conn.Write([]byte("250 queued\r\n"))
				got <- data.String()
				inData = false
				continue
			}
			data.WriteString(line)
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "DATA"):
			_, _ = conn.Write([]byte("354 go\r\n"))
			inData = true
		case strings.HasPrefix(upper, "QUIT"):
			_, _ = conn.Write([]byte("221 bye\r\n"))
			return
		default:
			_, _ = conn.Write([]byte("250 ok\r\n"))
		}
	}
}
