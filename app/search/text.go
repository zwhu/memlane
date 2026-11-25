package search

import (
	"github.com/egoist/mygo/ui"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var searchWrapperPrefixRe = regexp.MustCompile(`^(?:Lesson-Type:\s*\S+|State-Key:\s*\S+|(?:#\s*)?Session learning \(session [^\s)]+\)|Session ID:\s*\S+)\s*`)

func StripWrapperPrefixes(s string) string {
	out := strings.TrimSpace(s)
	for {
		next := strings.TrimSpace(searchWrapperPrefixRe.ReplaceAllString(out, ""))
		if next == out {
			return out
		}
		out = next
	}
}

func LooksLikeBareUUID(s string) bool {
	v := strings.TrimSpace(s)
	if len(v) < 32 || len(v) > 40 || strings.ContainsAny(v, " \t\n") {
		return false
	}
	hexCount := 0
	for _, r := range v {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			hexCount++
		} else if r != '-' {
			return false
		}
	}
	return hexCount >= 32 && utf8.RuneCountInString(v) == len(v)
}

// foldText gives simple Unicode case equivalents one representation, including
// forms such as the two lowercase Greek sigmas that ToLower leaves distinct.
func foldText(text string) string {
	return strings.Map(func(r rune) rune {
		if r < utf8.RuneSelf {
			return unicode.ToLower(r)
		}
		first := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			first = min(first, next)
		}
		return unicode.ToLower(first)
	}, text)
}

func queryTerms(query string) []string { return strings.Fields(foldText(query)) }

func KeywordMatch(title, body, query string) bool {
	terms := queryTerms(query)
	if len(terms) == 0 {
		return false
	}
	text := foldText(title + "\n" + body)
	for _, term := range terms {
		if !strings.Contains(text, term) {
			return false
		}
	}
	return true
}

func HitExcerpt(preview, query string) string {
	folded := foldText(preview)
	for _, term := range queryTerms(query) {
		start := strings.Index(folded, term)
		if start < 0 {
			continue
		}
		runes := []rune(preview)
		hit := utf8.RuneCountInString(folded[:start])
		from := max(0, hit-50)
		to := min(len(runes), hit+utf8.RuneCountInString(term)+130)
		text := string(runes[from:to])
		if from > 0 {
			text = "…" + text
		}
		if to < len(runes) {
			text += "…"
		}
		return text
	}
	return preview
}

// HighlightSpans retains the original text and style at each rune boundary.
func HighlightSpans(sp ui.Span, lowerTerms []string, markBg ui.Color) []ui.Span {
	if sp.Text == "" || len(lowerTerms) == 0 {
		return []ui.Span{sp}
	}
	source, folded := []rune(sp.Text), []rune(foldText(sp.Text))
	terms := make([][]rune, 0, len(lowerTerms))
	for _, term := range lowerTerms {
		if term != "" {
			terms = append(terms, []rune(foldText(term)))
		}
	}
	var out []ui.Span
	start := 0
	for pos := 0; pos < len(folded); {
		longest := 0
		for _, term := range terms {
			if n := len(term); n > longest && pos+n <= len(folded) && slices.Equal(folded[pos:pos+n], term) {
				longest = n
			}
		}
		if longest == 0 {
			pos++
			continue
		}
		if start < pos {
			unmarked := sp
			unmarked.Text = string(source[start:pos])
			out = append(out, unmarked)
		}
		hit := sp
		hit.Text, hit.Background = string(source[pos:pos+longest]), markBg
		if hit.Radius == 0 {
			hit.Radius = 2
		}
		out = append(out, hit)
		pos += longest
		start = pos
	}
	if len(out) == 0 {
		return []ui.Span{sp}
	}
	if start < len(source) {
		sp.Text = string(source[start:])
		out = append(out, sp)
	}
	return out
}
