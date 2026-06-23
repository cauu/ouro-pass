package domain

import "time"

// AdminSession is a server-side admin session (httpOnly cookie; token stored
// hashed) (§8.2).
type AdminSession struct {
	SessionToken string // stored as hash
	AdminID      string
	ExpiresAt    time.Time
	IP           *string
	CreatedAt    time.Time
}
