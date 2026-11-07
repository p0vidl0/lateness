package domain

import (
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

type OutletKind string

const (
	OutletSMTP     OutletKind = "smtp"
	OutletHTTP     OutletKind = "http"
	OutletTelegram OutletKind = "telegram"
)

type SMTPSecurity string

const (
	SMTPPlain    SMTPSecurity = "none"
	SMTPStartTLS SMTPSecurity = "starttls"
	SMTPTLS      SMTPSecurity = "tls" // implicit TLS, usually port 465
)

// OutletSettings holds the kind-specific part of an outlet. Exactly one field
// matches Outlet.Kind. The whole value is stored encrypted.
type OutletSettings struct {
	SMTP     *SMTPSettings     `json:"smtp,omitempty"`
	HTTP     *HTTPSettings     `json:"http,omitempty"`
	Telegram *TelegramSettings `json:"telegram,omitempty"`
}

type SMTPSettings struct {
	Host     string       `json:"host"`
	Port     int          `json:"port"`
	Security SMTPSecurity `json:"security"`
	Username string       `json:"username,omitempty"`
	Password string       `json:"password,omitempty"`
	From     string       `json:"from"`
	To       string       `json:"to"`
}

type HTTPSettings struct {
	URL string `json:"url"`
}

type TelegramSettings struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type Outlet struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Name      string
	Kind      OutletKind
	Settings  OutletSettings
	CreatedAt time.Time
	UpdatedAt time.Time
}

const MaxName = 200

// Validate normalizes and checks an outlet. Only the settings for Kind are kept.
func (o *Outlet) Validate() error {
	o.Name = strings.TrimSpace(o.Name)
	if o.Name == "" || len(o.Name) > MaxName {
		return fmt.Errorf("%w: name", ErrInvalid)
	}
	s := o.Settings
	switch o.Kind {
	case OutletSMTP:
		if s.SMTP == nil {
			return fmt.Errorf("%w: smtp settings", ErrInvalid)
		}
		if err := s.SMTP.validate(); err != nil {
			return err
		}
		o.Settings = OutletSettings{SMTP: s.SMTP}
	case OutletHTTP:
		if s.HTTP == nil {
			return fmt.Errorf("%w: http settings", ErrInvalid)
		}
		u, err := url.Parse(strings.TrimSpace(s.HTTP.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%w: http url", ErrInvalid)
		}
		o.Settings = OutletSettings{HTTP: &HTTPSettings{URL: u.String()}}
	case OutletTelegram:
		t := s.Telegram
		if t == nil {
			return fmt.Errorf("%w: telegram settings", ErrInvalid)
		}
		t.BotToken, t.ChatID = strings.TrimSpace(t.BotToken), strings.TrimSpace(t.ChatID)
		if t.BotToken == "" || strings.ContainsAny(t.BotToken, "/?# ") || t.ChatID == "" {
			return fmt.Errorf("%w: telegram bot token or chat id", ErrInvalid)
		}
		o.Settings = OutletSettings{Telegram: t}
	default:
		return fmt.Errorf("%w: outlet kind", ErrInvalid)
	}
	return nil
}

func (s *SMTPSettings) validate() error {
	s.Host = strings.TrimSpace(s.Host)
	s.From, s.To = strings.TrimSpace(s.From), strings.TrimSpace(s.To)
	if s.Host == "" || s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("%w: smtp host or port", ErrInvalid)
	}
	switch s.Security {
	case SMTPPlain, SMTPStartTLS, SMTPTLS:
	default:
		return fmt.Errorf("%w: smtp security", ErrInvalid)
	}
	if !validAddress(s.From) || !validAddress(s.To) {
		return fmt.Errorf("%w: smtp from or to", ErrInvalid)
	}
	return nil
}

func validAddress(addr string) bool {
	parsed, err := mail.ParseAddress(addr)
	return err == nil && parsed.Address == addr
}

// KeepSecrets copies secrets from prev when the edited outlet leaves them empty:
// forms never echo secrets back, so an empty field means "unchanged".
func (o *Outlet) KeepSecrets(prev Outlet) {
	if o.Kind != prev.Kind {
		return
	}
	switch o.Kind {
	case OutletSMTP:
		if o.Settings.SMTP != nil && prev.Settings.SMTP != nil && o.Settings.SMTP.Password == "" &&
			o.Settings.SMTP.Username == prev.Settings.SMTP.Username {
			o.Settings.SMTP.Password = prev.Settings.SMTP.Password
		}
	case OutletTelegram:
		if o.Settings.Telegram != nil && prev.Settings.Telegram != nil && o.Settings.Telegram.BotToken == "" {
			o.Settings.Telegram.BotToken = prev.Settings.Telegram.BotToken
		}
	}
}

// Target is a short, secret-free description of where the outlet sends.
func (o Outlet) Target() string {
	s := o.Settings
	switch {
	case s.SMTP != nil:
		return s.SMTP.To
	case s.HTTP != nil:
		if u, err := url.Parse(s.HTTP.URL); err == nil {
			return u.Scheme + "://" + u.Host + u.Path
		}
		return ""
	case s.Telegram != nil:
		return "chat " + s.Telegram.ChatID
	default:
		return ""
	}
}
