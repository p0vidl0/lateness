package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/p0vidl0/lateness/internal/domain"
)

func (s *Service) ListOutlets(ctx context.Context, proj domain.Project) ([]domain.Outlet, error) {
	return s.Store.ListOutlets(ctx, proj.ID)
}

func (s *Service) GetOutlet(ctx context.Context, proj domain.Project, id uuid.UUID) (domain.Outlet, error) {
	return s.Store.GetOutlet(ctx, proj.ID, id)
}

// OutletUsage counts active expectations per outlet of the project.
func (s *Service) OutletUsage(ctx context.Context, proj domain.Project) (map[uuid.UUID]int, error) {
	return s.Store.OutletUsage(ctx, proj.ID)
}

// SaveOutlet creates the outlet when o.ID is nil, otherwise updates it.
// Secrets left empty on update keep their stored value.
func (s *Service) SaveOutlet(ctx context.Context, proj domain.Project, o domain.Outlet) (domain.Outlet, error) {
	now := s.Now()
	o.ProjectID = proj.ID
	o.UpdatedAt = now
	if o.ID != uuid.Nil {
		prev, err := s.Store.GetOutlet(ctx, proj.ID, o.ID)
		if err != nil {
			return domain.Outlet{}, err
		}
		o.KeepSecrets(prev)
		o.CreatedAt = prev.CreatedAt
	}
	if err := o.Validate(); err != nil {
		return domain.Outlet{}, invalid(err)
	}
	if o.ID != uuid.Nil {
		return o, s.Store.UpdateOutlet(ctx, o)
	}
	o.ID = uuid.New()
	o.CreatedAt = now
	return o, s.Store.CreateOutlet(ctx, o)
}

func (s *Service) DeleteOutlet(ctx context.Context, proj domain.Project, id uuid.UUID) error {
	return s.Store.DeleteOutlet(ctx, proj.ID, id, s.Now())
}
