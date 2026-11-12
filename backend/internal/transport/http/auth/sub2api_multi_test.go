package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
)

func multiSnapshot(count int) *port.Snapshot {
	id := int64(10)
	result := &port.Snapshot{User: port.User{ID: 7, Status: "active", Username: "远端昵称", Email: "owner@example.com"}}
	platforms := []string{"openai", "anthropic", "gemini"}
	for i := 0; i < count; i++ {
		result.Keys = append(result.Keys, port.Key{ID: int64(i + 1), UserID: 7, Name: "同名Key", Key: fmt.Sprintf("synthetic-key-%d", i), Status: "active", GroupID: &id, Group: &port.Group{ID: id, Name: "同名分组", Status: "active", Platform: platforms[i%3]}})
	}
	return result
}
func decodeExchange(t *testing.T, w *httptest.ResponseRecorder) LoginResponse {
	t.Helper()
	var payload struct {
		Data LoginResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Data
}

func TestSub2APIHTTPMultiKeyProfileRefreshMeAndPartialFailure(t *testing.T) {
	router, db, remote := exchangeFixtureWithLimit(t, true, exchangeAllowLimiter{}, 10)
	remote.snapshot = multiSnapshot(4)
	remote.snapshot.Keys[3].Key = "synthetic-probe-failure"
	// Same email/username belongs to a pre-existing local user; it must not be claimed.
	verified := time.Now()
	ordinary := models.User{PublicID: "ordinary", Username: "远端昵称", Email: "owner@example.com", EmailVerifiedAt: &verified, Role: "admin", Status: "active"}
	if err := db.Create(&ordinary).Error; err != nil {
		t.Fatal(err)
	}
	w := exchangeRequest(router, exchangeBody)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	first := decodeExchange(t, w)
	summary := first.Sub2APIImport
	if summary == nil || summary.Imported != 3 || summary.Failed != 1 || summary.Items[3].Reason != "probe_failed" {
		t.Fatalf("summary=%+v", summary)
	}
	if first.User.ID == ordinary.ID || first.User.Role != "user" || first.User.Email != "" || first.User.EmailVerifiedAt != nil || first.User.ExternalProfile == nil || first.User.ExternalProfile.Email != ordinary.Email {
		t.Fatal("profile claimed local credentials")
	}
	var providers []models.LLMUserProvider
	db.Order("name").Find(&providers)
	if len(providers) != 3 {
		t.Fatal("key count")
	}
	ids := map[string]bool{}
	protocols := map[string]bool{}
	for _, p := range providers {
		if ids[p.PublicID] || p.Source != "sub2api" || p.Name == "sub2api" || p.LastSyncedName != p.Name {
			t.Fatal("keys merged or unnamed")
		}
		ids[p.PublicID] = true
		protocols[p.Protocol] = true
	}
	if len(protocols) != 3 {
		t.Fatal("protocols merged")
	}
	remote.snapshot.User.Username = "更新昵称"
	remote.snapshot.User.Email = "updated@example.com"
	w2 := exchangeRequest(router, exchangeBody)
	second := decodeExchange(t, w2)
	if w2.Code != 200 || second.User.ID != first.User.ID || second.User.PublicID != first.User.PublicID || second.Sub2APIImport.Reused != 3 || second.User.ExternalProfile.DisplayName != "更新昵称" {
		t.Fatal("repeat changed identity or failed sync")
	}
	// Refresh and me read the persisted display profile without calling remote auth.
	remote.err = fmt.Errorf("must not call remote on refresh")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(w2.Result().Cookies()[0])
	refreshed := httptest.NewRecorder()
	router.ServeHTTP(refreshed, request)
	if refreshed.Code != 200 || !strings.Contains(refreshed.Body.String(), `"externalProfile":{"provider":"sub2api","displayName":"更新昵称"`) {
		t.Fatalf("refresh=%d %s", refreshed.Code, refreshed.Body.String())
	}
	me := httptest.NewRecorder()
	router.ServeHTTP(me, httptest.NewRequest(http.MethodGet, "/test-me", nil))
	if me.Code != 200 || !strings.Contains(me.Body.String(), `"email":"updated@example.com"`) {
		t.Fatalf("me=%d %s", me.Code, me.Body.String())
	}
	for _, private := range []string{"synthetic-key-", "synthetic-probe-failure", "synthetic-private-upstream-body", "synthetic-login-token"} {
		if strings.Contains(w.Body.String()+w2.Body.String(), private) {
			t.Fatal("leaked", private)
		}
	}
	db.First(&ordinary, ordinary.ID)
	if ordinary.Email != "owner@example.com" || ordinary.Role != "admin" || ordinary.EmailVerifiedAt == nil {
		t.Fatal("ordinary account modified")
	}
}

func TestSub2APIHTTPConcurrentMultiKeyQuotaAndRevokedSummary(t *testing.T) {
	router, db, remote := exchangeFixtureWithLimit(t, true, exchangeAllowLimiter{}, 2)
	remote.snapshot = multiSnapshot(5)
	var wg sync.WaitGroup
	failures := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := exchangeRequest(router, exchangeBody)
			if w.Code != 200 {
				failures <- w.Body.String()
				return
			}
			var payload struct {
				Data LoginResponse `json:"data"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &payload)
			s := payload.Data.Sub2APIImport
			if s == nil || s.Imported+s.Reused != 2 || s.Failed != 3 {
				failures <- w.Body.String()
			}
		}()
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	var providers, users, identities int64
	db.Model(&models.LLMUserProvider{}).Count(&providers)
	db.Model(&models.User{}).Count(&users)
	db.Model(&models.Sub2APIIdentity{}).Count(&identities)
	if providers != 2 || users != 1 || identities != 1 {
		t.Fatalf("quota/idempotency=%d/%d/%d", providers, users, identities)
	}
	// Every remote key is now disabled: existing local records must not count as reusable.
	for i := range remote.snapshot.Keys {
		remote.snapshot.Keys[i].Status = "disabled"
	}
	w := exchangeRequest(router, exchangeBody)
	var failure struct {
		ErrorCode string `json:"errorCode"`
		Details   struct {
			Summary Sub2APIImportResponse `json:"sub2apiImport"`
		} `json:"details"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &failure)
	if w.Code != 403 || failure.ErrorCode != "auth.sub2api_no_usable_key" || failure.Details.Summary.Skipped != 5 || failure.Details.Summary.Reused != 0 {
		t.Fatalf("revoked=%d %s", w.Code, w.Body.String())
	}
	db.Model(&models.LLMUserProvider{}).Count(&providers)
	if providers != 2 {
		t.Fatal("deleted user provider data")
	}
}

func TestSub2APIHTTPProfileAndProvidersDoNotMutateBeforeTwoFactor(t *testing.T) {
	router, db, remote := exchangeFixtureWithLimit(t, true, exchangeAllowLimiter{}, 5)
	remote.snapshot = multiSnapshot(1)
	first := exchangeRequest(router, exchangeBody)
	if first.Code != 200 {
		t.Fatal(first.Body.String())
	}
	var user models.User
	db.First(&user)
	if err := db.Create(&models.UserTwoFactor{UserID: user.ID, TOTPEnabled: true, TOTPSecretEncrypted: "synthetic-encrypted-secret"}).Error; err != nil {
		t.Fatal(err)
	}
	var before models.Sub2APIIdentity
	db.First(&before)
	var provider models.LLMUserProvider
	db.First(&provider)
	remote.snapshot = multiSnapshot(2)
	remote.snapshot.User.Username = "不允许提前写入"
	remote.snapshot.Keys[0].Name = "不允许提前重命名"
	w := exchangeRequest(router, exchangeBody)
	result := decodeExchange(t, w)
	if w.Code != 200 || !result.TwoFactorRequired || result.User.ID != 0 || result.User.ExternalProfile != nil || result.Sub2APIImport.Reused != 1 || result.Sub2APIImport.Skipped != 1 || result.Sub2APIImport.Items[1].Reason != "two_factor_required" {
		t.Fatalf("2fa=%d %s", w.Code, w.Body.String())
	}
	for _, item := range result.Sub2APIImport.Items {
		if item.ProviderID != "" {
			t.Fatal("disclosed local provider before 2FA")
		}
	}
	var after models.Sub2APIIdentity
	db.First(&after)
	var latest models.LLMUserProvider
	db.First(&latest)
	if after.Username != before.Username || !after.ProfileSyncedAt.Equal(*before.ProfileSyncedAt) || latest.Name != provider.Name || latest.ModelsJSON != provider.ModelsJSON || latest.APIKeyEnc != provider.APIKeyEnc {
		t.Fatal("2FA mutated profile/provider")
	}
	var count int64
	db.Model(&models.LLMUserProvider{}).Count(&count)
	if count != 1 {
		t.Fatal("2FA imported new key")
	}
}

func TestSub2APIHTTPLegacyNameMigrationPreservesManualEditsModelsAndCredentials(t *testing.T) {
	router, db, remote := exchangeFixtureWithLimit(t, true, exchangeAllowLimiter{}, 4)
	remote.snapshot = multiSnapshot(2)
	w := exchangeRequest(router, exchangeBody)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	result := decodeExchange(t, w)
	firstID, secondID := result.Sub2APIImport.Items[0].ProviderID, result.Sub2APIImport.Items[1].ProviderID
	var original models.LLMUserProvider
	db.Where("public_id = ?", firstID).First(&original)
	// Simulate legacy schema rows, including a user who manually renamed theirs.
	db.Model(&models.LLMUserProvider{}).Where("public_id = ?", firstID).Updates(map[string]any{"name": "sub2api", "last_synced_name": ""})
	db.Model(&models.LLMUserProvider{}).Where("public_id = ?", secondID).Updates(map[string]any{"name": "手工名称", "last_synced_name": ""})
	remote.snapshot.Keys[0].Name = "新的Key名称"
	remote.snapshot.Keys[1].Name = "不应覆盖手工名称"
	w = exchangeRequest(router, exchangeBody)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var migrated, manual models.LLMUserProvider
	db.Where("public_id = ?", firstID).First(&migrated)
	db.Where("public_id = ?", secondID).First(&manual)
	if migrated.ID != original.ID || migrated.PublicID != original.PublicID || migrated.Name != "新的Key名称 · 同名分组 · #1" || migrated.LastSyncedName != migrated.Name || migrated.ModelsJSON != original.ModelsJSON || migrated.APIKeyEnc != original.APIKeyEnc || manual.Name != "手工名称" {
		t.Fatal("legacy migration overwrote manual data or rebuilt provider")
	}
	// Repeated automatic rename is allowed only while the stored name is still automatic.
	remote.snapshot.Keys[0].Name = "再次更新"
	w = exchangeRequest(router, exchangeBody)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	db.Where("public_id = ?", firstID).First(&migrated)
	if !strings.HasPrefix(migrated.Name, "再次更新 · ") {
		t.Fatal("last synced name not tracked")
	}
	db.Model(&migrated).Update("name", "新的手工名称")
	remote.snapshot.Keys[0].Name = "不可覆盖"
	remote.snapshot.Keys[1].Key = "synthetic-replaced-remote-credential"
	w = exchangeRequest(router, exchangeBody)
	result = decodeExchange(t, w)
	if w.Code != 200 || result.Sub2APIImport.Reused != 1 || result.Sub2APIImport.Items[1].Reason != "credential_changed" {
		t.Fatalf("credential changed=%d %s", w.Code, w.Body.String())
	}
	db.Where("public_id = ?", firstID).First(&migrated)
	db.Where("public_id = ?", secondID).First(&manual)
	if migrated.Name != "新的手工名称" || manual.Name != "手工名称" {
		t.Fatal("manual name overwritten")
	}
	db.Model(&migrated).Update("status", "suspended")
	db.Model(&manual).Update("status", "disabled")
	w = exchangeRequest(router, exchangeBody)
	var failure struct {
		Details struct {
			Summary Sub2APIImportResponse `json:"sub2apiImport"`
		} `json:"details"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &failure)
	if w.Code != 403 || failure.Details.Summary.Items[0].Reason != "provider_suspended" || failure.Details.Summary.Items[1].Reason != "provider_disabled" {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
	db.Where("public_id = ?", firstID).First(&migrated)
	if migrated.Status != "suspended" {
		t.Fatal("administrator suspension undone")
	}
}

func TestSub2APIHTTPDistinctKeyIDsNeverMergeEvenWhenCredentialsMatch(t *testing.T) {
	router, db, remote := exchangeFixtureWithLimit(t, true, exchangeAllowLimiter{}, 4)
	remote.snapshot = multiSnapshot(2)
	remote.snapshot.Keys[1].Key = remote.snapshot.Keys[0].Key
	remote.snapshot.Keys[1].Group.Platform = "openai"
	w := exchangeRequest(router, exchangeBody)
	result := decodeExchange(t, w)
	if w.Code != 200 || result.Sub2APIImport.Imported != 2 || result.Sub2APIImport.Items[0].ProviderID == result.Sub2APIImport.Items[1].ProviderID {
		t.Fatalf("merged=%d %s", w.Code, w.Body.String())
	}
	var count int64
	db.Model(&models.LLMUserProvider{}).Count(&count)
	if count != 2 {
		t.Fatal("keys merged")
	}
}
