package app

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/p0vidl0/lateness/internal/domain"
)

var t0 = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func sample(source domain.Source, status domain.Status) domain.Expectation {
	exp := domain.Expectation{
		ID: uuid.New(), Source: source, Status: status, OutletIDs: []uuid.UUID{uuid.New()},
		Schedule: domain.Schedule{Kind: domain.ScheduleInterval, Period: time.Hour, Slack: 10 * time.Minute},
	}
	if source == domain.SourceProbe {
		exp.Probe = &domain.Probe{Method: "GET", URL: "https://x"}
	}
	return exp
}

func TestSignalChangeOKFromLateNotifies(t *testing.T) {
	ch := signalChange(sample(domain.SourceIntake, domain.StatusLate), domain.OutcomeOK, "", t0)
	if ch.Expectation.Status != domain.StatusOnTime || ch.Transition == nil || len(ch.Deliveries) != 1 {
		t.Fatalf("%+v", ch)
	}
	if !ch.Expectation.Deadline.Equal(t0.Add(70*time.Minute)) || !ch.Expectation.LastOK.Equal(t0) {
		t.Fatalf("window %+v", ch.Expectation)
	}
}

func TestSignalChangeHeldOnlyRecords(t *testing.T) {
	ch := signalChange(sample(domain.SourceIntake, domain.StatusHeld), domain.OutcomeFail, "x", t0)
	if ch.Signal == nil || ch.Expectation != nil || ch.Transition != nil {
		t.Fatalf("%+v", ch)
	}
}

func TestSignalChangeBeginWhileLateOnlyRecords(t *testing.T) {
	ch := signalChange(sample(domain.SourceIntake, domain.StatusLate), domain.OutcomeBegin, "", t0)
	if ch.Expectation != nil {
		t.Fatalf("%+v", ch)
	}
}

func TestProbeChange(t *testing.T) {
	ok := probeChange(sample(domain.SourceProbe, domain.StatusQuiet), domain.ProbeResult{OK: true, Note: "200"}, t0)
	if ok.Expectation.Status != domain.StatusOnTime || !ok.Expectation.ProbeAt.Equal(t0.Add(time.Hour)) || len(ok.Deliveries) != 0 {
		t.Fatalf("first success: %+v", ok.Expectation)
	}
	fail := probeChange(sample(domain.SourceProbe, domain.StatusOnTime), domain.ProbeResult{Note: "503"}, t0)
	if fail.Transition != nil || fail.Expectation.Status != domain.StatusOnTime || fail.Signal.Outcome != domain.OutcomeFail ||
		!fail.Expectation.ProbeAt.Equal(t0.Add(domain.ProbeRetry)) {
		t.Fatalf("failure: %+v", fail)
	}
}

func TestOpenWindowProbesNowOrAtNext(t *testing.T) {
	exp := sample(domain.SourceProbe, domain.StatusQuiet)
	if err := openWindow(&exp, t0, true); err != nil || !exp.ProbeAt.Equal(t0) {
		t.Fatalf("now: %v %v", exp.ProbeAt, err)
	}
	if err := openWindow(&exp, t0, false); err != nil || !exp.ProbeAt.Equal(t0.Add(time.Hour)) || !exp.Deadline.Equal(t0.Add(70*time.Minute)) {
		t.Fatalf("next: %v %v", exp.ProbeAt, err)
	}
	intake := sample(domain.SourceIntake, domain.StatusQuiet)
	if err := openWindow(&intake, t0, true); err != nil || intake.ProbeAt != nil {
		t.Fatal("intake must not get probe_at")
	}
}
