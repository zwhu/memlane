//go:build darwin || linux

package agents

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func officialPolicyFixture(t *testing.T) OfficialCapturePolicyOptions {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python required for official policy contract", err)
	}
	python, err = filepath.Abs(python)
	if err != nil {
		t.Fatal(err)
	}
	source, err := filepath.Abs("testdata/cognee-policy-1.7.7/_capture_policy.py")
	if err != nil {
		t.Fatal(err)
	}
	return OfficialCapturePolicyOptions{Python: python, PolicyPath: source}
}

func policyQA(question, answer string) json.RawMessage {
	value, _ := json.Marshal(map[string]any{"type": "qa", "question": question, "answer": answer, "context": `{"origin":"memlane_pi_capture_v1"}`})
	return value
}
func policyTrace(name string, params any, result any, status string) json.RawMessage {
	value, _ := json.Marshal(map[string]any{"type": "trace", "origin_function": name, "method_params": params, "method_return_value": result, "status": status, "error_message": "", "generate_feedback_with_llm": false, "memory_context": `{"origin":"memlane_pi_capture_v1"}`})
	return value
}

func TestOfficialCapturePolicyPinnedSourceRedactsBeforeTruncation(t *testing.T) {
	options := officialPolicyFixture(t)
	private := "-----BEGIN PRIVATE KEY-----\n" + strings.Repeat("synthetic-private-material-", 600) + "\n-----END PRIVATE KEY-----"
	inputs := []json.RawMessage{policyQA("check API_KEY='synthetic-password'", private), policyTrace("bash", map[string]any{"command": "synthetic-check", "api_key": "synthetic-tool-key", "nested": map[string]any{"password": "synthetic-nested-password"}}, "Bearer synthetic-test-token", "success")}
	results, err := ApplyOfficialCapturePolicy(context.Background(), options, inputs)
	if err != nil || len(results) != 2 {
		t.Fatal(results, err)
	}
	for _, result := range results {
		if result.State != "accepted" {
			t.Fatal("safe synthetic entry rejected", result.State, result.Reason)
		}
		for _, forbidden := range []string{"synthetic-private-material", "synthetic-password", "synthetic-tool-key", "synthetic-nested-password", "synthetic-test-token"} {
			if strings.Contains(string(result.Entry), forbidden) {
				t.Fatal("secret survived upstream redaction", forbidden)
			}
		}
	}
	if !strings.Contains(string(results[0].Entry), "redacted:private-key") {
		t.Fatal("long private key was clipped before upstream matching")
	}
	if !strings.Contains(string(inputs[0]), "synthetic-private-material") {
		t.Fatal("input mutated")
	}
}

func TestOfficialCapturePolicyChecksSensitivePathsBeforeProjectionLimits(t *testing.T) {
	options := officialPolicyFixture(t)
	results, err := ApplyOfficialCapturePolicy(context.Background(), options, []json.RawMessage{
		policyTrace("read", map[string]any{"path": "/synthetic/.env"}, "synthetic output", "success"),
		policyTrace("bash", map[string]any{"command": "printf " + strings.Repeat("x", 17000) + "; cat /synthetic/.env"}, "synthetic output", "success"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.State != "policy_filtered" || result.Reason != "tool_policy" || len(result.Entry) != 0 {
			t.Fatal("raw sensitive path was clipped or escaped filtering", result.State, result.Reason)
		}
	}
}

func TestOfficialCapturePolicyReusesSelfAndConservativeLowSignalRules(t *testing.T) {
	options := officialPolicyFixture(t)
	results, err := ApplyOfficialCapturePolicy(context.Background(), options, []json.RawMessage{
		policyTrace("bash", map[string]any{"command": "python3 /synthetic/.cognee-plugin/scripts/store-to-session.py"}, "synthetic", "success"),
		policyTrace("bash", map[string]any{"command": "git status"}, map[string]any{"exit_code": 0}, "success"),
		policyTrace("bash", map[string]any{"command": "git status"}, "plain host text has no structured exit outcome", "success"),
		policyTrace("bash", map[string]any{"command": "git status"}, map[string]any{"exit_code": 1, "is_error": true}, "error"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Reason != "self_capture" || results[1].Reason != "low_signal" || results[2].State != "accepted" || results[3].State != "accepted" {
		t.Fatal("upstream self/low-signal semantics changed", results)
	}
}

func TestOfficialCapturePolicyExplicitControlsAndMalformedConfiguration(t *testing.T) {
	for _, mode := range []string{"disabled", "allow", "deny", "malformed", "redaction_disabled"} {
		t.Run(mode, func(t *testing.T) {
			options := officialPolicyFixture(t)
			options.Environment = map[string]string{}
			switch mode {
			case "disabled":
				options.Environment["COGNEE_CAPTURE"] = "false"
			case "allow":
				options.Environment["COGNEE_CAPTURE_TOOLS"] = `Bash(git:*)`
			case "deny":
				options.Environment["COGNEE_CAPTURE_DENY_TOOLS"] = "Bash"
			case "malformed":
				options.Environment["COGNEE_CAPTURE_TOOLS"] = "Bash(broken)"
			case "redaction_disabled":
				options.Environment["COGNEE_CAPTURE_REDACT"] = "false"
			}
			results, err := ApplyOfficialCapturePolicy(context.Background(), options, []json.RawMessage{policyQA("question", "API_KEY='synthetic-explicit'"), policyTrace("bash", map[string]any{"command": "synthetic-check"}, "safe", "success")})
			if mode == "malformed" {
				if err == nil {
					t.Fatal("invalid upstream config silently accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "disabled" {
				if results[0].Reason != "capture_disabled" || results[1].Reason != "capture_disabled" {
					t.Fatal(results)
				}
				return
			}
			if mode == "redaction_disabled" {
				if !strings.Contains(string(results[0].Entry), "synthetic-explicit") {
					t.Fatal("explicit upstream switch ignored")
				}
				return
			}
			if results[0].State != "accepted" || results[1].Reason != "tool_policy" {
				t.Fatal("tool filter altered QA channel", results)
			}
		})
	}
}

func TestOfficialCapturePolicyDoesNotInheritAmbientAgentEnvironment(t *testing.T) {
	t.Setenv("COGNEE_CAPTURE", "false")
	t.Setenv("COGNEE_CAPTURE_REDACT", "false")
	results, err := ApplyOfficialCapturePolicy(context.Background(), officialPolicyFixture(t), []json.RawMessage{policyQA("q", "API_KEY='synthetic-ambient-secret'")})
	if err != nil || results[0].State != "accepted" || strings.Contains(string(results[0].Entry), "synthetic-ambient-secret") {
		t.Fatal("ambient Agent environment changed host policy", results, err)
	}
}

func TestOfficialCapturePolicyRejectsTamperedSourceAndUnsafeProtocol(t *testing.T) {
	options := officialPolicyFixture(t)
	bad := filepath.Join(t.TempDir(), "tampered.py")
	if err := os.WriteFile(bad, []byte("raise Exception('synthetic-sensitive-diagnostic')"), 0600); err != nil {
		t.Fatal(err)
	}
	changed := options
	changed.PolicyPath = bad
	if _, err := ApplyOfficialCapturePolicy(context.Background(), changed, []json.RawMessage{policyQA("q", "a")}); err == nil || strings.Contains(err.Error(), "synthetic-sensitive") {
		t.Fatal("tampered source accepted or diagnostics leaked", err)
	}
	changed = options
	changed.Environment = map[string]string{"PYTHONPATH": "/synthetic/untrusted"}
	if _, err := ApplyOfficialCapturePolicy(context.Background(), changed, []json.RawMessage{policyQA("q", "a")}); err == nil {
		t.Fatal("ambient Python injection accepted")
	}
	for _, input := range []json.RawMessage{json.RawMessage(`{"type":"feedback"}`), json.RawMessage(`{"type":"qa","question":"q","answer":"a","context":"c","unexpected":"synthetic-sensitive-diagnostic"}`)} {
		if _, err := ApplyOfficialCapturePolicy(context.Background(), options, []json.RawMessage{input}); err == nil || strings.Contains(err.Error(), "synthetic-sensitive") {
			t.Fatal("unsupported payload accepted or leaked", err)
		}
	}
}

func TestOfficialCapturePolicyCancellationBoundsUntrustedCustomRegex(t *testing.T) {
	options := officialPolicyFixture(t)
	options.Environment = map[string]string{"COGNEE_CAPTURE_REDACT_PATTERNS": `["(a+)+$"]`}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := ApplyOfficialCapturePolicy(ctx, options, []json.RawMessage{policyQA(strings.Repeat("a", 36)+"!", "answer")})
	if err == nil || time.Since(start) > 4*time.Second {
		t.Fatal("policy cancellation failed", err, time.Since(start))
	}
}
