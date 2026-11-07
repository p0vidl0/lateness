package migrate

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed sql/*.sql
var files embed.FS

const Version = 1

var (
	ErrNotReady = errors.New("schema not ready")
	ErrMismatch = errors.New("schema version mismatch")
)

func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	names, err := fs.Glob(files, "sql/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		version, err := versionOf(name)
		if err != nil {
			return err
		}
		body, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		if err := applyOne(ctx, pool, version, string(body)); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return Require(ctx, pool)
}

func Require(ctx context.Context, pool *pgxpool.Pool) error {
	var got int
	err := pool.QueryRow(ctx, `SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&got)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: no schema version", ErrNotReady)
		}
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "42P01" {
			return fmt.Errorf("%w: %s", ErrNotReady, pg.Message)
		}
		return err
	}
	if got != Version {
		return fmt.Errorf("%w: have %d want %d", ErrMismatch, got, Version)
	}
	return nil
}

func applyOne(ctx context.Context, pool *pgxpool.Pool, version int, body string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var rel *string
	if err := tx.QueryRow(ctx, `SELECT to_regclass('public.schema_migrations')`).Scan(&rel); err != nil {
		return err
	}
	if rel != nil {
		var already int
		err := tx.QueryRow(ctx, `SELECT 1 FROM schema_migrations WHERE version = $1`, version).Scan(&already)
		if err == nil {
			return tx.Commit(ctx)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if version > 1 {
			var prev int
			err = tx.QueryRow(ctx, `SELECT 1 FROM schema_migrations WHERE version = $1`, version-1).Scan(&prev)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: missing version %d", ErrNotReady, version-1)
			}
			if err != nil {
				return err
			}
		}
	}

	if _, err := tx.Exec(ctx, body); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)`, version, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func versionOf(name string) (int, error) {
	base := name[strings.LastIndex(name, "/")+1:]
	part, _, ok := strings.Cut(base, "_")
	if !ok {
		return 0, fmt.Errorf("migration name %s", name)
	}
	n, err := strconv.Atoi(part)
	if err != nil {
		return 0, fmt.Errorf("migration name %s: %w", name, err)
	}
	return n, nil
}
