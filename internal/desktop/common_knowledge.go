package desktop

import (
	"errors"

	"github.com/zwhu/memlane/internal/records"
)

func scopeDisplayName(s records.Scope) string {
	switch s.Kind {
	case "preference":
		return "个人偏好"
	case "experience":
		return "通用经验"
	}
	return s.Name
}

func commonDatasets(scopes []records.Scope, category string) ([]string, error) {
	var ids []string
	for _, s := range scopes {
		if (s.Kind == "preference" || s.Kind == "experience") && (category == "" || s.Kind == category) {
			ids = append(ids, s.ID)
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("通用知识范围尚未载入")
	}
	return ids, nil
}

func (a *App) commonQueryCategory() string {
	if a.recordKind == "raw" {
		return ""
	}
	return a.commonCategory
}

func (a *App) selectCommonCategory(category string) {
	if a.page != "通用知识" || category != "preference" && category != "experience" {
		return
	}
	a.closeKnowledgeHistory()
	a.closeSourceReader()
	a.rememberDraft()
	if a.commonCategory == category {
		a.commonCategory = ""
	} else {
		a.commonCategory = category
	}
	a.nextCursor = ""
	a.items = nil
	keep := false
	if v := a.current(); v != nil && v.Origin != "raw" {
		for _, scope := range a.scopes {
			if scope.ID == v.Dataset && (scope.Kind == "preference" || scope.Kind == "experience") && (a.commonCategory == "" || scope.Kind == a.commonCategory) {
				keep = true
			}
		}
	}
	if !keep {
		a.selectedID, a.selectedItem = "", nil
	}
	a.history = nil
	a.commonSelectMatch = true
	a.detailMode = "view"
	a.editing = false
	a.refresh(false)
}

func (a *App) memoryScopeName(v MemoryItem) string {
	if label := a.scopeLabel(v.Dataset); label != "" {
		return label
	}
	return v.ScopeName
}

func sourceDisplayName(source string) string {
	switch source {
	case "用户：原文复制到全局":
		return "用户：原文复制到通用知识"
	case "traex：用户确认全局提炼":
		return "traex：用户确认通用提炼"
	}
	return source
}
