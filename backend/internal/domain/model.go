package domain

import (
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusQuiet  Status = "quiet"
	StatusOnTime Status = "on_time"
	StatusLate   Status = "late"
	StatusHeld   Status = "held"
)

type Outcome string

const (
	OutcomeOK    Outcome = "ok"
	OutcomeBegin Outcome = "begin"
	OutcomeFail  Outcome = "fail"
)

type Reason string

const (
	ReasonSignalOK     Reason = "signal_ok"
	ReasonSignalFail   Reason = "signal_fail"
	ReasonWindowClosed Reason = "window_closed"
	ReasonBeginExpired Reason = "begin_expired"
	ReasonHold         Reason = "hold"
	ReasonResume       Reason = "resume"
)

type ScheduleKind string

const (
	ScheduleInterval ScheduleKind = "interval"
	ScheduleCron     ScheduleKind = "cron"
)

type Schedule struct {
	Kind     ScheduleKind
	Period   time.Duration
	Slack    time.Duration
	Cron     string
	Timezone string
}

// Source says who produces signals: the user's job through intake, or the service itself through a probe.
type Source string

const (
	SourceIntake Source = "intake"
	SourceProbe  Source = "probe"
)

const (
	DeliveryPending = "pending"
	DeliverySending = "sending"
	DeliverySent    = "sent"
	DeliveryFailed  = "failed"
)

type Account struct {
	ID        uuid.UUID
	Email     string // empty when the account only signs in through a provider without email
	Name      string
	Admin     bool
	CreatedAt time.Time
}

// Display is the label shown for the account in pages.
func (a Account) Display() string {
	if a.Email != "" {
		return a.Email
	}
	if a.Name != "" {
		return a.Name
	}
	return a.ID.String()[:8]
}

// Identity links an account to an external sign-in provider.
type Identity struct {
	Provider string
	Subject  string
	Email    string
	Name     string
}

type Invite struct {
	ID        uuid.UUID
	Note      string
	Admin     bool
	CreatedBy *uuid.UUID
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
	UsedBy    *uuid.UUID
	RevokedAt *time.Time
}

type InviteState string

const (
	InviteActive  InviteState = "active"
	InviteUsed    InviteState = "used"
	InviteExpired InviteState = "expired"
	InviteRevoked InviteState = "revoked"
)

func (i Invite) State(now time.Time) InviteState {
	switch {
	case i.RevokedAt != nil:
		return InviteRevoked
	case i.UsedAt != nil:
		return InviteUsed
	case !now.Before(i.ExpiresAt):
		return InviteExpired
	default:
		return InviteActive
	}
}

type Project struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	Name      string
	CreatedAt time.Time
}

type Expectation struct {
	ID           uuid.UUID
	ProjectID    uuid.UUID
	Name         string
	Schedule     Schedule
	Source       Source
	Probe        *Probe // set when Source is SourceProbe
	Status       Status
	IntakeToken  string // set when Source is SourceIntake
	WindowAnchor time.Time
	Deadline     time.Time
	BegunAt      *time.Time
	RunDeadline  *time.Time
	LastOK       *time.Time
	ProbeAt      *time.Time
	OutletIDs    []uuid.UUID
	Version      int // optimistic lock: every state write bumps it
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Signal struct {
	ID            uuid.UUID
	ExpectationID uuid.UUID
	Outcome       Outcome
	Note          string
	ReceivedAt    time.Time
}

type Transition struct {
	ID            uuid.UUID
	ExpectationID uuid.UUID
	From          Status
	To            Status
	Reason        Reason
	At            time.Time
}

type Notice struct {
	ExpectationID uuid.UUID `json:"expectation_id"`
	Name          string    `json:"name"`
	From          Status    `json:"from"`
	To            Status    `json:"to"`
	Reason        Reason    `json:"reason"`
	At            time.Time `json:"at"`
}

type Delivery struct {
	ID            uuid.UUID
	ExpectationID uuid.UUID
	TransitionID  uuid.UUID
	ProjectID     uuid.UUID
	OutletID      *uuid.UUID
	Status        string
	AttemptCount  int
	LastError     string
	Notice        Notice
	CreatedAt     time.Time
	SentAt        *time.Time
	Outlet        *Outlet // filled by a claim when the outlet exists
}

func ValidStatus(s Status) bool {
	switch s {
	case StatusQuiet, StatusOnTime, StatusLate, StatusHeld:
		return true
	default:
		return false
	}
}

func ValidOutcome(o Outcome) bool {
	switch o {
	case OutcomeOK, OutcomeBegin, OutcomeFail:
		return true
	default:
		return false
	}
}
