package simpledesktop

import (
	"sort"

	"github.com/egoist/mygo/ui"
)

func smallSettingsCard(c *ui.Context, content func()) {
	ui.Column(c).Width(0).Grow(1).MinWidth(0).MinHeight(112).Padding(16).Gap(8).Background(ui.Hex("#ffffff")).Border(1, homeBorder).Radius(10).Children(content)
}

func (a *App) generationSummaryCards(c *ui.Context) {
	s := &a.integrations
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Stretch).Children(func() {
		smallSettingsCard(c, func() {
			homeText(c, "当前记忆处理", homeMeta)
			homeText(c, generationLabel(s.Generation, s.Generation.Active), homeHeading).FontSize(15).MaxLines(2)
			homeText(c, "Agent 接入与处理模型独立配置。", homeMeta)
		})
		smallSettingsCard(c, func() {
			homeText(c, "下次启动", homeMeta)
			label := "沿用当前配置"
			if !s.Loaded {
				label = "尚未读取"
			}
			if s.Generation.Next != nil {
				label = "下次启动使用：" + generationLabel(s.Generation, s.Generation.Next)
			}
			homeText(c, label, homeHeading).FontSize(15).MaxLines(2)
			if s.Generation.NextError != "" || s.Generation.StartupError != "" {
				homeText(c, "上次候选启动未完成，当前配置与待应用项已保留。", homeMeta)
			} else {
				homeText(c, "保存配置不会立即切换当前模型。", homeMeta)
			}
		})
	})
}

func (a *App) generationProfileCards(c *ui.Context) {
	s := &a.integrations
	ids := make([]string, 0, len(s.Generation.Profiles))
	for id, rows := range s.Generation.Profiles {
		if len(rows) > 0 && rows[len(rows)-1].Profile.Mode == "provider" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			homeText(c, "已保存 Provider 配置", homeHeading).FontSize(16)
			homeText(c, "保存、验证和启用分别操作；验证会调用模型并消耗额度。", homeMeta)
		})
		textButton(c, "添加 Provider", nil).Disabled(s.Busy || s.SaveOperation != "").OnClick(func() {
			s.editProfile("api-"+integrationOperation(), GenerationRow{Profile: GenerationProfile{Mode: "provider", Provider: "openai"}})
			s.MaxOutputTokens, s.Saved, s.SavedSignature, s.Error, s.Message = "16384", nil, "", "", ""
		})
	})
	width, _ := c.Size()
	columns := 3
	if width-homeSidebarWidth-64 < 840 {
		columns = 2
	}
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		for start := 0; start < len(ids); start += columns {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Stretch).Children(func() {
				for col := 0; col < columns; col++ {
					index := start + col
					if index >= len(ids) {
						ui.Column(c).Width(0).Grow(1)
						continue
					}
					id := ids[index]
					rows := s.Generation.Profiles[id]
					row := rows[len(rows)-1]
					ref := &GenerationRef{ID: id, Revision: row.Revision}
					smallSettingsCard(c, func() {
						label := generationLabel(s.Generation, ref)
						homeText(c, label, homeHeading).FontSize(14).MaxLines(2).Tooltip(label)
						state := "未验证"
						if s.Generation.validated(ref) {
							state = "已验证"
						}
						if active := s.Generation.Active; active != nil && *active == *ref {
							state = "当前使用"
						}
						homeText(c, state, homeMeta)
						textButton(c, "配置", nil).Label("配置模型：" + id).AlignSelf(ui.Start).Disabled(s.Busy || s.SaveOperation != "").OnClick(func() {
							s.editProfile(id, row)
							s.Error = ""
							s.Message = ""
						})
					})
				}
			})
		}
	})
}

func (a *App) advancedSettingsCards(c *ui.Context) {
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Stretch).Children(func() {
		smallSettingsCard(c, func() {
			homeText(c, "后台运行", homeHeading).FontSize(15)
			homeText(c, "关闭窗口后，后台服务继续运行。", homeBody).FontSize(12)
			homeText(c, "电脑重启后通过开发启动器重新启动。", homeMeta)
			homeText(c, "python3 scripts/dev.py", homeMeta)
		})
		smallSettingsCard(c, func() {
			homeText(c, "配置生效", homeHeading).FontSize(15)
			homeText(c, "服务与模型变更在重启后台后生效。", homeBody).FontSize(12)
			homeText(c, "向量模型由 Cognee 官方配置管理。", homeMeta)
		})
	})
}
