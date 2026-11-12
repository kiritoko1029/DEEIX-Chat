package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	appauth "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/auth"
	personal "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/personalprovider"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	providerrepo "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/postgres/personalprovider"
	userrepo "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/postgres/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type exchangeRemote struct {
	snapshot *port.Snapshot
	id       int64
	err      error
}

func (r *exchangeRemote) Fetch(context.Context, string, string, int64, bool) (port.Snapshot, error) {
	if r.err != nil {
		return port.Snapshot{}, r.err
	}
	if r.snapshot != nil {
		return *r.snapshot, nil
	}
	id := int64(10)
	return port.Snapshot{User: port.User{ID: r.id, Status: "active"}, Keys: []port.Key{{ID: 1, UserID: r.id, Key: "synthetic-api-key-for-tests", Status: "active", GroupID: &id, Group: &port.Group{ID: id, Status: "active", Platform: "openai"}}}}, nil
}

func TestSub2APIHTTPRejectedDetailsAreOnlySafeConstants(t *testing.T) {
	for _, tc := range []struct {
		name, stage, reason string
		err                 error
		id                  int64
	}{
		{"binding", port.StageAuthMe, port.ReasonSessionBindingMismatch, port.NewRejected(port.StageAuthMe, port.ReasonSessionBindingMismatch), 7},
		{"key owner", port.StageKeys, port.ReasonKeyOwnerMismatch, port.NewRejected(port.StageKeys, port.ReasonKeyOwnerMismatch), 7},
		{"identity", port.StageIdentity, port.ReasonUserIDMismatch, nil, 88888},
		{"malicious context", port.StageIdentity, port.ReasonUpstreamUnauthorized, fmt.Errorf("synthetic-private-token-key-url: %w", port.ErrRejected), 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, _, remote := exchangeFixture(t, true)
			remote.err = tc.err
			remote.id = tc.id
			w := exchangeRequest(router, exchangeBody)
			var payload struct {
				ErrorCode string            `json:"errorCode"`
				ErrorMsg  string            `json:"errorMsg"`
				Details   map[string]string `json:"details"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if w.Code != 401 || payload.ErrorCode != "auth.sub2api_rejected" || payload.ErrorMsg != "sub2api identity could not be verified" {
				t.Fatalf("response=%d %s", w.Code, w.Body.String())
			}
			if len(payload.Details) != 2 || payload.Details["stage"] != tc.stage || payload.Details["reason"] != tc.reason {
				t.Fatalf("details=%v", payload.Details)
			}
			for _, private := range []string{"synthetic-private-token-key-url", "synthetic-login-token", "synthetic-api-key", `"user_id"`, `"key"`, "https://"} {
				if strings.Contains(w.Body.String(), private) {
					t.Fatalf("private value leaked: %s", private)
				}
			}
		})
	}
}

type exchangeModels struct{}

func (exchangeModels) ListModels(_ context.Context, route llm.RouteConfig) ([]llm.ModelItem, error) {
	if !route.UntrustedEndpoint || (route.BaseURL != "https://sub.example.com/v1" && route.BaseURL != "https://sub.example.com/v1beta") || (route.Protocol != "openai_responses" && route.Protocol != "anthropic_messages" && route.Protocol != "google_generate_content") {
		return nil, fmt.Errorf("invalid test route")
	}
	if strings.Contains(route.APIKey, "synthetic-probe-failure") {
		return nil, fmt.Errorf("synthetic-private-upstream-body")
	}
	return []llm.ModelItem{{ID: "gpt-4o"}}, nil
}

func exchangeFixture(t *testing.T, enabled bool) (*gin.Engine, *gorm.DB, *exchangeRemote) {
	return exchangeFixtureWithLimiter(t, enabled, exchangeAllowLimiter{})
}

type exchangeAllowLimiter struct{}

func (exchangeAllowLimiter) AllowSlidingWindow(context.Context, string, int, time.Duration, time.Duration) (bool, error) {
	return true, nil
}
func (exchangeAllowLimiter) AllowFixedWindow(context.Context, []string, int, time.Duration) (bool, error) {
	return true, nil
}

func exchangeFixtureWithLimiter(t *testing.T, enabled bool, limiter middleware.RateLimiter) (*gin.Engine, *gorm.DB, *exchangeRemote) {
	return exchangeFixtureWithLimit(t, enabled, limiter, 1)
}
func exchangeFixtureWithLimit(t *testing.T, enabled bool, limiter middleware.RateLimiter, limit int) (*gin.Engine, *gorm.DB, *exchangeRemote) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "exchange.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqldb, _ := db.DB()
	sqldb.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqldb.Close() })
	if err := db.AutoMigrate(&models.User{}, &models.UserCredential{}, &models.UserSession{}, &models.UserAuthEvent{}, &models.UserTwoFactor{}, &models.UserIdentity{}, &models.Sub2APIIdentity{}, &models.LLMUserProvider{}); err != nil {
		t.Fatal(err)
	}
	runtime := config.NewRuntime(config.Config{Env: "production", CORSAllowOrigin: "https://chat.example.com", JWTSecret: "synthetic-signing-secret", DataEncryptionKey: "synthetic-encryption-secret", Sub2APIEnabled: enabled, Sub2APIBaseURL: "https://sub.example.com", PersonalProvidersEnabled: true, PersonalProvidersMaxPerUser: limit})
	repo := userrepo.NewRepo(db)
	service := appauth.NewServiceWithRuntime(runtime, repo, nil, nil)
	providers := personal.NewService(runtime, providerrepo.NewRepo(db), exchangeModels{})
	remote := &exchangeRemote{id: 7}
	service.SetSub2API(remote, repo, providers)
	router := gin.New()
	router.Use(middleware.RequestID())
	router.Use(middleware.SecurityHeaders())
	router.Use(middleware.CORS(runtime.Snapshot().CORSAllowOrigin))
	module := NewModule(NewHandler(service))
	router.GET("/test-me", func(c *gin.Context) {
		var user models.User
		db.Order("id desc").First(&user)
		c.Set(middleware.ContextKeyUserID, user.ID)
		module.Handler.Me(c)
	})
	module.RegisterPublicRoutes(router.Group("/api/v1"), middleware.NewFeatureGate(runtime))
	module.RegisterSub2APIRoutes(router.Group("/api/v1"), limiter, runtime)
	return router, db, remote
}

func exchangeRequest(router *gin.Engine, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sub2api/exchange", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://chat.example.com")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

const exchangeBody = `{"userID":7,"token":"synthetic-login-token"}`

func TestSub2APIHTTPConcurrentFirstExchange(t *testing.T) {
	router, db, _ := exchangeFixture(t, true)
	var wg sync.WaitGroup
	failures := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := exchangeRequest(router, exchangeBody)
			if r.Code != http.StatusOK {
				failures <- r.Body.String()
			}
		}()
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	var users, identities, providers int64
	db.Model(&models.User{}).Count(&users)
	db.Model(&models.Sub2APIIdentity{}).Count(&identities)
	db.Model(&models.LLMUserProvider{}).Count(&providers)
	if users != 1 || identities != 1 || providers != 1 {
		t.Fatalf("duplicate provisioning: %d/%d/%d", users, identities, providers)
	}
}

func TestSub2APIHTTPExchangeSessionCookieAndIdempotency(t *testing.T) {
	router, db, _ := exchangeFixture(t, true)
	response := exchangeRequest(router, exchangeBody)
	if response.Code != 200 {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable exchange")
	}
	var payload struct {
		Data LoginResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.AccessToken == "" || payload.Data.RefreshToken != "" || payload.Data.User.PasswordEnabled || payload.Data.User.InitialSecurityRequired {
		t.Fatal("invalid login response")
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].Path != "/api/v1/auth" || cookies[0].Name != refreshTokenCookieName {
		t.Fatalf("cookie=%+v", cookies)
	}
	if strings.Contains(response.Body.String(), "synthetic-login-token") || strings.Contains(response.Body.String(), "synthetic-api-key") {
		t.Fatal("upstream credentials leaked")
	}
	var wg sync.WaitGroup
	failures := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := exchangeRequest(router, exchangeBody)
			if r.Code != 200 {
				failures <- r.Body.String()
			}
		}()
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}
	var users, providers int64
	db.Model(&models.User{}).Count(&users)
	db.Model(&models.LLMUserProvider{}).Count(&providers)
	if users != 1 || providers != 1 {
		t.Fatalf("duplicates users=%d providers=%d", users, providers)
	}
	var provider models.LLMUserProvider
	db.First(&provider)
	if provider.APIKeyEnc == "synthetic-api-key-for-tests" || provider.ModelsJSON == "[]" {
		t.Fatal("provider not encrypted or configured")
	}
	db.Model(&provider).Update("status", "suspended")
	if r := exchangeRequest(router, exchangeBody); r.Code != http.StatusForbidden {
		t.Fatalf("suspended provider login=%d", r.Code)
	}
}

func TestSub2APIHTTPSecondFactorDoesNotIssueCookieOrSession(t *testing.T) {
	router, db, _ := exchangeFixture(t, true)
	if r := exchangeRequest(router, exchangeBody); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var user models.User
	db.First(&user)
	if err := db.Create(&models.UserTwoFactor{UserID: user.ID, TOTPEnabled: true, TOTPSecretEncrypted: "synthetic-encrypted-secret"}).Error; err != nil {
		t.Fatal(err)
	}
	r := exchangeRequest(router, exchangeBody)
	var payload struct {
		Data LoginResponse `json:"data"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &payload)
	if r.Code != 200 || !payload.Data.TwoFactorRequired || payload.Data.TwoFactorChallengeToken == "" || payload.Data.AccessToken != "" {
		t.Fatalf("2fa response=%d %s", r.Code, r.Body.String())
	}
	assertSub2APIExpiredCookie(t, r)
	var sessions int64
	db.Model(&models.UserSession{}).Count(&sessions)
	if sessions != 1 {
		t.Fatal("2FA created session")
	}
	db.Model(&user).Update("status", "suspended")
	if r := exchangeRequest(router, exchangeBody); r.Code != 401 {
		t.Fatalf("disabled user=%d", r.Code)
	}
}

func TestSub2APIHTTPRejectsDisabledMalformedAndMismatched(t *testing.T) {
	router, _, _ := exchangeFixture(t, false)
	if r := exchangeRequest(router, exchangeBody); r.Code != 404 {
		t.Fatal(r.Code)
	}
	router, db, remote := exchangeFixture(t, true)
	for _, body := range []string{`{"userID":0,"token":"x"}`, `{"userID":-1,"token":"x"}`, `{"userID":1.5,"token":"x"}`, `{"userID":7,"token":"bad\nheader"}`, `{"userID":7,"token":""}`, `{"userID":7,"token":"x","baseURL":"https://evil.example.com"}`, exchangeBody + `{}`} {
		if r := exchangeRequest(router, body); r.Code != 400 {
			t.Fatalf("%s: %d", body, r.Code)
		}
	}
	if r := exchangeRequest(router, `{"userID":7,"token":"`+strings.Repeat("x", 30<<10)+`"}`); r.Code != 413 {
		t.Fatal(r.Code)
	}
	remote.id = 8
	if r := exchangeRequest(router, exchangeBody); r.Code != 401 {
		t.Fatal(r.Code)
	}
	var count int64
	db.Model(&models.User{}).Count(&count)
	if count != 0 {
		t.Fatal("invalid requests provisioned account")
	}
}
