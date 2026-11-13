package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appadmin "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/admin"
	userapp "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	userrepo "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/postgres/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAdminSub2APIProfilesSQLite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "admin-profiles.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.User{}, &models.UserCredential{}, &models.UserSession{}, &models.UserAuthEvent{}, &models.UserIdentity{}, &models.AuthIdentityProvider{}, &models.Sub2APIIdentity{}, &models.Subscription{}, &models.BillingPlan{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 13, 5, 0, 0, 0, time.UTC)
	users := []models.User{
		{PublicID: "remote-first", Username: "sub2api_random_first", DisplayName: "Internal First", Role: "user", Status: "active"},
		{PublicID: "remote-second", Username: "sub2api_random_second", DisplayName: "Internal Second", Role: "user", Status: "active"},
		{PublicID: "remote-unsynced", Username: "sub2api_random_unsynced", Role: "user", Status: "active"},
		{PublicID: "ordinary", Username: "ordinary", DisplayName: "Local User", Email: "shared@example.com", EmailVerifiedAt: &now, Role: "user", Status: "active"},
		{PublicID: "admin", Username: "admin", Role: "admin", Status: "active"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	identities := []models.Sub2APIIdentity{
		{InstanceHash: "synthetic-instance-hash", Instance: "https://private-instance.example", RemoteID: 11, UserID: users[0].ID, Username: `Remote_%\Name`, Email: "shared@example.com", DisplayName: "来源昵称", ProfileSyncedAt: &now},
		{InstanceHash: "synthetic-instance-hash", Instance: "https://private-instance.example", RemoteID: 12, UserID: users[1].ID, Username: "SecondRemote", Email: "shared@example.com", DisplayName: "第二昵称", ProfileSyncedAt: &now},
		{InstanceHash: "synthetic-instance-hash", Instance: "https://private-instance.example", RemoteID: 13, UserID: users[2].ID, Username: "unsynced-stale-name", Email: "unsynced@example.com", DisplayName: "unsynced-stale-display"},
	}
	if err := db.Create(&identities).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.UserCredential{UserID: users[0].ID, PasswordHash: "synthetic-private-credential"}).Error; err != nil {
		t.Fatal(err)
	}
	repo := userrepo.NewRepo(db)
	service := appadmin.NewService(userapp.NewService(repo), handlerAuditServiceFake{})
	handler := NewHandler(service)
	router := gin.New()
	role := "admin"
	router.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, users[4].ID)
		c.Set(middleware.ContextKeyUserRole, role)
	})
	group := router.Group("/admin", middleware.AdminOnly())
	group.GET("/users", handler.ListUsers)
	group.PATCH("/users/:id", handler.PatchUser)
	group.PATCH("/users/:id/status", handler.UpdateUserStatus)

	profileQueries := 0
	if err := db.Callback().Query().After("gorm:query").Register("test:count_profile_queries", func(tx *gorm.DB) {
		if tx.Statement.Table == "identity_sub2api_identities" {
			profileQueries++
		}
	}); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	list := func(query string, page int, size int) ([]UserResponse, int64) {
		t.Helper()
		w := request(http.MethodGet, fmt.Sprintf("/admin/users?page=%d&page_size=%d&q=%s", page, size, url.QueryEscape(query)), "")
		if w.Code != http.StatusOK {
			t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
		}
		var payload struct {
			Data struct {
				Results []UserResponse `json:"results"`
				Total   int64          `json:"total"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		for _, private := range []string{"synthetic-private-credential", "private-instance.example", "synthetic-instance-hash", "instanceHash", "accessToken", "refreshToken", "passwordHash", "apiKey"} {
			if strings.Contains(w.Body.String(), private) {
				t.Fatalf("response leaked %s", private)
			}
		}
		return payload.Data.Results, payload.Data.Total
	}

	t.Run("batch display and unchanged local identity", func(t *testing.T) {
		profileQueries = 0
		items, total := list("", 1, 20)
		if total != 5 || len(items) != 5 || profileQueries != 1 {
			t.Fatalf("total=%d items=%d profile queries=%d", total, len(items), profileQueries)
		}
		byID := make(map[uint]UserResponse)
		for _, item := range items {
			byID[item.ID] = item
		}
		for index := 0; index < 2; index++ {
			item := byID[users[index].ID]
			profile := item.ExternalProfile
			if profile == nil || profile.Provider != "sub2api" || profile.RemoteID != identities[index].RemoteID || profile.Username != identities[index].Username || profile.DisplayName != identities[index].DisplayName || profile.Email != "shared@example.com" || profile.ProfileSyncedAt == nil || !profile.ProfileSyncedAt.Equal(now) {
				t.Fatalf("unexpected profile %+v", profile)
			}
			if item.Username != users[index].Username || item.DisplayName != users[index].DisplayName || item.Email != "" || item.EmailVerifiedAt != nil {
				t.Fatalf("local identity overwritten %+v", item)
			}
		}
		unsynced := byID[users[2].ID].ExternalProfile
		if unsynced == nil || unsynced.Provider != "sub2api" || unsynced.RemoteID != 13 || unsynced.ProfileSyncedAt != nil || unsynced.Username != "" || unsynced.Email != "" || unsynced.DisplayName != "" {
			t.Fatalf("unexpected unsynced binding %+v", unsynced)
		}
		ordinary := byID[users[3].ID]
		if ordinary.ExternalProfile != nil || ordinary.Username != "ordinary" || ordinary.Email != "shared@example.com" || ordinary.EmailVerifiedAt == nil {
			t.Fatalf("ordinary account changed %+v", ordinary)
		}
		encoded, err := json.Marshal(ordinary)
		if err != nil || strings.Contains(string(encoded), "externalProfile") {
			t.Fatal("ordinary profile must be omitted", err)
		}
		encoded, err = json.Marshal(unsynced)
		if err != nil || !strings.Contains(string(encoded), `"profileSyncedAt":null`) {
			t.Fatal("unsynced time must be null", err)
		}
	})

	t.Run("source search before pagination with literal wildcard escaping", func(t *testing.T) {
		for _, query := range []string{`remote_%\name`, "来源昵称", "%", "Remote_", `\`} {
			items, total := list(query, 1, 1)
			if total != 1 || len(items) != 1 || items[0].ID != users[0].ID {
				t.Fatalf("query=%q total=%d items=%+v", query, total, items)
			}
		}
		for page, wantID := range []uint{users[3].ID, users[1].ID, users[0].ID} {
			items, total := list("SHARED@EXAMPLE.COM", page+1, 1)
			if total != 3 || len(items) != 1 || items[0].ID != wantID {
				t.Fatalf("page=%d total=%d items=%+v", page+1, total, items)
			}
		}
		items, total := list("shared@example.com", 4, 1)
		if total != 3 || len(items) != 0 {
			t.Fatalf("last page total=%d items=%+v", total, items)
		}
		items, total = list("' OR 1=1 --", 1, 20)
		if total != 0 || len(items) != 0 {
			t.Fatal("query was not parameterized")
		}
		items, total = list("unsynced@example.com", 1, 20)
		if total != 0 || len(items) != 0 {
			t.Fatal("unsynced metadata must not be searchable")
		}
		filtered, total, err := repo.ListUsers(context.Background(), 0, 10, repository.UserListFilter{Query: "shared@example.com", IdentityProvider: "missing"})
		if err != nil || total != 0 || len(filtered) != 0 {
			t.Fatal("source search bypassed identity filter", err)
		}
		filtered, total, err = repo.ListUsers(context.Background(), 0, 10, repository.UserListFilter{Query: "shared@example.com", SubscriptionStatus: "active"})
		if err != nil || total != 0 || len(filtered) != 0 {
			t.Fatal("source search bypassed subscription filter", err)
		}
	})

	t.Run("patch and status responses preserve profile", func(t *testing.T) {
		for _, operation := range []struct{ path, body string }{
			{fmt.Sprintf("/admin/users/%d", users[0].ID), `{"displayName":"Edited Name"}`},
			{fmt.Sprintf("/admin/users/%d/status", users[0].ID), `{"status":"suspended"}`},
		} {
			profileQueries = 0
			w := request(http.MethodPatch, operation.path, operation.body)
			var payload struct {
				Data UserDataResponse `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			item := payload.Data.User
			if w.Code != 200 || profileQueries != 1 || item.ExternalProfile == nil || item.ExternalProfile.RemoteID != 11 || item.ExternalProfile.Username != identities[0].Username || item.DisplayName != "Edited Name" || item.Email != "" || item.EmailVerifiedAt != nil {
				t.Fatalf("patch status=%d queries=%d body=%s", w.Code, profileQueries, w.Body.String())
			}
		}
	})

	t.Run("source email never participates in local account lookup", func(t *testing.T) {
		local, err := repo.GetByEmail(context.Background(), "shared@example.com")
		if err != nil || local.ID != users[3].ID {
			t.Fatal("email lookup claimed source account", err)
		}
		locals, err := repo.ListUsersByLowerEmails(context.Background(), []string{"shared@example.com"})
		if err != nil || len(locals) != 1 || locals["shared@example.com"].ID != users[3].ID {
			t.Fatal("batch email lookup claimed source account", err)
		}
		if err := db.Model(&models.Sub2APIIdentity{}).Where("user_id = ?", users[1].ID).Update("email", "source-only@example.com").Error; err != nil {
			t.Fatal(err)
		}
		if _, err := repo.GetByEmail(context.Background(), "source-only@example.com"); !errors.Is(err, repository.ErrNotFound) {
			t.Fatal("source email became login email", err)
		}
		locals, err = repo.ListUsersByLowerEmails(context.Background(), []string{"source-only@example.com"})
		if err != nil || len(locals) != 0 {
			t.Fatal("source email became merge candidate", err)
		}
		var count int64
		if err := db.Model(&models.User{}).Count(&count).Error; err != nil || count != 5 {
			t.Fatal("users merged", err)
		}
	})

	t.Run("ordinary users cannot read admin profiles", func(t *testing.T) {
		role = "user"
		profileQueries = 0
		w := request(http.MethodGet, "/admin/users", "")
		if w.Code != http.StatusForbidden || profileQueries != 0 || strings.Contains(w.Body.String(), "externalProfile") {
			t.Fatalf("unauthorized profile read %d %s", w.Code, w.Body.String())
		}
	})
}
