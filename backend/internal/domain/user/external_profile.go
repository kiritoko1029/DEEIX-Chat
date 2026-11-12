package user

// ExternalProfile is display-only metadata. It is never a login identifier or
// evidence of local email ownership and must not be used for password reset/2FA.
type ExternalProfile struct {
	Provider    string
	DisplayName string
	Username    string
	Email       string
}
