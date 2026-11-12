package official

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadSavedCaptureReconstructsDigestAndNeverReadsHistory(t *testing.T) {
	for _, mode := range []string{"qa", "trace", "changed_body", "changed_id", "wrong_digest", "unknown", "foreign_user_scope"} {
		t.Run(mode, func(t *testing.T) {
			kind := "qa"
			if mode == "trace" {
				kind = "trace"
			}
			input := captureFixture(kind)
			identity, entry, err := prepareCapture(input)
			if err != nil {
				t.Fatal(err)
			}
			instance, dataset, user, id := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
			saved := records.OfficialCaptureDelivery{OfficialCaptureIdentity: records.OfficialCaptureIdentity{Operation: input.Operation, Session: input.Session, Kind: kind, PayloadDigest: identity.Digest, Instance: instance, Dataset: dataset}, State: "saved", OfficialID: id}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("read wrote data")
				}
				if r.URL.Path == "/api/v1/memlane/identity" {
					json.NewEncoder(w).Encode(map[string]string{"instance": instance, "cognee_version": "1.6.7"})
					return
				}
				if !strings.Contains(r.URL.Path, "/capture-receipts/"+input.Session+"/"+kind+"/"+input.Operation) {
					t.Error("history or unrelated content queried", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				response := map[string]any{"instance": instance, "dataset": dataset, "session": input.Session, "user": user, "operation": input.Operation, "kind": kind, "state": "saved", "entry_id": id, "entry": json.RawMessage(entry)}
				switch mode {
				case "changed_body":
					var fields map[string]any
					json.Unmarshal(entry, &fields)
					fields["answer"] = "changed actual answer"
					response["entry"] = fields
				case "changed_id":
					response["entry_id"] = uuid.NewString()
				case "unknown":
					response["state"] = "unknown"
					delete(response, "entry_id")
					delete(response, "entry")
				case "foreign_user_scope":
					response["dataset"] = uuid.NewString()
				}
				json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			// Modify the trusted expectation before the request, never from its handler.
			if mode == "wrong_digest" {
				saved.PayloadDigest = strings.Repeat("f", 64)
			}
			gotUser, got, err := (SessionReader{Endpoint: server.URL, Instance: instance, Dataset: dataset, Token: "synthetic"}).ReadSavedCapture(context.Background(), saved)
			if mode == "qa" || mode == "trace" {
				if err != nil || gotUser != user || !captureJSONEqual(got, entry) {
					t.Fatal(gotUser, string(got), err)
				}
			} else if err == nil {
				t.Fatal("unverified capture accepted", mode)
			}
		})
	}
}
