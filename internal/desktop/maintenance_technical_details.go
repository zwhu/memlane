package desktop

import "github.com/egoist/mygo/ui"

// Keys retain the exact operation identity; disclosure changes presentation only.
// BackupUI is reset with the library context, so old-library disclosure cannot
// expose a new library's operation by position or list index.
func (a *App) maintenanceTechnicalDetails(c *ui.Context, key string, body func()) {
	if a.backups.TechnicalDetails == nil {
		a.backups.TechnicalDetails = map[string]bool{}
	}
	open := a.backups.TechnicalDetails[key]
	label := "展开技术详情"
	if open {
		label = "收起技术详情"
	}
	quietButton(c, label).Key("maintenance-technical-" + key).AlignSelf(ui.Start).OnClick(func() { a.backups.TechnicalDetails[key] = !a.backups.TechnicalDetails[key] })
	if open {
		ui.Column(c).Gap(4).Children(body)
	}
}
