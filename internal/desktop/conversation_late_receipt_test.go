package desktop

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestConversationQueuedSuccessCannotOverwriteSwitchedOrReopenedSession(t *testing.T) {
	for _, returnToOriginal := range []bool{false, true} {
		t.Run(fmt.Sprint("return_to_original_", returnToOriginal), func(t *testing.T) {
			var requests atomic.Int64
			a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				if r.Op != "official_session" || r.SessionQuery == nil || r.SessionQuery.Selected == nil {
					return serviceipc.Response{Code: "unsupported", Error: "unexpected request"}
				}
				row := *r.SessionQuery.Selected
				n := requests.Add(1)
				return serviceipc.Response{OK: true, Result: records.SessionDetail{SessionSummary: row, QACount: 1, QAs: []records.SessionQA{{ID: "qa-" + row.Session, Question: row.Session, Answer: fmt.Sprintf("Current %s receipt %d\n\n", row.Session, n) + strings.Repeat("A scrollable current receipt paragraph.\n\n", 30)}}}}
			})
			original, _ := conversationTestState(a)
			other := records.SessionSummary{Session: "other-session", User: original.User, Dataset: original.Dataset}
			a.conversations.Rows = append(a.conversations.Rows, other)
			a.refreshSelectedConversation()
			var oldSuccess func()
			select {
			case oldSuccess = <-dispatch:
			case <-time.After(5 * time.Second):
				t.Fatal("old successful response was not queued")
			}
			// The successful A callback has already crossed IPC. Cancelling its
			// former context cannot remove this callback from the UI queue.
			a.selectConversation(other)
			awaitProcessingDispatch(t, dispatch)
			wantSession, wantReceipt := other.Session, int64(2)
			if returnToOriginal {
				a.selectConversation(original)
				awaitProcessingDispatch(t, dispatch)
				wantSession, wantReceipt = original.Session, 3
			}
			key := a.conversations.Selected
			view := a.conversations.Views[key]
			tt := ui.NewTester(a.View, 800, 620)
			view.Scroll.Y = 80
			tt.Frame()
			if view.Scroll.Y != 80 {
				t.Fatal("current response fixture is not scrollable")
			}
			current := view.Detail
			oldSuccess()
			tt.Frame()
			want := fmt.Sprintf("Current %s receipt %d", wantSession, wantReceipt)
			if a.conversations.Selected != key || view.Detail != current || view.Detail.Session != wantSession || !strings.HasPrefix(view.Detail.QAs[0].Answer, want) || a.conversations.Reading || view.Scroll.Y != 80 {
				t.Fatal("queued old success replaced current selection, response, request state or scroll")
			}
			if err := tt.Click("复制已载入内容"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(tt.Clipboard(), want) || strings.Contains(tt.Clipboard(), "receipt 1") {
				t.Fatal("copy used an older successful response")
			}
		})
	}
}
