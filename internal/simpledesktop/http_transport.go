package simpledesktop

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

var sharedHTTPTransport = http.DefaultTransport.(*http.Transport).Clone()

func noRedirectFunc(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func newHTTPClient(override *http.Client, timeout time.Duration) *http.Client {
	if override != nil {
		cloned := *override
		cloned.CheckRedirect = noRedirectFunc
		return &cloned
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     sharedHTTPTransport,
		CheckRedirect: noRedirectFunc,
	}
}

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
	var official *OfficialHTTPError
	if errors.As(err, &official) {
		return official.Status, true
	}
	var control *ControlResponseError
	if errors.As(err, &control) {
		return control.Status, true
	}
	msg := err.Error()
	for _, code := range []int{400, 401, 403, 404, 409, 500, 503} {
		if strings.Contains(msg, fmt.Sprintf("HTTP %d", code)) {
			return code, true
		}
	}
	return 0, false
}

func readCachedToken(path string, minLen int, requirePrivate bool) (string, error) {
	if requirePrivate {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return "", errors.New("private credential unavailable")
		}
	} else {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return "", errors.New("credential unavailable")
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < minLen {
		return "", errors.New("credential too short")
	}
	return token, nil
}
