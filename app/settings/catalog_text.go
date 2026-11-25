package settings

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/zwhu/memlane/app/api"
)

func knowledgePlaceholder(date string) string {
	if t, ok := api.OfficialTime(date); ok {
		return "记忆 · " + t.In(time.Local).Format("2006-01-02")
	}
	return "记忆"
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
