package simpledesktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestV4GroupAndControlsRender(t *testing.T) {
	clicked := false
	selected := ""
	tt := ui.NewTester(func(c *ui.Context) {
		applyAppearance(false)
		ui.Themed(c, homeTheme).Fill().Children(func() {
			v4GroupWrap(c, func() {
				v4Group(c, "测试分组", "共 2 项", func() {
					v4SmallButton(c, "刷新", v4IconRefresh, nil)
				}, func() {
					v4Row(c, v4RowOptions{
						Key:       "r1",
						Title:     "可点击行",
						Desc:      "副标题说明",
						Clickable: true,
						OnClick:   func() { clicked = true },
					})
					v4Row(c, v4RowOptions{
						Key:   "r2",
						Title: "权限行",
						Control: func() {
							v4TriAccessSelect(c, "tri-codex", "Codex", v4TriAccessState{
								Value:  "read",
								Saving: true,
								Error:  "保存失败，已回滚",
							}, func(v string) { selected = v })
						},
					})
				})
				v4SkeletonGroup(c, 2, 2, true, true)
			})
		})
	}, 900, 600)

	for _, want := range []string{"测试分组", "共 2 项", "可点击行", "副标题说明", "权限行", "保存中…", "只读", "保存失败，已回滚"} {
		if !tt.HasText(want) {
			t.Fatalf("missing expected text %q", want)
		}
	}
	if err := tt.Click("可点击行"); err != nil {
		t.Fatal(err)
	}
	if !clicked {
		t.Fatal("clickable v4Row did not invoke OnClick")
	}
	_ = selected
}

func TestV4DialogConfirmAndDangerEnterContract(t *testing.T) {
	open := true
	confirmed := 0
	cancelled := 0
	tt := ui.NewTester(func(c *ui.Context) {
		applyAppearance(false)
		ui.Themed(c, homeTheme).Fill().Children(func() {
			v4Dialog(c, &open, v4DialogOptions{
				Key:         "normal-dlg",
				Title:       "确认操作",
				Width:       v4DialogSmall,
				ConfirmText: "确认启用",
				OnConfirm:   func() { confirmed++; open = false },
				OnCancel:    func() { cancelled++ },
				Body: func() {
					homeText(c, "普通确认内容", homeBody)
				},
			})
		})
	}, 900, 600)

	if !tt.HasText("确认操作") || !tt.HasText("确认启用") {
		t.Fatal("normal v4Dialog missing title or confirm button")
	}
	if err := tt.Click("确认启用"); err != nil {
		t.Fatal(err)
	}
	if confirmed != 1 || open {
		t.Fatalf("normal v4Dialog confirm failed: confirmed=%d open=%v", confirmed, open)
	}

	// Danger dialog must NOT trigger OnConfirm on Enter.
	dangerOpen := true
	dangerConfirmed := 0
	ttDanger := ui.NewTester(func(c *ui.Context) {
		applyAppearance(true)
		ui.Themed(c, homeTheme).Fill().Children(func() {
			v4Dialog(c, &dangerOpen, v4DialogOptions{
				Key:         "danger-dlg",
				Title:       "删除模型配置？",
				Width:       v4DialogSmall,
				Danger:      true,
				HideCancel:  true,
				ConfirmText: "删除此配置",
				OnConfirm:   func() { dangerConfirmed++; dangerOpen = false },
				Body: func() {
					homeText(c, "此操作不可撤销", homeBody)
				},
			})
		})
	}, 900, 600)

	ttDanger.Key(0, ui.KeyEnter)
	if dangerConfirmed != 0 {
		t.Fatalf("danger v4Dialog must not trigger confirm on Enter: confirmed=%d open=%v", dangerConfirmed, dangerOpen)
	}
	dangerOpen = true
	ttDanger.Frame()
	if err := ttDanger.Click("删除此配置"); err != nil {
		t.Fatal(err)
	}
	if dangerConfirmed != 1 || dangerOpen {
		t.Fatalf("danger v4Dialog explicit click did not confirm: confirmed=%d open=%v", dangerConfirmed, dangerOpen)
	}
}
