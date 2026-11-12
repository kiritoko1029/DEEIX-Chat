package personalprovider

import (
	"context"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/personalprovider"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CreateLimited serializes all service-level creates for the owner, including
// manual imports, so simultaneous logins cannot overrun the provider quota.
func (r *Repo) CreateLimited(ctx context.Context, item *domain.Provider, limit int) (*domain.Provider, error) {
	if item == nil || item.OwnerUserID == 0 || limit <= 0 {
		return nil, repository.ErrInvalidInput
	}
	var result *domain.Provider
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var owner models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&owner, item.OwnerUserID).Error; err != nil {
			return err
		}
		repo := NewRepo(tx)
		count, err := repo.CountByOwner(ctx, item.OwnerUserID)
		if err != nil {
			return err
		}
		if count >= int64(limit) {
			return repository.ErrPersonalProviderLimitReached
		}
		result, err = repo.Create(ctx, item)
		return err
	})
	return result, err
}
