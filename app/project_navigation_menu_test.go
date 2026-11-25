package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestProjectMenuOwnsCreationAndCommonOwnsStorage(t *testing.T) {
	a := settingsFixture()
	a.projectCreate.Loaded = true
	a.projectCreate.Suggestions = []projectSuggestion{{ID: "old", Name: "历史项目", Kind: "dataset"}}
	a.ChooseProjectFolder = func() (string, error) { return "/synthetic/new-project", nil }
	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, "新建项目")
	if !a.projectCreate.Open || !tt.HasText("创建项目") || tt.HasText("关联已有官方数据组") {
		t.Fatal("single creation dialog not opened")
	}
	fixtureImage(t, tt, "project-create-empty")
	clickProjectUI(t, tt, "添加文件夹")
	if a.projectCreate.Folder != "/synthetic/new-project" {
		t.Fatal("directory picker result lost")
	}
	clickProjectUI(t, tt, "建议关联：历史项目")
	if a.projectCreate.Selected == nil || a.projectCreate.Selected.ID != "old" {
		t.Fatal("suggestion did not reuse existing record")
	}
	fixtureImage(t, tt, "project-create-suggestion")
	tt.Key(0, ui.KeyEscape)
	clickProjectUI(t, tt, "管理")
	if !tt.HasText("编辑名称") || !tt.HasText("/synthetic/memlane") {
		t.Fatal("project context missing directory and actions")
	}
	fixtureImage(t, tt, "project-context-menu")
	clickProjectUI(t, tt, "编辑名称")
	if a.projectCreate.TargetProject != "p" || a.projectCreate.Selected == nil || a.projectCreate.Selected.ID != "p" {
		t.Fatal("editing did not pin current project")
	}
	if tt.HasText("源文件夹") || tt.HasText("添加文件夹") || !tt.HasText("保存名称") {
		t.Fatal("linked project exposes binding editor")
	}
	fixtureImage(t, tt, "project-edit-name-only")
	tt.Key(0, ui.KeyEscape)
	clickProjectUI(t, tt, "设置")
	if tt.HasText("高级") {
		t.Fatal("retired advanced tab remains")
	}
	for _, label := range []string{"项目关联", "检查未保存的会话", "准备／核对通用记忆", "通用记忆存储"} {
		if tt.HasText(label) {
			t.Fatal("scoped action remains global", label)
		}
	}
	common := commonUIFixture()
	ct := ui.NewTester(common.View, 1200, 820)
	clickProjectUI(t, ct, "通用记忆菜单")
	if !ct.HasText("通用记忆数据组") || !ct.HasText("准备／核对通用记忆") {
		t.Fatal("common storage controls missing")
	}
	fixtureImage(t, ct, "common-storage-menu")
}
func TestProjectMenuReturnsToGlobalSettingsAndPreservesCreationDraft(t *testing.T) {
	a := settingsFixture()
	a.page = "设置"
	a.settingsOrigin = nil
	a.projectCreate.Loaded = true
	tt := ui.NewTester(a.View, 1200, 820)
	a.openProjectSettings(0)
	tt.Frame()
	if a.page != "知识" || a.kind != "管理" || a.access.OpenProject != "p" {
		t.Fatal("openProjectSettings did not open inline management tab")
	}
	clickProjectUI(t, tt, "设置")
	if a.page != "设置" || a.settingsOrigin != nil || a.access.OpenProject != "" {
		t.Fatal("project settings return loop")
	}
	a.openProjectCreation("")
	a.projectCreate.Folder = "/synthetic/new-workspace"
	a.projectCreate.Open = false
	a.openProjectCreation("")
	if a.projectCreate.Folder != "/synthetic/new-workspace" {
		t.Fatal("closing dialog discarded draft")
	}
}

func TestProjectSettingsBackLabelMatchesCommonKnowledgeOrigin(t *testing.T) {
	a := settingsFixture()
	a.Config.Common = []Project{{ID: "common", Name: "通用知识", Dataset: "common"}}
	a.page = "通用知识"
	a.state().Loaded = true
	tt := ui.NewTester(a.View, 1200, 820)
	a.openProjectSettings(0)
	tt.Frame()
	if tt.HasText("返回项目") {
		t.Fatal("back label names a different destination")
	}
	clickProjectUI(t, tt, "返回通用记忆")
	if a.page != "通用知识" {
		t.Fatal("back did not restore the common knowledge page")
	}
}
