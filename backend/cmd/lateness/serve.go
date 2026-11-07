package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/p0vidl0/lateness/internal/adapters/authprovider"
	"github.com/p0vidl0/lateness/internal/app"
	"github.com/p0vidl0/lateness/internal/web"
)

func serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cfg, store, err := open(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return err
	}
	service := newService(cfg, store)
	if err := service.Seed(ctx, app.Seed{Email: cfg.SeedEmail, Password: cfg.SeedPassword, SMTP: cfg.SeedSMTP}); err != nil {
		return err
	}
	if cfg.SeedEmail != "" {
		slog.Info("seed account ready", "email", cfg.SeedEmail)
	}
	var providers []app.AuthProvider
	if cfg.VKClientID != "" {
		providers = append(providers, authprovider.NewVK(cfg.VKClientID, cfg.VKClientSecret))
	}
	if cfg.TelegramLoginClientID != "" {
		providers = append(providers, authprovider.NewTelegram(cfg.TelegramLoginClientID, cfg.TelegramLoginClientSecret))
	}
	pages, err := web.New(service, cfg.PublicURL, cfg.SessionTTL, providers...)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: pages.Routes(func(rctx context.Context) error {
			if err := store.Ping(rctx); err != nil {
				return err
			}
			return store.RequireSchema(rctx)
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("serve listening", "addr", cfg.HTTPAddr)
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
