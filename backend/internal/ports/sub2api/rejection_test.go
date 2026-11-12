package sub2api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRejectedErrorIsSafeAndMatchesSentinel(t *testing.T) {
	for _, tc := range []struct{ stage, reason, wantStage, wantReason string }{
		{StageAuthMe, ReasonSessionBindingMismatch, StageAuthMe, ReasonSessionBindingMismatch},
		{StageKeys, ReasonKeyOwnerMismatch, StageKeys, ReasonKeyOwnerMismatch},
		{"https://private.example/token", "synthetic-secret-key", StageIdentity, ReasonUpstreamUnauthorized},
	} {
		err := NewRejected(tc.stage, tc.reason)
		if !errors.Is(err, ErrRejected) || err.Error() != ErrRejected.Error() {
			t.Fatal("rejection error contract changed")
		}
		details := RejectionDetailsFrom(fmt.Errorf("untrusted context: %w", err))
		if details.Stage != tc.wantStage || details.Reason != tc.wantReason {
			t.Fatalf("details=%+v", details)
		}
		encoded, _ := json.Marshal(details)
		if strings.Contains(string(encoded), "synthetic-secret") || strings.Contains(string(encoded), "private.example") {
			t.Fatal("untrusted details escaped")
		}
	}
}
