package user

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"gorm.io/gorm"
)

// ResolveSub2APIUser atomically creates the account and its immutable identity.
// Concurrent winners are resolved only by the unique remote subject, never by
// the proposed username. A deleted local account leaves a non-reassignable tombstone.
func (r *Repo) ResolveSub2APIUser(ctx context.Context, instance string, remoteID int64, input repository.CreateWithCredentialInput) (*domainuser.User, error) {
	if instance == "" || remoteID <= 0 || input.User == nil {
		return nil, repository.ErrInvalidInput
	}
	sum := sha256.Sum256([]byte(instance))
	hash := hex.EncodeToString(sum[:])
	lookup := func() (*domainuser.User, error) {
		var identity models.Sub2APIIdentity
		if err := r.db.WithContext(ctx).Where("instance_hash = ? AND remote_id = ?", hash, remoteID).First(&identity).Error; err != nil {
			return nil, translateError(err)
		}
		if identity.Instance != instance {
			return nil, repository.ErrConflict
		}
		return r.GetByID(ctx, identity.UserID)
	}
	// Distinguish missing identity from a tombstone with a missing local user.
	var count int64
	if err := r.db.WithContext(ctx).Model(&models.Sub2APIIdentity{}).Where("instance_hash = ? AND remote_id = ?", hash, remoteID).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return lookup()
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := r.createWithCredentialTx(tx, input); err != nil {
			return err
		}
		// Gorm fills the legacy default:true bool on create; explicitly disable
		// password login for this dedicated external identity in the same transaction.
		if err := tx.Model(&models.UserCredential{}).Where("user_id = ?", input.User.ID).Update("password_enabled", false).Error; err != nil {
			return err
		}
		return tx.Create(&models.Sub2APIIdentity{InstanceHash: hash, Instance: instance, RemoteID: remoteID, UserID: input.User.ID}).Error
	})
	if err != nil {
		if existing, lookupErr := lookup(); lookupErr == nil {
			return existing, nil
		}
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, repository.ErrConflict
		}
		return nil, translateError(err)
	}
	return r.GetByID(ctx, input.User.ID)
}

func (r *Repo) GetSub2APIProfile(ctx context.Context, userID uint) (*domainuser.ExternalProfile, error) {
	var identity models.Sub2APIIdentity
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&identity).Error; err != nil {
		return nil, translateError(err)
	}
	profile := sub2APIExternalProfile(identity)
	return &profile, nil
}

func (r *Repo) ListSub2APIProfilesByUserIDs(ctx context.Context, userIDs []uint) (map[uint]domainuser.ExternalProfile, error) {
	profiles := make(map[uint]domainuser.ExternalProfile, len(userIDs))
	if len(userIDs) == 0 {
		return profiles, nil
	}
	var identities []models.Sub2APIIdentity
	if err := r.db.WithContext(ctx).Where("user_id IN ?", userIDs).Find(&identities).Error; err != nil {
		return nil, translateError(err)
	}
	for _, identity := range identities {
		profiles[identity.UserID] = sub2APIExternalProfile(identity)
	}
	return profiles, nil
}

func sub2APIExternalProfile(identity models.Sub2APIIdentity) domainuser.ExternalProfile {
	profile := domainuser.ExternalProfile{
		Provider: "sub2api", RemoteID: identity.RemoteID, ProfileSyncedAt: identity.ProfileSyncedAt,
	}
	if identity.ProfileSyncedAt != nil {
		profile.DisplayName = identity.DisplayName
		profile.Username = identity.Username
		profile.Email = identity.Email
	}
	return profile
}

func (r *Repo) SyncSub2APIProfile(ctx context.Context, userID uint, instance string, remoteID int64, profile domainuser.ExternalProfile) error {
	sum := sha256.Sum256([]byte(instance))
	result := r.db.WithContext(ctx).Model(&models.Sub2APIIdentity{}).
		Where("user_id = ? AND instance_hash = ? AND instance = ? AND remote_id = ?", userID, hex.EncodeToString(sum[:]), instance, remoteID).
		Updates(map[string]any{"display_name": profile.DisplayName, "username": profile.Username, "email": profile.Email, "profile_synced_at": time.Now()})
	if result.Error != nil {
		return translateError(result.Error)
	}
	if result.RowsAffected != 1 {
		return repository.ErrNotFound
	}
	return nil
}

func (r *Repo) LookupSub2APIUser(ctx context.Context, instance string, remoteID int64) (*domainuser.User, error) {
	if instance == "" || remoteID <= 0 {
		return nil, repository.ErrInvalidInput
	}
	sum := sha256.Sum256([]byte(instance))
	var identity models.Sub2APIIdentity
	if err := r.db.WithContext(ctx).Where("instance_hash = ? AND remote_id = ?", hex.EncodeToString(sum[:]), remoteID).First(&identity).Error; err != nil {
		return nil, translateError(err)
	}
	if identity.Instance != instance {
		return nil, repository.ErrConflict
	}
	user, err := r.GetByID(ctx, identity.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, repository.ErrConflict
	}
	return user, err
}
