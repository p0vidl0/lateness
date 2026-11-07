package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/p0vidl0/lateness/internal/app"
	"github.com/p0vidl0/lateness/internal/domain"
)

// staleClaim returns a delivery stuck in "sending" (a sweep died mid-send) to the queue.
const staleClaim = 2 * time.Minute

func (s *Store) ClaimProbes(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]domain.Expectation, error) {
	var out []domain.Expectation
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			WITH due AS (
				SELECT id FROM expectations
				WHERE source = 'probe' AND status <> 'held' AND deleted_at IS NULL AND probe_at <= $1
				ORDER BY probe_at LIMIT $3
				FOR UPDATE SKIP LOCKED
			)
			UPDATE expectations e SET probe_at = $2 FROM due WHERE e.id = due.id
			RETURNING `+expectationCols, now, now.Add(lease), limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Expectation, error) { return scanExpectation(r) })
		if err != nil {
			return err
		}
		return attachOutlets(ctx, tx, out)
	})
	return out, err
}

func (s *Store) CloseDue(ctx context.Context, now time.Time, limit int) (int, error) {
	closed := 0
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT `+expectationCols+` FROM expectations e
			WHERE e.status IN ('quiet', 'on_time') AND e.deleted_at IS NULL
				AND (e.deadline_at <= $1 OR e.run_deadline_at <= $1)
			ORDER BY LEAST(e.deadline_at, COALESCE(e.run_deadline_at, e.deadline_at))
			LIMIT $2
			FOR UPDATE SKIP LOCKED`, now, limit)
		if err != nil {
			return err
		}
		due, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Expectation, error) { return scanExpectation(r) })
		if err != nil {
			return err
		}
		if err := attachOutlets(ctx, tx, due); err != nil {
			return err
		}
		for _, exp := range due {
			reason, ok := domain.CloseReason(exp.Deadline, exp.RunDeadline, now)
			next, open := domain.CloseWindow(exp.Status)
			if !ok || !open {
				continue
			}
			if _, err := tx.Exec(ctx, `
				UPDATE expectations SET status = $2, begun_at = NULL, run_deadline_at = NULL, updated_at = $3, version = version + 1
				WHERE id = $1`, exp.ID, next, now); err != nil {
				return err
			}
			tr := domain.Transition{ID: uuid.New(), ExpectationID: exp.ID, From: exp.Status, To: next, Reason: reason, At: now}
			if err := insertTransition(ctx, tx, tr); err != nil {
				return err
			}
			for _, d := range domain.PendingDeliveries(exp, tr, now) {
				if err := insertDelivery(ctx, tx, d); err != nil {
					return err
				}
			}
			closed++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return closed, nil
}

func (s *Store) ClaimDeliveries(ctx context.Context, now time.Time, limit int) ([]domain.Delivery, error) {
	var out []domain.Delivery
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE deliveries SET status = 'pending', claimed_at = NULL
			WHERE status = 'sending' AND claimed_at < $1`, now.Add(-staleClaim)); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			WITH picked AS (
				SELECT id FROM deliveries WHERE status = 'pending' ORDER BY created_at LIMIT $2 FOR UPDATE SKIP LOCKED
			)
			UPDATE deliveries d SET status = 'sending', claimed_at = $1, attempt_count = d.attempt_count + 1
			FROM picked WHERE d.id = picked.id
			RETURNING d.id, d.expectation_id, d.transition_id, d.project_id, d.outlet_id, d.attempt_count, d.payload, d.created_at`,
			now, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Delivery, error) {
			var d domain.Delivery
			var payload []byte
			if err := r.Scan(&d.ID, &d.ExpectationID, &d.TransitionID, &d.ProjectID, &d.OutletID, &d.AttemptCount, &payload, &d.CreatedAt); err != nil {
				return d, err
			}
			d.Status = domain.DeliverySending
			return d, json.Unmarshal(payload, &d.Notice)
		})
		if err != nil {
			return err
		}
		// A soft-deleted outlet still delivers what was queued before deletion.
		for i := range out {
			if out[i].OutletID == nil {
				continue
			}
			o, err := s.scanOutlet(tx.QueryRow(ctx, `SELECT `+outletCols+` FROM outlets WHERE id = $1`, *out[i].OutletID))
			if errors.Is(err, app.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			out[i].Outlet = &o
		}
		return nil
	})
	return out, err
}

func (s *Store) MarkDelivery(ctx context.Context, id uuid.UUID, status, lastError string, sentAt *time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE deliveries SET status = $2, last_error = $3, sent_at = $4 WHERE id = $1`,
		id, status, lastError, sentAt)
	return err
}
