package sub2api

import "errors"

const (
	StageAuthMe                  = "auth_me"
	StageKeys                    = "keys"
	StageIdentity                = "identity"
	ReasonSessionBindingMismatch = "session_binding_mismatch"
	ReasonTokenExpired           = "token_expired"
	ReasonTokenInvalid           = "token_invalid"
	ReasonUserInactive           = "user_inactive"
	ReasonUserIDMismatch         = "user_id_mismatch"
	ReasonKeyOwnerMismatch       = "key_owner_mismatch"
	ReasonUpstreamUnauthorized   = "upstream_unauthorized"
	ReasonUpstreamForbidden      = "upstream_forbidden"
)

// RejectionDetails contains only fixed diagnostic categories, never upstream
// text, credentials, identifiers or endpoint addresses.
type RejectionDetails struct {
	Stage  string `json:"stage"`
	Reason string `json:"reason"`
}

func SafeRejectionDetails(stage, reason string) RejectionDetails {
	switch stage {
	case StageAuthMe, StageKeys, StageIdentity:
	default:
		stage = StageIdentity
	}
	switch reason {
	case ReasonSessionBindingMismatch, ReasonTokenExpired, ReasonTokenInvalid,
		ReasonUserInactive, ReasonUserIDMismatch, ReasonKeyOwnerMismatch,
		ReasonUpstreamUnauthorized, ReasonUpstreamForbidden:
	default:
		reason = ReasonUpstreamUnauthorized
	}
	return RejectionDetails{Stage: stage, Reason: reason}
}

type RejectedError struct{ details RejectionDetails }

func NewRejected(stage, reason string) error {
	return &RejectedError{details: SafeRejectionDetails(stage, reason)}
}
func (*RejectedError) Error() string { return ErrRejected.Error() }
func (*RejectedError) Unwrap() error { return ErrRejected }

func RejectionDetailsFrom(err error) RejectionDetails {
	var rejected *RejectedError
	if errors.As(err, &rejected) && rejected != nil {
		return SafeRejectionDetails(rejected.details.Stage, rejected.details.Reason)
	}
	return SafeRejectionDetails(StageIdentity, ReasonUpstreamUnauthorized)
}
