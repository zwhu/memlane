package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

func TestHistoryDialogPreviewsRestoreWithoutWritingAndReturnsSelectedVersion(t *testing.T) {
	for _, width := range []int{1200, 800} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := New("test", nil)
			a.visualFixture = true
			a.page, a.scope = "项目", "project"
			a.selectedItem = &MemoryItem{ID: "source", Dataset: "project", Origin: "knowledge", Revision: 2, State: "deleted", Title: "已删除知识", Text: "保留当前正文"}
			a.selectedID = "source"
			a.history = []MemoryHistory{{Revision: 2, Text: "保留当前正文", Title: "当前"}, {Revision: 1, Text: "原始正文", Title: "原始"}}
			writes := 0
			a.onRestore = func(revision int64) {
				writes++
				if revision != 1 {
					t.Error("wrong restore revision")
				}
			}
			a.openKnowledgeHistory()
			tt := ui.NewTester(a.View, width, 720)
			if err := tt.Click("v1"); err != nil {
				t.Fatal(err)
			}
			button, ok := tt.Find("预览恢复此版本")
			if !ok || button.Y+button.H > 720 || button.X+button.W > float32(width) {
				t.Fatal("history restore preview outside window", button)
			}
			writeScreenshot(t, tt, fmt.Sprintf("h07-history-dialog-%d.png", width))
			if err := tt.Click("预览恢复此版本"); err != nil {
				t.Fatal(err)
			}
			writeScreenshot(t, tt, fmt.Sprintf("h07-restore-preview-%d.png", width))
			if a.knowledgeHistory.Open || a.detailMode != "restore" || a.restoreExpected != 2 || a.historyRevision != 1 || writes != 0 || a.selectedItem.Revision != 2 || a.selectedItem.Text != "保留当前正文" {
				t.Fatal("preview wrote or rebound current knowledge")
			}
			if err := tt.Click("返回历史"); err != nil {
				t.Fatal(err)
			}
			if !a.knowledgeHistory.Open || a.knowledgeHistory.Selected != 1 || a.detailMode != "view" {
				t.Fatal("return lost selected history or reopened an inline replacement")
			}
			if err := tt.Click("预览恢复此版本"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("确认恢复"); err != nil {
				t.Fatal(err)
			}
			if writes != 1 {
				t.Fatal("explicit restore confirmation did not call existing restore handler")
			}
		})
	}
}

func TestReviewOpensDeletedKnowledgeHistoryWithoutLeavingReview(t *testing.T) {
	a, task := materialUIFixture()
	a.scopes = []records.Scope{{ID: "project", Kind: "project", Name: "项目"}}
	a.organization.ReviewItem = "delete"
	a.history = []MemoryHistory{{Revision: 2, Text: "删除时保留正文"}, {Revision: 1, Text: "待恢复的正文"}}
	owner := MemoryItem{ID: "one", Dataset: "project", Origin: "knowledge", Revision: 2, State: "deleted", Title: "已删除知识", Text: "删除时保留正文"}
	a.openKnowledgeHistoryFor(owner)
	if a.page != "整理记录" || a.organization.Task.ID != task.ID || a.organization.ReviewItem != "delete" {
		t.Fatal("reading history changed review context")
	}
	a.knowledgeHistory.Selected = 1
	a.previewKnowledgeHistoryRestore()
	if a.page != "项目" || a.scope != "project" || a.selectedID != owner.ID || a.detailMode != "restore" || a.restoreExpected != 2 || a.organization.Task.ID != task.ID || a.organization.ReviewItem != "delete" {
		t.Fatal("restore preview lost exact source or review return context")
	}
}

func TestHistoryRestorePreviewRejectsRetiredLibraryAndKnownNewVersion(t *testing.T) {
	for _, why := range []string{"epoch", "version"} {
		t.Run(why, func(t *testing.T) {
			a := New("test", nil)
			a.page, a.scope = "项目", "project"
			a.selectedItem = &MemoryItem{ID: "source", Dataset: "project", Origin: "knowledge", Revision: 2, State: "active"}
			a.selectedID = "source"
			a.history = []MemoryHistory{{Revision: 2, Text: "当前"}, {Revision: 1, Text: "旧"}}
			a.openKnowledgeHistory()
			a.knowledgeHistory.Selected = 1
			if why == "epoch" {
				a.libraryEpoch++
			} else {
				a.selectedItem.Revision = 3
			}
			a.previewKnowledgeHistoryRestore()
			if a.detailMode == "restore" || a.restoreExpected != 0 {
				t.Fatal("stale modal became a restore mutation")
			}
		})
	}
}
