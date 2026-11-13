package repository

import (
	"context"
	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
)

// Sub2APIIdentityRepository binds a verified remote subject to a dedicated local
// account. It must never fall back to email or username matching.
type Sub2APIIdentityRepository interface {
	ResolveSub2APIUser(ctx context.Context, instance string, remoteID int64, input CreateWithCredentialInput) (*domainuser.User, error)
}

// Sub2APIProfileRepository isolates display metadata from security credentials.
type Sub2APIProfileRepository interface {
	GetSub2APIProfile(context.Context, uint) (*domainuser.ExternalProfile, error)
	SyncSub2APIProfile(context.Context, uint, string, int64, domainuser.ExternalProfile) error
}

// Sub2APIProfileBatchRepository reads saved display metadata for admin views.
type Sub2APIProfileBatchRepository interface {
	ListSub2APIProfilesByUserIDs(context.Context, []uint) (map[uint]domainuser.ExternalProfile, error)
}

// Sub2APIIdentityLookup reads an existing binding without provisioning an account.
// Missing bindings return ErrNotFound; tombstones and collisions must fail closed.
type Sub2APIIdentityLookup interface {
	LookupSub2APIUser(context.Context, string, int64) (*domainuser.User, error)
}
