package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/p0vidl0/lateness/internal/domain"
	"github.com/p0vidl0/lateness/internal/schedule"
	"github.com/p0vidl0/lateness/internal/secret"
)

// ExpectationInput is what a user edits. Source is fixed after creation.
type ExpectationInput struct {
	Name      string
	Schedule  domain.Schedule
	Source    domain.Source
	Probe     *domain.Probe
	OutletIDs []uuid.UUID
}

func (s *Service) CreateExpectation(ctx context.Context, proj domain.Project, in ExpectationInput) (domain.Expectation, error) {
	now := s.Now()
	exp := domain.Expectation{
		ID:        uuid.New(),
		ProjectID: proj.ID,
		Source:    in.Source,
		Status:    domain.StatusQuiet,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.applyInput(ctx, proj, &exp, in); err != nil {
		return domain.Expectation{}, err
	}
	switch exp.Source {
	case domain.SourceIntake:
		token, err := secret.Hex(32)
		if err != nil {
			return domain.Expectation{}, err
		}
		exp.IntakeToken = token
	case domain.SourceProbe:
	default:
		return domain.Expectation{}, fmt.Errorf("%w: source", ErrInvalid)
	}
	if err := openWindow(&exp, now, true); err != nil {
		return domain.Expectation{}, err
	}
	if err := s.Store.CreateExpectation(ctx, exp); err != nil {
		return domain.Expectation{}, err
	}
	return exp, nil
}

func (s *Service) UpdateExpectation(ctx context.Context, proj domain.Project, id uuid.UUID, in ExpectationInput) (domain.Expectation, error) {
	exp, err := s.Store.GetExpectation(ctx, proj.ID, id)
	if err != nil {
		return domain.Expectation{}, err
	}
	if err := s.applyInput(ctx, proj, &exp, in); err != nil {
		return domain.Expectation{}, err
	}
	now := s.Now()
	if exp.Status != domain.StatusHeld {
		deadline, err := schedule.Deadline(exp.Schedule, exp.WindowAnchor)
		if err != nil {
			return domain.Expectation{}, invalid(err)
		}
		exp.Deadline = deadline
		if exp.Source == domain.SourceProbe {
			exp.ProbeAt = &now // settings may have changed: look again right away
		}
	}
	if exp.BegunAt != nil {
		runDeadline := exp.BegunAt.Add(exp.Schedule.Slack)
		exp.RunDeadline = &runDeadline
	}
	exp.UpdatedAt = now
	if err := s.Store.Commit(ctx, Change{Expectation: &exp, Outlets: true}); err != nil {
		return domain.Expectation{}, err
	}
	return exp, nil
}

// applyInput validates the editable fields and copies them onto exp.
func (s *Service) applyInput(ctx context.Context, proj domain.Project, exp *domain.Expectation, in ExpectationInput) error {
	name, err := cleanName(in.Name)
	if err != nil {
		return err
	}
	if err := schedule.Validate(in.Schedule); err != nil {
		return invalid(err)
	}
	if exp.Source == domain.SourceProbe {
		if in.Probe == nil {
			return fmt.Errorf("%w: probe", ErrInvalid)
		}
		probe := *in.Probe
		if err := probe.Validate(); err != nil {
			return invalid(err)
		}
		exp.Probe = &probe
	}
	outletIDs, err := s.projectOutletIDs(ctx, proj, in.OutletIDs)
	if err != nil {
		return err
	}
	exp.Name, exp.Schedule, exp.OutletIDs = name, in.Schedule, outletIDs
	return nil
}

// projectOutletIDs drops duplicates and refuses outlets outside the project.
func (s *Service) projectOutletIDs(ctx context.Context, proj domain.Project, requested []uuid.UUID) ([]uuid.UUID, error) {
	if len(requested) == 0 {
		return nil, nil
	}
	outlets, err := s.Store.ListOutlets(ctx, proj.ID)
	if err != nil {
		return nil, err
	}
	known := make(map[uuid.UUID]bool, len(outlets))
	for _, o := range outlets {
		known[o.ID] = true
	}
	seen := make(map[uuid.UUID]bool, len(requested))
	out := make([]uuid.UUID, 0, len(requested))
	for _, id := range requested {
		if !known[id] {
			return nil, fmt.Errorf("%w: outlet", ErrInvalid)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func (s *Service) DeleteExpectation(ctx context.Context, proj domain.Project, id uuid.UUID) error {
	return s.Store.DeleteExpectation(ctx, proj.ID, id, s.Now())
}

func (s *Service) ListExpectations(ctx context.Context, proj domain.Project) ([]domain.Expectation, error) {
	return s.Store.ListExpectations(ctx, proj.ID)
}

func (s *Service) GetExpectation(ctx context.Context, proj domain.Project, id uuid.UUID) (domain.Expectation, error) {
	return s.Store.GetExpectation(ctx, proj.ID, id)
}

// Journal is everything the expectation card shows below the header.
type Journal struct {
	Signals     []domain.Signal
	Transitions []domain.Transition
	Deliveries  []domain.Delivery
}

func (s *Service) Journal(ctx context.Context, exp domain.Expectation) (Journal, error) {
	var j Journal
	var err error
	if j.Signals, err = s.Store.ListSignals(ctx, exp.ID); err != nil {
		return j, err
	}
	if j.Transitions, err = s.Store.ListTransitions(ctx, exp.ID); err != nil {
		return j, err
	}
	j.Deliveries, err = s.Store.ListDeliveries(ctx, exp.ID)
	return j, err
}

func (s *Service) Hold(ctx context.Context, proj domain.Project, id uuid.UUID) error {
	exp, err := s.Store.GetExpectation(ctx, proj.ID, id)
	if err != nil {
		return err
	}
	next, err := domain.Hold(exp.Status)
	if err != nil {
		return conflict(err)
	}
	domain.ClearRun(&exp)
	return s.changeStatus(ctx, exp, next, domain.ReasonHold)
}

func (s *Service) Resume(ctx context.Context, proj domain.Project, id uuid.UUID) error {
	exp, err := s.Store.GetExpectation(ctx, proj.ID, id)
	if err != nil {
		return err
	}
	next, err := domain.Resume(exp.Status, exp.LastOK != nil)
	if err != nil {
		return conflict(err)
	}
	domain.ClearRun(&exp)
	if err := openWindow(&exp, s.Now(), true); err != nil {
		return err
	}
	return s.changeStatus(ctx, exp, next, domain.ReasonResume)
}

func (s *Service) changeStatus(ctx context.Context, exp domain.Expectation, next domain.Status, reason domain.Reason) error {
	now := s.Now()
	tr := domain.Transition{ID: uuid.New(), ExpectationID: exp.ID, From: exp.Status, To: next, Reason: reason, At: now}
	exp.Status = next
	exp.UpdatedAt = now
	return s.Store.Commit(ctx, Change{Expectation: &exp, Transition: &tr})
}

// openWindow starts a new rhythm window at "at". A probe runs at the next scheduled
// instant, or right away when probeNow is set (new, resumed or edited expectation).
func openWindow(exp *domain.Expectation, at time.Time, probeNow bool) error {
	next, err := schedule.Next(exp.Schedule, at)
	if err != nil {
		return invalid(err)
	}
	exp.WindowAnchor = at
	exp.Deadline = next.Add(exp.Schedule.Slack)
	if exp.Source == domain.SourceProbe {
		if probeNow {
			next = at
		}
		exp.ProbeAt = &next
	}
	return nil
}
