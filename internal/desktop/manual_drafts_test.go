package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"github.com/zwhu/memlane/internal/store"
)

func manualTestApp(t *testing.T, root, identity, dataset string) (*App, chan func()) {
	t.Helper()
	a := New("test", nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.libraryContext = ctx
	a.library = serviceipc.Library{Root: root}
	a.visualFixture = true
	updates := make(chan func(), 128)
	a.dispatch = func(fn func()) { updates <- fn }
	a.configureManualDrafts(root)
	a.scopes = []records.Scope{{ID: dataset, Kind: "project", Name: "原项目"}}
	a.projects = []ProjectItem{{ID: dataset, Name: "原项目"}}
	a.page, a.scope = "项目", dataset
	a.manualScopesLoaded()
	a.loadManualDrafts(identity)
	manualAwait(t, updates)
	if !a.manualDrafts.Loaded {
		t.Fatal("manual fixture not restored", a.manualDrafts.Error)
	}
	t.Cleanup(func() { a.retireManualDrafts() })
	return a, updates
}
func manualAwait(t *testing.T, updates <-chan func()) {
	t.Helper()
	select {
	case fn := <-updates:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("manual draft dispatch missing")
	}
}
func manualFlush(t *testing.T, a *App, updates <-chan func()) error {
	t.Helper()
	done := make(chan error, 1)
	a.FlushDraftPreviews(func(err error) { done <- err })
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case err := <-done:
			return err
		case fn := <-updates:
			fn()
		case <-timeout.C:
			t.Fatal("manual draft exit barrier did not resolve")
			return errors.New("timeout")
		}
	}
}
func manualStop(t *testing.T, a *App) {
	t.Helper()
	w := a.manualDrafts.Writer
	a.retireManualDrafts()
	deadline := time.Now().Add(5 * time.Second)
	for {
		w.mu.Lock()
		running := w.running
		w.mu.Unlock()
		if !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("manual writer retirement did not drain")
		}
		time.Sleep(time.Millisecond)
	}
}
func manualEnvelopeFixture(identity, dataset string) manualDraftEnvelope {
	key := "new:" + dataset
	loc := manualDraftLocation{Page: "项目", Scope: dataset, Mode: "new", Kind: "knowledge", Scroll: 88}
	return manualDraftEnvelope{Format: manualDraftFormat, Identity: identity, Entries: map[string]manualDraftEntry{key: {Dataset: dataset, Title: "未保存标题", Text: "未保存的新内容", Location: loc}}, Active: &loc}
}
func writeManualFixture(t *testing.T, root string, e manualDraftEnvelope) {
	t.Helper()
	path := filepath.Join(root, "client-drafts")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(path, e.Identity+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestManualDraftReopenKeepsTextTargetVersionAndNoEffectiveWrite(t *testing.T) {
	root, identity, dataset, memory := t.TempDir(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	a, updates := manualTestApp(t, root, identity, dataset)
	item := MemoryItem{ID: memory, Dataset: dataset, Title: "原有效标题", Text: "原有效正文", ScopeName: "原项目", Revision: 1, State: "active", Origin: "knowledge"}
	a.selectedID, a.selectedItem = memory, &item
	a.editMemory()
	a.draftTitle, a.draft = "未保存标题", "未保存的新内容"
	a.detailScroll.Y = 145
	if err := manualFlush(t, a, updates); err != nil {
		t.Fatal(err)
	}
	manualStop(t, a)
	b, _ := manualTestApp(t, root, identity, dataset)
	if b.draft != "未保存的新内容" || b.draftTitle != "未保存标题" || !b.editing || b.selectedID != memory || b.drafts[memory].Revision != 1 || b.manualDrafts.Targets[memory] != dataset || b.detailScroll.Y != 145 {
		t.Fatal("process reopen lost manual draft or stable identity", b.drafts)
	}
	newer := item
	newer.Revision = 2
	newer.Text = "另一写入者的新有效正文"
	b.selectedItem = &newer
	b.editMemory()
	if b.drafts[memory].Revision != 1 || b.draft != "未保存的新内容" || b.current().Text != "另一写入者的新有效正文" {
		t.Fatal("source change silently replaced draft or baseline")
	}
}

func TestManualDraftRealProcessCanReadDurablePrivateEpochFile(t *testing.T) {
	root, identity, dataset := t.TempDir(), uuid.NewString(), uuid.NewString()
	a, updates := manualTestApp(t, root, identity, dataset)
	a.newMemory()
	a.draft = "未保存的新内容"
	if err := manualFlush(t, a, updates); err != nil {
		t.Fatal(err)
	}
	manualStop(t, a)
	cmd := exec.Command(os.Args[0], "-test.run=^TestManualDraftProcessReader$")
	cmd.Env = append(os.Environ(), "MEMLANE_MANUAL_DRAFT_TEST_ROOT="+root, "MEMLANE_MANUAL_DRAFT_TEST_ID="+identity)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("separate process cannot recover manual file: %v %s", err, output)
	}
	info, err := os.Stat(filepath.Join(root, "client-drafts", identity+".json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("manual draft file is not private", err)
	}
}
func TestManualDraftProcessReader(t *testing.T) {
	root := os.Getenv("MEMLANE_MANUAL_DRAFT_TEST_ROOT")
	if root == "" {
		t.Skip("isolated subprocess helper")
	}
	disk, e, err := openManualDraftDisk(root, os.Getenv("MEMLANE_MANUAL_DRAFT_TEST_ID"))
	if err != nil {
		t.Fatal(err)
	}
	defer disk.close()
	if len(e.Entries) != 1 {
		t.Fatal("durable entries missing")
	}
	for _, entry := range e.Entries {
		if entry.Text != "未保存的新内容" || entry.Revision != 0 {
			t.Fatal("subprocess changed draft")
		}
	}
}

func TestManualDraftOfflineFlushNeedsPreviouslyVerifiedIdentity(t *testing.T) {
	root, identity, dataset := t.TempDir(), uuid.NewString(), uuid.NewString()
	saved := manualEnvelopeFixture(identity, dataset)
	writeManualFixture(t, root, saved)
	a := New("test", nil)
	a.configureManualDrafts(root)
	a.runtimeStatus.Phase = "stopped"
	a.syncManualDrafts()
	if a.manualDrafts.Identity != "" || a.manualDrafts.Loaded || len(a.drafts) != 0 {
		t.Fatal("cold offline launch trusted cached epoch")
	}
	tt := ui.NewTester(a.View, 800, 720)
	if !tt.HasText("明确启动服务后核验当前库并恢复") {
		t.Fatal("offline identity boundary not visible")
	}
	// A live read proves identity; after that local persistence needs no daemon.
	a.retireManualDrafts()
	b, updates := manualTestApp(t, root, identity, dataset)
	b.runtimeStatus.Phase = "stopped"
	b.draft = "已核验epoch后的离线新编辑"
	if err := manualFlush(t, b, updates); err != nil {
		t.Fatal("verified epoch could not flush offline", err)
	}
	manualStop(t, b)
	disk, e, err := openManualDraftDisk(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.close()
	if e.Entries["new:"+dataset].Text != "已核验epoch后的离线新编辑" {
		t.Fatal("offline latest editor contents not durable")
	}
}

func TestManualDraftFailureBlocksApplyAndQuitWithoutDroppingIntent(t *testing.T) {
	root, identity, dataset := t.TempDir(), uuid.NewString(), uuid.NewString()
	a, updates := manualTestApp(t, root, identity, dataset)
	a.newMemory()
	a.draft = "不能丢失的草稿"
	a.draftTitle = "保存失败样本"
	var calls atomic.Int64
	a.manualDrafts.Writer.save = func(context.Context, []byte) error {
		calls.Add(1)
		return errors.New("synthetic filesystem write failure")
	}
	a.saveMemory()
	manualAwait(t, updates)
	key := a.newDraftKey()
	intent := a.pendingMutations[key]
	if a.saving || a.manualDrafts.Applying || intent.OperationID == "" || a.draft != "不能丢失的草稿" || a.manualDrafts.Error == "" {
		t.Fatal("failed local barrier dropped draft or retained saving lock")
	}
	if err := manualFlush(t, a, updates); err == nil || a.experienceExitPending {
		t.Fatal("unconfirmed manual write permitted exit")
	}
	if a.pendingMutations[key].OperationID != intent.OperationID || calls.Load() != 2 {
		t.Fatal("unknown local write was replaced or bypassed")
	}
}

func TestManualDraftUnknownPublishReplaysExactBeforeLatestAndBlocksExit(t *testing.T) {
	root, identity, dataset := t.TempDir(), uuid.NewString(), uuid.NewString()
	a, updates := manualTestApp(t, root, identity, dataset)
	a.newMemory()
	a.draft = "本地发布回执未知前的正文"
	w := a.manualDrafts.Writer
	actual := w.save
	var mu sync.Mutex
	writes := [][]byte{}
	fail := true
	w.save = func(ctx context.Context, data []byte) error {
		mu.Lock()
		writes = append(writes, append([]byte(nil), data...))
		reject := fail
		mu.Unlock()
		if err := actual(ctx, data); err != nil {
			return err
		}
		if reject {
			return errors.New("published but receipt unavailable")
		}
		return nil
	}
	if err := manualFlush(t, a, updates); err == nil {
		t.Fatal("unknown publish admitted quit")
	}
	a.draft = "未知后仍可继续编辑的新正文"
	mu.Lock()
	fail = false
	mu.Unlock()
	if err := manualFlush(t, a, updates); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(writes) != 3 || !bytes.Equal(writes[0], writes[1]) || bytes.Equal(writes[1], writes[2]) {
		t.Fatal("uncertain original not replayed exactly before newer draft")
	}
	mu.Unlock()
	manualStop(t, a)
	disk, e, err := openManualDraftDisk(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.close()
	if e.Entries["new:"+dataset].Text != "未知后仍可继续编辑的新正文" {
		t.Fatal("latest edit not published after recovery")
	}
}

func TestManualDraftRenameSameNameAndMissingScopeNeverRetarget(t *testing.T) {
	root, identity, original, other := t.TempDir(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	a, updates := manualTestApp(t, root, identity, original)
	a.newMemory()
	a.draft = "绑定原始范围的正文"
	if err := manualFlush(t, a, updates); err != nil {
		t.Fatal(err)
	}
	manualStop(t, a)
	b, more := manualTestApp(t, root, identity, original)
	b.scopes = []records.Scope{{ID: original, Kind: "project", Name: "更名后的原项目"}, {ID: other, Kind: "project", Name: "原项目"}}
	b.refreshManualDraftLabels()
	if b.manualDrafts.Targets[b.newDraftKey()] != original || b.draftScope != "更名后的原项目" {
		t.Fatal("rename rebound draft by reused display label")
	}
	if err := manualFlush(t, b, more); err != nil {
		t.Fatal(err)
	}
	// A reused label cannot be treated as an explicit target change.
	b.draftScope = "原项目"
	b.saveMemory()
	if b.saving || b.manualDrafts.Targets[b.newDraftKey()] != original || b.draftScope != "更名后的原项目" {
		t.Fatal("save followed reused label instead of frozen target")
	}
	b.scopes = []records.Scope{{ID: other, Kind: "project", Name: "更名后的原项目"}}
	b.refreshManualDraftLabels()
	if b.manualDrafts.Targets[b.newDraftKey()] != original || !strings.Contains(b.draftScope, "原范围不可用") {
		t.Fatal("removed original scope silently adopted another project")
	}
	if err := manualFlush(t, b, more); err != nil {
		t.Fatal("orphan draft lost durable preservation", err)
	}
	// A deliberate valid picker action can change a new draft's target.
	b.draftScope = b.scopeLabel(other)
	b.manualDraftScopeChanged()
	if b.manualDrafts.Targets[b.newDraftKey()] != other {
		t.Fatal("explicit scope choice not recorded")
	}
}

func TestManualDraftScopeChoicesDisambiguateIdenticalUUIDPrefixes(t *testing.T) {
	a := New("test", nil)
	one, two := "11111111-1111-4111-8111-111111111111", "11111111-2222-4222-8222-222222222222"
	a.scopes = []records.Scope{{ID: one, Kind: "project", Name: "同名"}, {ID: two, Kind: "project", Name: "同名"}}
	labels := a.scopeOptions()
	if len(labels) != 2 || labels[0] == labels[1] || a.datasetForLabel(labels[1]) != two || a.datasetForLabel("同名") != "" {
		t.Fatal("ambiguous picker guessed first target", labels)
	}
}

func TestManualDraftUnsafeFilesAndForbiddenPendingOperationsFailClosed(t *testing.T) {
	identity, dataset := uuid.NewString(), uuid.NewString()
	cases := []string{"wrong-epoch", "unknown-field", "duplicate-key", "delete", "restore", "move", "changed-intent", "world-readable", "symlink", "hardlink", "oversize"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			e := manualEnvelopeFixture(identity, dataset)
			key := "new:" + dataset
			if name == "delete" || name == "restore" || name == "move" || name == "changed-intent" {
				d := e.Entries[key]
				kind := "save"
				if name != "changed-intent" {
					kind = name
				}
				request := records.Mutation{OperationID: uuid.NewString(), Kind: kind, Memory: uuid.NewString(), Dataset: dataset, Source: "手动新增", Text: d.Text, Title: d.Title}
				d.Pending = &manualDraftIntent{Request: request, Text: d.Text, Title: d.Title}
				if name == "changed-intent" {
					d.Pending.Request.Text = "替换请求"
				}
				e.Entries[key] = d
			}
			if name == "wrong-epoch" {
				e.Identity = uuid.NewString()
			}
			raw, _ := json.Marshal(e)
			if name == "unknown-field" {
				raw = append([]byte(`{"unexpected":true,`), raw[1:]...)
			}
			if name == "duplicate-key" {
				raw = append([]byte(`{"format":1,`), raw[1:]...)
			}
			if name == "oversize" {
				raw = bytes.Repeat([]byte("x"), manualDraftMaxBytes+1)
			}
			directory := filepath.Join(root, "client-drafts")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, identity+".json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if name == "world-readable" {
				os.Chmod(path, 0644)
			}
			if name == "symlink" {
				os.Remove(path)
				target := filepath.Join(root, "outside.json")
				os.WriteFile(target, raw, 0600)
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if name == "hardlink" {
				if err := os.Link(path, filepath.Join(root, "another-name.json")); err != nil {
					t.Fatal(err)
				}
			}
			disk, _, err := openManualDraftDisk(root, identity)
			if disk != nil {
				disk.close()
			}
			if err == nil {
				t.Fatal("unsafe manual file accepted")
			}
		})
	}
}

func TestManualDraftSingleWriterAndDifferentEpochFilesRemainIsolated(t *testing.T) {
	root, one, two, dataset := t.TempDir(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	first, _, err := openManualDraftDisk(root, one)
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	if second, _, err := openManualDraftDisk(root, one); err == nil {
		second.close()
		t.Fatal("two writers acquired same epoch")
	}
	other, empty, err := openManualDraftDisk(root, two)
	if err != nil {
		t.Fatal(err)
	}
	defer other.close()
	old := manualEnvelopeFixture(one, dataset)
	data, _ := json.Marshal(old)
	if err := first.write(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	if len(empty.Entries) != 0 {
		t.Fatal("new epoch read old editor")
	}
	current, err := other.read(two)
	if err != nil || len(current.Entries) != 0 {
		t.Fatal("late old writer overwrote new epoch")
	}
}

func TestManualDraftPendingIntentCannotChangeScopeUntilExplicitReceiptReview(t *testing.T) {
	root, identity, dataset, other := t.TempDir(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	a, _ := manualTestApp(t, root, identity, dataset)
	a.scopes = append(a.scopes, records.Scope{ID: other, Kind: "project", Name: "另一个项目"})
	a.newMemory()
	a.draft = "待确认保存请求"
	key := a.newDraftKey()
	a.pendingMutations[key] = records.Mutation{OperationID: uuid.NewString(), Kind: "save", Memory: uuid.NewString(), Dataset: dataset, Source: "手动新增", Text: a.draft}
	a.draftScope = a.scopeLabel(other)
	a.manualDraftScopeChanged()
	if a.manualDrafts.Targets[key] != dataset || a.draftScope != a.scopeLabel(dataset) || a.pendingMutations[key].Dataset != dataset {
		t.Fatal("unresolved request silently moved scope")
	}
}

func TestManualDraftApplyUnknownReceiptReopensExactOperationWithoutDuplicateKnowledge(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(filepath.Join(t.TempDir(), "knowledge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	dataset, identity := uuid.NewString(), uuid.NewString()
	if _, err = database.DB.Exec(`INSERT INTO scopes(id,kind,name)VALUES(?,'project','synthetic')`, dataset); err != nil {
		t.Fatal(err)
	}
	var requestsMu sync.Mutex
	requests := []records.Mutation{}
	a, updates := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "apply" {
			t.Errorf("manual draft replay invoked %s", r.Op)
			return serviceipc.Response{OK: false}
		}
		requestsMu.Lock()
		requests = append(requests, r.Mutation)
		n := len(requests)
		requestsMu.Unlock()
		v, e := database.Apply(ctx, r.Mutation)
		if e != nil {
			return serviceipc.Response{OK: false, Error: e.Error()}
		}
		if n == 1 {
			return serviceipc.Response{OK: false, Error: "synthetic committed receipt lost"}
		}
		return serviceipc.Response{OK: true, Result: v}
	})
	root := t.TempDir()
	a.configureManualDrafts(root)
	a.page, a.scope = "项目", dataset
	a.scopes = []records.Scope{{ID: dataset, Kind: "project", Name: "synthetic"}}
	a.projects = []ProjectItem{{ID: dataset, Name: "synthetic"}}
	a.manualScopesLoaded()
	a.loadManualDrafts(identity)
	manualAwait(t, updates)
	a.newMemory()
	a.draft = "只应该保存一次的知识"
	a.draftTitle = "合成重放"
	a.saveMemory()
	manualAwait(t, updates)
	manualAwait(t, updates)
	key := a.newDraftKey()
	original := a.pendingMutations[key]
	if original.OperationID == "" || a.saving || a.draft != "只应该保存一次的知识" {
		t.Fatal("unknown Apply dropped original intent")
	}
	if err = manualFlush(t, a, updates); err != nil {
		t.Fatal(err)
	}
	manualStop(t, a)
	b, _ := manualTestApp(t, root, identity, dataset)
	b.library = a.library
	b.dispatch = a.dispatch
	b.saveMemory()
	for b.saving {
		manualAwait(t, updates)
	}
	requestsMu.Lock()
	defer requestsMu.Unlock()
	if len(requests) != 2 || !reflect.DeepEqual(requests[0], requests[1]) {
		t.Fatal("reopen regenerated unknown operation")
	}
	got, err := database.Current(ctx, original.Memory)
	if err != nil || got.Revision != 1 {
		t.Fatal("replay duplicated or modified effective knowledge", err, got.Revision)
	}
	var count int
	if err = database.DB.QueryRow(`SELECT count(*) FROM memories`).Scan(&count); err != nil || count != 1 {
		t.Fatal("replay created second memory", err, count)
	}
}

func TestManualDraftSavedKnowledgeRequiresDurableTombstoneBeforeQuit(t *testing.T) {
	root, identity, dataset := t.TempDir(), uuid.NewString(), uuid.NewString()
	a, updates := manualTestApp(t, root, identity, dataset)
	a.newMemory()
	a.draft = "保存后应退休的草稿"
	if err := manualFlush(t, a, updates); err != nil {
		t.Fatal(err)
	}
	key := a.newDraftKey()
	delete(a.drafts, key)
	delete(a.pendingMutations, key)
	a.editing = false
	a.detailMode = "view"
	if !a.HasDraftPreview() {
		t.Fatal("saved editor bypassed stale on-disk draft tombstone")
	}
	if err := manualFlush(t, a, updates); err != nil {
		t.Fatal(err)
	}
	manualStop(t, a)
	disk, e, err := openManualDraftDisk(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.close()
	if len(e.Entries) != 0 || e.Active != nil {
		t.Fatal("completed knowledge draft would resurrect on restart")
	}
}

func TestManualDraftPureLoadFailureAllowsExitWithoutTouchingFile(t *testing.T) {
	for _, kind := range []string{"corrupt", "locked"} {
		t.Run(kind, func(t *testing.T) {
			root, identity, dataset := t.TempDir(), uuid.NewString(), uuid.NewString()
			writeManualFixture(t, root, manualEnvelopeFixture(identity, dataset))
			path := filepath.Join(root, "client-drafts", identity+".json")
			if kind == "corrupt" {
				if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				disk, _, err := openManualDraftDisk(root, identity)
				if err != nil {
					t.Fatal(err)
				}
				defer disk.close()
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			a := New("test", nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a.libraryContext = ctx
			updates := make(chan func(), 8)
			a.dispatch = func(fn func()) { updates <- fn }
			a.configureManualDrafts(root)
			defer a.retireManualDrafts()
			a.loadManualDrafts(identity)
			manualAwait(t, updates)
			if a.manualDrafts.Error == "" || a.manualDrafts.Read || a.HasDraftPreview() {
				t.Fatal("pure load failure incorrectly acquired draft ownership")
			}
			if err := manualFlush(t, a, updates); err != nil {
				t.Fatal("untouched unread file blocked exit", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed load changed original file", err)
			}
		})
	}
}

func TestManualDraftRetiredWriterCannotPublishIntoNewEpoch(t *testing.T) {
	root, oldID, nextID, dataset := t.TempDir(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	a, updates := manualTestApp(t, root, oldID, dataset)
	a.newMemory()
	a.draft = "旧库排队草稿"
	entered, release := make(chan struct{}), make(chan struct{})
	w := a.manualDrafts.Writer
	w.save = func(ctx context.Context, data []byte) error {
		close(entered)
		<-release
		return w.disk.write(ctx, data)
	}
	a.syncManualDrafts()
	// Fixtures normally suppress automatic persistence; enqueue the same real barrier explicitly.
	if a.visualFixture {
		a.persistManualDrafts(func(error) {})
	}
	<-entered
	a.retireManualDrafts()
	b, more := manualTestApp(t, root, nextID, dataset)
	b.newMemory()
	b.draft = "新库独立草稿"
	if err := manualFlush(t, b, more); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "client-drafts", nextID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	manualAwait(t, updates)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) || b.draft != "新库独立草稿" {
		t.Fatal("retired writer changed new epoch", err)
	}
}

func TestManualDraftOwnerlessPendingIntentCannotBypassExit(t *testing.T) {
	a, updates := manualTestApp(t, t.TempDir(), uuid.NewString(), uuid.NewString())
	a.pendingMutations[uuid.NewString()] = records.Mutation{Kind: "save", OperationID: uuid.NewString()}
	if !a.HasDraftPreview() {
		t.Fatal("pending request lost its exit owner")
	}
	if err := manualFlush(t, a, updates); err == nil {
		t.Fatal("ownerless pending intent was discarded on exit")
	}
}
