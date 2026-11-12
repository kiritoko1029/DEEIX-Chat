package personalprovider

import (
	"context"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
)

// SyncImportedName uses compare-and-swap so a simultaneous manual edit or
// administrative suspension always wins over automatic naming.
func (r *Repo) SyncImportedName(ctx context.Context, owner uint, id, oldName, lastName, name string) (bool, error) {
	result := r.db.WithContext(ctx).Model(&models.LLMUserProvider{}).
		Where("owner_user_id = ? AND public_id = ? AND source = ? AND status = ? AND name = ? AND last_synced_name = ?", owner, id, "sub2api", "active", oldName, lastName).
		Updates(map[string]any{"name": name, "last_synced_name": name})
	return result.RowsAffected == 1, result.Error
}
