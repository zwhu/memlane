package official

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOfficialSessionsFilterDatasetWithoutChangingUpstreamPagination(t *testing.T) {
	const instance = "55dbbc14-3185-47e7-a870-547463c2ab5f"
	const dataset = "39dac5b0-dca9-4efb-915d-ef572c81904c"
	const owner = "e6a5ca85-2360-4d11-a4aa-78161f632096"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" || req.Header.Get("X-Api-Key") != "synthetic" {
			t.Error("non-read request or missing principal")
			w.WriteHeader(403)
			return
		}
		switch req.URL.Path {
		case "/api/v1/memlane/identity":
			json.NewEncoder(w).Encode(map[string]string{"instance": instance, "cognee_version": "1.6.7"})
		case "/api/v1/sessions":
			if req.URL.Query().Get("offset") != "2" || req.URL.Query().Get("limit") != "2" || req.URL.Query().Get("range") != "all" {
				t.Error("lost upstream pagination")
			}
			json.NewEncoder(w).Encode(SessionPage{Total: 9, Limit: 2, Offset: 2, HasMore: true, Sessions: []SessionSummary{
				{Session: "same-id", User: owner, Dataset: dataset},
				{Session: "same-id", User: owner, Dataset: "another-dataset"},
			}})
		default:
			t.Error("unexpected request", req.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	reader := SessionReader{Endpoint: server.URL, Instance: instance, Dataset: dataset, Token: "synthetic"}
	page, err := reader.ListPage(context.Background(), 2, 2)
	if err != nil || len(page.Sessions) != 1 || page.Total != 9 || page.Offset != 2 || !page.HasMore {
		t.Fatal("wrong project scope or relabeled upstream total", page, err)
	}
}

func TestOfficialSessionReadRejectsChangedOwnerDatasetAndService(t *testing.T) {
	const instance = "55dbbc14-3185-47e7-a870-547463c2ab5f"
	const dataset = "39dac5b0-dca9-4efb-915d-ef572c81904c"
	selected := SessionSummary{Session: "codex_synthetic", User: "e6a5ca85-2360-4d11-a4aa-78161f632096", Dataset: dataset}
	for _, mutation := range []string{"none", "owner", "dataset", "session", "service_after_read", "oversized_tail", "missing_count", "revoked"} {
		t.Run(mutation, func(t *testing.T) {
			identityReads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/api/v1/memlane/identity" {
					identityReads++
					actual := instance
					if mutation == "service_after_read" && identityReads > 1 {
						actual = dataset
					}
					json.NewEncoder(w).Encode(map[string]string{"instance": actual, "cognee_version": "1.6.7"})
					return
				}
				if req.URL.Path != "/api/v1/sessions/"+selected.Session {
					t.Error("wrong detail request", req.URL.Path)
				}
				if mutation == "revoked" {
					w.WriteHeader(403)
					return
				}
				detail := SessionDetail{SessionSummary: selected, QACount: 25, ToolCount: 1,
					QAs:    []SessionQA{{ID: "qa", Question: "合成问题", Answer: "**合成Markdown**"}},
					Traces: []SessionTrace{{ID: "trace", Function: "read", Status: "error", Error: "synthetic failure"}}}
				switch mutation {
				case "owner":
					detail.User = dataset
				case "dataset":
					detail.Dataset = instance
				case "session":
					detail.Session = "different"
				case "oversized_tail":
					detail.QAs = make([]SessionQA, 21)
				case "missing_count":
					detail.QACount = 0
				}
				json.NewEncoder(w).Encode(detail)
			}))
			defer server.Close()
			reader := SessionReader{Endpoint: server.URL, Instance: instance, Dataset: dataset, Token: "synthetic"}
			detail, err := reader.Read(context.Background(), selected)
			if mutation != "none" {
				if err == nil || len(detail.QAs) > 0 || len(detail.Traces) > 0 {
					t.Fatal("changed scope exposed official content", mutation)
				}
				return
			}
			if err != nil || len(detail.QAs) != 1 || detail.QACount != 25 || detail.QAs[0].Time != "" || detail.Traces[0].Status != "error" {
				t.Fatal("tail count, unknown time or tool failure was lost", detail, err)
			}
		})
	}
}
