package hooks

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zwhu/memlane/kit/agents"
)

const (
	HostTraeIDE     = "trae-ide"
	HostTraeX       = agents.HostTraeX
	HostCodex       = agents.HostCodex
	HostAntigravity = agents.HostAntigravity
	HostPi          = agents.HostPi

	SessionStart       = "SessionStart"
	UserPromptSubmit   = "UserPromptSubmit"
	PostToolUse        = "PostToolUse"
	PostToolUseFailure = "PostToolUseFailure"
	Stop               = "Stop"
	SessionEnd         = "SessionEnd"
)

func CanonicalHost(host string) string {
	switch host = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(host)), "_", "-"); host {
	case HostTraeIDE, HostTraeX, HostCodex, HostAntigravity, HostPi:
		return host
	}
	return ""
}

type Config struct {
	GatewayURL, KeyFile string
	Timeout             time.Duration
	HTTPClient          *http.Client
	MaxInputBytes       int64
	Verbose             bool
	ErrOut              io.Writer
}

var (
	ErrInvalidGateway = errors.New("invalid loopback gateway")
	ErrInvalidKey     = errors.New("invalid private key file")
	ErrInput          = errors.New("invalid hook input")
	ErrEvent          = errors.New("unsupported hook host or event")
	ErrTransport      = errors.New("gateway request failed")
	ErrResponse       = errors.New("invalid gateway response")
)

type HTTPError int

func (e HTTPError) Error() string { return "gateway returned HTTP " + strconv.Itoa(int(e)) }

func ValidateGatewayURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		strings.Contains(raw, "#") || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return ErrInvalidGateway
	}
	if host := u.Hostname(); host != "localhost" && host != "127.0.0.1" {
		return ErrInvalidGateway
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil || port == 0 {
		return ErrInvalidGateway
	}
	return nil
}

func ReadPrivateKey(path string) (string, error) {
	valid := func(info os.FileInfo) bool {
		return info.Mode() == 0o600 && info.Size() >= 1 && info.Size() <= 4096
	}
	initial, err := os.Lstat(path)
	if err != nil || !valid(initial) {
		return "", ErrInvalidKey
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", ErrInvalidKey
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !valid(info) || !os.SameFile(initial, info) {
		return "", ErrInvalidKey
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	key := strings.TrimSpace(string(data))
	if err != nil || len(data) > 4096 || key == "" || strings.ContainsFunc(key, func(r rune) bool { return r < 0x21 || r > 0x7e }) {
		return "", ErrInvalidKey
	}
	return key, nil
}
