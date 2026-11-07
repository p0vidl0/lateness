package app

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"github.com/p0vidl0/lateness/internal/domain"
	"github.com/p0vidl0/lateness/internal/secret"
)

// Profile is the account with its sign-in methods.
type Profile struct {
	Account     domain.Account
	HasPassword bool
	Identities  []domain.Identity
}

// Methods counts the ways the account can sign in.
func (p Profile) Methods() int {
	n := len(p.Identities)
	if p.HasPassword {
		n++
	}
	return n
}

func (s *Service) Profile(ctx context.Context, me domain.Account) (Profile, error) {
	hash, err := s.Store.PasswordHash(ctx, me.ID)
	if err != nil {
		return Profile{}, err
	}
	ids, err := s.Store.ListIdentities(ctx, me.ID)
	if err != nil {
		return Profile{}, err
	}
	return Profile{Account: me, HasPassword: hash != "", Identities: ids}, nil
}

func (s *Service) RenameAccount(ctx context.Context, me domain.Account, name string) error {
	name, err := cleanName(name)
	if err != nil {
		return err
	}
	return s.Store.RenameAccount(ctx, me.ID, name)
}

// ChangePassword sets or replaces the password. An existing password must be confirmed.
// An account without email (provider sign-up) gets one here, since email is the login.
// Every other session of the account is signed out.
func (s *Service) ChangePassword(ctx context.Context, me domain.Account, session, current, email, password string) error {
	hash, err := s.Store.PasswordHash(ctx, me.ID)
	if err != nil {
		return err
	}
	if hash != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return fmt.Errorf("%w: current password", ErrUnauthorized)
	}
	if len(password) < minPassword {
		return fmt.Errorf("%w: password", ErrInvalid)
	}
	email = normalizeEmail(email)
	if me.Email != "" {
		email = "" // the login stays as it is
	} else if !validEmail(email) {
		return fmt.Errorf("%w: email", ErrInvalid)
	}
	next, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := s.Store.SetPassword(ctx, me.ID, email, string(next)); err != nil {
		return err
	}
	return s.Store.DeleteOtherSessions(ctx, me.ID, secret.Hash(session))
}

// LinkIdentity attaches a provider identity to the signed-in account.
// An identity that belongs to someone else, or a second identity of the same
// provider, is ErrConflict. Linking the same identity again is a no-op.
func (s *Service) LinkIdentity(ctx context.Context, me domain.Account, id domain.Identity) error {
	owner, err := s.Store.FindAccountByIdentity(ctx, id.Provider, id.Subject)
	switch {
	case err == nil && owner.ID == me.ID:
		return nil
	case err == nil:
		return fmt.Errorf("%w: identity belongs to another account", ErrConflict)
	case !errors.Is(err, ErrNotFound):
		return err
	}
	id.Email = normalizeEmail(id.Email)
	return s.Store.AddIdentity(ctx, me.ID, id, s.Now())
}

// UnlinkIdentity removes a provider identity unless it is the last way to sign in.
func (s *Service) UnlinkIdentity(ctx context.Context, me domain.Account, provider string) error {
	p, err := s.Profile(ctx, me)
	if err != nil {
		return err
	}
	if p.Methods() <= 1 {
		return fmt.Errorf("%w: last sign-in method", ErrConflict)
	}
	return s.Store.DeleteIdentity(ctx, me.ID, provider)
}
