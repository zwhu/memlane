package official

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

func TestCodingKnowledgeActualOfficialEnvelope(t *testing.T) {
	dir := filepath.Join("testdata", "coding-memory-ec6f408")
	raw, err := os.ReadFile(filepath.Join(dir, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []records.OfficialDocument
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) != 2 {
		t.Fatal("actual sample catalog unavailable", err)
	}
	for _, row := range rows {
		text, err := os.ReadFile(filepath.Join(dir, row.ID+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		session := documentSessionReference(row.Metadata, string(text))
		if session != "codex_01a1ce9c-2a72-7983-8a63-c1f6f488a656" {
			t.Fatalf("actual coding lesson has no readable source: %s", row.ID)
		}
		content := records.OfficialDocumentText{Document: row, Text: string(text), SessionReference: session}
		body := LearningBody(content)
		if len(body) < 32 || !strings.HasSuffix(string(text), body) || strings.Contains(body, "Lesson-Type:") || strings.Contains(body, "State-Key:") || strings.Contains(body, "# Session learning") {
			t.Fatal("coding envelope leaked into body")
		}
		title, fallback := learningTitle(content)
		if !fallback || strings.HasPrefix(title, "Lesson-Type:") || !strings.HasPrefix(strings.TrimSpace(body), strings.TrimSuffix(title, "…")) {
			t.Fatal("title was not derived from actual statement", title)
		}
		info, ok := CodingLesson(content)
		if !ok || info.LessonType != "state" || info.StateKey == "" || info.SessionDate != "2026-11-12" || info.GitBranch != "main" || len(info.GitCommit) != 40 || len(info.Files) == 0 {
			t.Fatal("actual metadata not projected", info, ok)
		}
	}
}

func TestCodingKnowledgeRequiresMatchingRenderingAndMetadata(t *testing.T) {
	for _, mode := range []string{"state", "pitfall", "change", "future_type", "missing_kind", "wrong_kind", "wrong_key", "wrong_session", "missing_source", "ambiguous_source", "extra_header", "literal_user_text", "invalid_optional_fields"} {
		t.Run(mode, func(t *testing.T) {
			kind, key := "state", "test-baseline"
			if mode == "pitfall" || mode == "change" || mode == "future_type" {
				kind = mode
			}
			if kind == "change" || kind == "future_type" {
				key = ""
			}
			fields := map[string]any{"node_set": []string{"session_learnings", "session_learnings:source-session", "project"}, "lesson_type": kind, "state_key": key, "session_id": "source-session", "git_commit": "known-commit", "files": []string{"inventory.py"}}
			body := "Keep the atomic stock update.\n\nLiteral State-Key: inside the body stays.\n"
			text := "Lesson-Type: " + kind + "\n"
			if key != "" {
				text += "State-Key: " + key + "\n"
			}
			text += "# Session learning (session source-session)\n\n" + body
			want := mode == "state" || mode == "pitfall" || mode == "change" || mode == "future_type" || mode == "invalid_optional_fields"
			switch mode {
			case "missing_kind":
				delete(fields, "lesson_type")
			case "wrong_kind":
				fields["lesson_type"] = "change"
			case "wrong_key":
				fields["state_key"] = "another-key"
			case "wrong_session":
				fields["session_id"] = "another-session"
			case "missing_source":
				fields["node_set"] = []string{"session_learnings"}
			case "ambiguous_source":
				fields["node_set"] = []string{"session_learnings", "session_learnings:source-session", "session_learnings:other-session"}
			case "extra_header":
				text = "Lesson-Type: state\n" + text
			case "literal_user_text":
				text = "A user quoted:\n" + text
			case "invalid_optional_fields":
				fields["git_commit"], fields["files"] = "invalid\ncommit", []any{"inventory.py", 3}
			}
			metadata, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			ref := documentSessionReference(metadata, text)
			content := records.OfficialDocumentText{Document: records.OfficialDocument{Metadata: metadata}, Text: text, SessionReference: ref}
			info, ok := CodingLesson(content)
			if ok != want || (ref != "") != want {
				t.Fatal("mismatched coding source accepted or valid rendering rejected", ref, ok)
			}
			if want {
				if LearningBody(content) != body || info.LessonType != kind || info.StateKey != key {
					t.Fatal("body or classification changed")
				}
				if mode == "invalid_optional_fields" && (info.GitCommit != "" || info.Files != nil) {
					t.Fatal("malformed auxiliary fields displayed")
				}
			} else if LearningBody(content) != text {
				t.Fatal("unrecognized user text was stripped")
			}
		})
	}
}
