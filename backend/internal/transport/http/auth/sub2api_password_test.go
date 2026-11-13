package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	appauth "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/auth"
	personal "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/personalprovider"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	providerrepo "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/postgres/personalprovider"
	userrepo "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/postgres/user"
	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type httpPasswordRemote struct {
	exchangeRemote
	twoFactor  bool
	otpInvalid bool
	password   string
	calls      int
}

func (r *httpPasswordRemote) PasswordSettings(context.Context, string) (port.PasswordSettings, error) {
	return port.PasswordSettings{Version: "0.2.21", CaptchaProvider: "none", TotpEnabled: true}, nil
}
func (r *httpPasswordRemote) PasswordLogin(_ context.Context, _ string, input port.PasswordCredentials) (port.LoginProof, error) {
	r.calls++
	if input.Password != r.password {
		return port.LoginProof{}, &port.PasswordError{Kind: "invalid_credentials"}
	}
	if r.twoFactor {
		return port.LoginProof{RequiresTwoFactor: true, TempToken: "synthetic-temp", MaskedEmail: "a***"}, nil
	}
	return port.LoginProof{AccessToken: "synthetic-access", UserID: 7}, nil
}
func (r *httpPasswordRemote) PasswordTwoFactor(context.Context, string, string, string) (port.LoginProof, error) {
	if r.otpInvalid {
		return port.LoginProof{}, &port.PasswordError{Kind: "two_factor_invalid"}
	}
	return port.LoginProof{AccessToken: "synthetic-access", UserID: 7}, nil
}

func httpPasswordFixture(t *testing.T) (*gin.Engine, *gorm.DB, *httpPasswordRemote, *config.Runtime) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "password.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err = db.AutoMigrate(&models.User{}, &models.UserCredential{}, &models.UserSession{}, &models.UserAuthEvent{}, &models.UserTwoFactor{}, &models.UserIdentity{}, &models.Sub2APIIdentity{}, &models.LLMUserProvider{}); err != nil {
		t.Fatal(err)
	}
	runtime := config.NewRuntime(config.Config{Env: "production", CORSAllowOrigin: "https://chat.example.com", JWTSecret: "synthetic-signing-secret", DataEncryptionKey: "synthetic-encryption-secret", Sub2APIEnabled: true, Sub2APIPasswordLoginEnabled: true, Sub2APIBaseURL: "https://sub.example.com", PersonalProvidersEnabled: true, PersonalProvidersMaxPerUser: 5})
	repo := userrepo.NewRepo(db)
	service := appauth.NewServiceWithRuntime(runtime, repo, nil, nil)
	remote := &httpPasswordRemote{exchangeRemote: exchangeRemote{id: 7}, password: "remote-password"}
	service.SetSub2API(remote, repo, personal.NewService(runtime, providerrepo.NewRepo(db), exchangeModels{}))
	router := gin.New()
	router.Use(middleware.RequestID())
	module := NewModule(NewHandler(service))
	module.RegisterPublicRoutes(router.Group("/api/v1"), middleware.NewFeatureGate(runtime))
	module.RegisterSub2APIRoutes(router.Group("/api/v1"), exchangeAllowLimiter{}, runtime)
	return router, db, remote, runtime
}
func passwordHTTPRequest(router *gin.Engine, path, body, origin, platform string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "https://chat.example.com/api/v1/auth/sub2api/"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("User-Agent", "test-browser")
	req.RemoteAddr = "203.0.113.10:1234"
	if platform != "" {
		req.Header.Set(ClientPlatformHeader, platform)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

const httpPasswordBody = `{"email":"same@example.test","password":"remote-password"}`

func TestHTTPPasswordIdentityIsolationAndPasswordChanges(t *testing.T) {
	router, db, remote, _ := httpPasswordFixture(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("local-password"), bcrypt.MinCost)
	admin := models.User{PublicID: "local-admin", Username: "local-admin", Email: "same@example.test", Role: "admin", Status: "active"}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.UserCredential{UserID: admin.ID, PasswordHash: string(hash), PasswordEnabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	// Establish the existing URL exchange identity first.
	initial := exchangeRequest(router, exchangeBody)
	if initial.Code != 200 {
		t.Fatal(initial.Body.String())
	}
	var identity models.Sub2APIIdentity
	db.First(&identity)
	response := passwordHTTPRequest(router, "login", httpPasswordBody, "https://chat.example.com", "", nil)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var body struct {
		Data struct {
			Stage string        `json:"stage"`
			Login LoginResponse `json:"login"`
		}
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Stage != "authenticated" || body.Data.Login.User.ID != identity.UserID || body.Data.Login.User.ID == admin.ID {
		t.Fatal("identity mapping violated")
	}
	var count int64
	db.Model(&models.User{}).Count(&count)
	if count != 2 {
		t.Fatal("duplicate user")
	}
	var external models.User
	db.First(&external, identity.UserID)
	if external.Role != "user" || external.Email != "" {
		t.Fatal("email claimed or role elevated")
	}
	var credential models.UserCredential
	db.Where("user_id = ?", identity.UserID).First(&credential)
	if credential.PasswordEnabled || credential.PasswordHash != "" {
		t.Fatal("remote password persisted")
	}
	if strings.Contains(response.Body.String(), "refreshToken") || strings.Contains(response.Body.String(), "synthetic-access") {
		t.Fatal("secret exposed")
	}
	found := false
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == refreshTokenCookieName && cookie.MaxAge > 0 {
			found = true
			if !cookie.Secure || !cookie.HttpOnly {
				t.Fatal("unsafe session cookie")
			}
		}
	}
	if !found {
		t.Fatal("missing refresh cookie")
	}
	remote.password = "new-remote-password"
	for _, password := range []string{"remote-password", "local-password"} {
		body := `{"email":"same@example.test","password":"` + password + `"}`
		got := passwordHTTPRequest(router, "login", body, "https://chat.example.com", "", nil)
		if got.Code != 401 || !strings.Contains(got.Body.String(), "auth.sub2api_invalid_credentials") {
			t.Fatal("remote failure crossed local login boundary")
		}
	}
}

func TestHTTPPasswordCancelDoesNotTouchNewerSessionCookies(t *testing.T) {
	router, _, remote, _ := httpPasswordFixture(t)
	remote.twoFactor = true
	pending := passwordHTTPRequest(router, "login", httpPasswordBody, "https://chat.example.com", "", nil)
	var result struct {
		Data struct {
			FlowID string `json:"flowID"`
		} `json:"data"`
	}
	if err := json.Unmarshal(pending.Body.Bytes(), &result); err != nil || pending.Code != 200 || result.Data.FlowID == "" {
		t.Fatal("missing pending flow", err)
	}
	// Another login has replaced the shared refresh cookie while this flow waited.
	cookies := []*http.Cookie{{Name: refreshTokenCookieName, Value: "synthetic-newer-session"}}
	for _, cookie := range pending.Result().Cookies() {
		if cookie.Name == sub2APIPasswordCookie {
			cookies = append(cookies, cookie)
		}
	}
	body := `{"flowID":"` + result.Data.FlowID + `"}`
	for i := 0; i < 2; i++ {
		response := passwordHTTPRequest(router, "login/cancel", body, "https://chat.example.com", "", cookies)
		if response.Code != 200 || len(response.Header().Values("Set-Cookie")) != 0 {
			t.Fatalf("cancel mutated cookies: %d %v", response.Code, response.Header().Values("Set-Cookie"))
		}
	}
	response := passwordHTTPRequest(router, "login/2fa", `{"flowID":"`+result.Data.FlowID+`","code":"123456"}`, "https://chat.example.com", "", cookies)
	if response.Code != http.StatusGone {
		t.Fatal("flow was not cancelled", response.Code)
	}
}

func TestHTTPPasswordRemoteChallengeRetryAndLocalTwoFactor(t *testing.T) {
	router, db, remote, _ := httpPasswordFixture(t)
	remote.twoFactor = true
	first := passwordHTTPRequest(router, "login", httpPasswordBody, "https://chat.example.com", "", nil)
	if first.Code != 200 {
		t.Fatal(first.Body.String())
	}
	var initial struct {
		Data struct {
			Stage  string `json:"stage"`
			FlowID string `json:"flowID"`
		}
	}
	_ = json.Unmarshal(first.Body.Bytes(), &initial)
	if initial.Data.Stage != "remote_2fa" || initial.Data.FlowID == "" || strings.Contains(first.Body.String(), "synthetic-temp") {
		t.Fatal("challenge protocol")
	}
	var count int64
	db.Model(&models.User{}).Count(&count)
	if count != 0 {
		t.Fatal("user created before remote 2fa")
	}
	db.Model(&models.UserSession{}).Count(&count)
	if count != 0 {
		t.Fatal("session created before remote 2fa")
	}
	cookies := first.Result().Cookies()
	body := `{"flowID":"` + initial.Data.FlowID + `","code":"123456"}`
	remote.otpInvalid = true
	retry := passwordHTTPRequest(router, "login/2fa", body, "https://chat.example.com", "", cookies)
	if retry.Code != 400 || !strings.Contains(retry.Body.String(), "auth.sub2api_remote_two_factor_invalid") || !strings.Contains(retry.Body.String(), `"stage":"remote_2fa"`) {
		t.Fatal(retry.Body.String())
	}
	remote.otpInvalid = false
	complete := passwordHTTPRequest(router, "login/2fa", body, "https://chat.example.com", "", cookies)
	if complete.Code != 200 {
		t.Fatal(complete.Body.String())
	}
	replay := passwordHTTPRequest(router, "login/2fa", body, "https://chat.example.com", "", cookies)
	if replay.Code != 410 {
		t.Fatal("replayed")
	}
	var user models.User
	db.First(&user)
	if err := db.Create(&models.UserTwoFactor{UserID: user.ID, TOTPEnabled: true, TOTPSecretEncrypted: "synthetic-encrypted-secret"}).Error; err != nil {
		t.Fatal(err)
	}
	remote.twoFactor = false
	local := passwordHTTPRequest(router, "login", httpPasswordBody, "https://chat.example.com", "", cookies)
	if local.Code != 200 || !strings.Contains(local.Body.String(), `"stage":"local_2fa"`) || !strings.Contains(local.Body.String(), `"twoFactorRequired":true`) {
		t.Fatal(local.Body.String())
	}
	for _, cookie := range local.Result().Cookies() {
		if cookie.Name == refreshTokenCookieName && cookie.MaxAge > 0 {
			t.Fatal("session before local 2fa")
		}
	}
}

func TestHTTPPasswordBoundariesStrictJSONAndDisabled(t *testing.T) {
	for _, tc := range []struct {
		name, body, origin, platform string
		status                       int
	}{
		{"unknown remote ID", `{"email":"same@example.test","password":"remote-password","userID":7}`, "https://chat.example.com", "", 400},
		{"unknown nested", `{"email":"same@example.test","password":"remote-password","captcha":{"provider":"none","url":"https://evil.example.com"}}`, "https://chat.example.com", "", 400},
		{"trailing", httpPasswordBody + ` {}`, "https://chat.example.com", "", 400},
		{"oversize", `{"password":"` + strings.Repeat("x", 257<<10) + `"}`, "https://chat.example.com", "", 413},
		{"cross site", httpPasswordBody, "https://evil.example.com", "", 403},
		{"native", httpPasswordBody, "tauri://localhost", "desktop", 403},
		{"desktop web header", httpPasswordBody, "https://chat.example.com", "desktop", 403},
		{"missing origin", httpPasswordBody, "", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, _, remote, _ := httpPasswordFixture(t)
			r := passwordHTTPRequest(router, "login", tc.body, tc.origin, tc.platform, nil)
			if r.Code != tc.status || remote.calls != 0 {
				t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
			}
			if tc.origin != "https://chat.example.com" && len(r.Result().Cookies()) != 0 {
				t.Fatal("untrusted request mutated cookies")
			}
		})
	}
	router, _, remote, runtime := httpPasswordFixture(t)
	cfg := runtime.Snapshot()
	cfg.Sub2APIPasswordLoginEnabled = false
	runtime.Store(cfg)
	r := passwordHTTPRequest(router, "login", httpPasswordBody, "https://chat.example.com", "", nil)
	if r.Code != 403 || remote.calls != 0 {
		t.Fatal("disabled endpoint active")
	}
	req := httptest.NewRequest(http.MethodGet, "https://chat.example.com/api/v1/auth/sub2api/login-options", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) || !strings.Contains(w.Body.String(), `"browserOnly":true`) {
		t.Fatal(w.Body.String())
	}
}

func TestHTTPPasswordMandatoryLimiterAndCancelContract(t *testing.T) {
	router, _, remote, runtime := httpPasswordFixture(t)
	h := NewHandler(appauth.NewServiceWithRuntime(runtime, nil, nil, nil))
	route := gin.New()
	route.POST("/api/v1/auth/sub2api/login", h.sub2APIPasswordBoundary("https://chat.example.com"), h.sub2APIPasswordRateLimit(nil, runtime), h.LoginSub2APIPassword)
	rejected := passwordHTTPRequest(route, "login", httpPasswordBody, "https://chat.example.com", "", nil)
	if rejected.Code != 503 || !strings.Contains(rejected.Body.String(), "auth.sub2api_password_unavailable") {
		t.Fatal("limiter did not fail closed")
	}
	remote.twoFactor = true
	first := passwordHTTPRequest(router, "login", httpPasswordBody, "https://chat.example.com", "", nil)
	var payload struct {
		Data struct {
			FlowID string `json:"flowID"`
		}
	}
	_ = json.Unmarshal(first.Body.Bytes(), &payload)
	body := `{"flowID":"` + payload.Data.FlowID + `"}`
	for range 2 {
		cancelled := passwordHTTPRequest(router, "login/cancel", body, "https://chat.example.com", "", first.Result().Cookies())
		if cancelled.Code != 200 || !strings.Contains(cancelled.Body.String(), `"cancelled":true`) {
			t.Fatal("cancel is not idempotent")
		}
	}
	next := passwordHTTPRequest(router, "login/2fa", `{"flowID":"`+payload.Data.FlowID+`","code":"123456"}`, "https://chat.example.com", "", first.Result().Cookies())
	if next.Code != 410 {
		t.Fatal("cancelled flow remained usable")
	}
}

func TestHTTPPasswordPreservesExchangeErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{appauth.ErrInvalidCredentials, 401, "auth.invalid_credentials"},
		{appauth.ErrSub2APINoKey, 403, "auth.sub2api_no_usable_key"},
		{appauth.ErrSub2APIUnavailable, 502, "auth.sub2api_unavailable"},
	} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		handler := NewHandler(nil)
		handler.writeSub2APIPasswordResult(ctx, nil, tc.err)
		if recorder.Code != tc.status || !strings.Contains(recorder.Body.String(), tc.code) {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
}
