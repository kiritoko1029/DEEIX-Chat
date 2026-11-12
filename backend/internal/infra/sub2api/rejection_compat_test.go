package sub2api

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
)

func TestRejectedResponseSupportsStringCodeWithoutLeakingBody(t *testing.T) {
	for _, tt := range []struct{ name, body, want string }{
		{"legacy invalid token", `{"code":"INVALID_TOKEN","message":"FAKE_SECRET"}`, port.ReasonTokenInvalid},
		{"legacy expired token", `{"code":"TOKEN_EXPIRED"}`, port.ReasonTokenExpired},
		{"legacy session binding", `{"code":"SESSION_BINDING_MISMATCH"}`, port.ReasonSessionBindingMismatch},
		{"numeric code with reason", `{"code":401,"reason":"TOKEN_EXPIRED"}`, port.ReasonTokenExpired},
		{"numeric code alone", `{"code":401}`, port.ReasonUpstreamUnauthorized},
		{"unknown code", `{"code":"FAKE_SECRET","message":"TOKEN_EXPIRED"}`, port.ReasonUpstreamUnauthorized},
		{"object code", `{"code":{"reason":"TOKEN_EXPIRED"}}`, port.ReasonUpstreamUnauthorized},
		{"reason takes precedence", `{"reason":"TOKEN_EXPIRED","code":"INVALID_TOKEN"}`, port.ReasonTokenExpired},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(tt.body))}
			err := rejectedResponse(resp, port.StageAuthMe)
			details := port.RejectionDetailsFrom(err)
			if !errors.Is(err, port.ErrRejected) || details.Stage != port.StageAuthMe || details.Reason != tt.want {
				t.Fatalf("details=%+v err=%v", details, err)
			}
			if strings.Contains(err.Error(), "FAKE_SECRET") {
				t.Fatal("leaked upstream body")
			}
		})
	}
}
