package controlplane

import "regexp"

var secretReplacements = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)(\b(?:proxy-)?authorization\b["']?[ \t]*[:=][ \t]*)(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|(?:(?:bearer|basic)[ \t]+)?[^\s,;"'<>]+)`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)(\b(?:set-cookie|cookie)\b["']?[ \t]*:[ \t]*)(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|[^\r\n]+)`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)(\b(?:x-api-key|api[_-]?key|[a-z0-9_-]*token|password|secret|cookie)\b["']?[ \t]*[:=][ \t]*)(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|[^\s&,;"'<>]+)`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)(\b(?:bearer|basic)[ \t]+)[^\s,;"'<>]+`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://)[^\s/"'<>@]+@`), "${1}[REDACTED]@"},
	{regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]+|eyJ[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+){0,2})`), "[REDACTED]"},
	{regexp.MustCompile(`/(Users|home)/[^/\s"'<>]+`), "/${1}/[REDACTED]"},
}

// RedactSecretText removes common credential formats and home directory usernames.
func RedactSecretText(s string) string {
	for _, rule := range secretReplacements {
		s = rule.pattern.ReplaceAllString(s, rule.replacement)
	}
	return s
}
