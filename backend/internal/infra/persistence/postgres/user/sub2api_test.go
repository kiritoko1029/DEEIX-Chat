package user

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestSub2APIIdentityAtomicAndUnique(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "identity.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.User{}, &models.UserCredential{}, &models.Sub2APIIdentity{}); err != nil {
		t.Fatal(err)
	}
	repo := NewRepo(db)
	ctx := context.Background()
	now := time.Now()
	input := func(i int) repository.CreateWithCredentialInput {
		return repository.CreateWithCredentialInput{User: &domain.User{PublicID: fmt.Sprintf("public%d", i), Username: fmt.Sprintf("local%d", i), Role: "user", Status: "active", OnboardingCompletedAt: &now}, Credential: domain.Credential{PasswordEnabled: false}}
	}
	var wg sync.WaitGroup
	ids := make(chan uint, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u, err := repo.ResolveSub2APIUser(ctx, "https://sub.example.com", 7, input(i))
			if err != nil {
				errs <- err
				return
			}
			ids <- u.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var first uint
	for id := range ids {
		if first == 0 {
			first = id
		}
		if id != first {
			t.Fatal("identity split across local users")
		}
	}
	var users, credentials, mappings int64
	db.Model(&models.User{}).Count(&users)
	db.Model(&models.UserCredential{}).Count(&credentials)
	db.Model(&models.Sub2APIIdentity{}).Count(&mappings)
	if users != 1 || credentials != 1 || mappings != 1 {
		t.Fatalf("orphans: users=%d credentials=%d mappings=%d", users, credentials, mappings)
	}
	credential, err := repo.GetCredentialByUserID(ctx, first)
	if err != nil || credential.PasswordEnabled || credential.PasswordHash != "" {
		t.Fatal("external credential enabled")
	}
	other, err := repo.ResolveSub2APIUser(ctx, "https://other.example.com", 7, input(20))
	if err != nil || other.ID == first {
		t.Fatal("instances shared identity")
	}
	// Existing user names cannot be used to claim identities.
	if _, err := repo.ResolveSub2APIUser(ctx, "https://third.example.com", 7, repository.CreateWithCredentialInput{User: &domain.User{PublicID: "fresh", Username: other.Username, Role: "user", Status: "active"}}); err == nil {
		t.Fatal("claimed existing username")
	}
	if err := db.Delete(&models.User{}, first).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolveSub2APIUser(ctx, "https://sub.example.com", 7, input(30)); err == nil {
		t.Fatal("deleted identity reassigned")
	}
}

// legacySub2APIIdentity simulates an existing deployment before display-only fields.
type legacySub2APIIdentity struct {
	models.ControlPlaneModel
	InstanceHash string `gorm:"size:64;not null;uniqueIndex:idx_identity_sub2api_subject,priority:1"`
	Instance     string `gorm:"size:2048;not null"`
	RemoteID     int64  `gorm:"not null;uniqueIndex:idx_identity_sub2api_subject,priority:2"`
	UserID       uint   `gorm:"not null;uniqueIndex:idx_identity_sub2api_user"`
}

func (legacySub2APIIdentity) TableName() string { return "identity_sub2api_identities" }

func TestSub2APIProfileMigrationAndSubjectBoundUpdate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "profile-migration.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.User{}, &models.UserCredential{}, &legacySub2APIIdentity{}); err != nil {
		t.Fatal(err)
	}
	repo := NewRepo(db)
	ctx := context.Background()
	// Resolve against the old table with no profile columns by inserting the legacy record explicitly.
	user := models.User{PublicID: "unchanged-public-id", Username: "local-identity", Email: "local@example.com", Role: "user", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("https://sub.example.com"))
	identity := legacySub2APIIdentity{InstanceHash: hex.EncodeToString(hash[:]), Instance: "https://sub.example.com", RemoteID: 7, UserID: user.ID}
	if err := db.Create(&identity).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Sub2APIIdentity{}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetSub2APIProfile(ctx, user.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("legacy profile must be omitted before first sync", err)
	}
	profile := domain.ExternalProfile{Provider: "ignored", DisplayName: "远端昵称", Username: "remote", Email: "local@example.com"}
	if err := repo.SyncSub2APIProfile(ctx, user.ID, "https://other.example.com", 7, profile); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cross-instance update", err)
	}
	if err := repo.SyncSub2APIProfile(ctx, user.ID, "https://sub.example.com", 8, profile); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cross-subject update", err)
	}
	if err := repo.SyncSub2APIProfile(ctx, user.ID, "https://sub.example.com", 7, profile); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.GetSub2APIProfile(ctx, user.ID)
	if err != nil || loaded.Provider != "sub2api" || loaded.DisplayName != "远端昵称" {
		t.Fatal("profile not persisted", err)
	}
	var current models.User
	db.First(&current, user.ID)
	var currentIdentity models.Sub2APIIdentity
	db.First(&currentIdentity, identity.ID)
	if current.PublicID != user.PublicID || current.Email != user.Email || current.Username != user.Username || currentIdentity.ID != identity.ID || currentIdentity.UserID != user.ID || currentIdentity.RemoteID != 7 {
		t.Fatal("migration changed local identity")
	}
}
