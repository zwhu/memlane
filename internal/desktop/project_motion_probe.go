//go:build e2e

package desktop

import (
	"errors"
	"github.com/zwhu/memlane/internal/records"
	"os"
	"sync"
	"time"
)

func projectDrawStart() time.Time { return time.Now() }

// An explicit automation-only visual fixture exercises the production scene.
// It has no business client, no write path and no model authorization. Every
// window names the fixture; it must never count as a real task receipt.
func (a *App) UseProjectMotionFixture(stage string, reduced bool) error {
	if os.Getenv("MEMLANE_NATIVE_PROBE") == "" {
		return errors.New("explicit motion probe required")
	}
	switch stage {
	case "ready", "starting", "organizing", "saving", "indexing", "completed", "failed", "cancelled", "cancelling":
	default:
		return errors.New("unsupported visual fixture stage")
	}
	if a.projectOrganizer.Project != "motion-fixture-project" {
		a.closeProjectOrganizer()
		a.closeSourceReader()
		a.closeKnowledgeHistory()
		a.libraryContext = nil
		a.page, a.scope = "项目", "motion-fixture-project"
		a.projects = []ProjectItem{{ID: a.scope, Name: "C动效视觉夹具"}}
		a.items, a.selectedItem, a.selectedID = nil, nil, ""
		a.browsing = BrowsingUI{View: "memories"}
		a.loading, a.libraryError = false, ""
		zero := 0
		a.readingKnowledgeTotal, a.readingRawTotal = &zero, &zero
		a.readingCountsRoute = "项目:" + a.scope
		a.projectOrganizer = ProjectOrganizerUI{Open: true, Loaded: true, Project: a.scope, Binding: records.ProjectBinding{Project: a.scope, Agent: "codex", State: "fixture"}}
	}
	a.firstLaunch.State.ReducedMotion = reduced
	a.projectOrganizer.Task = nil
	if stage != "ready" {
		task := records.ProjectTask{ID: "motion-fixture-task", Attempt: 1, State: stage, AccessState: "fixture", Snapshot: records.ProjectTaskSnapshot{Project: a.scope, Executor: records.ProjectTaskExecutor{Agent: "codex", Model: "合成视觉夹具，不运行模型"}}}
		if stage == "indexing" || stage == "completed" {
			task.Receipt = &records.ProjectTaskReceipt{Persisted: true, Added: []records.ProjectTaskSaved{{Memory: "fixture-knowledge", Revision: 1}}}
			if stage == "completed" {
				task.Receipt.Indexed = 1
			}
		}
		a.projectOrganizer.Task = &task
	}
	return nil
}

type ProjectMotionFrame struct {
	Process      int     `json:"process"`
	Frame        uint64  `json:"frame"`
	Stage        string  `json:"stage"`
	TimeMS       int64   `json:"time_ms"`
	DeltaMS      float64 `json:"delta_ms"`
	CPUMS        float64 `json:"cpu_ms"`
	Requested    bool    `json:"requested"`
	Reduced      bool    `json:"reduced"`
	TiltX, TiltY float32
	Bounds       [4]float32 `json:"bounds"`
}

type ProjectMotionDiagnostics struct {
	WindowHidden    bool                 `json:"window_hidden"`
	TaskWatchActive bool                 `json:"task_watch_active"`
	Frames          uint64               `json:"frames"`
	Requests        uint64               `json:"requests"`
	Samples         []ProjectMotionFrame `json:"samples"`
	last            time.Time
}

var projectMotionProbes sync.Map

// Only opt-in automation bundles collect these bounded, CPU-side draw samples.
// They measure callback duration and spacing, not GPU execution or whole frames.
func (a *App) recordProjectDraw(stage string, frame, started time.Time, requested, reduced bool, x, y float32, bounds [4]float32) {
	if os.Getenv("MEMLANE_NATIVE_PROBE") == "" {
		return
	}
	v, _ := projectMotionProbes.LoadOrStore(a, &ProjectMotionDiagnostics{})
	d := v.(*ProjectMotionDiagnostics)
	d.Frames++
	if requested {
		d.Requests++
	}
	delta := float64(0)
	if !d.last.IsZero() {
		delta = frame.Sub(d.last).Seconds() * 1000
	}
	d.last = frame
	d.Samples = append(d.Samples, ProjectMotionFrame{Process: os.Getpid(), Frame: d.Frames, Stage: stage, TimeMS: frame.UnixMilli(), DeltaMS: delta, CPUMS: time.Since(started).Seconds() * 1000, Requested: requested, Reduced: reduced, TiltX: x, TiltY: y, Bounds: bounds})
	if len(d.Samples) > 512 {
		d.Samples = d.Samples[len(d.Samples)-512:]
	}
}

// Called on the UI thread, without drawing or requesting another frame.
func (a *App) ProjectMotionDiagnostics() ProjectMotionDiagnostics {
	if v, ok := projectMotionProbes.Load(a); ok {
		d := *v.(*ProjectMotionDiagnostics)
		d.WindowHidden, d.TaskWatchActive = a.windowHidden, a.projectOrganizer.WatchCancel != nil
		d.Samples = append([]ProjectMotionFrame(nil), d.Samples...)
		return d
	}
	return ProjectMotionDiagnostics{WindowHidden: a.windowHidden, TaskWatchActive: a.projectOrganizer.WatchCancel != nil}
}
