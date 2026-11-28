package app

import (
	"strings"

	"github.com/egoist/mygo/ui"
	appsearch "github.com/zwhu/memlane/app/search"
)

var v4IconUpload = v4IconSVG(`<path d="M10 13V4.5M6.5 8L10 4.5 13.5 8M4 15.5h12"/>`)

func (a *App) searchProjectName(dataset string) string {
	for _, p := range overviewProjects(a.Config) {
		if p.Dataset == dataset {
			return p.Name
		}
	}
	for _, p := range a.Config.Common {
		if p.Dataset == dataset {
			return p.Name
		}
	}
	return ""
}

func (a *App) searchTargetProject() (Project, bool) {
	if (a.page == "知识" || a.page == "图谱") && a.project >= 0 && a.project < len(a.Config.Projects) {
		return a.Config.Projects[a.project], true
	}
	for _, p := range a.Config.Projects {
		if !a.projectIsHidden(p) {
			return p, true
		}
	}
	return Project{}, false
}

func searchItemPrimaryText(i Item, query string) (string, string) {
	if i.Session != nil {
		topic := strings.TrimSpace(i.Title)
		if topic == "" || topic == i.ID || topic == "来源会话" || looksLikeBareUUID(topic) {
			topic = itemSessionIdentity(i)
		}
		_, preview := searchHitText(i, query)
		if strings.TrimSpace(preview) == strings.TrimSpace(topic) {
			preview = ""
		}
		return topic, preview
	}
	title, preview := searchHitText(i, query)
	if preview != "" {
		trimmedTitle := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(title), "…"))
		if strings.Contains(i.Kind, "知识") ||
			title == "记忆" || title == "原始记录" || title == "检索片段" ||
			(trimmedTitle != "" && strings.HasPrefix(strings.TrimSpace(preview), trimmedTitle)) {
			return preview, ""
		}
	}
	if looksLikeBareUUID(title) {
		if preview != "" {
			return firstReadingSentence(preview), preview
		}
		if strings.Contains(i.Kind, "知识") {
			return "记忆", ""
		}
		return "原始记录", ""
	}
	return title, preview
}

func v4DangerSoftBg() ui.Color                  { return appsearch.DangerSoftBg(&tok.Tokens) }
func v4KbdHint(c *ui.Context, key, desc string) { appsearch.KeyboardHint(c, &tok.Tokens, key, desc) }
func looksLikeBareUUID(s string) bool           { return appsearch.LooksLikeBareUUID(s) }
func highlightSpanByTerms(sp ui.Span, terms []string, color ui.Color) []ui.Span {
	return appsearch.HighlightSpans(sp, terms, color)
}
func (a *App) searchDialog(c *ui.Context)         { a.searchPage.RenderDialog(c) }
func (a *App) unifiedSearchTrigger(c *ui.Context) { a.searchPage.RenderTrigger(c) }
