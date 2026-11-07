package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/p0vidl0/lateness/internal/domain"
)

// Project is the single ownership check: every project-scoped scenario starts from
// the project it returns. Someone else's or a deleted project is ErrNotFound.
func (s *Service) Project(ctx context.Context, me domain.Account, id uuid.UUID) (domain.Project, error) {
	return s.Store.GetProject(ctx, me.ID, id)
}

func (s *Service) Projects(ctx context.Context, me domain.Account) ([]domain.Project, error) {
	return s.Store.ListProjects(ctx, me.ID)
}

func (s *Service) CreateProject(ctx context.Context, me domain.Account, name string) (domain.Project, error) {
	name, err := cleanName(name)
	if err != nil {
		return domain.Project{}, err
	}
	p := domain.Project{ID: uuid.New(), AccountID: me.ID, Name: name, CreatedAt: s.Now()}
	if err := s.Store.CreateProject(ctx, p); err != nil {
		return domain.Project{}, err
	}
	return p, nil
}

func (s *Service) RenameProject(ctx context.Context, me domain.Account, id uuid.UUID, name string) error {
	name, err := cleanName(name)
	if err != nil {
		return err
	}
	return s.Store.RenameProject(ctx, me.ID, id, name)
}

func (s *Service) DeleteProject(ctx context.Context, me domain.Account, id uuid.UUID) error {
	return s.Store.DeleteProject(ctx, me.ID, id, s.Now())
}

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > domain.MaxName {
		return "", fmt.Errorf("%w: name", ErrInvalid)
	}
	return name, nil
}
