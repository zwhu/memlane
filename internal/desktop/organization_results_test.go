package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func materialUIFixture() (*App, records.OrganizationTask) {
	a := New("test", nil)
	a.visualFixture = true
	a.page = "整理记录"
	inputs := []records.OrganizationInput{{MergeInput: records.MergeInput{Memory: "one", Revision: 1, Title: "依赖工具", Text: "使用 pnpm"}}, {MergeInput: records.MergeInput{Memory: "two", Revision: 1, Title: "提交锁文件", Text: "提交 pnpm-lock.yaml"}}}
	refs := []records.MergeSource{{Memory: "one", Revision: 1, Quote: "使用 pnpm"}, {Memory: "two", Revision: 1, Quote: "提交 pnpm-lock.yaml"}}
	task := records.OrganizationTask{ID: "task", Project: "project", State: "completed", Snapshot: records.OrganizationSnapshot{Schema: records.OrganizationReviewSchema, Inputs: inputs}, Result: &records.OrganizationResult{}, Entries: []records.OrganizationEntry{
		{ID: "auto", State: "applied", Item: records.OrganizationItem{Action: "summary", Title: "依赖摘要", Text: "依赖约定"}, Receipt: &records.OrganizationReceipt{Memory: "summary", Revision: 1}},
		{ID: "merge", State: "review_pending", Item: records.OrganizationItem{Action: "merge", Title: "合并依赖约定", Text: "使用 pnpm，并提交锁文件", Sources: refs}},
		{ID: "delete", State: "expired", Item: records.OrganizationItem{Action: "delete", Target: "one", Sources: refs[:1]}},
	}}
	a.organization.Task = &task
	a.organization.Selected = task.ID
	a.organization.Sources = map[string]MemoryItem{"one": {ID: "one", Dataset: "project", State: "active", Revision: 1}, "two": {ID: "two", Dataset: "project", State: "active", Revision: 1}}
	return a, task
}
func TestMaterialResultsGroupDecisionsAndPreserveSinglePreview(t *testing.T) {
	a, task := materialUIFixture()
	tt := ui.NewTester(a.View, 1280, 900)
	if !tt.HasText("自动完成") || !tt.HasText("需要你决定") || !tt.HasText("未完成与过期") {
		t.Fatal("result groups missing")
	}
	a.organization.Scroll.Y = 34
	a.openOrganizationEntry("merge")
	tt.Frame()
	if !tt.HasText("整理时的来源与范围") || !tt.HasText("建议结果") || !tt.HasText("具体影响") || !tt.HasText("使用 pnpm") || !tt.HasText("提交 pnpm-lock.yaml") {
		t.Fatal("frozen sources or impact missing")
	}
	if a.organization.Task.ID != task.ID || a.organization.ReviewItem != "merge" {
		t.Fatal("preview changed task")
	}
	a.organization.Scroll.Y = 80
	a.showOrganizationResult()
	if a.organization.Scroll.Y != 34 || a.organization.ReviewScroll["merge"].Y != 80 {
		t.Fatal("return lost reading position")
	}
	a.openOrganizationEntry("merge")
	if a.organization.Scroll.Y != 80 {
		t.Fatal("reentry lost preview position")
	}
}
func TestMaterialReviewSourceChangesBlockNewConfirmation(t *testing.T) {
	a, task := materialUIFixture()
	entry := task.Entries[1]
	if !a.organizationSourcesCurrent(task, entry) {
		t.Fatal("unchanged source rejected")
	}
	v := a.organization.Sources["one"]
	v.Revision = 2
	a.organization.Sources["one"] = v
	if a.organizationSourcesCurrent(task, entry) {
		t.Fatal("changed source accepted")
	}
	a.openOrganizationEntry("merge")
	tt := ui.NewTester(a.View, 980, 620)
	if !tt.HasText("来源版本或范围已变化") || !tt.HasText("重新整理相关材料") {
		t.Fatal("stale preview feedback absent")
	}
}
func TestMaterialCancelStatusRequiresProcessCleanupReceipt(t *testing.T) {
	task := records.OrganizationTask{State: "cancelled", Cancelled: true}
	if materialTaskStatus(task) == "已取消，已提交内容保留" {
		t.Fatal("cancel ACK described as process cleanup")
	}
	task.Execution = []byte(`{"process_group_gone":true,"process_reaped":true}`)
	if materialTaskStatus(task) != "已取消，已提交内容保留" {
		t.Fatal("cleanup receipt not adopted")
	}
}

// Protocol/render fixtures are distinct from the packaged real-model run.
func TestMaterialReviewMatrixRendersAllActionsAtBothWidths(t *testing.T) {
	for _, action := range []string{"merge", "overwrite", "delete", "move", "duplicate", "conflict", "global_experience"} {
		for _, width := range []int{1280, 980} {
			t.Run(action+"-"+fmt.Sprint(width), func(t *testing.T) {
				a, task := materialUIFixture()
				e := task.Entries[1]
				e.Item.Action = action
				e.Item.Title = "待审核的项目约定"
				e.Item.Target = "one"
				e.Item.Destination = "destination"
				e.Item.Conditions = "适用于具备对应项目约定的情形，项目明确要求优先。"
				if action == "merge" || action == "duplicate" || action == "conflict" || action == "global_experience" {
					e.Item.Target = ""
				}
				task.Entries = []records.OrganizationEntry{e}
				a.organization.Task = &task
				a.scopes = []records.Scope{{ID: "project", Kind: "project", Name: "原项目"}, {ID: "destination", Kind: "project", Name: "目标项目"}}
				task.Snapshot.Destinations = []records.Scope{{ID: "destination", Kind: "project"}}
				a.openOrganizationEntry(e.ID)
				height := 900
				if width == 980 {
					height = 620
				}
				tt := ui.NewTester(a.View, width, height)
				if !tt.HasText("整理时的来源与范围") || !tt.HasText("建议结果") || !tt.HasText("具体影响") {
					t.Fatal("review evidence or impact missing")
				}
				label := "确认这项改动"
				if action == "duplicate" || action == "conflict" {
					label = "记录已核对此项"
				}
				bounds, ok := tt.Find(label)
				if !ok || bounds.Y < 54 || bounds.Y+bounds.H > float32(height) {
					t.Fatal("primary action outside window", bounds)
				}
				if out := os.Getenv("MEMLANE_ORGANIZATION_FRAMES"); out != "" {
					if err := os.MkdirAll(out, 0700); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(out, action+"-"+fmt.Sprint(width)+".png"))
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(f, tt.Image())
					f.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}
