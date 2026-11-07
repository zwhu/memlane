package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func usageFixture(t *testing.T) (*Store, records.Connection, Version) {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "memory.sqlite"))
	mustMerge(t, e)
	t.Cleanup(func() { s.Close() })
	a := uuid.NewString()
	_, e = s.DB.Exec(`INSERT INTO scopes(id,kind,name) VALUES(?,'project','usage synthetic')`, a)
	mustMerge(t, e)
	c, e := s.ConfigureConnection(context.Background(), records.ConnectionDraft{Agent: "pi", Name: "usage synthetic", Project: a, AllowedScopes: []string{a}})
	mustMerge(t, e)
	v, e := s.Apply(context.Background(), Mutation{OperationID: uuid.NewString(), Kind: "save", Memory: uuid.NewString(), Dataset: a, Text: "usage synthetic source"})
	mustMerge(t, e)
	return s, c, v
}
func usageObservation(c records.Connection, v Version, kind string) records.UsageObservation {
	return records.UsageObservation{OperationID: uuid.NewString(), Digest: UsageDigest("actual request"), Kind: kind, Channel: "daily", WorkspaceID: UsageDigest("fixture-workspace"), ConnectionID: c.ID, Generation: c.Generation, Targets: []records.UsageTarget{{Memory: v.Memory, Dataset: v.Dataset, Revision: v.Revision}}}
}
func TestUsageReadReturnAdoptReplayAndIdentityFences(t *testing.T) {
	ctx := context.Background()
	s, c, v := usageFixture(t)
	o := usageObservation(c, v, "read")
	r, e := s.RecordUsage(ctx, o)
	mustMerge(t, e)
	replay, e := s.RecordUsage(ctx, o)
	mustMerge(t, e)
	if !reflect.DeepEqual(r, replay) {
		t.Fatal("receipt changed")
	}
	changed := o
	changed.Digest = UsageDigest("changed request")
	if _, e = s.RecordUsage(ctx, changed); e == nil {
		t.Fatal("ID changed content accepted")
	}
	returned := usageObservation(c, v, "return")
	returned.Targets = append(returned.Targets, returned.Targets[0])
	_, e = s.RecordUsage(ctx, returned)
	mustMerge(t, e)
	adopt := usageObservation(c, v, "adopt")
	adopt.Exposure = r.ID
	_, e = s.RecordUsage(ctx, adopt)
	mustMerge(t, e)
	_, e = s.RecordUsage(ctx, adopt)
	mustMerge(t, e)
	u, e := s.Usage(ctx, v.Memory)
	mustMerge(t, e)
	if u.Daily.Reads != 1 || u.Daily.Returns != 1 || u.Daily.Adoptions != 1 || u.StartedMS <= 0 {
		t.Fatal(u)
	}
	other, e := s.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "other", Project: v.Dataset, AllowedScopes: []string{v.Dataset}})
	mustMerge(t, e)
	stolen := usageObservation(other, v, "adopt")
	stolen.Exposure = r.ID
	if _, e = s.RecordUsage(ctx, stolen); e == nil {
		t.Fatal("other connection adopted exposure")
	}
	wrongWorkspace := usageObservation(c, v, "adopt")
	wrongWorkspace.WorkspaceID = UsageDigest("other-workspace")
	wrongWorkspace.Exposure = r.ID
	if _, e = s.RecordUsage(ctx, wrongWorkspace); e == nil {
		t.Fatal("cross-workspace adoption")
	}
	bad := usageObservation(c, v, "adopt")
	bad.Exposure = "missing"
	if _, e = s.RecordUsage(ctx, bad); e == nil {
		t.Fatal("invented exposure")
	}
	mustMerge(t, s.Disconnect(ctx, c.ID))
	if _, e = s.RecordUsage(ctx, o); e == nil {
		t.Fatal("revoked replay")
	}
}
func TestUsageChannelSeparationStaleVersionAndMove(t *testing.T) {
	ctx := context.Background()
	s, c, v := usageFixture(t)
	for _, ch := range []string{"daily", "verification", "app", "background"} {
		o := usageObservation(c, v, "read")
		o.Channel = ch
		if ch == "app" {
			o.Kind = "browse"
			o.ConnectionID = ""
			o.Generation = 0
		}
		if ch == "background" {
			o.ConnectionID = ""
			o.Generation = 0
		}
		_, e := s.RecordUsage(ctx, o)
		mustMerge(t, e)
	}
	u, e := s.Usage(ctx, v.Memory)
	mustMerge(t, e)
	if u.Daily.Reads != 1 || u.Verification.Reads != 1 || u.App.Browses != 1 || u.Background.Reads != 1 {
		t.Fatal(u)
	}
	_, e = s.Apply(ctx, Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: v.Memory, Dataset: v.Dataset, ExpectedRevision: 1, Text: "version two"})
	mustMerge(t, e)
	if _, e = s.RecordUsage(ctx, usageObservation(c, v, "read")); e == nil {
		t.Fatal("stale version counted")
	}
	// SQL movement is an explicit scope-fence fixture, not an App move proof.
	b := uuid.NewString()
	_, e = s.DB.Exec(`INSERT INTO scopes(id,kind,name,path) VALUES(?,'project','moved','')`, b)
	mustMerge(t, e)
	_, e = s.DB.Exec(`UPDATE memories SET dataset=? WHERE id=?`, b, v.Memory)
	mustMerge(t, e)
	if _, e = s.UsageForVersion(ctx, v); e == nil {
		t.Fatal("stale-scope version enriched with current scope usage")
	}
	u, e = s.Usage(ctx, v.Memory)
	mustMerge(t, e)
	if u.Daily.Reads != 0 || u.App.Browses != 0 {
		t.Fatal("old project usage moved", u)
	}
}
func TestUsageRetentionTombstoneAndSQLRollback(t *testing.T) {
	ctx := context.Background()
	s, c, v := usageFixture(t)
	o := usageObservation(c, v, "read")
	r, e := s.RecordUsage(ctx, o)
	mustMerge(t, e)
	_, e = s.DB.Exec(`UPDATE usage_events SET observed_ms=?`, time.Now().Add(-91*24*time.Hour).UnixMilli())
	mustMerge(t, e)
	fresh := usageObservation(c, v, "read")
	_, e = s.RecordUsage(ctx, fresh)
	mustMerge(t, e)
	r2, e := s.RecordUsage(ctx, o)
	mustMerge(t, e)
	if !reflect.DeepEqual(r, r2) {
		t.Fatal("retention lost receipt")
	}
	u, e := s.Usage(ctx, v.Memory)
	mustMerge(t, e)
	if u.Daily.Reads != 1 {
		t.Fatal("expired ID recounted", u)
	}
	adopt := usageObservation(c, v, "adopt")
	adopt.Exposure = r.ID
	if _, e = s.RecordUsage(ctx, adopt); e == nil {
		t.Fatal("expired exposure accepted")
	}
	_, e = s.DB.Exec(`CREATE TRIGGER usage_insert_fault BEFORE INSERT ON usage_events BEGIN SELECT RAISE(ABORT,'usage SQL fixture'); END`)
	mustMerge(t, e)
	fail := usageObservation(c, v, "read")
	if _, e = s.RecordUsage(ctx, fail); e == nil {
		t.Fatal("SQL failure succeeded")
	}
	var n int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM usage_operations WHERE id=?`, fail.OperationID).Scan(&n))
	if n != 0 {
		t.Fatal("partial receipt persisted")
	}
}
func TestUsageSortPagingScopeAndImportant(t *testing.T) {
	ctx := context.Background()
	s, c, v := usageFixture(t)
	second, e := s.Apply(ctx, Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: v.Dataset, Text: "second"})
	mustMerge(t, e)
	_, e = s.RecordUsage(ctx, usageObservation(c, v, "read"))
	mustMerge(t, e)
	mustMerge(t, s.SetImportant(ctx, records.ImportantRequest{Memory: second.Memory, ExpectedRevision: 1, Important: true}))
	q := records.ListQuery{Sort: "frequent", Datasets: []string{v.Dataset}, Limit: 1}
	page, e := s.List(ctx, q)
	mustMerge(t, e)
	if page.Items[0].Memory != v.Memory || page.NextCursor == "" {
		t.Fatal(page)
	}
	q.Cursor = page.NextCursor
	next, e := s.List(ctx, q)
	mustMerge(t, e)
	if len(next.Items) != 1 || next.Items[0].Memory != second.Memory {
		t.Fatal(next)
	}
	q.Sort = "important"
	if _, e = s.List(ctx, q); e == nil {
		t.Fatal("sort mismatch cursor")
	}
	q.Cursor = ""
	important, e := s.List(ctx, q)
	mustMerge(t, e)
	if important.Items[0].Memory != second.Memory {
		t.Fatal(important)
	}
	q.Cursor = page.NextCursor
	q.Datasets = nil
	q.Sort = "frequent"
	if _, e = s.List(ctx, q); e == nil {
		t.Fatal("scope mismatch cursor")
	}
	if e = s.SetImportant(ctx, records.ImportantRequest{Memory: second.Memory, ExpectedRevision: 8, Important: false}); e == nil {
		t.Fatal("stale important action")
	}
}

func TestUsageV9MigrationPreservesAllTablesAndCoverage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.sqlite")
	old, e := open(path, false)
	mustMerge(t, e)
	tx, e := old.DB.Begin()
	mustMerge(t, e)
	for _, m := range []func(*sql.Tx) error{migrateMerge, migrateOrganization, migrateOrganizationReviews, migrateExperiences, migrateSchedules, migrateOrganizationLeases, migrateOrganizationRecovery} {
		mustMerge(t, m(tx))
	}
	mustMerge(t, tx.Commit())
	old.Schema = 9
	task, _, _ := mergeFixture(t, old)
	// The latest v9 recovery policy and lease facts must be nonempty too.
	manual, _, _ := organizationFixture(t, old)
	_, e = old.CancelOrganization(mergeCtx, manual.ID)
	mustMerge(t, e)
	plan, e := old.PutSchedule(mergeCtx, records.ScheduleDraft{ID: uuid.NewString(), OperationID: uuid.NewString(), Enabled: true, Spec: records.ScheduleSpec{Project: manual.Project, Executor: "traex", Timezone: "UTC", Frequency: "interval", IntervalHours: 1, WindowMinutes: 60, MaterialLimit: 8, RetryLimit: 1, RecoveryWindowMinutes: 10}}, time.Now())
	mustMerge(t, e)
	due := time.UnixMilli(plan.NextRunMS).Add(time.Second)
	_, automatic, e := old.FireSchedule(mergeCtx, plan.ID, plan.Generation, due)
	mustMerge(t, e)
	if automatic == nil {
		t.Fatal("v9 automatic task absent")
	}
	recoveryFail(t, old, *automatic, "agent_transient", due)

	tables, e := old.DB.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	mustMerge(t, e)
	names := []string{}
	for tables.Next() {
		var name string
		mustMerge(t, tables.Scan(&name))
		names = append(names, name)
	}
	mustMerge(t, tables.Err())
	tables.Close()
	before := map[string][][]any{}
	for _, n := range names {
		before[n] = sqlRows(t, old.DB, n)
	}
	_, e = old.DB.Exec(`PRAGMA wal_autocheckpoint=0`)
	mustMerge(t, e)
	reader, e := sql.Open("sqlite", path)
	mustMerge(t, e)
	hold, e := reader.Begin()
	mustMerge(t, e)
	var n int
	mustMerge(t, hold.QueryRow(`SELECT count(*) FROM versions`).Scan(&n))
	mustMerge(t, old.Close())
	wal, e := os.Stat(path + "-wal")
	mustMerge(t, e)
	if wal.Size() == 0 {
		t.Fatal("committed WAL missing")
	}
	start := time.Now().UnixMilli()
	current, e := Open(path)
	mustMerge(t, e)
	defer current.Close()
	mustMerge(t, hold.Rollback())
	mustMerge(t, reader.Close())
	backups, e := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v9-*.sqlite"))
	mustMerge(t, e)
	if len(backups) != 1 || current.Schema != 10 {
		t.Fatal("v9 backup/schema", backups, current.Schema)
	}
	backup, e := sql.Open("sqlite", backups[0])
	mustMerge(t, e)
	defer backup.Close()
	var version int
	var integrity string
	mustMerge(t, backup.QueryRow(`PRAGMA user_version`).Scan(&version))
	mustMerge(t, backup.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	if version != 9 || integrity != "ok" {
		t.Fatal(version, integrity)
	}
	for _, name := range names {
		if !reflect.DeepEqual(before[name], sqlRows(t, current.DB, name)) || !reflect.DeepEqual(before[name], sqlRows(t, backup, name)) {
			t.Fatal("old table changed", name)
		}
	}
	recovered, e := current.OrganizationTask(mergeCtx, automatic.ID)
	mustMerge(t, e)
	if recovered.Recovery == nil || recovered.Recovery.State != "waiting" || recovered.Recovery.Used != 0 || recovered.Recovery.ExtraLimit != 1 {
		t.Fatal("v9 recovery changed", recovered.Recovery)
	}
	u, e := current.Usage(context.Background(), task.Snapshot.Inputs[0].Memory)
	mustMerge(t, e)
	if u.StartedMS < start-2 || u.Daily.Reads != 0 || u.Important {
		t.Fatal("fictional old usage", u)
	}
}

func TestUsageFlaggedCurrentReadKeepsCompatibilityButCannotAdopt(t *testing.T) {
	ctx := context.Background()
	s, c, v := usageFixture(t)
	for _, state := range []string{"archived", "deleted"} {
		_, e := s.DB.Exec(`UPDATE memories SET state=? WHERE id=?`, state, v.Memory)
		mustMerge(t, e)
		read := usageObservation(c, v, "read")
		receipt, e := s.RecordUsage(ctx, read)
		mustMerge(t, e)
		adopt := usageObservation(c, v, "adopt")
		adopt.Exposure = receipt.ID
		if _, e = s.RecordUsage(ctx, adopt); e == nil {
			t.Fatal("flagged knowledge adopted", state)
		}
	}
}

func TestUsageReadFencesSourceStaleAfterCapturedVersion(t *testing.T) {
	ctx := context.Background()
	s, c, v := usageFixture(t)
	// Explicit derived-state fault after the caller captured v: revision is unchanged.
	_, e := s.DB.Exec(`INSERT INTO derived_memories(memory,revision,state) VALUES(?,1,'stale')`, v.Memory)
	mustMerge(t, e)
	if _, e = s.RecordUsage(ctx, usageObservation(c, v, "read")); e == nil {
		t.Fatal("stale source after capture counted as successful read")
	}
	u, e := s.Usage(ctx, v.Memory)
	mustMerge(t, e)
	if u.Daily.Reads != 0 {
		t.Fatal("stale source produced event", u)
	}
}
