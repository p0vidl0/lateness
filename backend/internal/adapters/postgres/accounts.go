package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/p0vidl0/lateness/internal/app"
	"github.com/p0vidl0/lateness/internal/domain"
)

const accountCols = `a.id, a.email, a.name, a.is_admin, a.created_at`

func scanAccount(row pgx.Row, extra ...any) (domain.Account, error) {
	var a domain.Account
	var email *string
	if err := row.Scan(append([]any{&a.ID, &email, &a.Name, &a.Admin, &a.CreatedAt}, extra...)...); err != nil {
		return domain.Account{}, mapErr(err)
	}
	a.Email = deref(email)
	a.CreatedAt = a.CreatedAt.UTC()
	return a, nil
}

func (s *Store) CreateAccount(ctx context.Context, n app.NewAccount) (domain.Account, error) {
	a := n.Account
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if n.InviteHash != "" {
			// Lock the invite so two sign-ups cannot both use it.
			err := tx.QueryRow(ctx, `
				SELECT is_admin FROM invites
				WHERE token_hash = $1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > $2
				FOR UPDATE`, n.InviteHash, a.CreatedAt).Scan(&a.Admin)
			if errors.Is(err, pgx.ErrNoRows) {
				return app.ErrInviteUnusable
			}
			if err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO accounts (id, email, password_hash, name, is_admin, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
			a.ID, nullString(a.Email), nullString(n.PasswordHash), a.Name, a.Admin, a.CreatedAt); err != nil {
			return mapErr(err)
		}
		if n.Identity != nil {
			if err := insertIdentity(ctx, tx, a.ID, *n.Identity, a.CreatedAt); err != nil {
				return err
			}
		}
		if n.InviteHash != "" {
			if _, err := tx.Exec(ctx, `UPDATE invites SET used_at = $2, used_by = $3 WHERE token_hash = $1`,
				n.InviteHash, a.CreatedAt, a.ID); err != nil {
				return err
			}
		}
		return insertProject(ctx, tx, n.Project)
	})
	if err != nil {
		return domain.Account{}, err
	}
	return a, nil
}

func (s *Store) FindAccountByEmail(ctx context.Context, email string) (domain.Account, string, error) {
	var hash *string
	a, err := scanAccount(s.pool.QueryRow(ctx, `SELECT `+accountCols+`, a.password_hash FROM accounts a WHERE a.email = $1`, email), &hash)
	return a, deref(hash), err
}

func (s *Store) FindAccountByIdentity(ctx context.Context, provider, subject string) (domain.Account, error) {
	return scanAccount(s.pool.QueryRow(ctx, `
		SELECT `+accountCols+` FROM identities i JOIN accounts a ON a.id = i.account_id
		WHERE i.provider = $1 AND i.subject = $2`, provider, subject))
}

func (s *Store) AddIdentity(ctx context.Context, accountID uuid.UUID, id domain.Identity, now time.Time) error {
	return insertIdentity(ctx, s.pool, accountID, id, now)
}

func insertIdentity(ctx context.Context, db dbtx, accountID uuid.UUID, id domain.Identity, now time.Time) error {
	_, err := db.Exec(ctx, `
		INSERT INTO identities (provider, subject, account_id, email, created_at) VALUES ($1,$2,$3,$4,$5)`,
		id.Provider, id.Subject, accountID, id.Email, now)
	return mapErr(err)
}

func (s *Store) SetAdmin(ctx context.Context, accountID uuid.UUID, admin bool) error {
	return affected(s.pool.Exec(ctx, `UPDATE accounts SET is_admin = $2 WHERE id = $1`, accountID, admin))
}

func (s *Store) CreateSession(ctx context.Context, accountID uuid.UUID, tokenHash string, expires, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, account_id, expires_at, created_at) VALUES ($1,$2,$3,$4)`,
		tokenHash, accountID, expires, now)
	return mapErr(err)
}

func (s *Store) FindSession(ctx context.Context, tokenHash string, now time.Time) (domain.Account, error) {
	return scanAccount(s.pool.QueryRow(ctx, `
		SELECT `+accountCols+` FROM sessions s JOIN accounts a ON a.id = s.account_id
		WHERE s.token_hash = $1 AND s.expires_at > $2`, tokenHash, now))
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// Invites

const inviteCols = `id, note, is_admin, created_by, created_at, expires_at, used_at, used_by, revoked_at`

func scanInvite(row pgx.Row) (domain.Invite, error) {
	var i domain.Invite
	if err := row.Scan(&i.ID, &i.Note, &i.Admin, &i.CreatedBy, &i.CreatedAt, &i.ExpiresAt, &i.UsedAt, &i.UsedBy, &i.RevokedAt); err != nil {
		return domain.Invite{}, mapErr(err)
	}
	i.CreatedAt, i.ExpiresAt = i.CreatedAt.UTC(), i.ExpiresAt.UTC()
	i.UsedAt, i.RevokedAt = utcPtr(i.UsedAt), utcPtr(i.RevokedAt)
	return i, nil
}

func (s *Store) CreateInvite(ctx context.Context, i domain.Invite, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO invites (id, token_hash, note, is_admin, created_by, created_at, expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		i.ID, tokenHash, i.Note, i.Admin, i.CreatedBy, i.CreatedAt, i.ExpiresAt)
	return mapErr(err)
}

func (s *Store) FindInvite(ctx context.Context, tokenHash string) (domain.Invite, error) {
	return scanInvite(s.pool.QueryRow(ctx, `SELECT `+inviteCols+` FROM invites WHERE token_hash = $1`, tokenHash))
}

func (s *Store) ListInvites(ctx context.Context) ([]domain.Invite, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+inviteCols+` FROM invites ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Invite, error) { return scanInvite(r) })
}

func (s *Store) RevokeInvite(ctx context.Context, id uuid.UUID, now time.Time) error {
	return affected(s.pool.Exec(ctx, `
		UPDATE invites SET revoked_at = $2 WHERE id = $1 AND used_at IS NULL AND revoked_at IS NULL`, id, now))
}

func (s *Store) RenameAccount(ctx context.Context, accountID uuid.UUID, name string) error {
	return affected(s.pool.Exec(ctx, `UPDATE accounts SET name = $2 WHERE id = $1`, accountID, name))
}

func (s *Store) PasswordHash(ctx context.Context, accountID uuid.UUID) (string, error) {
	var hash *string
	if err := s.pool.QueryRow(ctx, `SELECT password_hash FROM accounts WHERE id = $1`, accountID).Scan(&hash); err != nil {
		return "", mapErr(err)
	}
	return deref(hash), nil
}

func (s *Store) SetPassword(ctx context.Context, accountID uuid.UUID, email, passwordHash string) error {
	return affected(s.pool.Exec(ctx, `
		UPDATE accounts SET password_hash = $2, email = COALESCE($3, email) WHERE id = $1`,
		accountID, passwordHash, nullString(email)))
}

func (s *Store) ListIdentities(ctx context.Context, accountID uuid.UUID) ([]domain.Identity, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT provider, subject, email FROM identities WHERE account_id = $1 ORDER BY provider`, accountID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Identity, error) {
		var id domain.Identity
		err := r.Scan(&id.Provider, &id.Subject, &id.Email)
		return id, err
	})
}

// DeleteIdentity removes a sign-in link. It is a credential, not user data, so it is
// deleted for real: the same identity can be linked again later.
func (s *Store) DeleteIdentity(ctx context.Context, accountID uuid.UUID, provider string) error {
	return affected(s.pool.Exec(ctx, `DELETE FROM identities WHERE account_id = $1 AND provider = $2`, accountID, provider))
}

func (s *Store) DeleteOtherSessions(ctx context.Context, accountID uuid.UUID, keepHash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE account_id = $1 AND token_hash <> $2`, accountID, keepHash)
	return err
}
