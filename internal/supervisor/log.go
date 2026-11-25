package supervisor

import (
	"os"
	"regexp"

	"github.com/zwhu/memlane/kit/daemon"
)

const DefaultLogLimitBytes = daemon.DefaultLogLimitBytes

var serviceLogRedactor = daemon.NewLogRedactor(daemon.RedactPattern{
	Pattern:     regexp.MustCompile(`(?i)(X-Memlane-Management[ 	]*:[ 	]*)\S+`),
	Replacement: `${1}[REDACTED]`,
})

func OpenPrivateLog(path string) (*os.File, error) {
	return daemon.OpenPrivateLog(path)
}

func RedactLogBytes(raw []byte) []byte {
	return serviceLogRedactor.RedactBytes(raw)
}

func RedactLogLine(line string) string {
	return serviceLogRedactor.RedactLine(line)
}

func TrimServiceLog(path string, limit int64) error {
	return serviceLogRedactor.TrimServiceLog(path, limit)
}

func TrimServiceLogDir(dir string, limit int64) error {
	return serviceLogRedactor.TrimServiceLogDir(dir, limit)
}

func ReadLogTail(path string, maxLines int, maxBytes int64) ([]string, error) {
	return serviceLogRedactor.ReadLogTail(path, maxLines, maxBytes)
}
