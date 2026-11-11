package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"strings"
	"testing"
)

func compactOrganizationFixture() *App {
	a := New("test", nil)
	a.visualFixture = true
	a.serviceState = "ready"
	a.runtimeStatus.Phase = "ready"
	a.page = "整理记录"
	a.organizationMode = true
	a.scopes = []records.Scope{{ID: "project", Kind: "project", Name: "第九包连续验收项目", Path: "/private/var/" + strings.Repeat("synthetic-root/", 12)}, {ID: "second", Kind: "project", Name: "另一项目", Path: "/private/var/" + strings.Repeat("synthetic-workspace/", 12)}, {ID: "third", Kind: "project", Name: "第三项目", Path: "/private/var/" + strings.Repeat("synthetic-workspace/", 12)}}
	a.organization = OrganizationUI{New: true, Project: "project", Kind: "knowledge", ReviewMode: true, Chosen: map[string]MemoryItem{"manual": {ID: "manual", Title: "手动保存的合成知识", Revision: 5}, "duplicate": {ID: "duplicate", Title: "新增重复记录", Revision: 1}}, Order: []string{"manual", "duplicate"}, Candidates: []MemoryItem{{ID: "manual", Title: "手动保存的合成知识", Revision: 5, Text: "合成材料正文"}, {ID: "duplicate", Title: "新增重复记录", Revision: 1, Text: "合成重复内容"}}, ProcessingLoaded: true, Processing: records.ProcessingConfiguration{Runtime: map[string]records.ProcessingReadiness{"current": {State: "ready"}}, Current: records.ProcessingSelection{Active: true, Revision: &records.ProcessingRevision{ID: "current", Spec: records.ProcessingSpec{Mode: "agent", Agent: "pi", Model: "deepseek-flash"}, Validation: &records.ProcessingValidation{State: "succeeded"}}}}}
	return a
}
func TestOrganizationChooserSharedCompactStates(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {800, 720}} {
		for _, state := range []string{"normal", "read-failed", "disabled", "frozen"} {
			t.Run(fmt.Sprintf("%d/%s", size[0], state), func(t *testing.T) {
				a := compactOrganizationFixture()
				switch state {
				case "read-failed":
					a.organization.ProcessingLoaded = false
					a.organization.ProcessingError = "记忆处理方式读取未完成，保留本次材料；请重试。"
				case "disabled":
					a.organization.Busy = true
				case "frozen":
					a.organization.Start = &records.OrganizationStart{OperationID: "original-op", Project: "project", Processing: &records.ProcessingTaskAuthorization{Revision: "current", Generation: 10}, Inputs: []records.MergeTarget{{Memory: "manual", Revision: 5}, {Memory: "duplicate", Revision: 1}}}
					a.organization.Error = "原启动请求尚未取得回执；材料保留。"
				}
				tt := ui.NewTester(a.View, size[0], size[1])
				writeScreenshot(t, tt, fmt.Sprintf("chooser-%d-%s.png", size[0], state))
				for _, scope := range a.scopes {
					if tt.HasText(scope.Path) {
						t.Fatal("full temporary path occupies default body")
					}
				}
				if tt.HasText("发现通用经验候选，预览确认后保存") || tt.HasText("可建议移入的项目（可不选；授权不读取其他项目知识）") {
					t.Fatal("advanced policy displaced primary scope/materials")
				}
				label := "本次材料"
				if state == "frozen" {
					label = "本次固定材料 · 2 条"
					if tt.HasText("读取当前处理方式") {
						t.Fatal("frozen request rendered editable/disabled configuration form")
					}
					if !tt.HasText("核对原请求回执") || !tt.HasText("放弃原请求，保留材料") {
						t.Fatal("original recovery actions lost")
					}
				}
				box, ok := tt.Find(label)
				if !ok || box.Y > float32(size[1]-180) {
					t.Fatalf("materials pushed below first screen: %s %+v", label, box)
				}
				if len(a.organization.Chosen) != 2 || a.organization.Order[0] != "manual" {
					t.Fatal("layout mutated material draft")
				}
				if state == "normal" {
					tt.Scroll(float32(size[0]-80), float32(size[1]-150), 0, 240)
					if e := tt.Click("更多整理选项"); e != nil {
						t.Fatal(e)
					}
					if !tt.HasText("同时提出修改建议，逐项确认后生效") {
						t.Fatal("advanced policy no longer reachable")
					}
				}
			})
		}
	}
}
