package settings

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/internal/controlplane"
)

type OfficialHTTPError struct {
	Status int
	Body   string
}

func (e *OfficialHTTPError) Error() string {
	return fmt.Sprintf("official HTTP %d", e.Status)
}

func httpStatusCode(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var gateway *api.APIError
	if errors.As(err, &gateway) {
		return gateway.Status, true
	}
	var official *OfficialHTTPError
	if errors.As(err, &official) {
		return official.Status, true
	}
	var control *ControlResponseError
	if errors.As(err, &control) {
		return control.Status, true
	}
	var shared *controlplane.ControlError
	if errors.As(err, &shared) {
		return shared.Status, true
	}
	msg := err.Error()
	for _, code := range []int{400, 401, 403, 404, 409, 500, 503} {
		if strings.Contains(msg, fmt.Sprintf("HTTP %d", code)) {
			return code, true
		}
	}
	return 0, false
}

const maxCachedTokenBytes = 64 << 10

func readCachedToken(path string, minLen int, requirePrivate bool) (string, error) {
	flags := os.O_RDONLY | syscall.O_NONBLOCK
	unavailable := "credential unavailable"
	if requirePrivate {
		flags |= syscall.O_NOFOLLOW
		unavailable = "private credential unavailable"
	}
	f, err := os.OpenFile(path, flags, 0)
	if err != nil {
		return "", errors.New(unavailable)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || (requirePrivate && info.Mode().Perm()&0077 != 0) {
		return "", errors.New(unavailable)
	}
	if info.Size() > maxCachedTokenBytes {
		return "", errors.New("credential too large")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxCachedTokenBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(raw)) > maxCachedTokenBytes {
		return "", errors.New("credential too large")
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < minLen {
		return "", errors.New("credential too short")
	}
	return token, nil
}
