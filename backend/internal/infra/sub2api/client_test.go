package sub2api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fakeResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}
}

func TestRejectedResponsesAllowlistAndNeverEchoUpstream(t *testing.T) {
	const sensitive = "synthetic-token-key-id-url-must-not-escape"
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"binding", `{"reason":"SESSION_BINDING_MISMATCH","message":"` + sensitive + `"}`, port.ReasonSessionBindingMismatch, 401},
		{"expired", `{"reason":"TOKEN_EXPIRED"}`, port.ReasonTokenExpired, 401},
		{"invalid", `{"reason":"INVALID_TOKEN"}`, port.ReasonTokenInvalid, 401},
		{"revoked", `{"reason":"TOKEN_REVOKED"}`, port.ReasonTokenInvalid, 401},
		{"empty", `{"reason":"EMPTY_TOKEN"}`, port.ReasonTokenInvalid, 401},
		{"header", `{"reason":"INVALID_AUTH_HEADER"}`, port.ReasonTokenInvalid, 401},
		{"inactive", `{"reason":"USER_INACTIVE"}`, port.ReasonUserInactive, 403},
		{"unknown401", `{"reason":"` + sensitive + `","message":"TOKEN_EXPIRED"}`, port.ReasonUpstreamUnauthorized, 401},
		{"unknown403", `{"reason":"` + sensitive + `"}`, port.ReasonUpstreamForbidden, 403},
		{"malicious object", `{"reason":{"token":"` + sensitive + `"}}`, port.ReasonUpstreamUnauthorized, 401},
		{"nested reason", `{"error":{"reason":"TOKEN_EXPIRED"}}`, port.ReasonUpstreamUnauthorized, 401},
		{"injected suffix", `{"reason":"TOKEN_EXPIRED ` + sensitive + `"}`, port.ReasonUpstreamUnauthorized, 401},
		{"malformed", `{"reason":"TOKEN_EXPIRED"} ` + sensitive, port.ReasonUpstreamForbidden, 403},
		{"oversized", `{"reason":"SESSION_BINDING_MISMATCH","message":"` + strings.Repeat("x", maxRejectedResponseBytes) + `"}`, port.ReasonUpstreamUnauthorized, 401},
	} {
		for _, stage := range []string{port.StageAuthMe, port.StageKeys} {
			t.Run(tc.name+stage, func(t *testing.T) {
				c := New()
				c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if stage == port.StageKeys && strings.HasSuffix(r.URL.Path, "/me") {
						return fakeResponse(r, 200, `{"code":0,"data":{"id":7,"status":"active"}}`), nil
					}
					return fakeResponse(r, tc.status, tc.body), nil
				})
				_, err := c.Fetch(context.Background(), "https://sub.example.com", sensitive, 7, false)
				if !errors.Is(err, port.ErrRejected) {
					t.Fatalf("error=%v", err)
				}
				details := port.RejectionDetailsFrom(err)
				if details.Stage != stage || details.Reason != tc.want {
					t.Fatalf("details=%+v", details)
				}
				encoded, _ := json.Marshal(details)
				if strings.Contains(err.Error()+string(encoded), sensitive) {
					t.Fatal("credentials leaked")
				}
			})
		}
	}
}

type rejectionCountingReader struct{ bytesRead int }

func (r *rejectionCountingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	r.bytesRead += len(p)
	return len(p), nil
}
func TestRejectedResponseReadIsBounded(t *testing.T) {
	r := &rejectionCountingReader{}
	err := rejectedResponse(&http.Response{StatusCode: 403, Body: io.NopCloser(r)}, port.StageKeys)
	if r.bytesRead != maxRejectedResponseBytes+1 || port.RejectionDetailsFrom(err).Reason != port.ReasonUpstreamForbidden {
		t.Fatalf("bytes=%d error=%v", r.bytesRead, err)
	}
}

func TestFetchIdentityAndKeyMismatchDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, me, keys, stage, reason string }{
		{"mismatched ID", `{"id":8,"status":"active"}`, `[]`, port.StageAuthMe, port.ReasonUserIDMismatch},
		{"inactive", `{"id":7,"status":"disabled"}`, `[]`, port.StageAuthMe, port.ReasonUserInactive},
		{"key owner", `{"id":7,"status":"active"}`, `[{"id":12345,"user_id":88888,"key":"synthetic-secret-key"}]`, port.StageKeys, port.ReasonKeyOwnerMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New()
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := `{"code":0,"data":` + tc.me + `}`
				if strings.HasSuffix(r.URL.Path, "/keys") {
					body = `{"code":0,"data":{"items":` + tc.keys + `,"total":1,"page":1,"pages":1,"page_size":100}}`
				}
				return fakeResponse(r, 200, body), nil
			})
			_, err := c.Fetch(context.Background(), "https://sub.example.com", "synthetic-token", 7, false)
			details := port.RejectionDetailsFrom(err)
			if !errors.Is(err, port.ErrRejected) || details.Stage != tc.stage || details.Reason != tc.reason {
				t.Fatalf("error=%v details=%+v", err, details)
			}
		})
	}
}

func TestFetchValidatesIdentityAndEveryKey(t *testing.T) {
	for _, tc := range []struct {
		name, me, keys string
		want           error
	}{
		{"valid", `{"id":7,"status":"active"}`, `[{"id":1,"user_id":7}]`, nil},
		{"wrong identity", `{"id":8,"status":"active"}`, `[]`, port.ErrRejected},
		{"inactive", `{"id":7,"status":"disabled"}`, `[]`, port.ErrRejected},
		{"wrong key owner", `{"id":7,"status":"active"}`, `[{"id":1,"user_id":9,"status":"inactive"}]`, port.ErrRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New()
			calls := 0
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("X-Forwarded-For") != "" {
					t.Fatal("forwarded client identity")
				}
				body := ""
				switch r.URL.Path {
				case "/api/v1/auth/me":
					body = `{"code":0,"data":` + tc.me + `}`
				case "/api/v1/keys":
					body = `{"code":0,"data":{"items":` + tc.keys + `,"total":1,"page":1,"pages":1,"page_size":100}}`
				case "/api/v1/settings/public":
					if r.Header.Get("Authorization") != "" {
						t.Fatal("JWT sent to public settings")
					}
					body = `{"code":0,"data":{"api_base_url":"https://gateway.example.com"}}`
				default:
					t.Fatal(r.URL.Path)
				}
				return fakeResponse(r, 200, body), nil
			})
			got, err := c.Fetch(context.Background(), "https://sub.example.com", "fake-login-token", 7, true)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
			if tc.want == nil && (len(got.Keys) != 1 || got.APIBaseURL == "") {
				t.Fatalf("snapshot=%+v", got)
			}
			if tc.name == "wrong identity" && calls != 1 {
				t.Fatal("fetched keys before identity validation")
			}
		})
	}
}

func TestFetchRejectsBadResponsesAndRedirects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"invalid JSON", 200, "sensitive upstream failure"},
		{"missing envelope code", 200, `{"data":{"id":7,"status":"active"}}`},
		{"null data", 200, `{"code":0,"data":null}`},
		{"too large", 200, strings.Repeat("x", maxResponseBytes+1)},
		{"redirect", 302, ""},
		{"failure", 500, "fake-login-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New()
			calls := 0
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				resp := fakeResponse(r, tc.status, tc.body)
				resp.Header.Set("Location", "https://attacker.example.com")
				return resp, nil
			})
			_, err := c.Fetch(context.Background(), "https://sub.example.com", "fake-login-token", 7, false)
			if !errors.Is(err, port.ErrUnavailable) || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
			if strings.Contains(err.Error(), "fake-login-token") {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestFetchPaginationAndBounds(t *testing.T) {
	for _, bad := range []bool{false, true} {
		c := New()
		calls := 0
		c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if strings.HasSuffix(r.URL.Path, "/me") {
				return fakeResponse(r, 200, `{"code":0,"data":{"id":7,"status":"active"}}`), nil
			}
			body := `{"code":0,"data":{"items":[{"id":1,"user_id":7}],"total":2,"page":1,"pages":2,"page_size":100}}`
			if r.URL.Query().Get("page") == "2" {
				body = `{"code":0,"data":{"items":[{"id":2,"user_id":7}],"total":2,"page":2,"pages":2,"page_size":100}}`
			}
			if bad {
				body = `{"code":0,"data":{"items":[],"total":3000,"page":1,"pages":30,"page_size":100}}`
			}
			return fakeResponse(r, 200, body), nil
		})
		snapshot, err := c.Fetch(context.Background(), "https://sub.example.com/api/v1/", "fake-login-token", 7, false)
		if bad {
			if !errors.Is(err, port.ErrUnavailable) || calls != 2 {
				t.Fatalf("%v / %d", err, calls)
			}
		} else if err != nil || len(snapshot.Keys) != 2 || calls != 3 {
			t.Fatalf("%v / %d", err, calls)
		}
	}
}

func TestClientPublicOnlyAndNoEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:8123")
	c := New()
	tr := c.http.Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Fatal("credential transport uses environment proxy")
	}
	for _, target := range []string{"http://example.com", "https://127.0.0.1", "https://169.254.169.254", "https://[::1]", "https://user:pass@example.com"} {
		_, err := c.Fetch(context.Background(), target, "fake-login-token", 7, false)
		if !errors.Is(err, port.ErrUnavailable) {
			t.Fatalf("accepted %s: %v", target, err)
		}
	}
	// localhost passes no literal IP shortcut; the guarded dialer must reject its DNS resolution.
	if conn, err := tr.DialContext(context.Background(), "tcp", "localhost:443"); err == nil {
		conn.Close()
		t.Fatal("DNS localhost resolved to forbidden address")
	}
}
