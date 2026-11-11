package desktop

// Overlay-only synthetic IPC/headless checks. No Native or extraction model.
import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestNextG06TypedFailuresKeepTextUnreadableAndRetryTruthful(t *testing.T) {
	for _, test := range []struct {
		code    string
		retry   bool
		meaning string
	}{{"scope_denied", false, "访问权限"}, {"permission_denied", false, "访问权限"}, {"invalid_provenance", false, "身份缺失"}, {"version_changed", false, "版本已经变化"}, {"cognee_unavailable", true, "暂不可用"}, {"timeout", true, "读取超时"}, {"service_stopped", false, "服务已停止"}} {
		t.Run(test.code, func(t *testing.T) {
			a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				if r.Op != "source_read" {
					return serviceipc.Response{Error: "unexpected read"}
				}
				return serviceipc.Response{Code: test.code, Error: "do-not-display-old-source-or-secret"}
			})
			frame := sourceReadingFrame()
			a.reader = frame.reader
			a.selectedItem = frame.selectedItem
			a.selectedID = frame.selectedID
			a.page, a.scope = "项目", frame.scope
			a.items = frame.items
			old := a.reader.Window.Body
			a.refreshSourceReader()
			loading := ui.NewTester(a.sourceReader, 480, 700)
			if loading.HasText(old) {
				t.Fatal("in-flight verification exposed cached source text")
			}
			awaitProcessingDispatch(t, dispatch)
			if sourceReaderCanRetry(a.reader) != test.retry || !strings.Contains(a.sourceReaderMessage(), test.meaning) {
				t.Fatal("failure kind or retry policy conflated", a.reader.ErrorCode, a.sourceReaderMessage())
			}
			for _, width := range []int{680, 360} {
				tt := ui.NewTester(a.sourceReader, width, 700)
				if tt.HasText(old) || strings.Contains(strings.Join(tt.Texts(), "\n"), "do-not-display") {
					t.Fatal("unverified source/raw diagnostic escaped")
				}
				if tt.HasText("重新读取") != test.retry {
					t.Fatal("retry affordance contradicts failure", test.code, tt.Texts())
				}
				_ = tt.Click("复制全文")
				if tt.Clipboard() != "" {
					t.Fatal("unverified source copied")
				}
			}
			if !test.retry {
				sequence := a.reader.Sequence
				a.refreshSourceReader()
				if a.reader.Sequence != sequence || a.reader.Loading {
					t.Fatal("permanent failure silently submitted another read")
				}
				a.selectSourceRecord(records.OrganizationSource{Memory: a.reader.Query.Memory, Revision: a.reader.Query.Revision})
				if a.reader.Sequence != sequence || a.reader.ErrorCode != test.code {
					t.Fatal("selected source button bypassed nonretryable failure")
				}

			}
		})
	}
}
func TestNextG06ChangedSourceNeedsReturnAndNoUnknownV0(t *testing.T) {
	a := sourceReadingFrame()
	a.reader.Result.State = "source_changed"
	a.reader.Result.CurrentRevision = 0
	if strings.Contains(a.sourceReaderMessage(), "当前 v0") || !strings.Contains(a.sourceReaderMessage(), "未知") {
		t.Fatal("unknown revision invented", a.sourceReaderMessage())
	}
	for _, state := range []string{"source_changed", "owner_changed", "upstream_changed", "upstream_unavailable"} {
		a.reader.Result.State = state
		if sourceReaderCanRetry(a.reader) {
			t.Fatal("unchanged query can retry a permanent source mismatch", state)
		}
	}
	a.detailScroll.Y = 80
	a.homeActivity.Return = true
	tt := ui.NewTester(a.View, 980, 620)
	if err := tt.Click("返回知识"); err != nil {
		t.Fatal(err)
	}
	if a.reader.Open || a.selectedID != "knowledge" || a.detailScroll.Y != 80 || !a.homeActivity.Return {
		t.Fatal("failure return lost reading/home context")
	}
}
func nextG08Frame() (*App, MemoryItem) {
	a := New("next-g08", nil)
	a.page, a.scope = "项目", "project"
	a.browsing.View = "memories"
	item := MemoryItem{ID: "knowledge", Dataset: "project", Revision: 2, Origin: "knowledge", State: "active", Text: "current knowledge", Title: "current title", Usage: &records.UsageInfo{StartedMS: 1800000000000}}
	a.selectedItem, a.selectedID = &item, item.ID
	a.browsing.Graph = records.CogneeGraph{Project: item.Dataset, Dataset: "material-dataset", CogneeVersion: "1.6.7", State: "available", Entities: []records.CogneeEntity{{ID: "entity", Name: "Actual object"}}, Mentions: []records.CogneeMention{{Entity: "entity", CogneeGraphSource: records.CogneeGraphSource{Memory: item.ID, Revision: 2, Origin: "knowledge", Quote: item.Text}}}}
	return a, item
}
func TestNextG08RelationsDistinguishUnknownLoadingFailureAndCurrentEmpty(t *testing.T) {
	for _, test := range []struct {
		name, state, text string
		mutate            func(*App)
	}{
		{"unread", "unread", "尚未取得", func(a *App) { a.browsing.Graph.Project = "" }},
		{"loading", "loading", "正在读取", func(a *App) { a.browsing.Loading = true }},
		{"failed", "failed", "读取失败", func(a *App) { a.browsing.Error = "do-not-display-transport-secret" }},
		{"stopped", "stopped", "服务已停止", func(a *App) { a.runtimeStatus.Phase = "stopped" }},
		{"unconnected", "unconnected", "尚未接入", func(a *App) { a.browsing.Graph.State = "unconnected" }},
		{"empty", "empty", "本次读取未找到", func(a *App) { a.browsing.Graph.Mentions = nil; a.browsing.Graph.State = "empty" }},
		{"old-version", "empty", "本次读取未找到", func(a *App) { a.browsing.Graph.Mentions[0].Revision = 1 }},
		{"unsupported-receipt", "unread", "尚未取得", func(a *App) { a.browsing.Graph.CogneeVersion = "unknown" }},
	} {
		for _, width := range []int{680, 320} {
			t.Run(fmt.Sprint(test.name, width), func(t *testing.T) {
				a, item := nextG08Frame()
				test.mutate(a)
				if a.knowledgeRelationState(item) != test.state {
					t.Fatal("relation state conflated", a.knowledgeRelationState(item))
				}
				tt := ui.NewTester(func(c *ui.Context) { a.knowledgeRelations(c, item) }, width, 600)
				text := strings.Join(tt.Texts(), "\n")
				if !strings.Contains(text, test.text) || tt.HasText("Actual object") || strings.Contains(text, "do-not-display") {
					t.Fatal("failure hidden or cached entity remained clickable", text)
				}
			})
		}
	}
	a, item := nextG08Frame()
	tt := ui.NewTester(func(c *ui.Context) { a.knowledgeRelations(c, item) }, 400, 500)
	if !tt.HasText("Actual object") || !tt.HasText("已核验当前版本 v2 · 1 个对象") {
		t.Fatal("verified current mention lost real entity entry")
	}
	before := a.selectedID
	if err := tt.Click("Actual object"); err != nil {
		t.Fatal(err)
	}
	if a.browsing.View != "entities" || a.browsing.Entity != "entity" || a.selectedID != before {
		t.Fatal("entity entry lost reading identity")
	}
}

func TestNextG06LateReceiptCannotCrossRestoredLibraryEpoch(t *testing.T) {
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		return serviceipc.Response{OK: true, Result: records.SourceRecord{State: "available", Record: records.Version{Memory: "raw", Dataset: "project", Revision: 1, Text: "old-library-verified-text", Origin: "raw"}}}
	})
	frame := sourceReadingFrame()
	a.reader = frame.reader
	a.refreshSourceReader()
	a.libraryEpoch++
	a.reader.Loading = false
	a.reader.Result = records.SourceRecord{}
	a.reader.Window = nil
	a.reader.Error = "replacement library source state"
	awaitProcessingDispatch(t, dispatch)
	if a.reader.Error != "replacement library source state" || a.reader.Window != nil || a.reader.Result.Record.Text != "" {
		t.Fatal("late old-library source became readable")
	}
}

func TestNextG06FullOriginalChangedDoesNotOfferUnchangedRetry(t *testing.T) {
	a := sourceReadingFrame()
	a.reader.HadSessionOriginal = true
	a.reader.Full = true
	a.reader.Result.OriginalState = "upstream_changed"
	if sourceReaderCanRetry(a.reader) || !strings.Contains(a.sourceReaderMessage(), "完整会话来源已变化") {
		t.Fatal("changed full original offered same retry")
	}
	tt := ui.NewTester(a.sourceReader, 480, 700)
	if tt.HasText("重新读取") || tt.HasText(a.reader.Window.Body) {
		t.Fatal("changed full original became readable/retryable")
	}
	if err := tt.Click("引用片段"); err != nil {
		t.Fatal(err)
	}
	if a.reader.Full || !tt.HasText(a.reader.Window.Body) {
		t.Fatal("explicit verified segment choice was removed")
	}
	if sourceReaderErrorCode(os.ErrPermission) != "permission_denied" {
		t.Fatal("filesystem permission failure treated as transient service failure")
	}
}
