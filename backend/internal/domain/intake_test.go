package domain

import (
	"strings"
	"testing"
)

func TestParseIntake(t *testing.T) {
	in, err := ParseIntake([]byte(`{"outcome":"ok","note":" done "}`))
	if err != nil {
		t.Fatal(err)
	}
	if in.Outcome != OutcomeOK || in.Note != "done" {
		t.Fatalf("%+v", in)
	}
	for _, raw := range []string{
		``,
		`{}`,
		`{"outcome":"start"}`,
		`{"outcome":"ok","extra":1}`,
		`{"outcome":"ok"}{"outcome":"fail"}`,
		`{"outcome":"ok","note":"` + strings.Repeat("a", MaxNoteLen+1) + `"}`,
	} {
		if _, err := ParseIntake([]byte(raw)); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	in, err = ParseIntake([]byte(`{"outcome":" begin "}`))
	if err != nil || in.Outcome != OutcomeBegin {
		t.Fatalf("%+v %v", in, err)
	}
}
