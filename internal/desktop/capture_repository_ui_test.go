package desktop

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func capturePanelPreview() (*records.DeliveryAssignmentPreview, records.CaptureOrigin) {
	v := e05PreviewFixture()
	origin := records.CaptureOrigin{Agent: "codex", Consent: uuid.NewString(), Repository: records.CaptureRepository{State: "known", Workspace: "/synthetic/repository", GitRoot: "/synthetic/repository", GitCommon: "/synthetic/repository/.git", RootDevice: 1, RootInode: 2, CommonDevice: 1, CommonInode: 3}}
	raw, _ := json.Marshal(origin)
	v.Arguments.Source = &records.AgentSource{Fragment: string(raw)}
	v.MatchingProject = nil
	v.SelectedProject = ""
	v.Code = "project_unregistered"
	return v, origin
}

func TestCaptureRepositoryPanelDefersWithoutWritesAndKeepsOriginal(t *testing.T) {
	a := New("test", nil)
	v, _ := capturePanelPreview()
	p := e05PanelFixture(a, v)
	tt := ui.NewTester(a.View, 1000, 720)
	if !tt.HasText("原始记录已在本机保留；暂缓不会删除材料。") || !tt.HasText(v.Arguments.Text) || !tt.HasText("添加项目并关联原始记录") {
		t.Fatal("capture panel missing original or consent")
	}
	if err := tt.Click("暂缓"); err != nil {
		t.Fatal(err)
	}
	if p.CaptureRoute != nil || p.CaptureReceipt != nil || p.Pending != nil || p.Preview.Arguments.Text != v.Arguments.Text {
		t.Fatal("defer changed source or authorization")
	}
}

func TestCaptureRepositoryConfirmationUsesSeparateRawRouting(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-project", true: "existing-project"}[existing], func(t *testing.T) {
			v, origin := capturePanelPreview()
			project := records.Scope{ID: uuid.NewString(), Kind: "project", Name: "Synthetic", Path: origin.Repository.GitRoot}
			registered, confirmed, delivered := 0, 0, 0
			routeID := ""
			a, updates := processingUIFixture(t, func(q serviceipc.Request) serviceipc.Response {
				switch q.Op {
				case "inspect_project":
					result := records.ProjectInspection{Root: project.Path, Identity: origin.Repository.RegistrationIdentity()}
					if existing {
						result.Existing = &project
					}
					return serviceipc.Response{OK: true, Result: result}
				case "register_project":
					if !q.UserConfirmed || q.Project.ExpectedRepository != origin.Repository.RegistrationIdentity() {
						t.Error("unfenced project registration")
					}
					registered++
					return serviceipc.Response{OK: true, Result: project}
				case "capture_route_confirm":
					r := q.CaptureRouteConfirmation
					if !q.UserConfirmed || r.EntryID != v.EntryID || r.Project != project.ID {
						t.Error("route confirmation lost explicit scope")
					}
					confirmed++
					routeID = r.OperationID
					return serviceipc.Response{OK: true, Result: records.CaptureRoute{OperationID: r.OperationID, LibraryID: r.LibraryID, ConnectionID: r.ConnectionID, ConsentID: origin.Consent, Repository: origin.Repository, Project: r.Project}}
				case "capture_deliver":
					if q.UserConfirmed {
						t.Error("automatic send forged manual confirmation")
					}
					delivered++
					return serviceipc.Response{OK: true, Result: records.CaptureDeliveryReceipt{RouteID: routeID, EntryID: v.EntryID, RequestDigest: v.RequestDigest, ReviewDigest: v.ReviewDigest, LibraryID: v.LibraryID, ConnectionID: v.Item.ConnectionID, OperationID: v.Item.OperationID, Project: project.ID, Memory: uuid.NewString(), Revision: 1, Persisted: true}}
				case "agent_tool", "delivery_assignment_confirm":
					t.Error("capture used manual knowledge save")
				}
				return serviceipc.Response{OK: true}
			})
			p := e05PanelFixture(a, v)
			p.CaptureName = "Synthetic"
			a.confirmCapturedRepository(p)
			awaitProcessingDispatch(t, updates)
			if p.Error != "" || p.CaptureReceipt == nil || p.CaptureRoute == nil || confirmed != 1 || delivered != 1 {
				t.Fatal(p.Error, p.Info, confirmed, delivered)
			}
			if existing && registered != 0 || !existing && registered != 1 {
				t.Fatal("project not reused", registered)
			}
		})
	}
}
