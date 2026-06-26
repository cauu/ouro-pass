package domain

import "errors"

// Repository / one-time-credential sentinel errors.
var (
	ErrNotFound = errors.New("not found")
	ErrConsumed = errors.New("already consumed")
	ErrExpired  = errors.New("expired")
	ErrPurpose  = errors.New("purpose mismatch")
	ErrConflict = errors.New("conflict")
)
