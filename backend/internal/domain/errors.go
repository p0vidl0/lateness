package domain

import "errors"

var (
	ErrInvalid = errors.New("invalid")
	ErrHeld    = errors.New("held")
	ErrNotHeld = errors.New("not held")
)
