package domain

// ApplySignal decides the next status after an intake outcome.
// A begin outcome never changes status. An ok outcome moves quiet or late to on_time.
// A fail outcome moves quiet or on_time to late. Held expectations are refused.
func ApplySignal(current Status, outcome Outcome) (Status, Reason, bool, error) {
	if !ValidStatus(current) || !ValidOutcome(outcome) {
		return "", "", false, ErrInvalid
	}
	if current == StatusHeld {
		return current, "", false, ErrHeld
	}
	switch outcome {
	case OutcomeBegin:
		return current, "", false, nil
	case OutcomeOK:
		if current == StatusOnTime {
			return current, "", false, nil
		}
		return StatusOnTime, ReasonSignalOK, true, nil
	case OutcomeFail:
		if current == StatusLate {
			return current, "", false, nil
		}
		return StatusLate, ReasonSignalFail, true, nil
	default:
		return "", "", false, ErrInvalid
	}
}

// CloseWindow moves an open expectation to late when its deadline has passed.
func CloseWindow(current Status) (Status, bool) {
	switch current {
	case StatusQuiet, StatusOnTime:
		return StatusLate, true
	default:
		return current, false
	}
}

// Hold suspends an expectation. A second hold is refused.
func Hold(current Status) (Status, error) {
	if !ValidStatus(current) {
		return "", ErrInvalid
	}
	if current == StatusHeld {
		return current, ErrHeld
	}
	return StatusHeld, nil
}

// Resume restores a held expectation. hadOK chooses on_time, otherwise quiet.
func Resume(current Status, hadOK bool) (Status, error) {
	if current != StatusHeld {
		return current, ErrNotHeld
	}
	if hadOK {
		return StatusOnTime, nil
	}
	return StatusQuiet, nil
}
