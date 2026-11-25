package profiles

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

type AgentFailure struct {
	ExitCode *int
	Reason   string
	Detail   string
}

func (e *AgentFailure) Error() string { return "profile_agent_failed" }
func (e *AgentFailure) Unwrap() error {
	if e.Reason == ErrOutputPipeOpen.Error() {
		return ErrOutputPipeOpen
	}
	return nil
}

func agentFailure(code *int, stderr []byte) error {
	text := string(stderr)
	return &AgentFailure{ExitCode: code, Reason: ClassifyFailureReason(text), Detail: SanitizeFailureDetail(text)}
}

var ansi = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\))`)
var errorLine = regexp.MustCompile(`(?i)\b(error|fatal|failed|unavailable|invalid|cannot|unsupported|could not|not supported|no complete|unexpected argument)\b`)
var redactions = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s<>]+`),
	regexp.MustCompile(`(?i)\bbearer\s+[^\s,;]+`),
	regexp.MustCompile(`(?i)["']?(?:api[_ -]?key|token|secret|password|authorization)["']?\s*[=:]\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)`),
	regexp.MustCompile(`\b(?:sk-|ghp_|github_pat_)[A-Za-z0-9_-]+`),
	regexp.MustCompile(`(?i)\b(?:SECRET|PRIVATE|API_KEY|TOKEN)[A-Za-z0-9_-]*\b`),
	regexp.MustCompile(`[\w.+-]+@[\w.-]+\.[a-zA-Z]{2,}`),
	regexp.MustCompile(`(?:/Users/|/private/|/var/|/tmp/|/home/)[^\r\n"'<> ,;]+(?: [^\r\n"'<> ,;]+)*`),
}

func SanitizeFailureDetail(stderr string) string {
	lines := strings.Split(ansi.ReplaceAllString(stderr, ""), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !errorLine.MatchString(line) {
			continue
		}
		for _, pattern := range redactions {
			line = pattern.ReplaceAllString(line, "<redacted>")
		}
		runes := []rune(line)
		return string(runes[:min(400, len(runes))])
	}
	return ""
}

func ClassifyFailureReason(stderr string) string {
	text := strings.ToLower(ansi.ReplaceAllString(stderr, ""))
	groups := []struct {
		reason string
		terms  []string
	}{
		{"arguments_rejected", []string{"unexpected argument", "unrecognized argument", "unknown option"}},
		{"authentication_required", []string{"unauthorized", "authentication failed", "authentication required", "not logged in", "please log in"}},
		{"quota_unavailable", []string{"rate limit", "quota exceeded", "usage limit"}},
		{"connection_failed", []string{"connection refused", "failed to connect", "network is unreachable"}},
	}
	for _, group := range groups {
		for _, term := range group.terms {
			if strings.Contains(text, term) {
				return group.reason
			}
		}
	}
	return "agent_exited"
}

func errorCode(err error) string {
	for _, known := range []error{ErrOutputPipeOpen, ErrSaveUnconfirmed, ErrCancelUnavailable, ErrImportUnavailable, ErrInvalidDocument, errInterrupted} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	var agent *AgentFailure
	switch {
	case errors.As(err, &agent):
		return agent.Error()
	case errors.Is(err, context.Canceled):
		return "profile_cancelled"
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, engine.ErrNotFound):
		return "profile_not_found"
	case errors.Is(err, domain.ErrConflict):
		return "profile_conflict"
	case errors.Is(err, domain.ErrInvalidArgument):
		return "profile_invalid_request"
	default:
		return "profile_unavailable"
	}
}
