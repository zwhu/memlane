//go:build e2e

package desktop

import "strings"

// Uses the normal navigation, authorization and polling paths. The caller owns
// an explicitly marked synthetic service; this method injects no content.
func (a *App) OpenNativeReadingVerification(project string) {
	a.navigate("项目", project)
	a.switchReadingKind("raw")
}

// Used only by the explicit synthetic Native copy probe. The probe persists
// hashes and lengths, never clipboard or conversation bodies.
func (a *App) NativeReadingCopyExpected(kind string) (string, bool) {
	s := &a.conversations
	if !a.conversationPageVisible() {
		return "", false
	}
	v := s.Views[s.Selected]
	if v == nil || v.Detail == nil {
		return "", false
	}
	if kind == "loaded" {
		return conversationCopy(*v.Detail), true
	}
	for _, qa := range v.Detail.QAs {
		if kind == "body-selection" && qa.Question != "" && qa.Answer != "" {
			return qa.Question + "\x00" + strings.SplitN(qa.Answer, "\n", 2)[0], true
		}
		// First closed backtick fence of the known synthetic sample;
		// independent of the Markdown renderer and not a general parser.
		if kind == "first-code" {
			parts := strings.SplitN(qa.Answer, "```", 3)
			if len(parts) == 3 {
				if n := strings.IndexByte(parts[1], '\n'); n >= 0 {
					return parts[1][n+1:], true
				}
			}
		}
		if kind == "question" && qa.Question != "" {
			return qa.Question, true
		}
		if kind == "answer" && qa.Answer != "" {
			return qa.Answer, true
		}
	}
	return "", false
}

func (a *App) NativeReadingVerificationState() map[string]any {
	if !a.conversationPageVisible() {
		return map[string]any{"mode": "project-content", "project": a.scope, "catalog_sequence": a.loadSequence,
			"loading": a.loading, "rows": len(a.items), "list_error": a.libraryError, "list_error_code": a.listReadErrorCode,
			"list_snapshot_current": a.hasCurrentListSnapshot(), "project_empty_guide": a.projectKnowledgeEmpty(),
			"record_kind": a.recordKind, "editing": a.editing, "detail_mode": a.detailMode}
	}
	s := &a.conversations
	if d := s.Documents; d != nil {
		out := map[string]any{"mode": "documents", "project": s.Project, "catalog_sequence": d.Sequence, "read_sequence": d.ReadSequence, "loading": d.Loading, "reading": d.Reading, "catalog_error": d.ErrorCode, "source_error": "", "rows": len(d.Rows), "selected": d.Selected, "page_visible": a.conversationPageVisible()}
		if view := d.Views[d.Selected]; view != nil {
			out["detail_present"] = view.Content != nil
			out["detail_error"] = view.ErrorCode
			out["scroll_y"] = view.Scroll.Y
			if view.Window != nil {
				out["text_page"] = view.Window.Page
			}
			_, out["copy_available"] = a.currentOfficialDocument()
			out["record_info"] = view.Info
		}
		out["list_scroll_y"] = d.ListScroll.Y
		out["source_open"] = d.Source != nil
		if source := d.Source; source != nil {
			out["source_sequence"] = source.Sequence
			out["source_error"] = source.Reading.SourceError
			if view := source.Reading.Views[source.Reading.Selected]; view != nil {
				out["source_scroll_y"] = view.Scroll.Y
				out["source_tools"] = view.Tools
				out["source_detail_present"] = view.Detail != nil
				if view.Detail != nil {
					out["source_qa_count"] = len(view.Detail.QAs)
					out["source_trace_count"] = len(view.Detail.Traces)
				}
			}
		}
		return out
	}
	out := map[string]any{"project": s.Project, "catalog_sequence": s.Sequence, "read_sequence": s.ReadSequence, "loading": s.Loading, "reading": s.Reading, "catalog_error": s.ErrorCode, "source_error": s.SourceError, "rows": len(s.Rows), "selected": s.Selected, "page_visible": a.conversationPageVisible()}
	if view := s.Views[s.Selected]; view != nil {
		out["detail_present"] = view.Detail != nil
		out["detail_error"] = view.ErrorCode
		out["scroll_y"] = view.Scroll.Y
		out["tools"] = view.Tools
		positions := map[string]float32{}
		ranges := map[string]float32{}
		for key, scroll := range view.TraceScroll {
			if scroll.MaxX > 0 {
				ranges[key] = scroll.MaxX
			}
			if scroll.X != 0 {
				positions[key] = scroll.X
			}
		}
		out["trace_scroll_x"] = positions
		out["trace_scroll_max_x"] = ranges
	}
	return out
}
