package domain

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCaptureFilterSchemaV2Migration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v2.sqlite3")
	db, err := OpenPrivateSQLite(path)
	must(t, err)
	v2Registry := strings.ReplaceAll(registryDDL, " auto_capture_reason TEXT NOT NULL DEFAULT '', auto_capture_actor TEXT NOT NULL DEFAULT '',\n", "")
	v2Capture := strings.ReplaceAll(captureDDL, " last_error_at TEXT NOT NULL DEFAULT '',", "")
	must(t, execSQL(ctx, db, schemaMetaDDL+v2Registry+v2Capture+`
		INSERT INTO schema_meta VALUES(1,2,'created','updated');
		INSERT INTO projects(id,name,auto_capture) VALUES('old','Old Project',0);
		INSERT INTO capture_inbox(id,project_id,dataset_id,agent_id,session_id,message_id,kind,payload_json,status,last_error,created_at,updated_at)
		VALUES('old-item','old','ds','agent','session','msg','capture_qa','{}','failed','old error','created','updated');
	`))
	must(t, db.Close())
	imported, err := ImportLegacyDailyStore(filepath.Dir(path), path)
	must(t, err)
	if !imported.AlreadyMigrated {
		t.Fatal("existing v2 database was not recognized by the legacy importer")
	}
	for range 2 {
		store, err := OpenStore(path)
		must(t, err)
		version, err := store.SchemaVersion()
		must(t, err)
		p, err := store.GetProject(ctx, "old")
		must(t, err)
		items, err := store.ListInboxItems(ctx, "old", "failed", 1)
		must(t, err)
		if version != SchemaVersion || p.Name != "Old Project" || p.AutoCapture || p.AutoCaptureUpdatedAt != "" || p.AutoCaptureReason != "" || p.AutoCaptureActor != "" ||
			len(items) != 1 || items[0].LastError != "old error" || items[0].LastErrorAt != "" || items[0].UpdatedAt != "updated" {
			t.Fatalf("migration changed historical values: v%d %+v %+v", version, p, items)
		}
		_, err = store.RecordCaptureFilterEvent(ctx, CaptureFilterEvent{ProjectID: "old", Rule: "*.key"})
		must(t, err)
		must(t, store.Close())
	}
}

func TestCaptureEventTimestampsAndRetention(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	must(t, s.UpsertProject(ctx, Project{ID: "p", Name: "Project", AutoCapture: true}))
	must(t, s.UpsertProject(ctx, Project{ID: "other", Name: "Other"}))
	p, err := s.SetProjectAutoCaptureWithMeta(ctx, "p", false, "system", "maintenance")
	must(t, err)
	_, err = time.Parse(time.RFC3339Nano, p.AutoCaptureUpdatedAt)
	must(t, err)
	unchanged, err := s.SetProjectAutoCapture(ctx, "p", false)
	must(t, err)
	if p.AutoCapture || p.AutoCaptureActor != "system" || p.AutoCaptureReason != "maintenance" ||
		unchanged.AutoCaptureUpdatedAt != p.AutoCaptureUpdatedAt || unchanged.AutoCaptureVersion != p.AutoCaptureVersion || unchanged.AutoCaptureActor != "system" {
		t.Fatalf("pause metadata changed: %+v / %+v", p, unchanged)
	}
	resumed, err := s.SetProjectAutoCapture(ctx, "p", true)
	must(t, err)
	if !resumed.AutoCapture || resumed.AutoCaptureVersion != p.AutoCaptureVersion+1 || resumed.AutoCaptureActor != "user" || resumed.AutoCaptureReason != "user_enabled" {
		t.Fatal(resumed)
	}
	item, err := s.EnqueueInboxItem(ctx, CaptureInboxItem{ProjectID: "p", DatasetID: "ds", AgentID: "agent", SessionID: "session", MessageID: "msg", Kind: "capture_qa", PayloadJSON: "{}"})
	must(t, err)
	_, err = s.ClaimNextInboxItem(ctx)
	must(t, err)
	must(t, s.MarkInboxFailed(ctx, item.ID, "connection refused"))
	items, err := s.ListInboxItems(ctx, "p", "failed", 1)
	must(t, err)
	if len(items) != 1 || items[0].LastErrorAt == "" || items[0].LastErrorAt != items[0].UpdatedAt {
		t.Fatalf("failure timestamp missing: %+v", items)
	}
	retry, err := s.RetryInboxItem(ctx, item.ID)
	must(t, err)
	if retry.LastErrorAt != "" || retry.LastError != "" || retry.Status != "pending" {
		t.Fatal(retry)
	}
	for i := range 201 {
		event := CaptureFilterEvent{ProjectID: "p", Rule: "*.key", OccurredAt: fmt.Sprintf("2026-11-26T00:00:00.%06d+00:00", i)}
		saved, err := s.RecordCaptureFilterEvent(ctx, event)
		must(t, err)
		if saved.ID == "" || saved.OccurredAt != event.OccurredAt {
			t.Fatal(saved)
		}
	}
	other, err := s.RecordCaptureFilterEvent(ctx, CaptureFilterEvent{ProjectID: "other", Rule: "*.pem"})
	must(t, err)
	_, err = time.Parse(time.RFC3339Nano, other.OccurredAt)
	must(t, err)
	count, err := s.CountCaptureFilterEvents(ctx, "p")
	must(t, err)
	recent, err := s.ListCaptureFilterEvents(ctx, "p", 2)
	must(t, err)
	total, err := s.CountCaptureFilterEvents(ctx, "")
	must(t, err)
	if count != 200 || total != 201 || len(recent) != 2 || recent[0].OccurredAt <= recent[1].OccurredAt || !strings.Contains(recent[0].OccurredAt, ".000200") {
		t.Fatalf("unexpected filter event retention: %d / %d %+v", count, total, recent)
	}
}
