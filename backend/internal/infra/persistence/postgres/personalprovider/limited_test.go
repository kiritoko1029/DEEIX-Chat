package personalprovider

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/personalprovider"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCreateLimitedSerializesOwnerQuotaAndUniquePublicID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "quota.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.User{}, &models.LLMUserProvider{}); err != nil {
		t.Fatal(err)
	}
	owner := models.User{PublicID: "owner", Username: "owner", Role: "user", Status: "active"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewRepo(db)
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := repo.CreateLimited(context.Background(), &domain.Provider{PublicID: fmt.Sprintf("candidate%d", i), OwnerUserID: owner.ID, Models: []domain.Model{}}, 1)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, repository.ErrPersonalProviderLimitReached) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("quota race: %d successful creates", success)
	}
	items, err := repo.ListByOwner(context.Background(), owner.ID)
	if err != nil || len(items) != 1 {
		t.Fatal("quota exceeded")
	}
	if _, err := repo.CreateLimited(context.Background(), &items[0], 2); !errors.Is(err, repository.ErrDuplicate) {
		t.Fatalf("database unique public ID not enforced: %v", err)
	}
}
