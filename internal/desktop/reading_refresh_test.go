package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/serviceipc"
	"testing"
	"time"
)

func TestSourceRefreshTransientFailureRetainsVerifiedBodyAndRecovers(t *testing.T) {
	frame := sourceReadingFrame()
	result := frame.reader.Result
	for _, code := range []string{"timeout", "deadline_exceeded", "service_unavailable", "cognee_unavailable"} {
		t.Run(code, func(t *testing.T) {
			fail := true
			a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				if fail {
					return serviceipc.Response{Code: code, Error: "synthetic outage"}
				}
				return serviceipc.Response{OK: true, Result: result}
			})
			a.reader = frame.reader
			a.reader.DirectRaw = true
			a.reader.Technical, a.reader.Search, a.reader.Scroll.Y = true, "原文", 40
			window := a.reader.Window
			tt := ui.NewTester(a.sourceReader, 680, 700)
			a.refreshSourceReader()
			awaitProcessingDispatch(t, dispatch)
			tt.Frame()
			if a.reader.Window != window || !tt.HasText(window.Body) || !tt.HasText("刷新暂未完成，正在显示上次核验的内容。") {
				t.Fatal("temporary refresh failure removed verified content or omitted stale notice")
			}
			if a.reader.Search != "原文" || !a.reader.Technical {
				t.Fatal("refresh discarded reader state")
			}
			if err := tt.Click("复制正文"); err != nil || tt.Clipboard() != window.Body {
				t.Fatal("valid retained text cannot be copied", err)
			}
			fail = false
			a.refreshSourceReader()
			awaitProcessingDispatch(t, dispatch)
			tt.Frame()
			if a.reader.Window != window || a.reader.ErrorCode != "" || tt.HasText("刷新暂未完成，正在显示上次核验的内容。") {
				t.Fatal("successful retry did not recover in place")
			}
		})
	}
}

func TestSourceRefreshRevocationAfterTransientFailureClearsRetainedContent(t *testing.T) {
	for _, code := range []string{"connection_revoked", "invalid_provenance", "version_changed", "source_read_failed", "unexpected_failure"} {
		t.Run(code, func(t *testing.T) {
			a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				return serviceipc.Response{Code: code, Error: "synthetic revocation"}
			})
			a.reader = sourceReadingFrame().reader
			body := a.reader.Window.Body
			a.reader.ErrorCode, a.reader.Error = "timeout", "synthetic timeout"
			a.refreshSourceReader()
			awaitProcessingDispatch(t, dispatch)
			tt := ui.NewTester(a.sourceReader, 680, 700)
			if a.reader.Window != nil || tt.HasText(body) {
				t.Fatal("revoked text remains readable")
			}
			_ = tt.Click("复制全文")
			if tt.Clipboard() != "" {
				t.Fatal("revoked text remains copyable")
			}
		})
	}
}

func TestSourceRefreshLateReceiptCannotRestoreClosedReader(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	result := sourceReadingFrame().reader.Result
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		close(started)
		<-release
		return serviceipc.Response{OK: true, Result: result}
	})
	a.reader = sourceReadingFrame().reader
	a.refreshSourceReader()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("source read did not start")
	}
	a.closeSourceReader()
	close(release)
	awaitProcessingDispatch(t, dispatch)
	if a.reader.Open || a.reader.Window != nil {
		t.Fatal("late receipt restored closed reader")
	}
}

func Test阅读后台刷新保留数量与正文(t *testing.T) {
	frame := sourceReadingFrame()
	result := frame.reader.Result
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		return serviceipc.Response{OK: true, Result: result}
	})
	a.reader = frame.reader
	a.page, a.scope, a.browsing.View = frame.page, frame.scope, "memories"
	a.projects, a.items = frame.projects, frame.items
	a.selectedID, a.selectedItem = frame.selectedID, frame.selectedItem
	knowledge, raw := 2, 1
	a.readingKnowledgeTotal, a.readingRawTotal = &knowledge, &raw
	a.readingCountsRoute, a.listTotal = "项目:project", 2
	tt := ui.NewTester(a.View, 1200, 820)
	body := a.reader.Window.Body
	window := a.reader.Window
	if !tt.HasText("2 条知识") || !tt.HasText(body) {
		t.Fatal("初始已核验内容未展示")
	}
	for i := 0; i < 3; i++ {
		a.loading = true
		a.refreshSourceReader()
		tt.Frame()
		if !tt.HasText("2 条知识") || !tt.HasText(body) {
			t.Fatal("后台刷新清空数量或正文")
		}
		awaitProcessingDispatch(t, dispatch)
		a.loading = false
		tt.Frame()
		if a.reader.Window != window || !tt.HasText(body) {
			t.Fatal("相同正文重建了阅读窗口")
		}
	}
}

func Test原文首次加载与来源变化不展示旧正文(t *testing.T) {
	for _, state := range []string{"", "source_changed", "owner_changed", "upstream_changed", "upstream_unavailable"} {
		t.Run(state, func(t *testing.T) {
			a := sourceReadingFrame()
			body := a.reader.Window.Body
			a.reader.Result.State = state
			a.reader.Loading = state == ""
			tt := ui.NewTester(a.sourceReader, 680, 700)
			if tt.HasText(body) {
				t.Fatal("未核验或已变化的正文仍可读")
			}
			_ = tt.Click("复制全文")
			if tt.Clipboard() != "" {
				t.Fatal("不可读正文仍可复制")
			}
		})
	}
}
