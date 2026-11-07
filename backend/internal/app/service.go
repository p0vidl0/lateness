// Package app holds the scenarios: accounts, projects, expectations, outlets, probes and sweep.
// It decides; adapters do I/O.
package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/p0vidl0/lateness/internal/domain"
)

var (
	ErrInvalid        = errors.New("invalid")
	ErrNotFound       = errors.New("not found")
	ErrConflict       = errors.New("conflict")
	ErrUnauthorized   = errors.New("unauthorized")
	ErrForbidden      = errors.New("forbidden")
	ErrInviteRequired = errors.New("invite required")
	ErrInviteUnusable = errors.New("invite is used, expired or revoked")
)

// Notifier sends one notice through one outlet.
type Notifier interface {
	Send(ctx context.Context, outlet domain.Outlet, notice domain.Notice) error
}

// Prober performs one probe attempt. It never returns an error: a failure is a result.
type Prober interface {
	Probe(ctx context.Context, probe domain.Probe) domain.ProbeResult
}

type Clock interface {
	Now() time.Time
}

type WallClock struct{}

func (WallClock) Now() time.Time { return time.Now().UTC() }

// ManualClock is a settable clock for tests.
type ManualClock struct {
	mu sync.Mutex
	t  time.Time
}

func NewManualClock(t time.Time) *ManualClock { return &ManualClock{t: t.UTC()} }

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type Service struct {
	Store      Store
	Clock      Clock
	SessionTTL time.Duration
	Notifier   Notifier
	Prober     Prober
}

// Now is the service clock, exposed so pages render relative times against the same clock as sweep.
func (s *Service) Now() time.Time {
	if s.Clock == nil {
		return time.Now().UTC()
	}
	return s.Clock.Now().UTC()
}

func invalid(err error) error {
	if err == nil || errors.Is(err, ErrInvalid) {
		return err
	}
	if errors.Is(err, domain.ErrInvalid) {
		return fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	return err
}

func conflict(err error) error {
	return fmt.Errorf("%w: %s", ErrConflict, err.Error())
}
