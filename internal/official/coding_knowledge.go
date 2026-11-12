package official

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/zwhu/memlane/internal/records"
)

// CodingLessonInfo describes a matching upstream rendering, not its author,
// trust, permissions, or whether the lesson is factually correct.
type CodingLessonInfo struct {
	LessonType, StateKey, SessionDate, GitCommit, GitBranch string
	Files                                                   []string
}

func codingLessonEnvelope(metadata json.RawMessage, text, session string) (CodingLessonInfo, string, bool) {
	var fields struct {
		LessonType string `json:"lesson_type"`
		StateKey   string `json:"state_key"`
		SessionID  string `json:"session_id"`
	}
	if len(metadata) > 64*1024 || json.Unmarshal(metadata, &fields) != nil || fields.SessionID != session || session == "" {
		return CodingLessonInfo{}, "", false
	}
	if fields.LessonType == "" || len(fields.LessonType) > 32 || strings.ContainsFunc(fields.LessonType, func(r rune) bool {
		return r != '_' && (r < 'a' || r > 'z')
	}) || !boundedLessonField(fields.StateKey, 512) {
		return CodingLessonInfo{}, "", false
	}
	prefix := "Lesson-Type: " + fields.LessonType + "\n"
	if fields.StateKey != "" {
		prefix += "State-Key: " + fields.StateKey + "\n"
	}
	prefix += "# Session learning (session " + session + ")\n\n"
	if !strings.HasPrefix(text, prefix) {
		return CodingLessonInfo{}, "", false
	}
	info := CodingLessonInfo{LessonType: fields.LessonType, StateKey: fields.StateKey}
	var values map[string]json.RawMessage
	_ = json.Unmarshal(metadata, &values)
	read := func(key string, max int) string {
		var value string
		if json.Unmarshal(values[key], &value) != nil || !boundedLessonField(value, max) {
			return ""
		}
		return value
	}
	info.SessionDate, info.GitCommit, info.GitBranch = read("session_date", 64), read("git_commit", 128), read("git_branch", 256)
	var files []string
	if json.Unmarshal(values["files"], &files) == nil && len(files) <= 128 {
		valid := true
		for _, file := range files {
			valid = valid && file != "" && boundedLessonField(file, 512)
		}
		if valid {
			info.Files = files
		}
	}
	return info, strings.TrimPrefix(text, prefix), true
}

func boundedLessonField(value string, max int) bool {
	return len(value) <= max && !strings.ContainsFunc(value, unicode.IsControl)
}

// CodingLesson projects metadata already carried by the official document.
// The session reference must first have matched node sets and rendering.
func CodingLesson(content records.OfficialDocumentText) (CodingLessonInfo, bool) {
	if content.SessionReference == "" || documentSessionReference(content.Document.Metadata, content.Text) != content.SessionReference {
		return CodingLessonInfo{}, false
	}
	info, _, ok := codingLessonEnvelope(content.Document.Metadata, content.Text, content.SessionReference)
	return info, ok
}
