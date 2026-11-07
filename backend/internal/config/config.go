// Package config reads LATENESS_* environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/p0vidl0/lateness/internal/domain"
)

type Config struct {
	DatabaseURL   string
	HTTPAddr      string
	PublicURL     string
	SecretKey     string // base64 of 32 bytes; seals outlet settings
	SessionTTL    time.Duration
	SweepInterval time.Duration

	TelegramAPIURL    string
	SMTPInsecureTLS   bool // local test SMTP with a self-signed certificate only
	ProbeAllowPrivate bool // let probes reach loopback and private networks (dev, e2e)

	VKClientID     string
	VKClientSecret string
	// Telegram Login over OIDC; client id and secret come from @BotFather.
	TelegramLoginClientID     string
	TelegramLoginClientSecret string

	SeedEmail    string
	SeedPassword string
	SeedSMTP     *domain.SMTPSettings
}

func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:    os.Getenv("LATENESS_DATABASE_URL"),
		HTTPAddr:       env("LATENESS_HTTP_ADDR", ":8080"),
		PublicURL:      env("LATENESS_PUBLIC_URL", "http://localhost:28080"),
		SecretKey:      os.Getenv("LATENESS_SECRET_KEY"),
		TelegramAPIURL: os.Getenv("LATENESS_TELEGRAM_API_URL"),
		VKClientID:     os.Getenv("LATENESS_VK_CLIENT_ID"),
		VKClientSecret: os.Getenv("LATENESS_VK_CLIENT_SECRET"),

		TelegramLoginClientID:     os.Getenv("LATENESS_TELEGRAM_LOGIN_CLIENT_ID"),
		TelegramLoginClientSecret: os.Getenv("LATENESS_TELEGRAM_LOGIN_CLIENT_SECRET"),
		SeedEmail:                 os.Getenv("LATENESS_SEED_EMAIL"),
		SeedPassword:              os.Getenv("LATENESS_SEED_PASSWORD"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("LATENESS_DATABASE_URL is required")
	}
	if cfg.SecretKey == "" {
		return Config{}, fmt.Errorf("LATENESS_SECRET_KEY is required (base64 of 32 random bytes: openssl rand -base64 32)")
	}
	var err error
	if cfg.SessionTTL, err = duration("LATENESS_SESSION_TTL", 30*24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.SweepInterval, err = duration("LATENESS_SWEEP_INTERVAL", 5*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.SMTPInsecureTLS, err = flag("LATENESS_SMTP_INSECURE_TLS"); err != nil {
		return Config{}, err
	}
	if cfg.ProbeAllowPrivate, err = flag("LATENESS_PROBE_ALLOW_PRIVATE"); err != nil {
		return Config{}, err
	}
	if (cfg.VKClientID == "") != (cfg.VKClientSecret == "") {
		return Config{}, fmt.Errorf("LATENESS_VK_CLIENT_ID and LATENESS_VK_CLIENT_SECRET go together")
	}
	if (cfg.TelegramLoginClientID == "") != (cfg.TelegramLoginClientSecret == "") {
		return Config{}, fmt.Errorf("LATENESS_TELEGRAM_LOGIN_CLIENT_ID and LATENESS_TELEGRAM_LOGIN_CLIENT_SECRET go together")
	}
	if host := os.Getenv("LATENESS_SEED_SMTP_HOST"); host != "" {
		port, err := strconv.Atoi(os.Getenv("LATENESS_SEED_SMTP_PORT"))
		if err != nil {
			return Config{}, fmt.Errorf("LATENESS_SEED_SMTP_PORT: %w", err)
		}
		cfg.SeedSMTP = &domain.SMTPSettings{
			Host: host, Port: port,
			Security: domain.SMTPSecurity(env("LATENESS_SEED_SMTP_SECURITY", string(domain.SMTPTLS))),
			From:     env("LATENESS_SEED_SMTP_FROM", "lateness@localhost"),
			To:       env("LATENESS_SEED_SMTP_TO", cfg.SeedEmail),
		}
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func duration(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return d, nil
}

func flag(key string) (bool, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return v, nil
}
