package desktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

func TestCommonSourcePermissionPageHidesStaleBodyAndKeepsReadingContext(t *testing.T) {
	for _, width := range []int{1200, 800} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := New("test", nil)
			a.visualFixture = true
			a.page, a.scope, a.commonCategory = "通用知识", "", "experience"
			a.scopes = []records.Scope{{ID: "experience", Kind: "experience", Name: "通用经验"}, {ID: "source-project", Kind: "project", Name: "原项目"}}
			a.selectedItem = &MemoryItem{ID: "common", Dataset: "experience", State: "active", Origin: "knowledge", Revision: 1, Title: "可独立参考的通用经验", Text: strings.Repeat("当前通用知识正文\n", 60), Experience: &records.ExperienceInfo{Kind: "copy"}}
			a.selectedID = "common"
			a.items = []MemoryItem{*a.selectedItem}
			a.query = "preserve query"
			a.detailScroll.Y = 45
			a.reader = SourceReaderUI{Open: true, Query: records.SourceRead{Project: "experience", Owner: "common", OwnerRevision: 1, Memory: "source", Revision: 2}, ErrorCode: "scope_denied", Window: newTextWindow("FORBIDDEN_CACHED_SOURCE_BODY"), Result: records.SourceRecord{State: "available"}}
			tt := ui.NewTester(a.View, width, 720)
			if !tt.HasText("没有这个项目的访问权限") || tt.HasText("FORBIDDEN_CACHED_SOURCE_BODY") || tt.HasText("复制全文") || tt.HasText("重新读取") {
				t.Fatal("permission page exposed source or offered an invalid action")
			}
			footer, ok := tt.Find("当前知识与选中结果保持")
			if !ok || footer.Y+footer.H > 720 || footer.Y < 600 {
				t.Fatal("permission return footer not fixed", footer)
			}
			writeScreenshot(t, tt, "l05-permission-"+fmt.Sprint(width)+".png")
			if err := tt.Click("返回知识"); err != nil {
				t.Fatal(err)
			}
			if a.reader.Open || a.selectedID != "common" || a.page != "通用知识" || a.commonCategory != "experience" || a.query != "preserve query" || a.detailScroll.Y != 45 {
				t.Fatalf("return changed common reading context: open=%v selected=%s page=%s category=%s query=%s scroll=%v", a.reader.Open, a.selectedID, a.page, a.commonCategory, a.query, a.detailScroll.Y)
			}
		})
	}
}
