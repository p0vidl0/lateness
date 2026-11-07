package app

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/p0vidl0/lateness/internal/domain"
	"github.com/p0vidl0/lateness/internal/secret"
)

const (
	sessionBytes     = 32
	inviteBytes      = 24
	minPassword      = 8
	inviteTTL        = 7 * 24 * time.Hour
	firstProjectName = "Мой проект"
)

// AuthProvider is an external sign-in (VK ID now, generic OIDC later):
// authorization code flow with PKCE.
type AuthProvider interface {
	ID() string
	Label() string
	AuthURL(state, challenge, redirectURI string) string
	// Exchange trades the callback code for the user's identity. extra carries
	// provider-specific callback parameters (VK sends device_id).
	Exchange(ctx context.Context, code, verifier, redirectURI string, extra url.Values) (domain.Identity, error)
}

func (s *Service) Login(ctx context.Context, email, password string) (string, domain.Account, error) {
	email = normalizeEmail(email)
	account, hash, err := s.Store.FindAccountByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) || (err == nil && hash == "") {
		_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
		return "", domain.Account{}, fmt.Errorf("%w: invalid email or password", ErrUnauthorized)
	}
	if err != nil {
		return "", domain.Account{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", domain.Account{}, fmt.Errorf("%w: invalid email or password", ErrUnauthorized)
	}
	return s.startSession(ctx, account)
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.Store.DeleteSession(ctx, secret.Hash(token))
}

// Session resolves a session cookie to its account.
func (s *Service) Session(ctx context.Context, token string) (domain.Account, error) {
	if token == "" {
		return domain.Account{}, ErrUnauthorized
	}
	account, err := s.Store.FindSession(ctx, secret.Hash(token), s.Now())
	if errors.Is(err, ErrNotFound) {
		return domain.Account{}, ErrUnauthorized
	}
	return account, err
}

// RegisterWithInvite creates a password account and its first project, consuming the invite.
func (s *Service) RegisterWithInvite(ctx context.Context, inviteToken, email, password string) (string, domain.Account, error) {
	email = normalizeEmail(email)
	if !validEmail(email) {
		return "", domain.Account{}, fmt.Errorf("%w: email", ErrInvalid)
	}
	if len(password) < minPassword {
		return "", domain.Account{}, fmt.Errorf("%w: password", ErrInvalid)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", domain.Account{}, err
	}
	account, err := s.createAccount(ctx, domain.Account{Email: email}, string(hash), nil, secret.Hash(inviteToken))
	if err != nil {
		return "", domain.Account{}, err
	}
	return s.startSession(ctx, account)
}

// LoginExternal signs in through a provider identity: a known identity logs in, a matching
// email links the identity to that account, otherwise a valid invite creates an account.
func (s *Service) LoginExternal(ctx context.Context, id domain.Identity, inviteToken string) (string, domain.Account, error) {
	if id.Provider == "" || id.Subject == "" {
		return "", domain.Account{}, fmt.Errorf("%w: identity", ErrInvalid)
	}
	id.Email = normalizeEmail(id.Email)
	if !validEmail(id.Email) {
		id.Email = ""
	}
	account, err := s.Store.FindAccountByIdentity(ctx, id.Provider, id.Subject)
	if err == nil {
		return s.startSession(ctx, account)
	}
	if !errors.Is(err, ErrNotFound) {
		return "", domain.Account{}, err
	}
	if id.Email != "" {
		account, _, err = s.Store.FindAccountByEmail(ctx, id.Email)
		if err == nil {
			if err := s.Store.AddIdentity(ctx, account.ID, id, s.Now()); err != nil {
				return "", domain.Account{}, err
			}
			return s.startSession(ctx, account)
		}
		if !errors.Is(err, ErrNotFound) {
			return "", domain.Account{}, err
		}
	}
	if inviteToken == "" {
		return "", domain.Account{}, ErrInviteRequired
	}
	account, err = s.createAccount(ctx, domain.Account{Email: id.Email, Name: strings.TrimSpace(id.Name)}, "", &id, secret.Hash(inviteToken))
	if err != nil {
		return "", domain.Account{}, err
	}
	return s.startSession(ctx, account)
}

func (s *Service) createAccount(ctx context.Context, account domain.Account, passwordHash string, id *domain.Identity, inviteHash string) (domain.Account, error) {
	now := s.Now()
	account.ID = uuid.New()
	account.CreatedAt = now
	return s.Store.CreateAccount(ctx, NewAccount{
		Account:      account,
		PasswordHash: passwordHash,
		Identity:     id,
		Project:      domain.Project{ID: uuid.New(), AccountID: account.ID, Name: firstProjectName, CreatedAt: now},
		InviteHash:   inviteHash,
	})
}

func (s *Service) startSession(ctx context.Context, account domain.Account) (string, domain.Account, error) {
	plain, err := secret.Hex(sessionBytes)
	if err != nil {
		return "", domain.Account{}, err
	}
	now := s.Now()
	ttl := s.SessionTTL
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	if err := s.Store.CreateSession(ctx, account.ID, secret.Hash(plain), now.Add(ttl), now); err != nil {
		return "", domain.Account{}, err
	}
	return plain, account, nil
}

// Invites

// IssueInvite creates a single-use invite. by is nil when issued from the CLI.
func (s *Service) IssueInvite(ctx context.Context, by *domain.Account, note string, admin bool) (domain.Invite, string, error) {
	if by != nil && !by.Admin {
		return domain.Invite{}, "", ErrForbidden
	}
	note = strings.TrimSpace(note)
	if len(note) > domain.MaxName {
		return domain.Invite{}, "", fmt.Errorf("%w: note", ErrInvalid)
	}
	plain, err := secret.Hex(inviteBytes)
	if err != nil {
		return domain.Invite{}, "", err
	}
	now := s.Now()
	inv := domain.Invite{ID: uuid.New(), Note: note, Admin: admin, CreatedAt: now, ExpiresAt: now.Add(inviteTTL)}
	if by != nil {
		inv.CreatedBy = &by.ID
	}
	if err := s.Store.CreateInvite(ctx, inv, secret.Hash(plain)); err != nil {
		return domain.Invite{}, "", err
	}
	return inv, plain, nil
}

// Invite looks an invite up by its token, for the sign-up page.
func (s *Service) Invite(ctx context.Context, token string) (domain.Invite, error) {
	if token == "" {
		return domain.Invite{}, ErrNotFound
	}
	return s.Store.FindInvite(ctx, secret.Hash(token))
}

func (s *Service) ListInvites(ctx context.Context, me domain.Account) ([]domain.Invite, error) {
	if !me.Admin {
		return nil, ErrForbidden
	}
	return s.Store.ListInvites(ctx)
}

func (s *Service) RevokeInvite(ctx context.Context, me domain.Account, id uuid.UUID) error {
	if !me.Admin {
		return ErrForbidden
	}
	return s.Store.RevokeInvite(ctx, id, s.Now())
}

// Seed

// Seed makes sure the dev operator account exists, is an admin, and has an SMTP outlet.
type Seed struct {
	Email    string
	Password string
	SMTP     *domain.SMTPSettings
}

func (s *Service) Seed(ctx context.Context, seed Seed) error {
	if seed.Email == "" {
		return nil
	}
	if len(seed.Password) < minPassword {
		return fmt.Errorf("%w: seed password", ErrInvalid)
	}
	email := normalizeEmail(seed.Email)
	account, _, err := s.Store.FindAccountByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		hash, herr := bcrypt.GenerateFromPassword([]byte(seed.Password), bcrypt.DefaultCost)
		if herr != nil {
			return herr
		}
		account, err = s.createAccount(ctx, domain.Account{Email: email, Admin: true}, string(hash), nil, "")
	}
	if err != nil {
		return err
	}
	if !account.Admin {
		if err := s.Store.SetAdmin(ctx, account.ID, true); err != nil {
			return err
		}
	}
	if seed.SMTP == nil {
		return nil
	}
	projects, err := s.Store.ListProjects(ctx, account.ID)
	if err != nil || len(projects) == 0 {
		return err
	}
	outlets, err := s.Store.ListOutlets(ctx, projects[0].ID)
	if err != nil || len(outlets) > 0 {
		return err
	}
	smtp := *seed.SMTP
	_, err = s.SaveOutlet(ctx, projects[0], domain.Outlet{Name: "Dev SMTP", Kind: domain.OutletSMTP, Settings: domain.OutletSettings{SMTP: &smtp}})
	return err
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func validEmail(email string) bool {
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return false
	}
	host := email[strings.LastIndex(email, "@")+1:]
	return strings.Contains(host, ".") || host == "localhost"
}

var dummyHash = func() string {
	hash, err := bcrypt.GenerateFromPassword([]byte("lateness-dummy-password"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return string(hash)
}()
