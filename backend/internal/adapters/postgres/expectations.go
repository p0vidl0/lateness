package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/p0vidl0/lateness/internal/app"
	"github.com/p0vidl0/lateness/internal/domain"
)

// expectationCols expects the table to be aliased as e.
const expectationCols = `e.id, e.project_id, e.name, e.source, e.probe, e.intake_token,
	e.schedule_kind, e.period_seconds, e.slack_seconds, e.cron_expr, e.cron_timezone,
	e.status, e.window_anchor, e.deadline_at, e.begun_at, e.run_deadline_at, e.last_ok_at, e.probe_at,
	e.version, e.created_at, e.updated_at`

func scanExpectation(row pgx.Row) (domain.Expectation, error) {
	var exp domain.Expectation
	var probe []byte
	var token, cron *string
	var period *int64
	var slack int64
	if err := row.Scan(
		&exp.ID, &exp.ProjectID, &exp.Name, &exp.Source, &probe, &token,
		&exp.Schedule.Kind, &period, &slack, &cron, &exp.Schedule.Timezone,
		&exp.Status, &exp.WindowAnchor, &exp.Deadline, &exp.BegunAt, &exp.RunDeadline, &exp.LastOK, &exp.ProbeAt,
		&exp.Version, &exp.CreatedAt, &exp.UpdatedAt,
	); err != nil {
		return domain.Expectation{}, mapErr(err)
	}
	if probe != nil {
		exp.Probe = &domain.Probe{}
		if err := json.Unmarshal(probe, exp.Probe); err != nil {
			return domain.Expectation{}, err
		}
	}
	exp.IntakeToken, exp.Schedule.Cron = deref(token), deref(cron)
	if period != nil {
		exp.Schedule.Period = time.Duration(*period) * time.Second
	}
	exp.Schedule.Slack = time.Duration(slack) * time.Second
	exp.WindowAnchor, exp.Deadline = exp.WindowAnchor.UTC(), exp.Deadline.UTC()
	exp.BegunAt, exp.RunDeadline, exp.LastOK, exp.ProbeAt = utcPtr(exp.BegunAt), utcPtr(exp.RunDeadline), utcPtr(exp.LastOK), utcPtr(exp.ProbeAt)
	exp.CreatedAt, exp.UpdatedAt = exp.CreatedAt.UTC(), exp.UpdatedAt.UTC()
	return exp, nil
}

// expectationArgs are the stored values of every mutable column, $2…$18 in insert and update.
func expectationArgs(exp domain.Expectation) ([]any, error) {
	var probe []byte
	if exp.Probe != nil {
		raw, err := json.Marshal(exp.Probe)
		if err != nil {
			return nil, err
		}
		probe = raw
	}
	var period *int64
	if exp.Schedule.Kind == domain.ScheduleInterval {
		v := int64(exp.Schedule.Period / time.Second)
		period = &v
	}
	tz := exp.Schedule.Timezone
	if tz == "" {
		tz = "UTC"
	}
	return []any{
		exp.Name, probe, exp.Schedule.Kind, period, int64(exp.Schedule.Slack / time.Second),
		nullString(exp.Schedule.Cron), tz, exp.Status, exp.WindowAnchor, exp.Deadline,
		exp.BegunAt, exp.RunDeadline, exp.LastOK, exp.ProbeAt, exp.UpdatedAt,
	}, nil
}

func (s *Store) CreateExpectation(ctx context.Context, exp domain.Expectation) error {
	args, err := expectationArgs(exp)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO expectations (
				id, name, probe, schedule_kind, period_seconds, slack_seconds, cron_expr, cron_timezone,
				status, window_anchor, deadline_at, begun_at, run_deadline_at, last_ok_at, probe_at, updated_at,
				project_id, source, intake_token, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
			append(append([]any{exp.ID}, args...), exp.ProjectID, exp.Source, nullString(exp.IntakeToken), exp.CreatedAt)...); err != nil {
			return mapErr(err)
		}
		return replaceOutlets(ctx, tx, exp.ID, exp.OutletIDs)
	})
}

func (s *Store) Commit(ctx context.Context, ch app.Change) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if exp := ch.Expectation; exp != nil {
			args, err := expectationArgs(*exp)
			if err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `
				UPDATE expectations SET
					name = $2, probe = $3, schedule_kind = $4, period_seconds = $5, slack_seconds = $6,
					cron_expr = $7, cron_timezone = $8, status = $9, window_anchor = $10, deadline_at = $11,
					begun_at = $12, run_deadline_at = $13, last_ok_at = $14, probe_at = $15, updated_at = $16,
					version = version + 1
				WHERE id = $1 AND version = $17 AND deleted_at IS NULL`,
				append(append([]any{exp.ID}, args...), exp.Version)...)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return app.ErrConflict
			}
			if ch.Outlets {
				if err := replaceOutlets(ctx, tx, exp.ID, exp.OutletIDs); err != nil {
					return err
				}
			}
		}
		if sig := ch.Signal; sig != nil {
			if _, err := tx.Exec(ctx, `
				INSERT INTO signals (id, expectation_id, outcome, note, received_at) VALUES ($1,$2,$3,$4,$5)`,
				sig.ID, sig.ExpectationID, sig.Outcome, sig.Note, sig.ReceivedAt); err != nil {
				return err
			}
		}
		if ch.Transition != nil {
			if err := insertTransition(ctx, tx, *ch.Transition); err != nil {
				return err
			}
		}
		for _, d := range ch.Deliveries {
			if err := insertDelivery(ctx, tx, d); err != nil {
				return err
			}
		}
		if ch.Expectation != nil {
			ch.Expectation.Version++
		}
		return nil
	})
}

func (s *Store) GetExpectation(ctx context.Context, projectID, id uuid.UUID) (domain.Expectation, error) {
	return s.oneExpectation(ctx, `e.project_id = $1 AND e.id = $2`, projectID, id)
}

func (s *Store) GetByIntakeToken(ctx context.Context, token string) (domain.Expectation, error) {
	return s.oneExpectation(ctx, `e.intake_token = $1 AND e.source = 'intake'`, token)
}

func (s *Store) oneExpectation(ctx context.Context, where string, args ...any) (domain.Expectation, error) {
	exp, err := scanExpectation(s.pool.QueryRow(ctx, `
		SELECT `+expectationCols+` FROM expectations e WHERE `+where+` AND e.deleted_at IS NULL`, args...))
	if err != nil {
		return domain.Expectation{}, err
	}
	if exp.OutletIDs, err = outletIDs(ctx, s.pool, exp.ID); err != nil {
		return domain.Expectation{}, err
	}
	return exp, nil
}

func (s *Store) ListExpectations(ctx context.Context, projectID uuid.UUID) ([]domain.Expectation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+expectationCols+` FROM expectations e
		WHERE e.project_id = $1 AND e.deleted_at IS NULL ORDER BY e.name LIMIT 500`, projectID)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Expectation, error) { return scanExpectation(r) })
	if err != nil {
		return nil, err
	}
	return items, attachOutlets(ctx, s.pool, items)
}

func (s *Store) DeleteExpectation(ctx context.Context, projectID, id uuid.UUID, now time.Time) error {
	return affected(s.pool.Exec(ctx, `
		UPDATE expectations SET deleted_at = $3, version = version + 1
		WHERE project_id = $1 AND id = $2 AND deleted_at IS NULL`, projectID, id, now))
}

func (s *Store) ListSignals(ctx context.Context, expectationID uuid.UUID) ([]domain.Signal, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, expectation_id, outcome, note, received_at FROM signals
		WHERE expectation_id = $1 ORDER BY received_at DESC LIMIT 100`, expectationID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Signal, error) {
		var sig domain.Signal
		err := r.Scan(&sig.ID, &sig.ExpectationID, &sig.Outcome, &sig.Note, &sig.ReceivedAt)
		sig.ReceivedAt = sig.ReceivedAt.UTC()
		return sig, err
	})
}

func (s *Store) ListTransitions(ctx context.Context, expectationID uuid.UUID) ([]domain.Transition, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, expectation_id, from_status, to_status, reason, at FROM transitions
		WHERE expectation_id = $1 ORDER BY at DESC LIMIT 100`, expectationID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Transition, error) {
		var tr domain.Transition
		err := r.Scan(&tr.ID, &tr.ExpectationID, &tr.From, &tr.To, &tr.Reason, &tr.At)
		tr.At = tr.At.UTC()
		return tr, err
	})
}

func (s *Store) ListDeliveries(ctx context.Context, expectationID uuid.UUID) ([]domain.Delivery, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, expectation_id, transition_id, project_id, outlet_id, status, attempt_count, last_error, payload, created_at, sent_at
		FROM deliveries WHERE expectation_id = $1 ORDER BY created_at DESC LIMIT 200`, expectationID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Delivery, error) {
		var d domain.Delivery
		var payload []byte
		if err := r.Scan(&d.ID, &d.ExpectationID, &d.TransitionID, &d.ProjectID, &d.OutletID, &d.Status,
			&d.AttemptCount, &d.LastError, &payload, &d.CreatedAt, &d.SentAt); err != nil {
			return d, err
		}
		d.CreatedAt, d.SentAt = d.CreatedAt.UTC(), utcPtr(d.SentAt)
		return d, json.Unmarshal(payload, &d.Notice)
	})
}

func replaceOutlets(ctx context.Context, tx pgx.Tx, expectationID uuid.UUID, ids []uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM expectation_outlets WHERE expectation_id = $1`, expectationID); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `INSERT INTO expectation_outlets (expectation_id, outlet_id) VALUES ($1,$2)`, expectationID, id); err != nil {
			return err
		}
	}
	return nil
}

func outletIDs(ctx context.Context, db dbtx, expectationID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := db.Query(ctx, `
		SELECT eo.outlet_id FROM expectation_outlets eo JOIN outlets o ON o.id = eo.outlet_id
		WHERE eo.expectation_id = $1 ORDER BY o.name`, expectationID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

func attachOutlets(ctx context.Context, db dbtx, items []domain.Expectation) error {
	for i := range items {
		ids, err := outletIDs(ctx, db, items[i].ID)
		if err != nil {
			return err
		}
		items[i].OutletIDs = ids
	}
	return nil
}

func insertTransition(ctx context.Context, db dbtx, tr domain.Transition) error {
	_, err := db.Exec(ctx, `
		INSERT INTO transitions (id, expectation_id, from_status, to_status, reason, at) VALUES ($1,$2,$3,$4,$5,$6)`,
		tr.ID, tr.ExpectationID, tr.From, tr.To, tr.Reason, tr.At)
	return err
}

func insertDelivery(ctx context.Context, db dbtx, d domain.Delivery) error {
	payload, err := json.Marshal(d.Notice)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
		INSERT INTO deliveries (id, expectation_id, transition_id, project_id, outlet_id, status, payload, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		d.ID, d.ExpectationID, d.TransitionID, d.ProjectID, d.OutletID, d.Status, payload, d.CreatedAt)
	return err
}
