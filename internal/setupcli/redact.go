package setupcli

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
	"github.com/zwhu/memlane/internal/supervisor"
)

var (
	localPath           = regexp.MustCompile(`(?i)(?:^|[\s"'=:/])\.local(?:/[^\s"'<>:,;)]*)?`)
	privateText         = regexp.MustCompile(`(?i)owner\.token|management\.token|service-environment\.json|x-api-key|owner_token|hooks\.json`)
	instanceID          = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	operationIdentifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
	longHash            = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
	shortHash           = regexp.MustCompile(`^[a-fA-F0-9]{16}$`)
	versionPattern      = regexp.MustCompile(`^[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:-[A-Za-z0-9.-]{1,60})?(?:\+[A-Za-z0-9.-]{1,60})?$`)
)

// SanitizeString is for display paths and known metadata. Arbitrary errors and
// log content must first be projected onto a finite set of safe values.
func SanitizeString(s string, home string, extraSecrets ...string) string {
	for _, secret := range extraSecrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	if home != "" && home != string(filepath.Separator) && (s == home || strings.HasPrefix(s, home+string(filepath.Separator))) {
		s = "~" + strings.TrimPrefix(s, home)
	}
	if privateText.MatchString(s) {
		return "[redacted-private-content]"
	}
	s = localPath.ReplaceAllString(s, "[redacted-local-path]")
	return controlplane.RedactSecretText(supervisor.RedactLogLine(bootstrap.RedactInstallerText(s)))
}

// SanitizeLogLine keeps one placeholder per log line. Arbitrary launcher and
// service logs can contain unstructured conversation data and credentials.
func SanitizeLogLine(line string, home string, extraSecrets ...string) string {
	switch safe := strings.TrimSpace(SanitizeString(line, home, extraSecrets...)); safe {
	case "", "starting", "ready", "stopped", "Owned service groups exited; daily store released.":
		return safe
	default:
		return "[redacted-log-line]"
	}
}

func safeHash(value string, short bool) string {
	if short && shortHash.MatchString(value) || !short && longHash.MatchString(value) {
		return value
	}
	return ""
}

func safeOperationID(value string) string {
	if operationIdentifier.MatchString(value) {
		return value
	}
	return ""
}

func safeInstanceID(value string) string {
	if instanceID.MatchString(value) {
		return value
	}
	return ""
}

func known(value string, values ...string) string {
	if oneOf(value, values...) {
		return value
	}
	return ""
}
