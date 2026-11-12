package agentbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestPiCaptureCLIConstructsOnlyHostAuthorizedRequests(t *testing.T) {
	for _, mode := range []string{"authorize", "submit", "inspect"} {
		t.Run(mode, func(t *testing.T) {
			root, workspace := t.TempDir(), t.TempDir()
			connection, project := uuid.NewString(), uuid.NewString()
			var output bytes.Buffer
			calls := 0
			payload := "{}"
			if mode != "authorize" {
				payload = `{"batch":"` + strings.Repeat("a", 64) + `","grant":"` + strings.Repeat("b", 64) + `","host_session":"synthetic-session","entries":[]}`
			}
			args := []string{mode, "--root", root, "--connection", connection, "--generation", "2", "--workspace", workspace, "--project", project}
			call := func(ctx context.Context, actualRoot string, q serviceipc.Request, out any) error {
				calls++
				if actualRoot != root || q.Op != "pi_capture_"+mode || q.UserConfirmed {
					t.Error("CLI crossed trusted App consent boundary")
				}
				request := q.AgentRequest
				if mode != "authorize" {
					if q.PiCaptureBatch == nil {
						t.Fatal("batch missing")
					}
					request = q.PiCaptureBatch.Request
				}
				if request.ConnectionID != connection || request.Generation != 2 || request.Workspace != workspace || request.Arguments.Scope != project || request.Tool != "memory_save" || request.Channel != "daily" || request.ToolMode != "" {
					t.Error("host authority changed", request)
				}
				*out.(*json.RawMessage) = json.RawMessage(`{"synthetic_receipt":true}`)
				return nil
			}
			if code := runPiCaptureCLI(context.Background(), args, strings.NewReader(payload), &output, "", "", call); code != 0 || calls != 1 {
				t.Fatal(code, calls, output.String())
			}
			var result Result
			if json.Unmarshal(output.Bytes(), &result) != nil || !result.OK || result.Protocol != 1 {
				t.Fatal("invalid host protocol", output.String())
			}
		})
	}
}

func TestPiCaptureCLIRejectsBodyAuthorityAndIsolatedChannelsBeforeIPC(t *testing.T) {
	connection := uuid.NewString()
	base := []string{"authorize", "--root", t.TempDir(), "--connection", connection, "--generation", "1", "--workspace", t.TempDir()}
	for _, test := range []struct{ body, mode, channel string }{
		{`{"request":{"connection_id":"forged"}}`, "", ""}, {`{"endpoint":"http://foreign"}`, "", ""}, {`null`, "", ""}, {`{} {}`, "", ""}, {`{bad}`, "", ""}, {`{}`, "connection", "verification"}, {`{}`, "", "processing"}, {`{"credential":"synthetic-secret"}`, "", ""},
	} {
		var output bytes.Buffer
		calls := 0
		code := runPiCaptureCLI(context.Background(), base, strings.NewReader(test.body), &output, test.mode, test.channel, func(context.Context, string, serviceipc.Request, any) error { calls++; return nil })
		if code == 0 || calls != 0 || strings.Contains(output.String(), "synthetic-secret") || strings.Contains(output.String(), "forged") {
			t.Fatal("invalid capture request reached IPC or leaked input", code, calls, output.String())
		}
	}
}

func TestPiCaptureCLIErrorDoesNotEchoTransportOrSource(t *testing.T) {
	for _, failure := range []error{errors.New("synthetic-sensitive-transport"), &serviceipc.ServiceError{Code: "scope_denied", Message: "synthetic-sensitive-diagnostic"}, &serviceipc.ServiceError{Code: "synthetic/secret/path", Message: "private"}} {
		var output bytes.Buffer
		args := []string{"authorize", "--root", t.TempDir(), "--connection", uuid.NewString(), "--generation", "1", "--workspace", t.TempDir()}
		if code := runPiCaptureCLI(context.Background(), args, strings.NewReader(`{}`), &output, "", "daily", func(context.Context, string, serviceipc.Request, any) error { return failure }); code == 0 {
			t.Fatal("failed call succeeded")
		}
		if strings.Contains(output.String(), "sensitive") || strings.Contains(output.String(), "secret/path") || strings.Contains(output.String(), "private") {
			t.Fatal("raw failure leaked", output.String())
		}
	}
}
