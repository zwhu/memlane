package official

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

func TestImportedSourceRequiresPreservedIdentityBodyAndCurrentParent(t *testing.T) {
	for _, mode := range []string{"read", "unlinked", "other_library", "other_memory", "other_revision", "not_raw", "changed_body", "changed_parent", "duplicate_link", "other_dataset"} {
		t.Run(mode, func(t *testing.T) {
			parent := documentTestRow("e6a5ca85-2360-4d11-a4aa-78161f632096")
			target := documentTestRow("b6a5ca85-2360-4d11-a4aa-78161f632096")
			parentBody, targetBody := "Preserved knowledge", "Preserved original\n原文\n"
			hash := func(s string) string { d := sha256.Sum256([]byte(s)); return hex.EncodeToString(d[:]) }
			ref := records.OfficialDocumentReference{Document: target.ID, Memory: target.ID, Project: documentTestDataset, Revision: 1, Digest: hash(targetBody), Quote: "原文"}
			refs := []records.OfficialDocumentReference{ref}
			if mode == "duplicate_link" {
				refs = append(refs, ref)
			}
			metadata := func(memory, origin, body string, sources []records.OfficialDocumentReference) json.RawMessage {
				v, _ := json.Marshal(map[string]any{"memlane_import_v1": map[string]any{"import_id": memory, "library": documentTestInstance, "project": documentTestDataset, "memory": memory, "revision": 1, "origin": origin, "digest": hash(body), "sources": sources}})
				return v
			}
			parent.Metadata = metadata(parent.ID, "knowledge", parentBody, refs)
			target.Metadata = metadata(target.ID, "raw", targetBody, nil)
			var envelope map[string]map[string]any
			json.Unmarshal(target.Metadata, &envelope)
			i := envelope["memlane_import_v1"]
			switch mode {
			case "other_library":
				i["library"] = parent.ID
			case "other_memory":
				i["memory"] = parent.ID
			case "other_revision":
				i["revision"] = 2
			case "not_raw":
				i["origin"] = "knowledge"
			case "other_dataset":
				target.Dataset = documentTestInstance
			}
			target.Metadata, _ = json.Marshal(envelope)
			targetReads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("X-Api-Key") != "synthetic" {
					t.Error("unexpected write/principal")
					w.WriteHeader(403)
					return
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/identity"):
					json.NewEncoder(w).Encode(map[string]string{"instance": documentTestInstance, "cognee_version": "1.6.7"})
				case strings.HasSuffix(r.URL.Path, "/data"):
					p := parent
					if mode == "changed_parent" && targetReads > 0 {
						p.Name = "Changed"
					}
					json.NewEncoder(w).Encode([]records.OfficialDocument{p, target})
				case strings.HasSuffix(r.URL.Path, "/"+parent.ID+"/raw"):
					fmt.Fprint(w, parentBody)
				case strings.HasSuffix(r.URL.Path, "/"+target.ID+"/raw"):
					targetReads++
					if mode == "changed_body" {
						fmt.Fprint(w, "altered")
						return
					}
					fmt.Fprint(w, targetBody)
				default:
					t.Error("unexpected route")
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			r := DocumentReader{SessionReader{Endpoint: server.URL, Instance: documentTestInstance, Dataset: documentTestDataset, Token: "synthetic"}}
			id := target.ID
			if mode == "unlinked" {
				id = parent.ID
			}
			out, err := r.ReadImportedSource(context.Background(), parent, id, 0)
			if mode == "read" {
				if err != nil || out.Content.Text != targetBody || out.Reference != ref || out.Content.SessionReference != "" {
					t.Fatal("source read mismatch", err)
				}
			} else if err == nil || out.Content.Text != "" || out.Document.ID != "" {
				t.Fatal("unverified source returned", err)
			}
			if (mode == "unlinked" || mode == "duplicate_link") && targetReads != 0 {
				t.Fatal("unlinked target was downloaded")
			}
		})
	}
}
