package domain

import "time"

// AdminRole is the RBAC role for an admin user.
type AdminRole string

const (
	RoleOwner    AdminRole = "owner"
	RoleOperator AdminRole = "operator"
	RoleViewer   AdminRole = "viewer"
)

// AdminUser is a backend administrator; identity is an owner stake key
// (wallet-signature login). The `owner` role's key must be in the on-chain pool
// owner list (§8.1, C9).
type AdminUser struct {
	AdminID      string
	PoolID       string
	OwnerKeyHash string
	Role         AdminRole
	LastLoginAt  *time.Time
	CreatedAt    time.Time
}
