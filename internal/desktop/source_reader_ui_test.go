package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"testing"
)

// The normal detail/history/editor must not bypass the reader's current
// authorization and source-version checks for official original records.
func TestOfficialOriginalRequiresVerifiedReaderInStandardPanes(t *testing.T) {
	for _, detail := range []*records.AgentSource{{Official: &records.CogneeOriginal{}}, {Native: &records.CodexOriginal{}}} {
		for _, mode := range []string{"view", "history", "restore"} {
			a := New("test", nil)
			item := MemoryItem{ID: "synthetic-original", Origin: "raw", Title: "Original", Text: "unverified original body", Revision: 1, SourceDetail: detail}
			a.selectedID, a.selectedItem, a.detailMode = item.ID, &item, mode
			a.history = []MemoryHistory{{Revision: 1, Text: item.Text}}
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Children(func() { a.memoryRow(c, item, true); a.memoryDetail(c) })
			}, 1200, 820)
			if tt.HasText(item.Text) {
				t.Fatal("unverified original escaped reader checks")
			}
			a.editMemory()
			if a.editing {
				t.Fatal("original body opened in unverified editor")
			}
		}
	}
}
