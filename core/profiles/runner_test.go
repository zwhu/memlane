package profiles

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestProcessOutputAndPostExitPipeGuard(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := exec.Command("/bin/sh", "-c", "printf hello; printf 'error detail' >&2")
	var states []string
	out, stderr, err := runProcess(ctx, cmd, time.Millisecond, time.Second, func(p Progress) {
		states = append(states, p.State)
	})
	if err != nil || string(out) != "hello" || string(stderr) != "error detail" || states[len(states)-1] != "exited" {
		t.Fatalf("out=%q stderr=%q progress=%v err=%v", out, stderr, states, err)
	}

	cmd = exec.Command("/bin/sh", "-c", "sleep 30 & exit 0")
	states = nil
	out, stderr, err = runProcess(ctx, cmd, time.Millisecond, 30*time.Millisecond, func(p Progress) {
		states = append(states, p.State)
	})
	if !errors.Is(err, ErrOutputPipeOpen) || out != nil || stderr != nil || !slices.Contains(states, "exited") {
		t.Fatalf("pipe guard: %+v, %v", states, err)
	}
}

func TestProcessCancellationAndIsolatedEnvironment(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := exec.Command("/bin/sh", "-c", "printf active; sleep 30 & wait")
	_, _, err := runProcess(ctx, cmd, 2*time.Millisecond, time.Second, func(p Progress) {
		if p.State == "activity" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runProcess: %v", err)
	}

	env := isolatedEnv(strings.Fields("PATH=/bin HOME=/tmp COGNEE_TOKEN=1 MEMLANE_PROJECT_NAME=1 LLM_MODEL=1 OPENAI_API_KEY=1 ANTHROPIC_API_KEY=1 GEMINI_API_KEY=1 OLLAMA_HOST=1 FALLBACK_PROVIDER=1 MEMLANE_PROCESSING_SESSION=0 openai_api_key=1 cognee_token=1"))
	if len(env) != 3 || env["MEMLANE_PROCESSING_SESSION"] != "1" || env["PATH"] != "/bin" || env["HOME"] != "/tmp" {
		t.Fatalf("unexpected environment: %v", env)
	}
}
