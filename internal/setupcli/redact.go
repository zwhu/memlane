package setupcli

import (
	"github.com/zwhu/memlane/internal/controlplane"
	"github.com/zwhu/memlane/internal/supervisor"
	"github.com/zwhu/memlane/kit/aiinstaller/cli"
)

var installerRedactors = []func(string) string{supervisor.RedactLogLine, controlplane.RedactSecretText}
var installerLogLines = []string{"starting", "ready", "stopped", "Owned service groups exited; daily store released."}

// SanitizeString is for display paths and known metadata.
func SanitizeString(s string, home string, extraSecrets ...string) string {
	return cli.SanitizeString(s, home, installerRedactors, extraSecrets...)
}

// SanitizeLogLine emits known launcher and service state messages.
func SanitizeLogLine(line string, home string, extraSecrets ...string) string {
	return cli.SanitizeLogLine(line, home, installerLogLines, installerRedactors, extraSecrets...)
}

func safeHash(value string, short bool) string { return cli.SafeHash(value, short) }
func safeOperationID(value string) string      { return cli.SafeOperationID(value) }
func safeInstanceID(value string) string       { return cli.SafeInstanceID(value) }
func known(value string, values ...string) string {
	return cli.Known(value, values...)
}
