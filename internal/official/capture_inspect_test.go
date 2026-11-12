package official

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestOfficialCaptureInspectIsReadOnlyAndRequiresActualPayload(t *testing.T) {
	for _, mode := range []string{"qa", "trace", "unknown", "wrong_digest", "changed_body", "wrong_scope", "ambiguous", "cache_unavailable"} {
		t.Run(mode, func(t *testing.T) {
			kind := "qa"
			if mode == "trace" {
				kind = "trace"
			}
			input := captureFixture(kind)
			expected, entry, err := prepareCapture(input)
			if err != nil {
				t.Fatal(err)
			}
			instance, dataset, id, user := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("X-Api-Key") != "synthetic-key" {
					t.Error("lookup wrote or lost credential")
					w.WriteHeader(403)
					return
				}
				if r.URL.Path == "/api/v1/memlane/identity" {
					json.NewEncoder(w).Encode(map[string]string{"instance": instance, "cognee_version": "1.6.7"})
					return
				}
				reads++
				if !strings.HasSuffix(r.URL.Path, "/capture-receipts/"+input.Session+"/"+kind+"/"+input.Operation) || r.URL.Query().Get("expected_instance") != instance {
					t.Error("lookup scope missing", r.URL.Path)
				}
				if mode == "ambiguous" {
					w.WriteHeader(409)
					return
				}
				if mode == "cache_unavailable" {
					w.WriteHeader(503)
					return
				}
				response := map[string]any{"instance": instance, "dataset": dataset, "session": input.Session, "user": user, "operation": input.Operation, "kind": kind, "state": "saved", "entry_id": id, "entry": json.RawMessage(entry)}
				switch mode {
				case "unknown":
					response["state"] = "unknown"
					delete(response, "entry_id")
					delete(response, "entry")
				case "wrong_scope":
					response["dataset"] = uuid.NewString()
				case "changed_body", "wrong_digest":
					var altered map[string]any
					json.Unmarshal(entry, &altered)
					if mode == "changed_body" {
						altered["answer"] = "tampered despite matching operation marker"
					} else {
						var marker map[string]any
						json.Unmarshal([]byte(altered["context"].(string)), &marker)
						marker["digest"] = strings.Repeat("f", 64)
						encoded, _ := json.Marshal(marker)
						altered["context"] = string(encoded)
					}
					response["entry"] = altered
				}
				json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			receipt, err := (SessionReader{Endpoint: server.URL, Instance: instance, Dataset: dataset, Token: "synthetic-key"}).InspectCapture(context.Background(), input)
			if mode == "qa" || mode == "trace" {
				if err != nil || receipt == nil || receipt.ID != id || receipt.Digest != expected.Digest {
					t.Fatal(receipt, err)
				}
			} else if mode == "unknown" {
				if err != nil || receipt != nil {
					t.Fatal("unknown outcome became confirmed", receipt, err)
				}
			} else if err == nil || receipt != nil {
				t.Fatal("unverified lookup accepted", receipt, err)
			}
			if reads != 1 {
				t.Fatal("lookup unexpectedly replayed", reads)
			}
		})
	}
}
