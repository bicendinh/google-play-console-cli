package fault

import (
	"regexp"
	"strings"
	"unicode"
)

var redactions = []*regexp.Regexp{
	regexp.MustCompile(`(?is)-----BEGIN [^-]*PRIVATE KEY-----.*?(-----END [^-]*PRIVATE KEY-----|$)`),
	regexp.MustCompile(`(?i)(?:bearer|basic)\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)(?:access_token|refresh_token|id_token|client_secret|private_key|api[_-]?key|authorization)["']?\s*[:=]\s*["']?[^\s,"';&}]+`),
	regexp.MustCompile(`\b(?:ya29\.[A-Za-z0-9._~-]+|AIza[A-Za-z0-9_-]{20,})\b`),
}

func Sanitize(message string) string {
	for _, pattern := range redactions {
		message = pattern.ReplaceAllString(message, "[REDACTED]")
	}
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, message)
	message = strings.Join(strings.Fields(message), " ")
	if len([]rune(message)) > 2048 {
		message = string([]rune(message)[:2048]) + "…"
	}
	return message
}
