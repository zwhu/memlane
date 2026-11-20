package simpledesktop

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestSlowSidebarProjectDoesNotBlockOtherProjectBadges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "processing-status") {
			w.Write([]byte(`{"items":[]}`))
		} else {
			w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	a := settingsFixture()
	a.Client.Config.ServiceURL = server.URL
	a.access.Directory.Bindings = nil
	a.access.Directory.Projects = append(a.access.Directory.Projects,
		AccessProject{ID: "third", Dataset: "third"}, AccessProject{ID: "fourth", Dataset: "fourth"})
	a.access.Queues = nil
	updates := make(chan func(), 10)
	a.Update = func(fn func()) { updates <- fn }
	slow := a.projectQueueStateFor(a.access.Directory.Projects[0])
	slow.Loading = true
	fast := a.projectQueueStateFor(a.access.Directory.Projects[1])
	a.refreshSidebarQueueIfDue(time.Now())
	if !fast.Loading {
		t.Fatal("a slow project blocked the entire sidebar queue")
	}
	active := 0
	for _, q := range a.access.Queues {
		if q.Loading {
			active++
		}
	}
	if active != 3 || a.projectQueueStateFor(a.access.Directory.Projects[3]).Loading {
		t.Fatal("sidebar project reads exceeded the concurrency limit")
	}
	drainReading(t, updates, func() bool { return !fast.Loading })
	if !fast.Loaded || !slow.Loading {
		t.Fatal("independent project could not finish while the slow project remained pending")
	}
}

func TestSidebarPendingBadgeOpensProjectAndClearsAfterProcessing(t *testing.T) {
	a := settingsFixture()
	p := a.access.Directory.Projects[0]
	q := a.projectQueueStateFor(p)
	q.Documents = []Item{{ID: "unprocessed"}}
	q.Sessions = []queuedSession{{Agent: AccessAgent{ID: "a"}, Binding: ProjectBinding{HostSessionKey: "s"}, View: &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 2}}}}
	if label, _ := a.sidebarQueueBadge(a.Config.Projects[0]); label != "" {
		t.Fatal("normal automatic work should not notify", label)
	}
	now := time.Now()
	q.Sessions[0].View.Loaded = false
	q.observeNotificationFailures(now.Add(-3 * time.Minute))
	q.observeNotificationFailures(now.Add(-2 * time.Minute))
	if label, _ := a.sidebarQueueBadge(a.Config.Projects[0]); label != "" {
		t.Fatal("only two failed checks notified")
	}
	q.observeNotificationFailures(now)
	a.page = "总览"
	tt := ui.NewTester(a.View, 1200, 820)
	if label, _ := a.sidebarQueueBadge(a.Config.Projects[0]); label != "!" {
		t.Fatal("persistent failure was hidden")
	}
	clickProjectUI(t, tt, "待处理文档：memlane")
	if a.access.OpenProject != p.ID || a.page != "知识" || a.kind != "管理" {
		t.Fatal("persistent notification did not open project management tab")
	}
	q.Sessions[0].View.Loaded = true
	q.observeNotificationFailures(time.Now())
	tt.Frame()
	if label, _ := a.sidebarQueueBadge(a.Config.Projects[0]); label != "" {
		t.Fatal("successful recovery retained error notification")
	}
}

func TestSidebarUnknownBadgeAppearsOnFirstReadAfterReload(t *testing.T) {
	a := settingsFixture()
	p := a.access.Directory.Projects[0]
	for reload := 0; reload < 2; reload++ {
		// Reconstruct the UI cache, as on reload. Source status, not the old counter,
		// must restore the notification on the first successful status response.
		a.access.Queues = nil
		q := a.projectQueueStateFor(p)
		q.Loaded = true
		q.Sessions = []queuedSession{{View: &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 1, Unknown: 1}}}}
		if label, _ := a.sidebarQueueBadge(a.Config.Projects[0]); label != "!" {
			t.Fatal("confirmed failure waits again after reload", reload, label)
		}
		q.Sessions[0].View.observe(PendingMaterials{Signature: "resolved"})
		if label, _ := a.sidebarQueueBadge(a.Config.Projects[0]); label != "" {
			t.Fatal("resolved failure left a stale dot", label)
		}
	}
}
