package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/gin-gonic/gin"
)

func oldCookieExchange(router *gin.Engine, body, mediaType, origin, platform string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sub2api/exchange", strings.NewReader(body))
	r.Header.Set("Content-Type", mediaType)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if platform != "" {
		r.Header.Set(ClientPlatformHeader, platform)
	}
	r.AddCookie(&http.Cookie{Name: refreshTokenCookieName, Value: "synthetic-old-account-refresh"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func assertSub2APIExpiredCookie(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != refreshTokenCookieName || cookies[0].Value != "" || cookies[0].MaxAge >= 0 || !cookies[0].HttpOnly || cookies[0].Path != "/api/v1/auth" {
		t.Fatalf("expected single refresh deletion: %+v", cookies)
	}
	if w.Result().Header.Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable boundary response")
	}
}

func TestSub2APIExchangeClearsOldCookieOnEveryTrustedFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		enabled  bool
		body     string
		remoteID int64
		want     int
	}{
		{"disabled", false, exchangeBody, 7, 404},
		{"bad JSON", true, `{`, 7, 400},
		{"bad ID", true, `{"userID":0,"token":"x"}`, 7, 400},
		{"oversize", true, `{"userID":7,"token":"` + strings.Repeat("x", 30<<10) + `"}`, 7, 413},
		{"mismatch", true, exchangeBody, 8, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, _, remote := exchangeFixture(t, tc.enabled)
			remote.id = tc.remoteID
			w := oldCookieExchange(router, tc.body, "application/json; charset=utf-8", "https://chat.example.com", "")
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			assertSub2APIExpiredCookie(t, w)
		})
	}
}

func TestSub2APIExchangeReplacesOldCookieOnSuccessAndDeletesFor2FA(t *testing.T) {
	router, db, _ := exchangeFixture(t, true)
	w := oldCookieExchange(router, exchangeBody, "application/json", "https://chat.example.com", "")
	cookies := w.Result().Cookies()
	if w.Code != 200 || len(cookies) != 1 || cookies[0].Value == "" || cookies[0].Value == "synthetic-old-account-refresh" || cookies[0].MaxAge <= 0 {
		t.Fatalf("success cookie order=%+v response=%s", cookies, w.Body.String())
	}
	var user models.User
	db.First(&user)
	if err := db.Create(&models.UserTwoFactor{UserID: user.ID, TOTPEnabled: true, TOTPSecretEncrypted: "synthetic-encrypted-secret"}).Error; err != nil {
		t.Fatal(err)
	}
	w = oldCookieExchange(router, exchangeBody, "application/json", "https://chat.example.com", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"twoFactorRequired":true`) {
		t.Fatal(w.Body.String())
	}
	assertSub2APIExpiredCookie(t, w)
	native := oldCookieExchange(router, exchangeBody, "application/json", "tauri://localhost", "desktop")
	if native.Code != 200 || !strings.Contains(native.Body.String(), `"twoFactorRequired":true`) || len(native.Result().Cookies()) != 0 {
		t.Fatal("native 2FA must not modify browser cookies")
	}
}

func TestSub2APIExchangeNativeNeverSetsOrDeletesCookies(t *testing.T) {
	router, _, remote := exchangeFixture(t, true)
	for _, id := range []int64{7, 8} {
		remote.id = id
		w := oldCookieExchange(router, exchangeBody, "application/json", "tauri://localhost", "desktop")
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("native response changes browser cookies")
		}
		if id == 7 && (w.Code != 200 || !strings.Contains(w.Body.String(), `"refreshToken":`)) {
			t.Fatal(w.Body.String())
		}
		if id == 8 && w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
}

func TestSub2APIExchangeUntrustedRequestsCannotLogOut(t *testing.T) {
	router, _, _ := exchangeFixture(t, true)
	for _, tc := range []struct {
		media, origin string
		want          int
	}{
		{"application/x-www-form-urlencoded", "https://chat.example.com", 415},
		{"text/plain", "https://chat.example.com", 415},
		{"multipart/form-data; boundary=x", "https://chat.example.com", 415},
		{"", "https://chat.example.com", 415},
		{"application/json", "https://attacker.example.com", 403},
		{"text/plain", "https://attacker.example.com", 403},
		{"application/json", "null", 403},
	} {
		w := oldCookieExchange(router, exchangeBody, tc.media, tc.origin, "")
		if w.Code != tc.want || len(w.Result().Cookies()) != 0 {
			t.Fatalf("%s %s: %d cookies=%v", tc.media, tc.origin, w.Code, w.Result().Cookies())
		}
		if w.Result().Header.Get("Cache-Control") != "no-store" {
			t.Fatal("CORS error cacheable")
		}
	}
}

type exchangeDenyLimiter struct {
	exchangeAllowLimiter
	err error
}

func (l exchangeDenyLimiter) AllowFixedWindow(context.Context, []string, int, time.Duration) (bool, error) {
	return false, l.err
}
func TestSub2APIExchangeRateRejectionStillClearsOldCookie(t *testing.T) {
	for _, err := range []error{nil, errors.New("synthetic store error")} {
		router, _, _ := exchangeFixtureWithLimiter(t, true, exchangeDenyLimiter{err: err})
		w := oldCookieExchange(router, exchangeBody, "application/json", "https://chat.example.com", "")
		want := 429
		if err != nil {
			want = 503
		}
		if w.Code != want {
			t.Fatal(w.Code)
		}
		assertSub2APIExpiredCookie(t, w)
	}
}
