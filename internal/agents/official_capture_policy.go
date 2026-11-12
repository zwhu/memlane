//go:build darwin || linux

package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unicode/utf8"
)

//go:embed official_capture_policy.py
var officialCapturePolicyWrapper string

// Paths and environment are trusted host/component settings, never IPC Agent
// payload fields. No ambient Agent environment, credentials or Python site are
// inherited. Upstream source is read once, verified, then executed from those
// exact bytes, avoiding a path replacement between verification and execution.
type OfficialCapturePolicyOptions struct {
	Python      string
	PolicyPath  string
	Environment map[string]string
}

type OfficialCapturePolicyResult struct {
	State  string          `json:"state"`
	Reason string          `json:"reason,omitempty"`
	Entry  json.RawMessage `json:"entry,omitempty"`
}

type capturePolicyOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *capturePolicyOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4*1024*1024 {
		b.overflow = true
		return 0, errors.New("capture policy output exceeds budget")
	}
	return b.Buffer.Write(p)
}

func ApplyOfficialCapturePolicy(ctx context.Context, options OfficialCapturePolicyOptions, entries []json.RawMessage) ([]OfficialCapturePolicyResult, error) {
	fail := errors.New("official capture policy could not be verified")
	if !filepath.IsAbs(options.Python) || !filepath.IsAbs(options.PolicyPath) || len(entries) > 64 {
		return nil, fail
	}
	file, err := os.Open(options.PolicyPath)
	if err != nil {
		return nil, fail
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 128*1024 {
		return nil, fail
	}
	source, err := io.ReadAll(io.LimitReader(file, 128*1024+1))
	if err != nil || len(source) > 128*1024 || !utf8.Valid(source) {
		return nil, fail
	}
	sum := sha256.Sum256(source)
	if hex.EncodeToString(sum[:]) != OfficialPiCapturePolicyDigest {
		return nil, fail
	}
	body, err := json.Marshal(struct {
		Source  string            `json:"source"`
		Entries []json.RawMessage `json:"entries"`
	}{string(source), entries})
	if err != nil || len(body) > 2*1024*1024 {
		return nil, fail
	}
	if entries == nil {
		return nil, fail
	}
	operation, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(operation, options.Python, "-I", "-S", "-c", officialCapturePolicyWrapper)
	command.Env = []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1"}
	allowed := map[string]bool{"COGNEE_CAPTURE": true, "COGNEE_CAPTURE_TOOLS": true, "COGNEE_CAPTURE_DENY_TOOLS": true, "COGNEE_CAPTURE_DENY_PATHS": true, "COGNEE_CAPTURE_REDACT": true, "COGNEE_CAPTURE_REDACT_PATTERNS": true}
	for key, value := range options.Environment {
		if !allowed[key] || len(value) > 16384 || bytes.IndexByte([]byte(value), 0) >= 0 {
			return nil, fail
		}
		command.Env = append(command.Env, key+"="+value)
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = 2 * time.Second
	command.Stdin = bytes.NewReader(body)
	var output capturePolicyOutput
	command.Stdout, command.Stderr = &output, io.Discard
	if err = command.Run(); err != nil || output.overflow {
		return nil, fail
	}
	var response struct {
		OK      bool                          `json:"ok"`
		Digest  string                        `json:"policy_digest"`
		Results []OfficialCapturePolicyResult `json:"results"`
	}
	if json.Unmarshal(output.Bytes(), &response) != nil || !response.OK || response.Digest != OfficialPiCapturePolicyDigest || len(response.Results) != len(entries) {
		return nil, fail
	}
	for _, result := range response.Results {
		if result.State == "accepted" {
			if len(result.Entry) == 0 || result.Reason != "" {
				return nil, fail
			}
		} else if result.State == "policy_filtered" {
			if len(result.Entry) != 0 || (result.Reason != "capture_disabled" && result.Reason != "tool_policy" && result.Reason != "self_capture" && result.Reason != "low_signal") {
				return nil, fail
			}
		} else {
			return nil, fail
		}
	}
	return response.Results, nil
}
