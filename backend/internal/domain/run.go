package domain

import "time"

// ArmRun starts or extends an in-flight run window using the schedule slack.
func ArmRun(exp *Expectation, now time.Time) {
	exp.BegunAt = &now
	deadline := now.Add(exp.Schedule.Slack)
	exp.RunDeadline = &deadline
}

// ClearRun drops an armed run without changing the rhythm window.
func ClearRun(exp *Expectation) {
	exp.BegunAt = nil
	exp.RunDeadline = nil
}

// CloseReason picks window_closed versus begin_expired when a due expectation is closed.
func CloseReason(deadline time.Time, runDeadline *time.Time, now time.Time) (Reason, bool) {
	windowDue := !now.Before(deadline)
	runDue := runDeadline != nil && !now.Before(*runDeadline)
	if !windowDue && !runDue {
		return "", false
	}
	if runDue && (!windowDue || !runDeadline.After(deadline)) {
		return ReasonBeginExpired, true
	}
	return ReasonWindowClosed, true
}
