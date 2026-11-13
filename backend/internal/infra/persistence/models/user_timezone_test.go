package models_test

import (
	"path/filepath"
	"testing"

	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type legacyTimezoneUser struct {
	models.User `gorm:"embedded"`
	Timezone    string `gorm:"size:64;not null;default:'Etc/UTC';comment:时区"`
}

func (legacyTimezoneUser) TableName() string { return "identity_users" }

func TestTimezoneDefaultMigrationPreservesExistingValues(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "timezone.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec(`PRAGMA foreign_keys = ON`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&legacyTimezoneUser{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO identity_users (public_id, username) VALUES ('old-id', 'existing-user')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE timezone_test_user_links (user_id INTEGER REFERENCES identity_users(id) ON DELETE CASCADE, note TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO timezone_test_user_links (user_id, note) VALUES (1, 'preserve-related-data')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO identity_users (public_id, username) VALUES ('new-id', 'new-user')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO identity_users (public_id, username, timezone) VALUES ('explicit-id', 'explicit-user', 'Europe/Berlin')`).Error; err != nil {
		t.Fatal(err)
	}
	var related int64
	if err := db.Table("timezone_test_user_links").Where("user_id = ? AND note = ?", 1, "preserve-related-data").Count(&related).Error; err != nil || related != 1 {
		t.Fatal("migration removed related user data", err, related)
	}
	var foreignKeys int
	if err := db.Raw(`PRAGMA foreign_keys`).Scan(&foreignKeys).Error; err != nil || foreignKeys != 1 {
		t.Fatal("foreign key enforcement was not restored", err, foreignKeys)
	}
	for name, want := range map[string]string{"existing-user": "Etc/UTC", "new-user": domainuser.DefaultNewUserTimezone, "explicit-user": "Europe/Berlin"} {
		var user models.User
		if err := db.Where("username = ?", name).First(&user).Error; err != nil {
			t.Fatal(err)
		}
		if user.Timezone != want {
			t.Fatalf("%s timezone=%q want=%q", name, user.Timezone, want)
		}
	}
}
