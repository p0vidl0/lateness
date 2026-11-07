package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/p0vidl0/lateness/internal/domain"
)

const projectCols = `id, account_id, name, created_at`

func scanProject(row pgx.Row) (domain.Project, error) {
	var p domain.Project
	if err := row.Scan(&p.ID, &p.AccountID, &p.Name, &p.CreatedAt); err != nil {
		return domain.Project{}, mapErr(err)
	}
	p.CreatedAt = p.CreatedAt.UTC()
	return p, nil
}

func insertProject(ctx context.Context, db dbtx, p domain.Project) error {
	_, err := db.Exec(ctx, `INSERT INTO projects (id, account_id, name, created_at) VALUES ($1,$2,$3,$4)`,
		p.ID, p.AccountID, p.Name, p.CreatedAt)
	return mapErr(err)
}

func (s *Store) CreateProject(ctx context.Context, p domain.Project) error {
	return insertProject(ctx, s.pool, p)
}

func (s *Store) ListProjects(ctx context.Context, accountID uuid.UUID) ([]domain.Project, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+projectCols+` FROM projects WHERE account_id = $1 AND deleted_at IS NULL ORDER BY created_at`, accountID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Project, error) { return scanProject(r) })
}

func (s *Store) GetProject(ctx context.Context, accountID, id uuid.UUID) (domain.Project, error) {
	return scanProject(s.pool.QueryRow(ctx, `
		SELECT `+projectCols+` FROM projects WHERE account_id = $1 AND id = $2 AND deleted_at IS NULL`, accountID, id))
}

func (s *Store) RenameProject(ctx context.Context, accountID, id uuid.UUID, name string) error {
	return affected(s.pool.Exec(ctx, `
		UPDATE projects SET name = $3 WHERE account_id = $1 AND id = $2 AND deleted_at IS NULL`, accountID, id, name))
}

// DeleteProject marks the project and everything in it, so readers only ever
// check deleted_at on their own table.
func (s *Store) DeleteProject(ctx context.Context, accountID, id uuid.UUID, now time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := affected(tx.Exec(ctx, `
			UPDATE projects SET deleted_at = $3 WHERE account_id = $1 AND id = $2 AND deleted_at IS NULL`,
			accountID, id, now)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE expectations SET deleted_at = $2, version = version + 1 WHERE project_id = $1 AND deleted_at IS NULL`, id, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE outlets SET deleted_at = $2 WHERE project_id = $1 AND deleted_at IS NULL`, id, now)
		return err
	})
}
