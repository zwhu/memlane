package simpledesktop

import (
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Presentation is derived only. Body always remains the official raw bytes.
type Presentation struct {
	DisplayTitle, VisibleBody string
	WrapperRecognized         bool
	Unrecognized              bool
	TechnicalFields           []ReadingField
}
type ReadingField struct{ Name, Value string }

var machineName = regexp.MustCompile(`^(?:text_[0-9a-fA-F]{32}(?:\.(?:md|txt))?|(?:user_context|memory)-[0-9a-fA-F]{32}(?:\.txt)?|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$`)
var imageOnly = regexp.MustCompile(`^!\[[^\]]*\]\([^)]*\)$`)

func readableName(s string) string {
	s = strings.TrimSpace(s)
	if machineName.MatchString(s) {
		return ""
	}
	return s
}
func readableDocumentTitle(d Document) string {
	if d.Label != nil {
		if s := readableName(*d.Label); s != "" {
			return s
		}
	}
	return readableName(d.Name)
}
func knowledgePlaceholder(date string) string {
	if t, ok := officialTime(date); ok {
		return "记忆 · " + t.In(time.Local).Format("2006-01-02")
	}
	return "记忆"
}
func uniqueLearningSession(d Document) string {
	var source string
	for _, s := range nodeSets(d) {
		if !strings.HasPrefix(s, "session_learnings:") {
			continue
		}
		id := strings.TrimPrefix(s, "session_learnings:")
		if id == "" || (source != "" && id != source) {
			return ""
		}
		source = id
	}
	if id, _ := d.Metadata["session_id"].(string); id != "" && id != source {
		return ""
	}
	return source
}
func presentKnowledge(d Document, raw string) Presentation {
	p := Presentation{DisplayTitle: readableDocumentTitle(d), VisibleBody: raw}
	p.TechnicalFields = []ReadingField{{"官方名称", d.Name}, {"数据组 ID", d.Dataset}, {"文档 ID", d.ID}, {"原始更新时间", d.Updated}, {"入库时间", d.Created}}
	if d.knowledge() {
		if day, ok := d.knowledgeDay(time.Local); ok {
			p.TechnicalFields = append(p.TechnicalFields, ReadingField{"来源日期", day})
		}
	}
	if d.Label != nil {
		p.TechnicalFields = append(p.TechnicalFields, ReadingField{"官方 label", *d.Label})
	}
	if category := commonCategory(d); category != "" || d.directMemory() {
		// A managed common document is ordinary Markdown with explicit metadata;
		// it is not a malformed session learning and has no wrapper to remove.
		if d.directMemory() {
			p.TechnicalFields = append(p.TechnicalFields, ReadingField{"内容类型", "直接保存的记忆"})
		} else {
			p.TechnicalFields = append(p.TechnicalFields, ReadingField{"内容类型", "通用记忆"}, ReadingField{"分类", category})
		}
		if source, _ := d.Metadata["source"].(string); source != "" {
			p.TechnicalFields = append(p.TechnicalFields, ReadingField{"原始来源", source})
		}
		if p.DisplayTitle == "" {
			p.DisplayTitle = firstReadingSentence(raw)
		}
		if p.DisplayTitle == "" {
			p.DisplayTitle = knowledgePlaceholder(d.date())
		}
		return p
	}
	for _, key := range []string{"session_id", "lesson_type", "state_key"} {
		if s, _ := d.Metadata[key].(string); s != "" {
			p.TechnicalFields = append(p.TechnicalFields, ReadingField{key, s})
		}
	}
	if d.knowledge() {
		source := uniqueLearningSession(d)
		if source != "" {
			p.TechnicalFields = append(p.TechnicalFields, ReadingField{"来源会话 ID", source})
		}
		// Match only the exact fixed prefix, including optional typed metadata.
		lines := strings.SplitAfter(raw, "\n")
		n := 0
		for n < len(lines) && strings.TrimSpace(lines[n]) == "" {
			n++
		}
		seen := map[string]bool{}
		valid := source != ""
		for n < len(lines) {
			line := strings.TrimSuffix(strings.TrimSuffix(lines[n], "\n"), "\r")
			field, value, found := strings.Cut(line, ": ")
			key := map[string]string{"Lesson-Type": "lesson_type", "State-Key": "state_key"}[field]
			if !found || key == "" {
				break
			}
			if seen[key] || value == "" {
				valid = false
			}
			seen[key] = true
			if meta, exists := d.Metadata[key]; exists && meta != value {
				valid = false
			}
			p.TechnicalFields = append(p.TechnicalFields, ReadingField{field, value})
			n++
		}
		for n < len(lines) && strings.TrimSpace(lines[n]) == "" {
			n++
		}
		if valid && n < len(lines) && strings.TrimRight(lines[n], "\r\n") == "# Session learning (session "+source+")" {
			n++
			for n < len(lines) && strings.TrimSpace(lines[n]) == "" {
				n++
			}
			p.VisibleBody = strings.Join(lines[n:], "")
			p.WrapperRecognized = true
		} else if raw != "" {
			p.Unrecognized = true
		}
	}
	if p.DisplayTitle == "" && !p.Unrecognized {
		p.DisplayTitle = firstReadingSentence(p.VisibleBody)
	}
	if p.DisplayTitle == "" {
		p.DisplayTitle = knowledgePlaceholder(d.date())
	}
	return p
}

func readingSourceLabel(i Item) string {
	label := "来源会话"
	if i.SourceDocument != "" {
		return "来源文档"
	}
	if i.Document != nil {
		if date, _ := i.Document.Metadata["session_date"].(string); date != "" {
			if t, err := time.Parse(time.RFC3339Nano, date); err == nil {
				return label + " · " + t.In(time.Local).Format("2006-01-02 15:04")
			}
			if t, err := time.Parse("2006-01-02", date); err == nil {
				return label + " · " + t.Format("2006-01-02")
			}
		}
	}
	return label
}

func firstReadingSentence(body string) string {
	return firstReadingSentenceLimit(body, 80)
}

func firstReadingSentenceLimit(body string, limit int) string {
	var paragraph []string
	fence := ""
	for _, line := range strings.Split(body, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "```") || strings.HasPrefix(s, "~~~") {
			mark := s[:3]
			if fence == "" {
				fence = mark
			} else if fence == mark {
				fence = ""
			}
			continue
		}
		if fence != "" || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			continue
		}
		if s == "" {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		if imageOnly.MatchString(s) {
			continue
		}
		s = strings.TrimLeft(s, "#>*-+ ")
		if s != "" {
			paragraph = append(paragraph, s)
		}
	}
	runes := []rune(strings.Join(strings.Fields(strings.Join(paragraph, " ")), " "))
	end := len(runes)
	for n, r := range runes {
		if strings.ContainsRune("。！？", r) || ((r == '.' || r == '!' || r == '?') && (n+1 == len(runes) || unicode.IsSpace(runes[n+1]))) {
			end = n + 1
			break
		}
	}
	runes = runes[:end]
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return string(runes)
}

func itemPresentation(i Item) Presentation {
	if i.Document != nil && i.Kind == "知识" {
		return presentKnowledge(*i.Document, i.Body)
	}
	p := Presentation{DisplayTitle: i.Title, VisibleBody: i.Body}
	if i.Kind == "知识片段" || i.Kind == "原始材料片段" {
		if readableName(p.DisplayTitle) == "" {
			start := strings.TrimSpace(i.Body)
			if strings.HasPrefix(start, "Lesson-Type:") || strings.HasPrefix(start, "State-Key:") || strings.HasPrefix(start, "# Session learning (session ") {
				p.DisplayTitle = "检索片段"
			} else {
				p.DisplayTitle = firstReadingSentence(i.Body)
			}
		}
		if p.DisplayTitle == "" {
			p.DisplayTitle = "检索片段"
		}
	}
	if i.Kind == "会话" && (i.Title == i.ID || readableName(i.Title) == "") {
		p.DisplayTitle = "来源会话"
	}
	if i.Session != nil {
		p.TechnicalFields = []ReadingField{{"会话 ID", i.ID}, {"数据组 ID", i.Dataset}, {"所属主体 ID", i.Session.User}, {"最近活动时间", i.Date}, {"服务端首次记录时间", i.Session.Started}}
		if i.Session.IsSubagent {
			role := subagentRoleLabel(i.Session)
			if role == "" {
				role = "未命名角色"
			}
			p.TechnicalFields = append(p.TechnicalFields, ReadingField{"子会话角色", role})
			if i.Session.ParentSessionID != "" {
				p.TechnicalFields = append(p.TechnicalFields, ReadingField{"所属主会话", i.Session.ParentSessionID})
			}
		}
	}
	return p
}

func readingUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if status, ok := httpStatusCode(err); ok && (status == 401 || status == 403 || status == 404) {
		return true
	}
	s := err.Error()
	for _, reason := range []string{"identity changed", "dataset changed", "credential unavailable"} {
		if strings.Contains(s, reason) {
			return true
		}
	}
	return false
}

// memoryKindLabel changes system labels only; stored content and route keys stay intact.
func memoryKindLabel(kind string) string {
	switch kind {
	case "知识":
		return "记忆"
	case "知识片段":
		return "记忆片段"
	case "通用知识":
		return "通用记忆"
	}
	return kind
}
