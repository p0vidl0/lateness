package domain

import (
	"errors"
	"testing"
	"time"
)

func TestProbeValidate(t *testing.T) {
	p := Probe{Method: " head ", URL: "https://example.com/health"}
	if err := p.Validate(); err != nil || p.Method != "HEAD" || p.Timeout != DefaultProbeTimeout {
		t.Fatalf("%+v %v", p, err)
	}
	for _, bad := range []Probe{
		{Method: "TRACE", URL: "https://example.com"},
		{URL: "ftp://example.com"},
		{URL: "https://user:pass@example.com"},
		{URL: "https://example.com", ExpectStatus: 99},
		{URL: "https://example.com", Timeout: 2 * time.Minute},
	} {
		if err := bad.Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if !(Probe{}).Accepts(204) || (Probe{}).Accepts(301) || !(Probe{ExpectStatus: 301}).Accepts(301) {
		t.Fatal("status matching")
	}
	if ProbeRetryAfter(Schedule{Kind: ScheduleInterval, Period: 10 * time.Second}) != 10*time.Second ||
		ProbeRetryAfter(Schedule{Kind: ScheduleCron}) != ProbeRetry {
		t.Fatal("retry pause")
	}
}

func TestOutletValidateKeepsOnlyItsKind(t *testing.T) {
	o := Outlet{Name: " tg ", Kind: OutletTelegram, Settings: OutletSettings{
		Telegram: &TelegramSettings{BotToken: "1:x", ChatID: " -100 "},
		HTTP:     &HTTPSettings{URL: "https://leftover"},
	}}
	if err := o.Validate(); err != nil || o.Name != "tg" || o.Settings.HTTP != nil || o.Settings.Telegram.ChatID != "-100" {
		t.Fatalf("%+v %v", o, err)
	}
	if o.Target() != "chat -100" {
		t.Fatalf("target %q", o.Target())
	}
	for _, bad := range []Outlet{
		{Name: "x", Kind: OutletSMTP, Settings: OutletSettings{SMTP: &SMTPSettings{Host: "h", Port: 25, Security: "ssl", From: "a@b.c", To: "a@b.c"}}},
		{Name: "x", Kind: OutletSMTP, Settings: OutletSettings{SMTP: &SMTPSettings{Host: "h", Port: 25, Security: SMTPTLS, From: "nope", To: "a@b.c"}}},
		{Name: "x", Kind: OutletHTTP, Settings: OutletSettings{HTTP: &HTTPSettings{URL: "javascript:alert(1)"}}},
		{Name: "x", Kind: OutletTelegram, Settings: OutletSettings{Telegram: &TelegramSettings{BotToken: "1:x/../", ChatID: "1"}}},
		{Name: "", Kind: OutletHTTP, Settings: OutletSettings{HTTP: &HTTPSettings{URL: "https://x"}}},
	} {
		if err := bad.Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestKeepSecrets(t *testing.T) {
	prev := Outlet{Kind: OutletSMTP, Settings: OutletSettings{SMTP: &SMTPSettings{Username: "u", Password: "p"}}}
	edit := Outlet{Kind: OutletSMTP, Settings: OutletSettings{SMTP: &SMTPSettings{Username: "u"}}}
	edit.KeepSecrets(prev)
	if edit.Settings.SMTP.Password != "p" {
		t.Fatal("password dropped")
	}
	other := Outlet{Kind: OutletSMTP, Settings: OutletSettings{SMTP: &SMTPSettings{Username: "someone-else"}}}
	other.KeepSecrets(prev)
	if other.Settings.SMTP.Password != "" {
		t.Fatal("password moved to another user")
	}
	tg := Outlet{Kind: OutletTelegram, Settings: OutletSettings{Telegram: &TelegramSettings{ChatID: "2"}}}
	tg.KeepSecrets(Outlet{Kind: OutletTelegram, Settings: OutletSettings{Telegram: &TelegramSettings{BotToken: "t", ChatID: "1"}}})
	if tg.Settings.Telegram.BotToken != "t" {
		t.Fatal("token dropped")
	}
}

func TestInviteState(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := Invite{ExpiresAt: now.Add(time.Hour)}
	if inv.State(now) != InviteActive || inv.State(now.Add(time.Hour)) != InviteExpired {
		t.Fatal("expiry")
	}
	inv.UsedAt = &now
	if inv.State(now) != InviteUsed {
		t.Fatal("used")
	}
	inv.RevokedAt = &now
	if inv.State(now) != InviteRevoked {
		t.Fatal("revoked")
	}
}
