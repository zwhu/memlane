package desktop

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

// Native window screenshots remain a separate acceptance gate. These frames
// exercise the actual MyGo page at ordinary/narrow sizes with synthetic facts.
func TestReviewStateFramesKeepActionsReachable(t *testing.T) {
	for _, scenario := range []string{"delete", "move", "permission", "changed"} {
		for _, width := range []int{1200, 800} {
			t.Run(fmt.Sprintf("%s-%d", scenario, width), func(t *testing.T) {
				a, task := materialUIFixture()
				e := task.Entries[1]
				e.Item.Target, e.Item.Destination = "one", "destination"
				if scenario == "delete" || scenario == "move" {
					e.Item.Action = scenario
					e.Item.Sources = append([]records.MergeSource{}, e.Item.Sources[:1]...)
				}
				if scenario == "delete" {
					e.Item.Title = "删除重复的锁文件提醒"
				}
				if scenario == "move" {
					e.Item.Title = "调整知识的项目归属"
				}
				a.scopes = []records.Scope{{ID: "project", Kind: "project", Name: "原项目"}, {ID: "destination", Kind: "project", Name: "目标项目"}}
				task.Entries = []records.OrganizationEntry{e}
				task.Snapshot.Destinations = []records.Scope{{ID: "destination", Kind: "project"}}
				a.organization.Task = &task
				a.organization.Tasks = []records.OrganizationSummary{{ID: task.ID, Project: task.Project, State: "completed", Title: "项目知识整理", Origin: "manual", Processing: &records.ProcessingSpec{Mode: "agent", Agent: "pi", Model: "synthetic-model"}}}
				a.organization.TasksLoaded, a.organization.TasksTotal = true, 1
				a.organization.ReviewItem = e.ID
				if scenario == "permission" {
					a.organization.SourceErrors = map[string]string{"one": "unreadable"}
					a.organization.SourceErrorCodes = map[string]string{"one": "scope_denied"}
				} else if scenario == "changed" {
					v := a.organization.Sources["one"]
					v.Revision = 2
					a.organization.Sources["one"] = v
				} else if scenario == "delete" {
					q := deletionReviewQuery(task, e)
					r := records.DeletionImpact{Memory: q.Memory, Dataset: q.Dataset, Revision: q.ExpectedRevision, CoverageDatasets: []string{"project"}, DirectMemoryCount: 1, ReferenceCount: 1, References: []records.DeletionReference{{Memory: "summary", Revision: 1, Dataset: "project", Title: "依赖摘要", SourceRevision: 1}}}
					a.organization.DeletionImpact = &OrganizationDeletionImpactUI{Task: task.ID, Item: e.ID, Digest: records.MergeDigest(e.Item), Root: a.library.Root, Epoch: a.libraryEpoch, OrganizationEpoch: a.organization.Epoch, Query: q, Result: &r}
				}
				tt := ui.NewTester(a.View, width, 800)
				bounds, ok := tt.Find("确认这项改动")
				if !ok || bounds.Y < 54 || bounds.Y+bounds.H > 800 {
					t.Fatal("review action outside window", bounds)
				}
				if scenario == "move" && !tt.HasText("明确授权来源与目标项目的范围变化") {
					t.Fatal("separate scope control missing")
				}
				if path := os.Getenv("MEMLANE_REVIEW_FRAMES"); path != "" {
					if err := os.MkdirAll(path, 0700); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(path, fmt.Sprintf("%s-%d.png", scenario, width)))
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
