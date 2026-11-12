package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

// Receipt identity is for a list query, not just a project or its total count.
// No content is duplicated; a confirmed empty result is useful state too.
func (a *App) readingListSnapshotKey() string {
	query := a.listQuery()
	query.Cursor = ""
	return records.MergeDigest(struct {
		Root, Page string
		Query      records.ListQuery
	}{a.library.Root, a.page, query})
}

func (a *App) hasCurrentListSnapshot() bool {
	return a.listReadSnapshot != "" && a.listReadSnapshot == a.readingListSnapshotKey()
}

func (a *App) readingListFailureAction(c *ui.Context) {
	if a.libraryError == "" {
		return
	}
	label := "读取未完成"
	if a.hasCurrentListSnapshot() {
		label = "刷新未完成"
	}
	quietButton(c, label).Label("重试读取内容").Tooltip(a.libraryError).FontSize(10).Padding(1, 0).Disabled(a.loading).OnClick(func() { a.refresh(false) })
}

func (a *App) readingListEmpty(c *ui.Context) {
	if a.loading && a.libraryContext != nil && !a.hasCurrentListSnapshot() {
		a.empty(c, "正在读取内容", "")
	} else if a.libraryError != "" && !a.hasCurrentListSnapshot() {
		message := "重试后继续查看当前范围。"
		if listReadInvalidatesSnapshot(a.listReadErrorCode) {
			message = "当前范围无法读取，请核对权限或来源。"
		}
		a.empty(c, "读取未完成", message)
	} else if a.projectKnowledgeEmpty() {
		a.projectEmptyKnowledge(c)
	} else if a.readingFilter != "" || a.readingFilters.active() || a.page == "通用知识" && a.commonCategory != "" {
		a.empty(c, "没有匹配的内容", "清除筛选可查看这个范围的全部内容。")
	} else {
		a.empty(c, "列表为空", "保存的记忆会出现在这里。")
	}
}

func listReadInvalidatesSnapshot(code string) bool {
	switch code {
	case "scope_denied", "permission_denied", "connection_revoked", "authorization_required", "cognee_authorization_required", "scope_changed", "not_found", "source_not_found", "stale_source":
		return true
	}
	return false
}

func (a *App) withdrawListSnapshot(code string) {
	if !listReadInvalidatesSnapshot(code) {
		return
	}
	a.rememberDraft()
	a.closeSourceReader()
	a.listReadSnapshot = ""
	a.items, a.selectedItem, a.history = nil, nil, nil
	a.selectedID, a.nextCursor, a.readingCountsRoute = "", "", ""
	a.listTotal = 0
	a.readingKnowledgeTotal, a.readingRawTotal = nil, nil
}
