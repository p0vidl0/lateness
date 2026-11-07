// Package delivery sends notices through outlets: SMTP, HTTP webhook and Telegram.
package delivery

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/p0vidl0/lateness/internal/domain"
)

const (
	sendTimeout        = 10 * time.Second
	DefaultTelegramAPI = "https://api.telegram.org"
)

type Router struct {
	Client      *http.Client // nil uses a client with sendTimeout
	TelegramAPI string       // base URL of the Bot API; empty uses DefaultTelegramAPI
	// SMTPInsecureTLS skips certificate checks for SMTP. Only for a local test
	// server with a self-signed certificate; never in production.
	SMTPInsecureTLS bool
}

func (r Router) Send(ctx context.Context, outlet domain.Outlet, notice domain.Notice) error {
	s := outlet.Settings
	switch {
	case outlet.Kind == domain.OutletSMTP && s.SMTP != nil:
		return r.sendSMTP(ctx, *s.SMTP, notice)
	case outlet.Kind == domain.OutletHTTP && s.HTTP != nil:
		return r.sendHTTP(ctx, *s.HTTP, notice)
	case outlet.Kind == domain.OutletTelegram && s.Telegram != nil:
		return r.sendTelegram(ctx, *s.Telegram, notice)
	default:
		return fmt.Errorf("outlet %q has no settings for its kind", outlet.Kind)
	}
}

func (r Router) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return &http.Client{Timeout: sendTimeout}
}

// Subject is the one-line summary used as mail subject and message title.
func Subject(n domain.Notice) string {
	if n.To == domain.StatusOnTime {
		return "Lateness: " + n.Name + " is on time"
	}
	return "Lateness: " + n.Name + " became late"
}

// Text is the plain body shared by mail and Telegram.
func Text(n domain.Notice) string {
	return strings.Join([]string{
		"Expectation: " + n.Name,
		"Id: " + n.ExpectationID.String(),
		"Previous status: " + string(n.From),
		"Current status: " + string(n.To),
		"Reason: " + string(n.Reason),
		"At: " + n.At.UTC().Format(time.RFC3339),
	}, "\n")
}

func Letter(s domain.SMTPSettings, n domain.Notice) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", s.From)
	fmt.Fprintf(&b, "To: %s\r\n", s.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", Subject(n))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString(strings.ReplaceAll(Text(n), "\n", "\r\n"))
	b.WriteString("\r\n")
	return []byte(b.String())
}

func (r Router) sendSMTP(ctx context.Context, s domain.SMTPSettings, n domain.Notice) error {
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	tlsConfig := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: r.SMTPInsecureTLS} //nolint:gosec // opt-in for a local test server
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	var conn net.Conn
	var err error
	if s.Security == domain.SMTPTLS {
		conn, err = (&tls.Dialer{Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	client, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = client.Close() }()
	if s.Security == domain.SMTPStartTLS {
		if err := client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if s.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return err
		}
	}
	if err := client.Mail(s.From); err != nil {
		return err
	}
	if err := client.Rcpt(s.To); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(Letter(s, n)); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func (r Router) sendHTTP(ctx context.Context, s domain.HTTPSettings, n domain.Notice) error {
	payload, err := json.Marshal(n)
	if err != nil {
		return err
	}
	_, err = r.post(ctx, s.URL, payload)
	return err
}

func (r Router) sendTelegram(ctx context.Context, s domain.TelegramSettings, n domain.Notice) error {
	base := strings.TrimRight(r.TelegramAPI, "/")
	if base == "" {
		base = DefaultTelegramAPI
	}
	payload, err := json.Marshal(map[string]any{
		"chat_id":                  s.ChatID,
		"text":                     Subject(n) + "\n\n" + Text(n),
		"disable_web_page_preview": true,
	})
	if err != nil {
		return err
	}
	body, err := r.post(ctx, base+"/bot"+s.BotToken+"/sendMessage", payload)
	if err != nil {
		// The token is part of the URL: never let it reach logs or the journal.
		return fmt.Errorf("telegram: %s", strings.ReplaceAll(err.Error(), s.BotToken, "***"))
	}
	var reply struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if json.Unmarshal(body, &reply) == nil && !reply.OK {
		return fmt.Errorf("telegram: %s", reply.Description)
	}
	return nil
}

// post sends JSON and returns the start of a 2xx response body.
func (r Router) post(ctx context.Context, url string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}
	return body, nil
}
