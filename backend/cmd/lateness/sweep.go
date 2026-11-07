package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/p0vidl0/lateness/internal/app"
)

func sweep() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cfg, store, err := open(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := waitSchema(ctx, store); err != nil {
		return err
	}
	service := newService(cfg, store)
	slog.Info("sweep started", "interval", cfg.SweepInterval.String())
	for {
		pass(ctx, service)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(cfg.SweepInterval):
		}
	}
}

func pass(ctx context.Context, service *app.Service) {
	r, err := service.SweepOnce(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("sweep", "err", err.Error())
	}
	if r.Probed > 0 || r.Closed > 0 || r.Sent > 0 || r.Failed > 0 {
		slog.Info("sweep pass", "probed", r.Probed, "closed", r.Closed, "sent", r.Sent, "failed", r.Failed)
	}
}

// invite prints a single-use sign-up link. It bootstraps the first admin in production.
func invite(args []string) error {
	fs := flag.NewFlagSet("invite", flag.ContinueOnError)
	admin := fs.Bool("admin", false, "the account created by this invite can issue invites")
	note := fs.String("note", "", "who the invite is for")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	cfg, store, err := open(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := waitSchema(ctx, store); err != nil {
		return err
	}
	inv, token, err := newService(cfg, store).IssueInvite(ctx, nil, *note, *admin)
	if err != nil {
		return err
	}
	fmt.Printf("%s/invite/%s\n", cfg.PublicURL, token)
	fmt.Printf("expires %s\n", inv.ExpiresAt.Format(time.RFC3339))
	return nil
}
