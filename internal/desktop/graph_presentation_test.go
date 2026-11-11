package desktop

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func graphPresentationApp() *App {
	a := browsingContextApp()
	a.visualFixture = true
	a.projects = []ProjectItem{{ID: "alpha", Name: "示例项目", Path: "/synthetic/project"}}
	a.scopes = []records.Scope{{ID: "alpha", Name: "示例项目", Path: "/synthetic/project", Kind: "project"}}
	a.selectedItem.Title = "有版本依据的关联知识"
	a.items = []MemoryItem{*a.selectedItem}
	a.browsing.Graph.Entities[0].Name = "请求标识"
	a.browsing.Graph.Entities[0].Description = "用于核对请求日志的项目对象。"
	a.browsing.Graph.Mentions[0].Quote = "对应知识中的原始提及。"
	a.browseEntity("entity")
	return a
}

func TestGraphPresentationOrdinaryAndNarrowThreePanes(t *testing.T) {
	for _, width := range []int{1280, 800} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := graphPresentationApp()
			tt := ui.NewTester(a.View, width, 720)
			within := func(label string) {
				t.Helper()
				r, ok := tt.Find(label)
				if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > float32(width)+1 || r.Y+r.H > 721 {
					t.Fatalf("%q outside window: %+v found=%v", label, r, ok)
				}
			}
			within("项目实体")
			within("实体详情")
			within("返回知识")
			within("在记忆视图打开")
			within("查看关系图")
			if tt.HasText("官方接入与同步") || tt.HasText("项目限定记忆与材料的归属") {
				t.Fatal("old full-width explorer returned")
			}
			list, _ := tt.Find("项目实体")
			detail, _ := tt.Find("实体详情")
			gap := detail.X - list.X
			expected := float32(322)
			if width == 800 {
				expected = 222
			}
			if gap < expected-10 || gap > expected+10 {
				t.Fatalf("wrong list pane width: list=%+v detail=%+v gap=%f", list, detail, gap)
			}
			writeScreenshot(t, tt, fmt.Sprintf("graph-%d-entity.png", width))
			a.browsing.Search = "不存在的对象"
			tt.Frame()
			if !tt.HasText("没有匹配的对象") || a.browsing.Entity != "entity" || len(a.browsing.Graph.Entities) != 1 {
				t.Fatal("search zero became empty project or cleared selected context")
			}
			a.browsing.Search = ""
			tt.Frame()
			if err := tt.Click("查看关系图"); err != nil {
				t.Fatal(err)
			}
			within("项目关系图")
			within("查看实体")
			if !tt.HasText("看清知识与对象的关系") || !tt.HasText("有版本依据的关联知识") {
				t.Fatal("true graph nodes absent")
			}
			writeScreenshot(t, tt, fmt.Sprintf("graph-%d-canvas.png", width))
			if err := tt.Click("材料接入与同步"); err != nil {
				t.Fatal(err)
			}
			within("返回实体浏览")
			if a.page != "项目" || a.scope != "alpha" || tt.HasText("准备官方环境") || tt.HasText("真实任务与同步") || tt.HasText("验证提炼 Agent 并继续") {
				t.Fatal("project material action reopened obsolete initialization")
			}
			writeScreenshot(t, tt, fmt.Sprintf("graph-%d-materials.png", width))
		})
	}
}

func TestGraphPresentationEmptyLoadingFailureAndForeignScope(t *testing.T) {
	cases := []struct {
		name              string
		graph             records.CogneeGraph
		loading           bool
		failure, expected string
	}{
		{name: "unconnected", graph: records.CogneeGraph{Project: "alpha", State: "unconnected"}, expected: "当前项目暂无关联对象"},
		{name: "loading", loading: true, expected: "正在读取项目实体"},
		{name: "failed", failure: "synthetic service failure", expected: "实体读取失败"},
		{name: "foreign", graph: records.CogneeGraph{Project: "beta", Entities: []records.CogneeEntity{{ID: "foreign", Name: "不可披露的其他项目对象"}}}, expected: "材料项目不一致"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := graphPresentationApp()
			a.browsing.Graph = tc.graph
			a.browsing.Loading = tc.loading
			a.browsing.Error = tc.failure
			tt := ui.NewTester(a.View, 800, 720)
			if !tt.HasText(tc.expected) || tt.HasText("不可披露的其他项目对象") {
				t.Fatal("bad scope/state presentation", tt.Texts())
			}
			if !tt.HasText("返回知识") {
				t.Fatal("empty or failure removed return path")
			}
		})
	}
}

func TestGraphMaterialAuthorizationUsesOnlyExistingExplicitProjectGrant(t *testing.T) {
	a := graphPresentationApp()
	m := &GraphMaterialUI{Project: "alpha", ConnectionID: "allowed", Endpoint: "http://127.0.0.1:18099", Dataset: "dataset", Credential: "synthetic-credential", Connections: []records.Connection{{ID: "allowed", Agent: "codex", Generation: 4, Enabled: true, AllowedScopes: []string{"alpha"}}}}
	authorization, err := a.graphAuthorization(m)
	if err != nil {
		t.Fatal(err)
	}
	if authorization.Binding.Project != "alpha" || authorization.Binding.Workspace != "/synthetic/project" || authorization.Binding.Generation != 4 || authorization.Binding.ConnectionID != "allowed" {
		t.Fatal("authorization scope was replaced", authorization.Binding)
	}
	m.Connections[0].Agent = "pi"
	if _, err := a.graphAuthorization(m); err == nil {
		t.Fatal("pi grant was described as supported graph provenance")
	}
	m.Connections[0].Agent = "codex"
	m.Connections[0].AllowedScopes = []string{"beta"}
	if _, err := a.graphAuthorization(m); err == nil {
		t.Fatal("foreign grant accepted")
	}
	m.Connections[0].AllowedScopes = []string{"alpha"}
	a.scope = "beta"
	if _, err := a.graphAuthorization(m); err == nil {
		t.Fatal("stale panel widened project scope")
	}
}

func TestGraphMaterialMissingGrantNeverCreatesConnectionOrStartsTask(t *testing.T) {
	var calls atomic.Int64
	a, _ := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		calls.Add(1)
		return serviceipc.Response{OK: false, Error: "no operation authorized"}
	})
	a.page, a.scope = "项目", "alpha"
	a.browsing.Material = &GraphMaterialUI{Project: "alpha", Loaded: true, Endpoint: "http://127.0.0.1:18099", Dataset: "dataset", Credential: "synthetic-only"}
	a.authorizeGraphMaterial()
	a.syncGraphMaterial()
	if calls.Load() != 0 || a.browsing.Material.Busy || a.browsing.Material.Message == "" {
		t.Fatal("missing grant implicitly configured connection or task")
	}
}

func TestGraphMaterialBindingReadFiltersProjectAndCannotGrantProvenance(t *testing.T) {
	a, dispatch := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "cognee_bindings":
			return serviceipc.Response{OK: true, Result: []records.CogneeBinding{{Project: "alpha", Dataset: "dataset", Instance: "instance", ConnectionID: "allowed", Generation: 3}, {Project: "beta", Dataset: "foreign"}}}
		case "cognee_graph":
			return serviceipc.Response{OK: true, Result: records.CogneeGraph{Project: r.CogneeGraphQuery.Project, State: "empty"}}
		case "connections":
			return serviceipc.Response{OK: true, Result: []records.Connection{{ID: "allowed", Agent: "codex", Generation: 3, Enabled: true, AllowedScopes: []string{"alpha"}}, {ID: "other", Agent: "codex", Generation: 9, Enabled: true, AllowedScopes: []string{"beta"}}, {ID: "pi", Agent: "pi", Enabled: true, AllowedScopes: []string{"alpha"}}}}
		default:
			t.Errorf("binding read performed %s", r.Op)
			return serviceipc.Response{OK: false}
		}
	})
	a.page, a.scope = "项目", "alpha"
	a.browsing.Graph = records.CogneeGraph{Project: "alpha", State: "unconnected"}
	a.browsing.Material = &GraphMaterialUI{Project: "alpha", Legacy: true}
	a.openGraphMaterial()
	awaitExperienceDispatch(t, dispatch)
	m := a.browsing.Material
	if m == nil || !m.Loaded || len(m.Bindings) != 1 || len(m.Connections) != 1 || !graphBindingAuthorized(m) {
		t.Fatal("existing project binding not adopted", m)
	}
	if len(a.browsing.Graph.Entities) != 0 || a.browsing.Graph.State != "unconnected" {
		t.Fatal("binding authorization fabricated graph provenance")
	}
	a.scope = "beta"
	a.browseProjectChanged()
	awaitExperienceDispatch(t, dispatch)
	if a.browsing.Material != nil {
		t.Fatal("project change retained old credential panel")
	}
}

func TestGraphMaterialScopeMismatchedSyncReceiptIsNotAdopted(t *testing.T) {
	m := &GraphMaterialUI{Project: "alpha"}
	query := records.CogneeSync{Project: "alpha", Dataset: "dataset", Session: "selected-session"}
	if graphReceiptMatches(m, records.CogneeSyncReceipt{Project: "beta", Dataset: "dataset", Session: "selected-session"}, query) {
		t.Fatal("foreign project receipt accepted")
	}
	if graphReceiptMatches(m, records.CogneeSyncReceipt{Project: "alpha", Dataset: "dataset", Session: "different-session"}, query) {
		t.Fatal("other session receipt accepted")
	}
}

func TestGraphNodesRequireScopedActualMentionVersions(t *testing.T) {
	a := graphPresentationApp()
	a.browsing.Graph.Mentions[0].Revision = 0
	_, memories := a.projectGraphNodes()
	if len(memories) != 0 {
		t.Fatal("unversioned graph memory drawn")
	}
	a.browsing.Graph.Project = "beta"
	nodes, _ := a.projectGraphNodes()
	if len(nodes) != 0 || len(a.entityMentions("entity")) != 0 || len(a.memoryEntities(*a.selectedItem)) != 0 {
		t.Fatal("foreign graph projection leaked into current project")
	}
}

func TestGraphMaterialExplicitAuthorizationReusesIPCWithoutAgentSetup(t *testing.T) {
	var authorizations atomic.Int64
	a, dispatch := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "cognee_authorize":
			authorizations.Add(1)
			if !r.UserConfirmed || r.CogneeAuthorization == nil {
				t.Error("missing explicit authorization")
			}
			request := *r.CogneeAuthorization
			if request.Binding.Project != "alpha" || request.Binding.ConnectionID != "existing" || request.Binding.Generation != 6 || request.Binding.Workspace != "/synthetic/project" {
				t.Error("authorization changed frozen project grant")
			}
			binding := request.Binding
			binding.Instance = "verified-instance"
			return serviceipc.Response{OK: true, Result: binding}
		case "cognee_graph":
			if r.CogneeGraphQuery.Project != "alpha" || r.CogneeGraphQuery.Dataset != "real-dataset" {
				t.Error("graph query broadened source")
			}
			return serviceipc.Response{OK: true, Result: records.CogneeGraph{Project: "alpha", Dataset: "real-dataset", State: "empty"}}
		default:
			t.Errorf("project material authorization invoked %s", r.Op)
			return serviceipc.Response{OK: false}
		}
	})
	a.page, a.scope = "项目", "alpha"
	a.scopes = []records.Scope{{ID: "alpha", Kind: "project", Path: "/synthetic/project"}}
	a.browsing.Material = &GraphMaterialUI{Project: "alpha", Loaded: true, ConnectionID: "existing", Endpoint: "http://127.0.0.1:18099", Dataset: "real-dataset", Credential: "synthetic-credential", Connections: []records.Connection{{ID: "existing", Agent: "codex", Generation: 6, Enabled: true, AllowedScopes: []string{"alpha"}}}}
	a.authorizeGraphMaterial()
	awaitExperienceDispatch(t, dispatch)
	awaitExperienceDispatch(t, dispatch)
	m := a.browsing.Material
	if authorizations.Load() != 1 || m.Binding == nil || m.Credential != "" || m.Busy || a.browsing.Dataset != "real-dataset" || len(a.browsing.Graph.Entities) != 0 {
		t.Fatal("binding receipt or source state incorrect")
	}
	if a.page != "项目" || a.scope != "alpha" || a.cognee.FirstTask {
		t.Fatal("authorization entered legacy first task")
	}
}

func TestGraphMaterialExplicitSessionSyncAdoptsOnlyOwnReceipt(t *testing.T) {
	var syncs atomic.Int64
	a, dispatch := experienceUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "cognee_sync":
			syncs.Add(1)
			q := r.CogneeSync
			if q == nil || q.Project != "alpha" || q.Dataset != "dataset" || q.Session != "selected-session" || !r.UserConfirmed {
				t.Error("sync expanded authorized session")
			}
			return serviceipc.Response{OK: true, Result: records.CogneeSyncReceipt{Project: "alpha", Dataset: "dataset", Session: "selected-session", OfficialState: "persisted", Knowledge: []records.SaveReceipt{{Persisted: true, SavedRevision: 1}}}}
		case "cognee_graph":
			return serviceipc.Response{OK: true, Result: records.CogneeGraph{Project: "alpha", Dataset: "dataset", State: "empty"}}
		default:
			t.Errorf("session sync invoked %s", r.Op)
			return serviceipc.Response{OK: false}
		}
	})
	a.page, a.scope = "项目", "alpha"
	binding := records.CogneeBinding{Project: "alpha", Dataset: "dataset", ConnectionID: "existing", Generation: 3}
	a.browsing.Material = &GraphMaterialUI{Project: "alpha", Loaded: true, Binding: &binding, Session: "selected-session", Connections: []records.Connection{{ID: "existing", Agent: "codex", Generation: 3, Enabled: true, AllowedScopes: []string{"alpha"}}}}
	a.syncGraphMaterial()
	awaitExperienceDispatch(t, dispatch)
	awaitExperienceDispatch(t, dispatch)
	if syncs.Load() != 1 || a.browsing.Material.Receipt == nil || a.browsing.Material.Busy || len(a.browsing.Graph.Entities) != 0 {
		t.Fatal("sync receipt fabricated graph or was lost")
	}
}
