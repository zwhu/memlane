package desktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestGraphTypesLongMetadataStaysInsideList(t *testing.T) {
	for _, width := range []int{1280, 800} {
		a := graphPresentationApp()
		types := []string{}
		for i := 0; i < 32; i++ {
			types = append(types, fmt.Sprintf("类型%02d-", i)+strings.Repeat("L", 1015))
		}
		a.browsing.Graph.Entities[0].Types = types
		tt := ui.NewTester(a.View, width, 720)
		count, ok := tt.Find("1 条知识")
		right := float32(528)
		if width == 800 {
			right = 428
		}
		writeScreenshot(t, tt, fmt.Sprintf("graph-types-%d.png", width))
		if !ok || count.X < 212 || count.X+count.W > right || count.Y+count.H > 720 {
			t.Fatalf("long types pushed related count outside list: %+v", count)
		}
	}
}

func TestGraphTypesFullReadAndProjectReturnPreserveActualFilter(t *testing.T) {
	for _, width := range []int{1280, 800} {
		a := graphPresentationApp()
		types := []string{}
		for i := 0; i < 32; i++ {
			types = append(types, fmt.Sprintf("类型%02d-", i)+strings.Repeat("L", 1015))
		}
		a.browsing.Graph.Entities[0].Types = types
		a.browsing.Graph.Entities[0].Description = strings.Repeat("用于核对请求日志的项目对象。\n", 50)
		entity := a.browsing.Graph.Entities[0]
		graph := a.browsing.Graph
		a.browsing.TypeFilter = types[17]
		a.browsing.EntityBody.Y = 35
		a.saveBrowsing()
		tt := ui.NewTester(a.View, width, 720)
		if err := tt.Click("查看全部类型（32）"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		want := strings.Join(types, "\n\n")
		key := "entity-types-" + a.scope + "-" + entity.ID
		w := a.textWindows[key]
		if w == nil || w.Body != want || !a.browsing.EntityTypes.Open || a.browsing.TypeFilter != types[17] {
			t.Fatal("type viewer changed actual values/filter")
		}
		if err := tt.Click("复制全文"); err != nil {
			t.Fatal(err)
		}
		if tt.Clipboard() != want {
			t.Fatal("full labels omitted from copy")
		}
		for i := 0; i < len(w.Offsets)-1; i++ {
			w.Page = i
			tt.Frame()
			if !tt.HasText(want[w.Offsets[i]:w.Offsets[i+1]]) {
				t.Fatal("type page truncated")
			}
		}
		writeScreenshot(t, tt, fmt.Sprintf("graph-types-full-last-%d.png", width))
		if err := tt.Click("返回实体详情"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if a.browsing.EntityTypes.Open || a.browsing.EntityBody.Y != 35 || a.browsing.Entity != entity.ID || a.browsing.TypeFilter != types[17] || !tt.Focused("查看全部类型（32）") {
			t.Fatalf("read-only return lost entity/filter/scroll/focus: open=%v y=%v entity=%s filtermatch=%v focus=%v", a.browsing.EntityTypes.Open, a.browsing.EntityBody.Y, a.browsing.Entity, a.browsing.TypeFilter == types[17], tt.Focused("查看全部类型（32）"))
		}
		a.browseMemory("knowledge")
		a.returnToBrowsingExplorer()
		if a.browsing.TypeFilter != types[17] || a.browsing.Entity != entity.ID {
			t.Fatal("knowledge return dropped type selection")
		}
		a.saveBrowsing()
		a.scope = "beta"
		a.browseProjectChanged()
		if a.browsing.TypeFilter != "" || len(a.entityTypeOptions()) != 1 {
			t.Fatal("project switch leaked old types")
		}
		a.scope = "alpha"
		a.browseProjectChanged()
		a.browsing.Graph = graph
		if a.browsing.TypeFilter != types[17] {
			t.Fatal("return project lost selected actual type")
		}
		// The read disclosure is bound to the active library epoch, not only labels.
		prior := a.entityTypesView(entity)
		prior.Open = true
		a.libraryEpoch++
		if a.entityTypesView(entity) == prior || a.browsing.EntityTypes.Open {
			t.Fatal("type disclosure survived library epoch as an active owner")
		}
	}
}
