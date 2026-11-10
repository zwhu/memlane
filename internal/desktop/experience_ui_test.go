package desktop

// These GUI/IPC fault fixtures verify receipt recovery and navigation races.
// They do not replace real App/Agent/Rust acceptance.
import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func experienceUIFixture(t *testing.T, handle func(serviceipc.Request) serviceipc.Response) (*App, chan func()) {
	t.Helper()
	root := t.TempDir()
	path := serviceipc.SocketPath(root)
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close(); os.RemoveAll(filepath.Dir(path)) })
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			var request serviceipc.Request
			if json.NewDecoder(conn).Decode(&request) == nil {
				response := handle(request)
				response.Protocol, response.ID = 1, request.ID
				_ = json.NewEncoder(conn).Encode(response)
			}
			conn.Close()
		}
	}()
	a := New("test", nil)
	a.libraryContext = context.Background()
	a.library = serviceipc.Library{Root: root}
	a.organization.Client = serviceipc.OrganizationClient{Root: root}
	a.visualFixture = true
	dispatched := make(chan func(), 4)
	a.dispatch = func(fn func()) { dispatched <- fn }
	project := uuid.NewString()
	a.scopes = []records.Scope{{ID: project, Kind: "project", Name: "合成项目"}, {ID: "global", Kind: "experience", Name: "全局经验"}}
	a.page, a.scope, a.detailMode = "项目", project, "view"
	v := MemoryItem{ID: uuid.NewString(), Dataset: project, Text: "合成项目约定", Title: "合成", State: "active", Revision: 1}
	a.selectedID, a.selectedItem = v.ID, &v
	return a, dispatched
}

func awaitExperienceDispatch(t *testing.T, dispatched chan func()) {
	t.Helper()
	select {
	case fn := <-dispatched:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("IPC callback absent")
	}
}

func TestExperienceUIReadFailureRetriesSavedReceiptAfterSourceChanged(t *testing.T) {
	copies, reads := 0, 0
	receipt := records.OrganizationReceipt{ID: "saved-copy", Memory: "global-copy", Revision: 1}
	a, dispatched := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "experience_copy" {
			copies++
			return serviceipc.Response{OK: true, Result: receipt}
		}
		reads++
		if reads == 1 {
			return serviceipc.Response{OK: false, Code: "service_error", Error: "read fault fixture after committed copy"}
		}
		return serviceipc.Response{OK: true, Result: records.Version{Memory: receipt.Memory, Dataset: "global", Revision: 1}}
	})
	a.beginExperienceCopy(*a.current())
	a.experience.Conditions = "适用于已确认相同约定的项目。"
	a.confirmExperienceCopy()
	awaitExperienceDispatch(t, dispatched)
	if a.experience.Receipt == nil || a.experience.Copy == nil || a.experience.Error == "" {
		t.Fatal("committed receipt lost")
	}
	a.experience.CurrentRevision = 2
	tt := ui.NewTester(a.View, 1200, 900)
	if e := tt.Click("重试复制回执"); e != nil {
		t.Fatal("source edit disabled committed receipt recovery", e)
	}
	awaitExperienceDispatch(t, dispatched)
	if copies != 1 || reads != 2 || a.selectedID != receipt.Memory || a.experience.Copy != nil {
		t.Fatal("copy repeated or result not recovered", copies, reads, a.selectedID)
	}
}

func TestExperienceUICopyReplyPreservesNewEditRoute(t *testing.T) {
	release := make(chan struct{})
	receipt := records.OrganizationReceipt{ID: "saved-copy", Memory: "global-copy", Revision: 1}
	a, dispatched := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "experience_copy" {
			<-release
			return serviceipc.Response{OK: true, Result: receipt}
		}
		return serviceipc.Response{OK: true, Result: records.Version{Memory: receipt.Memory, Dataset: "global", Revision: 1}}
	})
	a.beginExperienceCopy(*a.current())
	a.experience.Conditions = "适用于已确认相同约定的项目。"
	a.confirmExperienceCopy()
	a.navigate("项目", a.experience.Source.Dataset)
	v := a.experience.Source
	a.selectedID, a.selectedItem = v.ID, &v
	a.editMemory()
	a.draft = "用户正在编辑的新草稿"
	selected := a.selectedID
	close(release)
	awaitExperienceDispatch(t, dispatched)
	if a.selectedID != selected || a.detailMode != "edit" || a.draft != "用户正在编辑的新草稿" || a.experience.Copy != nil {
		t.Fatal("late copy reply replaced new route")
	}
}

func TestExperienceCopyCancelKeepsSourceRouteScrollAndSnapshot(t *testing.T) {
	a, _ := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response { return serviceipc.Response{OK: false} })
	source := *a.current()
	a.detailScroll.Y = 130
	page, scope := a.page, a.scope
	a.beginExperienceCopy(source)
	if a.page != "通用知识" || a.experience.Source.Text != source.Text || a.experience.CurrentRevision != source.Revision {
		t.Fatal("preview lost snapshot or route")
	}
	a.experience.Conditions = "独立预览草稿"
	a.closeExperienceCopy()
	if a.page != page || a.scope != scope || a.selectedID != source.ID || a.detailScroll.Y != 130 || a.experience.Copy != nil {
		t.Fatal("cancel changed source reading", a.page, a.selectedID, a.detailScroll.Y)
	}
}
