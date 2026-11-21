package simpledesktop

import (
	"strings"
	"sync"
	"unicode"

	"github.com/egoist/mygo/ui"
)

type inlineMarkdownCacheKey struct {
	raw    string
	size   float32
	color  ui.Color
	codeBg ui.Color
	link   ui.Color
}

type inlineMarkdownCacheEntry struct {
	preview string
	spans   []ui.Span
}

var (
	inlineMarkdownMu    sync.RWMutex
	inlineMarkdownCache = map[inlineMarkdownCacheKey]inlineMarkdownCacheEntry{}
)

// previewMarkdownSource flattens multi-line memory text into a compact preview
// string while preserving inline Markdown (`code`, **bold**, *italic*, ~~del~~,
// [label](url)) and stripping noisy block prefixes (#, -, *, >, ```).
func previewMarkdownSource(s string) string {
	lines := strings.Split(s, "\n")
	parts := make([]string, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		line = stripBlockPrefix(line)
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, " ")
}

func stripBlockPrefix(line string) string {
	for {
		trimmed := strings.TrimLeft(line, " \t")
		switch {
		case strings.HasPrefix(trimmed, "> "):
			line = trimmed[2:]
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ "):
			line = trimmed[2:]
		case strings.HasPrefix(trimmed, "#"):
			i := 0
			for i < len(trimmed) && i < 6 && trimmed[i] == '#' {
				i++
			}
			if i > 0 && i < len(trimmed) && trimmed[i] == ' ' {
				line = trimmed[i+1:]
				continue
			}
			return trimmed
		default:
			return trimmed
		}
	}
}

// parseInlineMarkdown turns a preview string into styled ui.Span runs:
// inline code uses SF Mono with a rounded fill-2 pill, plus bold (**...**),
// italic (*...*), strikethrough (~~...~~), and links ([label](url)).
// Underscores are intentionally left literal so snake_case / __init__ identifiers
// in developer memories are never corrupted.
func parseInlineMarkdown(src string, baseSize float32, textColor ui.Color) []ui.Span {
	if src == "" {
		return nil
	}
	spans := make([]ui.Span, 0, 8)
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			spans = append(spans, ui.Span{Text: plain.String()})
			plain.Reset()
		}
	}
	codeSize := baseSize * 0.9
	for i := 0; i < len(src); {
		switch {
		case src[i] == '`':
			if end := strings.IndexByte(src[i+1:], '`'); end > 0 {
				flush()
				code := src[i+1 : i+1+end]
				spans = append(spans, ui.Span{
					Text:       code,
					Font:       "SF Mono",
					Size:       codeSize,
					Weight:     400,
					Color:      textColor,
					Background: tok.Fill2,
					Radius:     3,
				})
				i += end + 2
				continue
			}
		case strings.HasPrefix(src[i:], "**"):
			if end := strings.Index(src[i+2:], "**"); end > 0 {
				inner := src[i+2 : i+2+end]
				if strings.TrimSpace(inner) != "" && !strings.HasPrefix(inner, " ") && !strings.HasSuffix(inner, " ") {
					flush()
					spans = append(spans, ui.Span{Text: inner, Weight: 600})
					i += end + 4
					continue
				}
			}
		case strings.HasPrefix(src[i:], "~~"):
			if end := strings.Index(src[i+2:], "~~"); end > 0 {
				inner := src[i+2 : i+2+end]
				if strings.TrimSpace(inner) != "" {
					flush()
					spans = append(spans, ui.Span{Text: inner, Strikethrough: true})
					i += end + 4
					continue
				}
			}
		case src[i] == '*' && (i+1 >= len(src) || src[i+1] != '*'):
			if end := findClosingAsterisk(src, i+1); end > i+1 {
				inner := src[i+1 : end]
				if !strings.HasPrefix(inner, " ") && !strings.HasSuffix(inner, " ") {
					flush()
					spans = append(spans, ui.Span{Text: inner, Italic: true})
					i = end + 1
					continue
				}
			}
		case src[i] == '[':
			if label, next, ok := parseInlineLink(src, i); ok {
				flush()
				spans = append(spans, ui.Span{Text: label, Color: tok.Accent})
				i = next
				continue
			}
		}
		plain.WriteByte(src[i])
		i++
	}
	flush()
	return spans
}

func findClosingAsterisk(src string, start int) int {
	for j := start; j < len(src); j++ {
		if src[j] == '`' {
			return -1
		}
		if src[j] == '*' {
			if j+1 < len(src) && src[j+1] == '*' {
				j++
				continue
			}
			if j > start && !unicode.IsSpace(rune(src[j-1])) {
				return j
			}
		}
	}
	return -1
}

func parseInlineLink(src string, start int) (label string, next int, ok bool) {
	closeBracket := strings.IndexByte(src[start+1:], ']')
	if closeBracket <= 0 {
		return "", 0, false
	}
	mid := start + 1 + closeBracket
	if mid+1 >= len(src) || src[mid+1] != '(' {
		return "", 0, false
	}
	closeParen := strings.IndexByte(src[mid+2:], ')')
	if closeParen <= 0 {
		return "", 0, false
	}
	label = src[start+1 : mid]
	url := strings.TrimSpace(src[mid+2 : mid+2+closeParen])
	if label == "" || url == "" || strings.ContainsAny(url, " \t\n") {
		return "", 0, false
	}
	return label, mid + 2 + closeParen + 1, true
}

// v4InlineMarkdown renders a single- or multi-line preview with inline Markdown
// formatting (code pills, bold, italic, strikethrough, link labels).
func v4InlineMarkdown(c *ui.Context, raw string, size float32, weight int, lineHeight float32, color ui.Color) ui.Element {
	key := inlineMarkdownCacheKey{raw: raw, size: size, color: color, codeBg: tok.Fill2, link: tok.Accent}
	inlineMarkdownMu.RLock()
	entry, ok := inlineMarkdownCache[key]
	inlineMarkdownMu.RUnlock()
	if !ok {
		preview := previewMarkdownSource(raw)
		var spans []ui.Span
		if strings.ContainsAny(preview, "`*~[") {
			spans = parseInlineMarkdown(preview, size, color)
		}
		entry = inlineMarkdownCacheEntry{preview: preview, spans: spans}
		inlineMarkdownMu.Lock()
		if len(inlineMarkdownCache) > 512 {
			clear(inlineMarkdownCache)
		}
		inlineMarkdownCache[key] = entry
		inlineMarkdownMu.Unlock()
	}
	if len(entry.spans) == 0 {
		return ui.Text(c, entry.preview).Font("system-ui").FontSize(size).FontWeight(weight).LineHeight(lineHeight).TextColor(color)
	}
	return ui.RichText(c, entry.spans...).Font("system-ui").FontSize(size).FontWeight(weight).LineHeight(lineHeight).TextColor(color)
}
