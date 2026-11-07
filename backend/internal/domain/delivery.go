package domain

import (
	"time"

	"github.com/google/uuid"
)

// PendingDeliveries builds one pending delivery row per assigned outlet, or a single
// row without an outlet when nothing is assigned, so the miss stays visible.
func PendingDeliveries(exp Expectation, tr Transition, now time.Time) []Delivery {
	notice := Notice{
		ExpectationID: exp.ID,
		Name:          exp.Name,
		From:          tr.From,
		To:            tr.To,
		Reason:        tr.Reason,
		At:            tr.At,
	}
	base := Delivery{
		ExpectationID: exp.ID,
		TransitionID:  tr.ID,
		ProjectID:     exp.ProjectID,
		Status:        DeliveryPending,
		Notice:        notice,
		CreatedAt:     now,
	}
	if len(exp.OutletIDs) == 0 {
		base.ID = uuid.New()
		return []Delivery{base}
	}
	out := make([]Delivery, 0, len(exp.OutletIDs))
	for _, outletID := range exp.OutletIDs {
		d := base
		d.ID = uuid.New()
		id := outletID
		d.OutletID = &id
		out = append(out, d)
	}
	return out
}

// NotifyTransition reports whether a status change should create outbound deliveries.
func NotifyTransition(from, to Status, reason Reason) bool {
	if to == StatusLate {
		return true
	}
	return from == StatusLate && to == StatusOnTime && reason == ReasonSignalOK
}
