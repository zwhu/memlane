package simpledesktop

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func lessonDocument() Document {
	return Document{ID: "doc", Dataset: "dataset", Name: "text_0123456789abcdef0123456789abcdef", Updated: "2026-11-13T00:00:00Z", Metadata: map[string]any{"node_set": []any{"session_learnings", "session_learnings:s"}, "session_id": "s", "lesson_type": "pitfall", "state_key": "k"}}
}
func TestReadingPresentationStrictWrapperAndRaw(t *testing.T) {
	d := lessonDocument()
	raw := "Lesson-Type: pitfall\nState-Key: k\n# Session learning (session s)\n\nKeep docs/development.md intact. Next sentence.\n\nLesson-Type: actual user content\n```go\nx := 1\n```\n"
	p := presentKnowledge(d, raw)
	want := strings.SplitN(raw, "\n\n", 2)[1]
	if !p.WrapperRecognized || p.VisibleBody != want || p.DisplayTitle != "Keep docs/development.md intact." {
		t.Fatalf("unexpected presentation: %+v", p)
	}
	i := Item{Kind: "知识", Document: &d, Body: raw}
	if itemPresentation(i).VisibleBody == i.Body || i.Body != raw {
		t.Fatal("visible/raw copy contract broken")
	}
	for _, mutate := range []func(*Document, *string){
		func(d *Document, raw *string) { d.Metadata["session_id"] = "other" },
		func(d *Document, raw *string) {
			d.Metadata["node_set"] = []any{"session_learnings", "session_learnings:s", "session_learnings:other"}
		},
		func(d *Document, raw *string) {
			*raw = strings.Replace(*raw, "State-Key: k", "State-Key: different", 1)
		},
		func(d *Document, raw *string) { *raw = "Lesson-Type: pitfall\n" + *raw },
		func(d *Document, raw *string) {
			*raw = strings.Replace(*raw, "# Session learning", "## Session learning", 1)
		},
		func(d *Document, raw *string) { d.Metadata["node_set"] = []any{"session_learnings"} },
	} {
		d := lessonDocument()
		copy := raw
		mutate(&d, &copy)
		p := presentKnowledge(d, copy)
		if p.WrapperRecognized || p.VisibleBody != copy || !p.Unrecognized || !strings.HasPrefix(p.DisplayTitle, "知识") {
			t.Fatalf("unsafe cleanup: %+v", p)
		}
	}
	d = lessonDocument()
	d.Metadata = map[string]any{}
	if p := presentKnowledge(d, raw); p.VisibleBody != raw || p.WrapperRecognized {
		t.Fatal("ordinary document stripped")
	}
}
func TestReadingTitlePriorityAndUnicode(t *testing.T) {
	d := lessonDocument()
	label := "Readable label"
	d.Label = &label
	if p := presentKnowledge(d, ""); p.DisplayTitle != label {
		t.Fatal(p)
	}
	label = d.Name
	d.Name = "Unknown technical-looking name.txt"
	if p := presentKnowledge(d, ""); p.DisplayTitle != d.Name {
		t.Fatal(p)
	}
	for _, name := range []string{"text_0123456789abcdef0123456789abcdef.md", "6f3dc3b3-0a4b-419c-ab4d-aba7e3e1e980"} {
		if readableName(name) != "" {
			t.Fatal(name)
		}
	}
	for _, name := range []string{"text_short", "text_0123456789abcdef0123456789abcdef.json", "An English title"} {
		if readableName(name) != name {
			t.Fatal(name)
		}
	}
	cases := map[string]string{
		"# 中文第一句。第二句":                                                    "中文第一句。",
		"docs/development.md documents config/daily-runtime.json. More.": "docs/development.md documents config/daily-runtime.json.",
		"```go\nx:=1\n```\n\nEnglish sentence! Next":                     "English sentence!",
		"```\nx\n```": "",
		"  \n\n":      "",
		"![image](x)": "",
	}
	for raw, want := range cases {
		if got := firstReadingSentence(raw); got != want {
			t.Errorf("%q => %q want %q", raw, got, want)
		}
	}
	got := firstReadingSentence(strings.Repeat("中", 100))
	if utf8.RuneCountInString(got) != 81 || !strings.HasSuffix(got, "…") {
		t.Fatal(got)
	}
}

func TestReadingSearchSnippetRemainsPartial(t *testing.T) {
	i := Item{ID: "chunk", Kind: "知识片段", Title: lessonDocument().Name, Body: "A partial passage. Remaining fragment", Preview: "A partial passage.", SourceDocument: "real-document"}
	p := itemPresentation(i)
	if p.DisplayTitle != "A partial passage." || p.VisibleBody != i.Body || i.Document != nil {
		t.Fatal("snippet treated as full document")
	}
	i.Body = "Lesson-Type: state\n# Session learning (session opaque)\n\nUnknown fragment"
	if p := itemPresentation(i); p.DisplayTitle != "检索片段" || p.VisibleBody != i.Body {
		t.Fatal("fragment wrapper mistaken for readable title or stripped")
	}
}
