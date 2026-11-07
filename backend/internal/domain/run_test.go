package domain

import (
	"testing"
	"time"
)

func TestCloseReason(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 5, 0, 0, time.UTC)
	window := time.Date(2026, 1, 2, 12, 10, 0, 0, time.UTC)
	run := time.Date(2026, 1, 2, 12, 5, 0, 0, time.UTC)

	reason, ok := CloseReason(window, &run, now)
	if !ok || reason != ReasonBeginExpired {
		t.Fatalf("run only: %s %v", reason, ok)
	}

	windowDue := time.Date(2026, 1, 2, 12, 4, 0, 0, time.UTC)
	reason, ok = CloseReason(windowDue, &run, now)
	if !ok || reason != ReasonWindowClosed {
		t.Fatalf("window only: %s %v", reason, ok)
	}

	lateRun := time.Date(2026, 1, 2, 12, 11, 0, 0, time.UTC)
	reason, ok = CloseReason(windowDue, &lateRun, now)
	if !ok || reason != ReasonWindowClosed {
		t.Fatalf("window before run: %s %v", reason, ok)
	}
}

func TestNotifyTransition(t *testing.T) {
	if !NotifyTransition(StatusQuiet, StatusLate, ReasonWindowClosed) {
		t.Fatal("late should notify")
	}
	if !NotifyTransition(StatusLate, StatusOnTime, ReasonSignalOK) {
		t.Fatal("return to on_time should notify")
	}
	if NotifyTransition(StatusQuiet, StatusOnTime, ReasonSignalOK) {
		t.Fatal("first ok should stay quiet in notifications")
	}
}
