package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const MaxNoteLen = 2000

type Intake struct {
	Outcome Outcome
	Note    string
}

type intakeBody struct {
	Outcome string `json:"outcome"`
	Note    string `json:"note"`
}

// ParseIntake reads one signal body. Unknown fields are rejected.
func ParseIntake(raw []byte) (Intake, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var body intakeBody
	if err := dec.Decode(&body); err != nil {
		return Intake{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	if dec.More() {
		return Intake{}, fmt.Errorf("%w: extra json", ErrInvalid)
	}
	outcome := Outcome(strings.TrimSpace(body.Outcome))
	if !ValidOutcome(outcome) {
		return Intake{}, fmt.Errorf("%w: outcome", ErrInvalid)
	}
	note := strings.TrimSpace(body.Note)
	if len(note) > MaxNoteLen {
		return Intake{}, fmt.Errorf("%w: note", ErrInvalid)
	}
	return Intake{Outcome: outcome, Note: note}, nil
}
