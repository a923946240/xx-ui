package sub

import (
	"encoding/base64"
	"net/url"
	"strings"
)

// shadowrocketSubscriptionLink uses Shadowrocket's Base64URL userinfo dialect
// for SS2022: its plaintext importer can retain percent escapes in the password.
// This is a client-specific compatibility format, not the SIP002 AEAD-2022
// encoding. Keep the standard generator and all other clients unchanged.
func shadowrocketSubscriptionLink(link, userAgent string) string {
	agent := strings.ToLower(strings.TrimSpace(userAgent))
	if agent != "shadowrocket" && !strings.HasPrefix(agent, "shadowrocket/") && !strings.HasPrefix(agent, "shadowrocket ") {
		return link
	}
	lines := strings.Split(link, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "ss://2022-") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.User == nil || u.Host == "" {
			continue
		}
		password, ok := u.User.Password()
		if !ok {
			continue
		}
		_, rest, ok := strings.Cut(line, "@")
		if !ok {
			continue
		}
		userinfo := base64.RawURLEncoding.EncodeToString([]byte(u.User.Username() + ":" + password))
		lines[i] = "ss://" + userinfo + "@" + rest
	}
	return strings.Join(lines, "\n")
}
