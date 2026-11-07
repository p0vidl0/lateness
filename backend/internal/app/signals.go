package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/p0vidl0/lateness/internal/domain"
)

const (
	commitAttempts = 3
	probeLease     = domain.MaxProbeTimeout + 30*time.Second
	probeWorkers   = 8
)

// AcceptSignal records one intake body for the expectation behind token.
func (s *Service) AcceptSignal(ctx context.Context, token string, raw []byte) error {
	intake, err := domain.ParseIntake(raw)
	if err != nil {
		return invalid(err)
	}
	for attempt := 1; ; attempt++ {
		exp, err := s.Store.GetByIntakeToken(ctx, token)
		if err != nil {
			return err
		}
		err = s.Store.Commit(ctx, signalChange(exp, intake.Outcome, intake.Note, s.Now()))
		if !errors.Is(err, ErrConflict) || attempt == commitAttempts {
			return err
		}
	}
}

// signalChange decides what one outcome does to an expectation.
func signalChange(exp domain.Expectation, outcome domain.Outcome, note string, now time.Time) Change {
	ch := Change{Signal: &domain.Signal{ID: uuid.New(), ExpectationID: exp.ID, Outcome: outcome, Note: note, ReceivedAt: now}}
	if exp.Status == domain.StatusHeld {
		return ch
	}
	switch outcome {
	case domain.OutcomeBegin:
		if exp.Status == domain.StatusLate {
			return ch
		}
		domain.ArmRun(&exp, now)
	case domain.OutcomeOK:
		if err := openWindow(&exp, now, false); err != nil {
			// The stored schedule was valid when saved; keep the signal anyway.
			return ch
		}
		exp.LastOK = &now
		domain.ClearRun(&exp)
	case domain.OutcomeFail:
		domain.ClearRun(&exp)
	}
	next, reason, changed, err := domain.ApplySignal(exp.Status, outcome)
	if err == nil && changed {
		tr := domain.Transition{ID: uuid.New(), ExpectationID: exp.ID, From: exp.Status, To: next, Reason: reason, At: now}
		ch.Transition = &tr
		exp.Status = next
		if domain.NotifyTransition(tr.From, tr.To, tr.Reason) {
			ch.Deliveries = domain.PendingDeliveries(exp, tr, now)
		}
	}
	exp.UpdatedAt = now
	ch.Expectation = &exp
	return ch
}

// probeChange records one probe attempt. Success is an ok signal. Failure is logged
// and retried soon but changes no status: lateness comes from the window closing.
func probeChange(exp domain.Expectation, res domain.ProbeResult, now time.Time) Change {
	if res.OK {
		return signalChange(exp, domain.OutcomeOK, res.Note, now)
	}
	retry := now.Add(domain.ProbeRetryAfter(exp.Schedule))
	exp.ProbeAt = &retry
	exp.UpdatedAt = now
	return Change{
		Expectation: &exp,
		Signal:      &domain.Signal{ID: uuid.New(), ExpectationID: exp.ID, Outcome: domain.OutcomeFail, Note: res.Note, ReceivedAt: now},
	}
}

// RunProbes performs every due probe once and records the results.
func (s *Service) RunProbes(ctx context.Context, limit int) (int, error) {
	if s.Prober == nil {
		return 0, nil
	}
	due, err := s.Store.ClaimProbes(ctx, s.Now(), probeLease, limit)
	if err != nil || len(due) == 0 {
		return 0, err
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, probeWorkers)
	for _, exp := range due {
		wg.Add(1)
		slots <- struct{}{}
		go func(exp domain.Expectation) {
			defer func() { <-slots; wg.Done() }()
			res := s.Prober.Probe(ctx, *exp.Probe)
			// A concurrent edit or hold wins; the lease brings the probe back if needed.
			if err := s.Store.Commit(ctx, probeChange(exp, res, s.Now())); err != nil && !errors.Is(err, ErrConflict) {
				slog.Error("probe result", "expectation", exp.ID.String(), "err", err.Error())
			}
		}(exp)
	}
	wg.Wait()
	return len(due), nil
}
