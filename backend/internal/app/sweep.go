package app

import (
	"context"
	"fmt"
	"time"

	"github.com/p0vidl0/lateness/internal/domain"
)

const (
	sweepBatch          = 50
	maxDeliveryAttempts = 5
)

type SweepResult struct {
	Probed int
	Closed int
	Sent   int
	Failed int
}

// SweepOnce runs due probes, closes overdue windows, then sends pending deliveries.
// Probes go first so a fresh success can still keep a window open.
func (s *Service) SweepOnce(ctx context.Context) (SweepResult, error) {
	if s.Notifier == nil {
		return SweepResult{}, fmt.Errorf("notifier is not configured")
	}
	var result SweepResult
	var err error
	if result.Probed, err = s.RunProbes(ctx, sweepBatch); err != nil {
		return result, err
	}
	if result.Closed, err = s.Store.CloseDue(ctx, s.Now(), sweepBatch); err != nil {
		return result, err
	}
	claimed, err := s.Store.ClaimDeliveries(ctx, s.Now(), sweepBatch)
	if err != nil {
		return result, err
	}
	for _, d := range claimed {
		status, lastError := s.deliver(ctx, d)
		var sentAt *time.Time
		if status == domain.DeliverySent {
			now := s.Now()
			sentAt = &now
			result.Sent++
		}
		if status == domain.DeliveryFailed {
			result.Failed++
		}
		if err := s.Store.MarkDelivery(ctx, d.ID, status, lastError, sentAt); err != nil {
			return result, err
		}
	}
	return result, nil
}

// deliver sends one claimed delivery and picks its next status.
func (s *Service) deliver(ctx context.Context, d domain.Delivery) (status, lastError string) {
	if d.Outlet == nil {
		return domain.DeliveryFailed, "outlet is not configured"
	}
	err := s.Notifier.Send(ctx, *d.Outlet, d.Notice)
	switch {
	case err == nil:
		return domain.DeliverySent, ""
	case d.AttemptCount >= maxDeliveryAttempts:
		return domain.DeliveryFailed, err.Error()
	default:
		return domain.DeliveryPending, err.Error()
	}
}
