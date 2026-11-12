package auth

import (
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"

	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
)

func sub2APIText(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, value)
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return strings.TrimSpace(string(runes))
}

func sub2APIProfile(remote port.User) domainuser.ExternalProfile {
	username := sub2APIText(remote.Username, 128)
	email := strings.TrimSpace(remote.Email)
	// Reject altered/control-containing addresses instead of turning them into a different address.
	if len(email) > 254 || email != sub2APIText(email, 254) {
		email = ""
	}
	if address, err := mail.ParseAddress(email); err != nil || address.Address != email {
		email = ""
	}
	display := username
	if display == "" {
		display = sub2APIText(email, 128)
	}
	if display == "" {
		display = "sub2api 用户"
	}
	return domainuser.ExternalProfile{Provider: "sub2api", DisplayName: display, Username: username, Email: email}
}

func sub2APIProviderName(key port.Key) string {
	name := sub2APIText(key.Name, 128)
	if name == "" {
		name = "Key"
	}
	if key.Group != nil {
		if group := sub2APIText(key.Group.Name, 128); group != "" {
			name += " · " + group
		}
	}
	suffix := " · #" + formatSub2APIKeyID(key.ID)
	return sub2APIText(name, 64-len([]rune(suffix))) + suffix
}
