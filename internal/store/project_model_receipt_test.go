package store

import (
	"strings"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

func TestProjectModelReceiptRejectsIncompleteOrWrongAdapterProof(t *testing.T) {
	task := records.ProjectTask{Snapshot: records.ProjectTaskSnapshot{Executor: records.ProjectTaskExecutor{Agent: "pi", Model: "model"}}}
	good := func() records.ProjectTaskExecution {
		return records.ProjectTaskExecution{
			Terminal: true, Reaped: true, GroupGone: true, Model: "model", Provider: "provider", PermissionProfile: "pi_project_readonly",
			Pi:               &records.ProjectPiExecution{NativeSchemaRequested: true, NativeSchemaResponseStatus: 200, ReadPhaseEnded: true},
			KnowledgeContext: &records.ProjectKnowledgeContextReceipt{Pages: 1, SentPages: 1, Complete: true, Digest: strings.Repeat("a", 64)},
		}
	}
	if !projectModelComplete(task, good()) {
		t.Fatal("complete pi proof rejected")
	}
	cases := map[string]func(*records.ProjectTaskExecution){
		"not-terminal": func(e *records.ProjectTaskExecution) { e.Terminal = false },
		"not-reaped":   func(e *records.ProjectTaskExecution) { e.Reaped = false },
		"group-alive":  func(e *records.ProjectTaskExecution) { e.GroupGone = false },
		"wrong-model":  func(e *records.ProjectTaskExecution) { e.Model = "other" },
		"no-provider":  func(e *records.ProjectTaskExecution) { e.Provider = "" },
		"codex-profile": func(e *records.ProjectTaskExecution) {
			e.PermissionProfile = "memlane_project"
			e.Thread = "thread"
			e.Turn = "turn"
		},
		"no-pi-proof":     func(e *records.ProjectTaskExecution) { e.Pi = nil },
		"pi-failure":      func(e *records.ProjectTaskExecution) { e.Pi.FailureCode = "failed" },
		"no-schema":       func(e *records.ProjectTaskExecution) { e.Pi.NativeSchemaRequested = false },
		"schema-failed":   func(e *records.ProjectTaskExecution) { e.Pi.NativeSchemaResponseStatus = 500 },
		"read-open":       func(e *records.ProjectTaskExecution) { e.Pi.ReadPhaseEnded = false },
		"no-context":      func(e *records.ProjectTaskExecution) { e.KnowledgeContext = nil },
		"partial-context": func(e *records.ProjectTaskExecution) { e.KnowledgeContext.Complete = false },
		"unsent-context":  func(e *records.ProjectTaskExecution) { e.KnowledgeContext.SentPages = 0 },
		"invalid-digest":  func(e *records.ProjectTaskExecution) { e.KnowledgeContext.Digest = "invalid" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			e := good()
			change(&e)
			if projectModelComplete(task, e) {
				t.Fatal("incomplete receipt accepted")
			}
		})
	}
	task.Snapshot.Executor.Agent = "codex"
	if projectModelComplete(task, good()) {
		t.Fatal("pi proof accepted for Codex")
	}
}
