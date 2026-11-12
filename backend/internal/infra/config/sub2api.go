package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/security"
)

// Sub2APIFrameOrigin allows only the explicitly trusted management origin when
// embedding is opted into. Link parameters never influence this policy.
func (c Config) Sub2APIFrameOrigin() string {
	if !c.Sub2APIEnabled || !c.Sub2APIAllowEmbedding || c.LocalMode || c.validateSub2API() != nil {
		return ""
	}
	origin, err := security.HTTPOrigin(c.Sub2APIBaseURL)
	if err != nil {
		return ""
	}
	return origin
}

// validateSub2API prevents credentials from being sent to an insecure or ambiguous
// endpoint. Both endpoints are chosen by the operator, never by a login link.
func (c Config) validateSub2API() error {
	if !c.Sub2APIEnabled {
		return nil
	}
	if c.LocalMode {
		return errors.New("invalid config: sub2api login is unavailable in desktop local mode")
	}
	if strings.TrimSpace(c.Sub2APIBaseURL) == "" {
		return errors.New("invalid config: SUB2API_BASE_URL is required when SUB2API_ENABLED=true")
	}
	for label, value := range map[string]string{
		"SUB2API_BASE_URL":     c.Sub2APIBaseURL,
		"SUB2API_API_BASE_URL": c.Sub2APIAPIBaseURL,
	} {
		if value == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
			return fmt.Errorf("invalid config: %s must be an HTTPS URL without credentials, query or fragment", label)
		}
		if port := parsed.Port(); port != "" {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return fmt.Errorf("invalid config: %s has an invalid port", label)
			}
		}
		if err := security.ValidateOutboundHTTPURL(value, security.NewPublicOnlyOutboundPolicy()); err != nil {
			return fmt.Errorf("invalid config: %s must address a public HTTPS service", label)
		}
	}
	return nil
}
