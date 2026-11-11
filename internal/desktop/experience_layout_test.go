package desktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestExperienceCopyReadOnlyRequestAndBoundedOriginal(t *testing.T) {
	for _, width := range []int{1200, 800} {
		a, _ := experienceUIFixture(t, func(recordsRequest serviceipc.Request) serviceipc.Response { return serviceipc.Response{OK: false} })
		a.beginExperienceCopy(*a.current())
		a.experience.Submitted = true
		a.experience.Conditions = "冻结条件，不可改写原请求"
		a.experience.Source.Text = strings.Repeat("保留完整原文，不做摘要。\n", 120)
		tt := ui.NewTester(func(c *ui.Context) { a.experienceCopyPreview(c) }, width, 820)
		if tt.HasText("说明哪些项目或情形适用；项目明确约定优先") {
			t.Fatal("submitted request rendered editable conditions")
		}
		if !tt.HasText(a.experience.Source.Text) {
			t.Fatal("full source bytes absent")
		}
		if width == 1200 {
			bounds, ok := tt.Find("适用条件")
			if !ok || bounds.Y > 600 {
				t.Fatalf("long original displaced conditions: %v", bounds)
			}
		}
		if a.experience.Conditions != "冻结条件，不可改写原请求" {
			t.Fatal("request changed")
		}
		writeScreenshot(t, tt, fmt.Sprintf("copy-frozen-%d.png", width))
	}
}
