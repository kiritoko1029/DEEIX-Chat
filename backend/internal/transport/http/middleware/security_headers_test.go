package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSecurityHeadersProtectSub2APIEntryAtResponseCommit(t *testing.T) {
	for _, target := range []string{"/?user_id=1&token=synthetic", "/?token=%ZZ", "/?user_id=", "/?%74oken=x", "/api/v1/auth/sub2api/exchange"} {
		for _, mode := range []string{"body", "head", "flush", "gzip"} {
			t.Run(target+mode, func(t *testing.T) {
				router := gin.New()
				router.Use(SecurityHeaders(), Compress())
				router.Any("/", func(c *gin.Context) {
					c.Header("Cache-Control", "public, max-age=3600")
					c.Header("Expires", "future")
					if mode == "head" {
						c.Status(204)
						return
					}
					if mode == "flush" {
						c.Writer.Flush()
						return
					}
					c.String(200, strings.Repeat("entry", 500))
				})
				router.POST("/api/v1/auth/sub2api/exchange", func(c *gin.Context) { c.Header("Cache-Control", "public"); c.Status(429) })
				method := http.MethodGet
				if strings.Contains(target, "/exchange") {
					method = http.MethodPost
				}
				r := httptest.NewRequest(method, target, nil)
				if mode == "gzip" {
					r.Header.Set("Accept-Encoding", "gzip")
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				h := w.Result().Header
				if h.Get("Cache-Control") != "no-store" || h.Get("Expires") != "" || h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Frame-Options") != "DENY" {
					t.Fatalf("headers=%v", h)
				}
			})
		}
	}
}

func TestTrustedSub2APIEmbeddingHeaders(t *testing.T) {
	for _, tt := range []struct{ origin, target, csp, xfo string }{
		{"https://sub.example.test", "/?src_host=https://attacker.example", "frame-ancestors 'self' https://sub.example.test", ""},
		{"https://sub.example.test", "/chat", "frame-ancestors 'self' https://sub.example.test", ""},
		{"https://sub.example.test", "/api/v1/files/content", "", "DENY"},
		{"", "/", "", "DENY"},
		{"*", "/", "", "DENY"},
		{"https://sub.example.test; frame-src *", "/", "", "DENY"},
		{"https://sub.example.test/path", "/", "", "DENY"},
		{"https://127.0.0.1", "/", "", "DENY"},
	} {
		t.Run(tt.origin+tt.target, func(t *testing.T) {
			router := gin.New()
			router.Use(SecurityHeaders(tt.origin))
			router.NoRoute(func(c *gin.Context) { c.String(200, "test") })
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", tt.target, nil))
			if w.Header().Get("Content-Security-Policy") != tt.csp || w.Header().Get("X-Frame-Options") != tt.xfo {
				t.Fatal(w.Header())
			}
		})
	}
}

func TestSecurityHeadersLeaveOrdinaryCachePolicy(t *testing.T) {
	router := gin.New()
	router.Use(SecurityHeaders())
	router.GET("/", func(c *gin.Context) { c.Header("Cache-Control", "public, max-age=3600"); c.String(200, "index") })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/?theme=dark", nil))
	if w.Result().Header.Get("Cache-Control") != "public, max-age=3600" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal(w.Header())
	}
}
