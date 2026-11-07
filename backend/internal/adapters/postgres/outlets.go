package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/p0vidl0/lateness/internal/app"
	"github.com/p0vidl0/lateness/internal/domain"
)

const outletCols = `id, project_id, name, kind, settings, created_at, updated_at`

func (s *Store) scanOutlet(row pgx.Row) (domain.Outlet, error) {
	var o domain.Outlet
	var sealed []byte
	if err := row.Scan(&o.ID, &o.ProjectID, &o.Name, &o.Kind, &sealed, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return domain.Outlet{}, mapErr(err)
	}
	plain, err := s.cipher.Open(sealed)
	if err != nil {
		return domain.Outlet{}, fmt.Errorf("outlet %s settings: %w", o.ID, err)
	}
	if err := json.Unmarshal(plain, &o.Settings); err != nil {
		return domain.Outlet{}, fmt.Errorf("outlet %s settings: %w", o.ID, err)
	}
	o.CreatedAt, o.UpdatedAt = o.CreatedAt.UTC(), o.UpdatedAt.UTC()
	return o, nil
}

func (s *Store) seal(settings domain.OutletSettings) ([]byte, error) {
	plain, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	return s.cipher.Seal(plain)
}

func (s *Store) ListOutlets(ctx context.Context, projectID uuid.UUID) ([]domain.Outlet, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+outletCols+` FROM outlets WHERE project_id = $1 AND deleted_at IS NULL ORDER BY name`, projectID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Outlet, error) { return s.scanOutlet(r) })
}

func (s *Store) GetOutlet(ctx context.Context, projectID, id uuid.UUID) (domain.Outlet, error) {
	return s.scanOutlet(s.pool.QueryRow(ctx, `
		SELECT `+outletCols+` FROM outlets WHERE project_id = $1 AND id = $2 AND deleted_at IS NULL`, projectID, id))
}

func (s *Store) CreateOutlet(ctx context.Context, o domain.Outlet) error {
	sealed, err := s.seal(o.Settings)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO outlets (id, project_id, name, kind, settings, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		o.ID, o.ProjectID, o.Name, o.Kind, sealed, o.CreatedAt, o.UpdatedAt)
	return mapErr(err)
}

func (s *Store) UpdateOutlet(ctx context.Context, o domain.Outlet) error {
	sealed, err := s.seal(o.Settings)
	if err != nil {
		return err
	}
	return affected(s.pool.Exec(ctx, `
		UPDATE outlets SET name = $3, kind = $4, settings = $5, updated_at = $6
		WHERE project_id = $1 AND id = $2 AND deleted_at IS NULL`,
		o.ProjectID, o.ID, o.Name, o.Kind, sealed, o.UpdatedAt))
}

func (s *Store) DeleteOutlet(ctx context.Context, projectID, id uuid.UUID, now time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var outletID uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT id FROM outlets WHERE project_id = $1 AND id = $2 AND deleted_at IS NULL FOR UPDATE`,
			projectID, id).Scan(&outletID); err != nil {
			return mapErr(err)
		}
		var used bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM expectation_outlets eo JOIN expectations e ON e.id = eo.expectation_id
			WHERE eo.outlet_id = $1 AND e.deleted_at IS NULL)`, id).Scan(&used); err != nil {
			return err
		}
		if used {
			return fmt.Errorf("%w: outlet is assigned", app.ErrConflict)
		}
		_, err := tx.Exec(ctx, `UPDATE outlets SET deleted_at = $2 WHERE id = $1`, id, now)
		return err
	})
}

func (s *Store) OutletUsage(ctx context.Context, projectID uuid.UUID) (map[uuid.UUID]int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT eo.outlet_id, COUNT(*) FROM expectation_outlets eo JOIN expectations e ON e.id = eo.expectation_id
		WHERE e.project_id = $1 AND e.deleted_at IS NULL GROUP BY eo.outlet_id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
