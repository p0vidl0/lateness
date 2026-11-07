package domain

import "testing"

func TestApplySignal(t *testing.T) {
	cases := []struct {
		name    string
		status  Status
		outcome Outcome
		next    Status
		reason  Reason
		changed bool
		wantErr error
	}{
		{"quiet ok", StatusQuiet, OutcomeOK, StatusOnTime, ReasonSignalOK, true, nil},
		{"late ok", StatusLate, OutcomeOK, StatusOnTime, ReasonSignalOK, true, nil},
		{"on time ok stays", StatusOnTime, OutcomeOK, StatusOnTime, "", false, nil},
		{"quiet begin", StatusQuiet, OutcomeBegin, StatusQuiet, "", false, nil},
		{"on time begin", StatusOnTime, OutcomeBegin, StatusOnTime, "", false, nil},
		{"quiet fail", StatusQuiet, OutcomeFail, StatusLate, ReasonSignalFail, true, nil},
		{"on time fail", StatusOnTime, OutcomeFail, StatusLate, ReasonSignalFail, true, nil},
		{"late fail stays", StatusLate, OutcomeFail, StatusLate, "", false, nil},
		{"held ok", StatusHeld, OutcomeOK, StatusHeld, "", false, ErrHeld},
		{"bad outcome", StatusQuiet, Outcome("start"), "", "", false, ErrInvalid},
		{"bad status", Status("down"), OutcomeOK, "", "", false, ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, reason, changed, err := ApplySignal(tc.status, tc.outcome)
			if err != tc.wantErr {
				t.Fatalf("err %v, want %v", err, tc.wantErr)
			}
			if next != tc.next || reason != tc.reason || changed != tc.changed {
				t.Fatalf("got (%s %s %v), want (%s %s %v)", next, reason, changed, tc.next, tc.reason, tc.changed)
			}
		})
	}
}

func TestCloseWindow(t *testing.T) {
	if next, ok := CloseWindow(StatusQuiet); !ok || next != StatusLate {
		t.Fatalf("quiet: %s %v", next, ok)
	}
	if next, ok := CloseWindow(StatusOnTime); !ok || next != StatusLate {
		t.Fatalf("on_time: %s %v", next, ok)
	}
	if _, ok := CloseWindow(StatusLate); ok {
		t.Fatal("late should stay")
	}
	if _, ok := CloseWindow(StatusHeld); ok {
		t.Fatal("held should stay")
	}
}

func TestHoldResume(t *testing.T) {
	next, err := Hold(StatusOnTime)
	if err != nil || next != StatusHeld {
		t.Fatalf("hold: %s %v", next, err)
	}
	if _, err := Hold(StatusHeld); err != ErrHeld {
		t.Fatalf("second hold: %v", err)
	}
	back, err := Resume(StatusHeld, true)
	if err != nil || back != StatusOnTime {
		t.Fatalf("resume ok: %s %v", back, err)
	}
	back, err = Resume(StatusHeld, false)
	if err != nil || back != StatusQuiet {
		t.Fatalf("resume quiet: %s %v", back, err)
	}
	if _, err := Resume(StatusQuiet, false); err != ErrNotHeld {
		t.Fatalf("resume open: %v", err)
	}
}
