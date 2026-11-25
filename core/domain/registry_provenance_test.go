package domain

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestRegistryAgentGrantsAndCaptureScope(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	must(t, s.UpsertProject(ctx, Project{ID: "existing", Name: "existing", AutoCapture: true}))
	must(t, s.UpsertCommonConfig(ctx, CommonConfig{ID: "common", Name: "common"}))
	must(t, s.ConnectAgent(ctx, Agent{ID: "agent", Name: "agent", DefaultAccess: AccessMaintain}))
	must(t, s.UpsertProject(ctx, Project{ID: "new", Name: "new", AutoCapture: true}))
	for _, id := range []string{"existing", "new"} {
		acl, err := s.EvaluateProjectACL(ctx, id, "agent")
		must(t, err)
		if acl.Access != AccessNone || acl.CanCapture || acl.CanAutoProcess || len(acl.Permissions) != 0 {
			t.Fatalf("agent received a default project grant for %s: %+v", id, acl)
		}
	}
	common, err := s.EvaluateCommonACL(ctx, "agent")
	must(t, err)
	if common.Access != AccessNone || common.CanCapture || common.CanAutoProcess || len(common.Permissions) != 0 {
		t.Fatalf("agent received a default common grant: %+v", common)
	}
	must(t, s.UpsertAgent(ctx, Agent{ID: "known", Name: "known", DefaultAccess: AccessMaintain}))
	must(t, s.UpsertProject(ctx, Project{ID: "protected", Name: "profile", DatasetName: "ml-user-profiles", AutoCapture: true}))
	protected, err := s.EvaluateProjectACL(ctx, "protected", "known")
	must(t, err)
	if protected.Access != AccessNone || protected.Capture || protected.AutoProcess || protected.CaptureScope != "default" {
		t.Fatalf("protected project default: %+v", protected)
	}

	must(t, s.UpsertRegistryDefaults(ctx, RegistryDefaults{Access: AccessMaintain, Capture: false, AutoProcess: true}))
	falseValue, trueValue := false, true
	for _, tc := range []struct {
		access      AccessLevel
		capture     *bool
		scope       string
		canCapture  bool
		permissions []string
	}{
		{AccessNone, nil, "default", false, []string{}},
		{AccessRead, &trueValue, "ok", false, []string{"read"}},
		{AccessMaintain, &falseValue, "agent", false, []string{"delete", "read", "write"}},
		{AccessMaintain, &trueValue, "ok", true, []string{"delete", "read", "write"}},
	} {
		must(t, s.SetACLOverride(ctx, ACLOverride{TargetKind: "project", TargetID: "existing", AgentID: "agent", Access: &tc.access, Capture: tc.capture}))
		acl, err := s.EvaluateProjectACL(ctx, "existing", "agent")
		must(t, err)
		if acl.Access != tc.access || acl.CaptureScope != tc.scope || acl.CanCapture != tc.canCapture ||
			!reflect.DeepEqual(acl.Permissions, tc.permissions) {
			t.Errorf("%s: %+v", tc.access, acl)
		}
	}
	updated, err := s.SetProjectAutoCapture(ctx, "existing", false)
	must(t, err)
	acl, err := s.EvaluateProjectACL(ctx, "existing", "agent")
	must(t, err)
	if updated.AutoCapture || updated.AutoCaptureVersion != 2 || updated.AutoCaptureUpdatedAt == "" ||
		acl.CaptureScope != "project" || acl.CanCapture || acl.AutoCaptureVersion != 2 {
		t.Fatalf("project capture disabled: project=%+v acl=%+v", updated, acl)
	}
	enabled, err := s.SetProjectAutoCapture(ctx, "existing", true)
	must(t, err)
	if !enabled.AutoCapture || enabled.AutoCaptureVersion != 3 {
		t.Fatalf("project capture re-enabled: %+v", enabled)
	}
}

func TestRegistryProjectPathAndSessionLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	must(t, s.UpsertProject(ctx, Project{ID: "outer", Name: "outer", Aliases: []string{"/repo"}}))
	must(t, s.UpsertProject(ctx, Project{ID: "inner", Name: "inner", Aliases: []string{"/repo/modules/sub"}}))
	for path, want := range map[string]string{"/repo/modules/sub//src/../main.go": "inner", "/repo/modules/submarine/file": "outer"} {
		p, err := s.MatchProjectByPath(ctx, path)
		must(t, err)
		if p.ID != want {
			t.Fatalf("%s matched %s, want %s", path, p.ID, want)
		}
	}
	_, err := s.MatchProjectByPath(ctx, "/repo-other/file")
	wantError(t, err, ErrNotFound)
	must(t, s.UpsertSessionBinding(ctx, "session", "outer", 12))
	binding, err := s.GetSessionBinding(ctx, "session")
	must(t, err)
	if binding.ProjectID != "outer" || binding.UpdatedAt != 12 {
		t.Fatalf("new binding: %+v", binding)
	}
	must(t, s.UpsertSessionBinding(ctx, "session", "inner", 42))
	binding, err = s.GetSessionBinding(ctx, "session")
	must(t, err)
	outer, err := s.ListProjectSessions(ctx, "outer")
	must(t, err)
	inner, err := s.ListProjectSessions(ctx, "inner")
	must(t, err)
	if binding.ProjectID != "inner" || binding.UpdatedAt != 42 || len(outer) != 0 || len(inner) != 1 || inner[0] != binding {
		t.Fatalf("updated binding=%+v outer=%+v inner=%+v", binding, outer, inner)
	}
	must(t, s.DeleteProject(ctx, "inner"))
	_, err = s.GetSessionBinding(ctx, "session")
	wantError(t, err, ErrNotFound)
}

func TestProvenanceStatusAndEarliestEvidence(t *testing.T) {
	for status, want := range map[string]int{
		"unknown": 0, "unconfirmed": 0, "created_at": 1, "created_at_fallback": 1,
		"legacy_json_locked": 2, "session_date": 3, "linked_qa": 4,
	} {
		if rank := ProvenanceStatusRank(status); rank != want {
			t.Errorf("rank of %s = %d, want %d", status, rank, want)
		}
	}
	pairs := []DateEvidence{{"2026-04-01", "linked_qa"}, {"2026-03-01", "created_at"}, {"invalid", "linked_qa"}}
	date, status := SelectBestDateEvidence(pairs)
	if date != "2026-03-01" || status != "created_at" {
		t.Fatalf("later evidence affected earlier lock: %s %s", date, status)
	}
	date, status = SelectBestDateEvidence(append(pairs, DateEvidence{"2026-03-01T01:00:00Z", "session_date"}))
	if date != "2026-03-01" || status != "session_date" {
		t.Fatalf("same-day rank tie-break: %s %s", date, status)
	}
}

func TestDocumentProvenanceReplacementAndReadOnlyResolution(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	base := DocumentProvenance{DatasetID: "d", DocumentID: "old", LessonType: "lesson", StateKey: "key",
		FirstSourceDate: "2026-03-01", SourceDateStatus: "created_at", SourceQAIDs: []string{"old-qa"}}
	old, err := s.LockDocumentProvenance(ctx, base)
	must(t, err)
	base.DocumentID, base.FirstSourceDate, base.SourceDateStatus, base.SourceQAIDs = "new", "2026-04-01", "linked_qa", []string{"new-qa"}
	newDoc, err := s.LockDocumentProvenance(ctx, base)
	must(t, err)
	locked, err := s.GetKeyedProvenance(ctx, "d", "lesson", "key")
	must(t, err)
	if newDoc.SyncSeq <= old.SyncSeq || newDoc.FirstSourceDate != old.FirstSourceDate || newDoc.SourceDateStatus != old.SourceDateStatus ||
		locked.FirstSourceDate != old.FirstSourceDate || locked.SourceDateStatus != old.SourceDateStatus {
		t.Fatalf("replacement changed earliest evidence: old=%+v new=%+v keyed=%+v", old, newDoc, locked)
	}
	db := openReadOnlyTestDB(t, s.Path())
	readOnly := Store{db: db}
	input := ResolveLessonDateInput{DatasetID: "d", DocumentID: "new", LessonType: "lesson", StateKey: "key",
		SystemMetadata: map[string]any{"session_date": "2026-02-01"}, ExternalMetadata: map[string]any{"session_date": "2026-01-01"},
		CreatedAt: "2026-06-02T01:00:00Z", Location: time.FixedZone("west", -7*60*60)}
	assertDate := func(date, status string) {
		t.Helper()
		got, err := readOnly.ResolveLessonDate(ctx, input)
		must(t, err)
		if got.Date != date || got.Status != status {
			t.Fatalf("resolved %+v, want %s %s", got, date, status)
		}
	}
	assertDate("2026-03-01", "created_at")
	input.DocumentID = "unseen"
	assertDate("2026-03-01", "created_at")
	input.StateKey = "unknown"
	assertDate("2026-02-01", "session_date")
	input.SystemMetadata = nil
	assertDate("2026-01-01", "session_date")
	input.ExternalMetadata = nil
	assertDate("2026-06-01", "created_at")
	_, err = s.GetDocumentProvenance(ctx, "d", "unseen")
	wantError(t, err, ErrNotFound)
	doc, err := s.GetDocumentProvenance(ctx, "d", "new")
	must(t, err)
	if !reflect.DeepEqual(doc, newDoc) {
		t.Fatalf("resolution changed document provenance: before=%+v after=%+v", newDoc, doc)
	}
}
