// Command lateness runs one role: serve (pages and intake), sweep (probes, windows,
// deliveries), invite (prints a sign-up link), health (container healthcheck) or
// receiver (e2e fake for outlets and probes).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
	_ "time/tzdata" // cron time zones must not depend on the image having zoneinfo

	"github.com/p0vidl0/lateness/internal/adapters/delivery"
	"github.com/p0vidl0/lateness/internal/adapters/postgres"
	"github.com/p0vidl0/lateness/internal/adapters/probe"
	"github.com/p0vidl0/lateness/internal/app"
	"github.com/p0vidl0/lateness/internal/config"
	"github.com/p0vidl0/lateness/internal/migrate"
	"github.com/p0vidl0/lateness/internal/secret"
)

// version is the git commit, set at build time: -ldflags "-X main.version=…".
var version = "dev"

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if len(os.Args) < 2 {
		usage()
	}
	if os.Args[1] == "serve" || os.Args[1] == "sweep" {
		slog.Info("lateness", "role", os.Args[1], "version", version)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve()
	case "sweep":
		err = sweep()
	case "invite":
		err = invite(os.Args[2:])
	case "health":
		err = health()
	case "version":
		fmt.Println(version)
	case "receiver":
		err = receiver()
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "lateness %s: %s\n", os.Args[1], err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: lateness serve | sweep | invite [-admin] [-note text] | health | version | receiver")
	os.Exit(2)
}

// open loads config and connects to Postgres. The caller closes the store.
func open(ctx context.Context) (config.Config, *postgres.Store, error) {
	cfg, err := config.Load()
	if err != nil {
		return cfg, nil, err
	}
	cipher, err := secret.NewCipher(cfg.SecretKey)
	if err != nil {
		return cfg, nil, fmt.Errorf("LATENESS_SECRET_KEY: %w", err)
	}
	store, err := postgres.Open(ctx, cfg.DatabaseURL, cipher)
	return cfg, store, err
}

func newService(cfg config.Config, store *postgres.Store) *app.Service {
	if cfg.SMTPInsecureTLS {
		slog.Warn("LATENESS_SMTP_INSECURE_TLS is on: SMTP certificates are not verified")
	}
	return &app.Service{
		Store:      store,
		Clock:      app.WallClock{},
		SessionTTL: cfg.SessionTTL,
		Notifier:   delivery.Router{TelegramAPI: cfg.TelegramAPIURL, SMTPInsecureTLS: cfg.SMTPInsecureTLS},
		Prober:     probe.New(cfg.ProbeAllowPrivate),
	}
}

// waitSchema waits for serve to apply migrations; roles other than serve never migrate.
func waitSchema(ctx context.Context, store *postgres.Store) error {
	deadline := time.Now().Add(90 * time.Second)
	for attempt := 1; ; attempt++ {
		err := store.RequireSchema(ctx)
		if err == nil || !errors.Is(err, migrate.ErrNotReady) || time.Now().After(deadline) {
			return err
		}
		if attempt == 1 || attempt%5 == 0 {
			slog.Info("waiting for schema", "err", err.Error())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// health asks the local serve for /readyz. Images without curl use it as a healthcheck.
func health() error {
	addr := os.Getenv("LATENESS_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/readyz")
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("readyz: %s", resp.Status)
	}
	return nil
}
