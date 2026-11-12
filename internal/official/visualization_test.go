package official

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestVisualizationOfficialPayloadPreservedAndScopeChangesRejected(t *testing.T) {
	for _, mode := range []string{"read", "missing_capability", "changed_instance", "wrong_dataset", "dangling_link"} {
		t.Run(mode, func(t *testing.T) {
			instance, dataset := uuid.NewString(), uuid.NewString()
			const payload = `{"nodes":[{"id":"doc","stage":"document","type":"TextDocument"},{"id":"chunk","stage":"chunk"},{"id":"entity","stage":"entity"}],"links":[{"source":"doc","target":"chunk","relation":"has_chunk"},{"source":"chunk","target":"entity","relation":"mentions"}],"color_maps":{"type":{"TextDocument":"#A550FF"}},"provenance_index":{"entity":{"original":["chunk"]}},"future_official_field":{"kept":true}}`
			identities, graphs := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != "GET" || req.Header.Get("X-Api-Key") != "synthetic-token" {
					t.Error("unexpected write or credential")
					w.WriteHeader(403)
					return
				}
				if req.URL.Path == "/api/v1/memlane/identity" {
					identities++
					id := instance
					if mode == "changed_instance" && identities == 2 {
						id = uuid.NewString()
					}
					caps := []string{"official_visualization_v1"}
					if mode == "missing_capability" {
						caps = nil
					}
					json.NewEncoder(w).Encode(map[string]any{"instance": id, "cognee_version": "1.6.7", "source_commit": CodingMemoryServiceCommit, "capabilities": caps})
					return
				}
				graphs++
				q := req.URL.Query()
				if req.URL.Path != "/api/v1/memlane/datasets/"+dataset+"/visualization" || q.Get("expected_instance") != instance || q.Get("max_nodes") != "1000" || q.Get("seed_node_ids") != "doc" || len(q) != 3 {
					t.Error("unexpected dataset or graph options")
				}
				d := dataset
				if mode == "wrong_dataset" {
					d = uuid.NewString()
				}
				p := payload
				if mode == "dangling_link" {
					p = strings.Replace(p, `"target":"entity"`, `"target":"absent"`, 1)
				}
				fmt.Fprintf(w, `{"instance":%q,"dataset":%q,"scope":"bounded_subgraph","max_nodes":1000,"payload":%s}`, instance, d, p)
			}))
			defer server.Close()
			r := VisualizationReader{SessionReader{Endpoint: server.URL, Instance: instance, Dataset: dataset, Token: "synthetic-token"}}
			out, err := r.Read(context.Background(), 1000, []string{"doc"})
			if mode == "read" {
				if err != nil || string(out.Payload) != payload || out.Scope != "bounded_subgraph" {
					t.Fatalf("payload not preserved: %v", err)
				}
			} else if err == nil || out.Payload != nil {
				t.Fatal("invalid graph published")
			}
			if mode == "missing_capability" && graphs != 0 {
				t.Fatal("unsupported service read graph")
			}
			for _, limit := range []int{0, 1001} {
				if _, err := r.Read(context.Background(), limit, nil); err == nil {
					t.Fatal("unbounded request accepted")
				}
			}
		})
	}
}

func TestVisualizationRejectsMissingCollectionsAndDuplicateIdentities(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"nodes":[],"links":[],"color_maps":null}`, `{"nodes":[{"id":"same"},{"id":"same"}],"links":[],"color_maps":{}}`} {
		if validateVisualization(json.RawMessage(raw), 1000) == nil {
			t.Fatal("invalid graph accepted")
		}
	}
	if err := validateVisualization(json.RawMessage(`{"nodes":[],"links":[],"color_maps":{}}`), 1000); err != nil {
		t.Fatal(err)
	}
}
