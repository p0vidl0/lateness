package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/p0vidl0/lateness/internal/domain"
)

// NewAccount is everything created together with an account, in one transaction.
type NewAccount struct {
	Account      domain.Account
	PasswordHash string           // empty for provider-only accounts
	Identity     *domain.Identity // set when signing up through a provider
	Project      domain.Project   // the first project
	InviteHash   string           // consumed atomically; empty only for the seed account
}

// Change is one atomic write to an expectation and its journal.
type Change struct {
	// Expectation, when set, is written with an optimistic check on Version.
	// A concurrent write makes Commit return ErrConflict.
	Expectation *domain.Expectation
	Outlets     bool // also replace the outlet assignment from Expectation.OutletIDs
	Signal      *domain.Signal
	Transition  *domain.Transition
	Deliveries  []domain.Delivery
}

type Store interface {
	CreateAccount(ctx context.Context, n NewAccount) (domain.Account, error)
	FindAccountByEmail(ctx context.Context, email string) (domain.Account, string, error)
	FindAccountByIdentity(ctx context.Context, provider, subject string) (domain.Account, error)
	AddIdentity(ctx context.Context, accountID uuid.UUID, id domain.Identity, now time.Time) error
	SetAdmin(ctx context.Context, accountID uuid.UUID, admin bool) error
	RenameAccount(ctx context.Context, accountID uuid.UUID, name string) error
	PasswordHash(ctx context.Context, accountID uuid.UUID) (string, error)
	// SetPassword stores a new hash and, when email is not empty, the account's email.
	SetPassword(ctx context.Context, accountID uuid.UUID, email, passwordHash string) error
	ListIdentities(ctx context.Context, accountID uuid.UUID) ([]domain.Identity, error)
	DeleteIdentity(ctx context.Context, accountID uuid.UUID, provider string) error
	CreateSession(ctx context.Context, accountID uuid.UUID, tokenHash string, expires, now time.Time) error
	FindSession(ctx context.Context, tokenHash string, now time.Time) (domain.Account, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	// DeleteOtherSessions signs the account out everywhere except the session keepHash.
	DeleteOtherSessions(ctx context.Context, accountID uuid.UUID, keepHash string) error

	CreateInvite(ctx context.Context, inv domain.Invite, tokenHash string) error
	FindInvite(ctx context.Context, tokenHash string) (domain.Invite, error)
	ListInvites(ctx context.Context) ([]domain.Invite, error)
	RevokeInvite(ctx context.Context, id uuid.UUID, now time.Time) error

	CreateProject(ctx context.Context, p domain.Project) error
	ListProjects(ctx context.Context, accountID uuid.UUID) ([]domain.Project, error)
	GetProject(ctx context.Context, accountID, id uuid.UUID) (domain.Project, error)
	RenameProject(ctx context.Context, accountID, id uuid.UUID, name string) error
	// DeleteProject hides the project with its expectations and outlets.
	DeleteProject(ctx context.Context, accountID, id uuid.UUID, now time.Time) error

	CreateExpectation(ctx context.Context, exp domain.Expectation) error
	GetExpectation(ctx context.Context, projectID, id uuid.UUID) (domain.Expectation, error)
	GetByIntakeToken(ctx context.Context, token string) (domain.Expectation, error)
	ListExpectations(ctx context.Context, projectID uuid.UUID) ([]domain.Expectation, error)
	DeleteExpectation(ctx context.Context, projectID, id uuid.UUID, now time.Time) error
	Commit(ctx context.Context, ch Change) error
	ListSignals(ctx context.Context, expectationID uuid.UUID) ([]domain.Signal, error)
	ListTransitions(ctx context.Context, expectationID uuid.UUID) ([]domain.Transition, error)
	ListDeliveries(ctx context.Context, expectationID uuid.UUID) ([]domain.Delivery, error)

	ListOutlets(ctx context.Context, projectID uuid.UUID) ([]domain.Outlet, error)
	GetOutlet(ctx context.Context, projectID, id uuid.UUID) (domain.Outlet, error)
	CreateOutlet(ctx context.Context, o domain.Outlet) error
	UpdateOutlet(ctx context.Context, o domain.Outlet) error
	// DeleteOutlet returns ErrConflict while an active expectation uses the outlet.
	DeleteOutlet(ctx context.Context, projectID, id uuid.UUID, now time.Time) error
	OutletUsage(ctx context.Context, projectID uuid.UUID) (map[uuid.UUID]int, error)

	// ClaimProbes returns due probe expectations and moves their probe_at by lease,
	// so another sweep does not run the same probe meanwhile.
	ClaimProbes(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]domain.Expectation, error)
	CloseDue(ctx context.Context, now time.Time, limit int) (int, error)
	ClaimDeliveries(ctx context.Context, now time.Time, limit int) ([]domain.Delivery, error)
	MarkDelivery(ctx context.Context, id uuid.UUID, status, lastError string, sentAt *time.Time) error
}
