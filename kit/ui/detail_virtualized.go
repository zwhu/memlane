package ui

import (
	"strings"
	"unicode/utf8"

	mygoui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

const (
	detailVirtualizeMinBytes = 6144
	detailVirtualizeMinLines = 100
	detailChunkTargetBytes   = 1600
	detailChunkTargetLines   = 18
	detailChunkMaxBytes      = 2800
	detailChunkMaxLines      = 28
	detailCodeChunkMaxLines  = 28
	detailTraceInlineBytes   = 3072
	detailTraceInlineLines   = 40
)

func ScrollStateFor(states *map[string]*mygoui.ScrollState, key string) *mygoui.ScrollState {
	if *states == nil {
		*states = map[string]*mygoui.ScrollState{}
	}
	scroll := (*states)[key]
	if scroll == nil {
		scroll = &mygoui.ScrollState{}
		(*states)[key] = scroll
	}
	return scroll
}

func ListStateFor(states *map[string]*mygoui.ListState, key string) *mygoui.ListState {
	if *states == nil {
		*states = map[string]*mygoui.ListState{}
	}
	list := (*states)[key]
	if list == nil {
		list = &mygoui.ListState{}
		(*states)[key] = list
	}
	return list
}

func CountNewlinesUpTo(s string, limit int) int {
	n := 0
	for pos := 0; pos < len(s); {
		idx := strings.IndexByte(s[pos:], '\n')
		if idx < 0 {
			break
		}
		n++
		if n >= limit {
			return n
		}
		pos += idx + 1
	}
	return n
}

func ShouldVirtualizeMarkdown(src string) bool {
	return len(src) > detailVirtualizeMinBytes || CountNewlinesUpTo(src, detailVirtualizeMinLines+1) > detailVirtualizeMinLines
}

func SplitUTF8Chunk(s string, maxBytes int) []string {
	if len(s) <= maxBytes || maxBytes <= 0 {
		return []string{strings.Clone(s)}
	}
	out := make([]string, 0, len(s)/maxBytes+1)
	for len(s) > 0 {
		if len(s) <= maxBytes {
			out = append(out, strings.Clone(s))
			break
		}
		cut := maxBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		if cut == 0 {
			_, size := utf8.DecodeRuneInString(s)
			cut = max(1, size)
		}
		out = append(out, strings.Clone(s[:cut]))
		s = s[cut:]
	}
	return out
}

func parseFenceOpen(line string) (char byte, length int, ok bool) {
	indent := 0
	for indent < len(line) && indent < 3 && line[indent] == ' ' {
		indent++
	}
	if indent < len(line) && line[indent] == '\t' {
		return 0, 0, false
	}
	rest := line[indent:]
	if len(rest) < 3 || (rest[0] != '`' && rest[0] != '~') {
		return 0, 0, false
	}
	ch := rest[0]
	n := 0
	for n < len(rest) && rest[n] == ch {
		n++
	}
	if n < 3 {
		return 0, 0, false
	}
	if ch == '`' && strings.IndexByte(rest[n:], '`') >= 0 {
		return 0, 0, false
	}
	return ch, n, true
}

func isFenceClose(line string, fenceChar byte, fenceLen int) bool {
	indent := 0
	for indent < len(line) && indent < 3 && line[indent] == ' ' {
		indent++
	}
	if indent < len(line) && line[indent] == '\t' {
		return false
	}
	rest := line[indent:]
	n := 0
	for n < len(rest) && rest[n] == fenceChar {
		n++
	}
	if n < fenceLen {
		return false
	}
	for i := n; i < len(rest); i++ {
		if rest[i] != ' ' && rest[i] != '\t' {
			return false
		}
	}
	return true
}

func isATXHeading(line string) bool {
	indent := 0
	for indent < len(line) && indent < 3 && line[indent] == ' ' {
		indent++
	}
	rest := line[indent:]
	n := 0
	for n < len(rest) && n < 6 && rest[n] == '#' {
		n++
	}
	if n == 0 {
		return false
	}
	return n == len(rest) || rest[n] == ' ' || rest[n] == '\t'
}

// SplitMarkdownChunks splits large Markdown text into renderable chunks without
// breaking fenced code blocks, tables, or UTF-8 rune boundaries.
func SplitMarkdownChunks(src string) []string {
	if !ShouldVirtualizeMarkdown(src) {
		return []string{src}
	}
	var chunks []string
	var buf strings.Builder
	bufLines := 0
	inFence := false
	pendingFenceHeader := false
	var fenceChar byte
	fenceLen := 0
	fenceHeader := ""

	appendLine := func(line string) {
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(line)
		bufLines++
	}
	flushPlain := func() {
		if buf.Len() > 0 {
			chunks = append(chunks, buf.String())
			buf.Reset()
		}
		bufLines = 0
	}
	flushOpenFence := func() {
		if buf.Len() > 0 {
			buf.WriteByte('\n')
			buf.WriteString(strings.Repeat(string(fenceChar), fenceLen))
			chunks = append(chunks, buf.String())
			buf.Reset()
		}
		bufLines = 0
		pendingFenceHeader = true
	}

	for pos := 0; pos < len(src); {
		nl := strings.IndexByte(src[pos:], '\n')
		var rawLine string
		if nl >= 0 {
			rawLine = src[pos : pos+nl]
			pos += nl + 1
		} else {
			rawLine = src[pos:]
			pos = len(src)
		}
		line := strings.TrimSuffix(rawLine, "\r")

		if inFence {
			if isFenceClose(line, fenceChar, fenceLen) {
				if pendingFenceHeader {
					pendingFenceHeader = false
					inFence = false
					fenceHeader = ""
					continue
				}
				appendLine(line)
				inFence = false
				fenceHeader = ""
				if bufLines >= detailChunkTargetLines || buf.Len() >= detailChunkTargetBytes {
					flushPlain()
				}
				continue
			}
			if pendingFenceHeader {
				appendLine(fenceHeader)
				pendingFenceHeader = false
			}
			if len(line) > detailChunkMaxBytes {
				if bufLines > 1 {
					flushOpenFence()
				} else {
					buf.Reset()
					bufLines = 0
				}
				closeFence := strings.Repeat(string(fenceChar), fenceLen)
				for _, part := range SplitUTF8Chunk(line, detailChunkMaxBytes) {
					chunks = append(chunks, fenceHeader+"\n"+part+"\n"+closeFence)
				}
				pendingFenceHeader = true
				continue
			}
			if bufLines > 1 && (bufLines >= detailCodeChunkMaxLines || buf.Len()+1+len(line) > detailChunkMaxBytes) {
				flushOpenFence()
				appendLine(fenceHeader)
				pendingFenceHeader = false
			}
			appendLine(line)
			continue
		}

		if ch, flen, ok := parseFenceOpen(line); ok {
			if buf.Len() > 0 && (bufLines >= detailChunkTargetLines || buf.Len() >= detailChunkTargetBytes) {
				flushPlain()
			}
			inFence = true
			pendingFenceHeader = false
			fenceChar = ch
			fenceLen = flen
			fenceHeader = line
			appendLine(line)
			continue
		}

		if len(line) > detailChunkMaxBytes {
			flushPlain()
			for _, part := range SplitUTF8Chunk(line, detailChunkMaxBytes) {
				chunks = append(chunks, part)
			}
			continue
		}

		trimmed := strings.TrimSpace(line)
		isBlank := trimmed == ""
		isTable := strings.Contains(trimmed, "|")
		if buf.Len() > 0 {
			switch {
			case isATXHeading(line) && bufLines >= 12 && !isTable:
				flushPlain()
			case isTable && bufLines < detailChunkMaxLines && buf.Len()+1+len(line) <= detailChunkMaxBytes:
				// Keep consecutive Markdown table rows together until hard limit.
			case bufLines >= detailChunkMaxLines || buf.Len()+1+len(line) > detailChunkMaxBytes:
				flushPlain()
			}
		}

		appendLine(line)
		if isBlank && (bufLines >= detailChunkTargetLines || buf.Len() >= detailChunkTargetBytes) {
			flushPlain()
		}
	}

	if buf.Len() > 0 {
		if inFence {
			buf.WriteByte('\n')
			buf.WriteString(strings.Repeat(string(fenceChar), fenceLen))
		}
		chunks = append(chunks, buf.String())
	}
	if len(chunks) == 0 {
		return []string{src}
	}
	return chunks
}

func AppendCodeFenceChunks(out *[]string, lang, content string) {
	if content == "" {
		*out = append(*out, "```"+lang+"\n\n```")
		return
	}
	var buf strings.Builder
	lines := 0
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		*out = append(*out, "```"+lang+"\n"+buf.String()+"\n```")
		buf.Reset()
		lines = 0
	}
	for pos := 0; pos < len(content); {
		nl := strings.IndexByte(content[pos:], '\n')
		var line string
		if nl >= 0 {
			line = content[pos : pos+nl]
			pos += nl + 1
		} else {
			line = content[pos:]
			pos = len(content)
		}
		line = strings.TrimSuffix(line, "\r")
		if len(line) > detailChunkTargetBytes {
			flush()
			for _, part := range SplitUTF8Chunk(line, detailChunkTargetBytes) {
				*out = append(*out, "```"+lang+"\n"+part+"\n```")
			}
			continue
		}
		if buf.Len() > 0 && (lines >= detailCodeChunkMaxLines || buf.Len()+1+len(line) > detailChunkMaxBytes) {
			flush()
		}
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(line)
		lines++
	}
	flush()
}

func SplitJSONTraceChunks(paramsJSON, resultJSON []byte, errText string) []string {
	params := string(paramsJSON)
	result := string(resultJSON)
	totalBytes := len(params) + len(result) + len(errText)
	if totalBytes <= detailTraceInlineBytes {
		totalLines := CountNewlinesUpTo(params, detailTraceInlineLines+1) +
			CountNewlinesUpTo(result, detailTraceInlineLines+1) +
			CountNewlinesUpTo(errText, detailTraceInlineLines+1)
		if totalLines <= detailTraceInlineLines {
			return []string{"```json\n" + params + "\n```\n\n```json\n" + result + "\n```\n\n" + errText}
		}
	}
	var chunks []string
	AppendCodeFenceChunks(&chunks, "json", params)
	AppendCodeFenceChunks(&chunks, "json", result)
	if strings.TrimSpace(errText) != "" {
		chunks = append(chunks, SplitMarkdownChunks(errText)...)
	}
	if len(chunks) == 0 {
		return []string{""}
	}
	return chunks
}

func IsFenceSplitBoundary(prev, next string) bool {
	return (strings.HasPrefix(next, "```") && strings.HasSuffix(prev, "```")) ||
		(strings.HasPrefix(next, "~~~") && strings.HasSuffix(prev, "~~~"))
}

type VirtualizedMarkdownOptions struct {
	Cache             *ChunkCache
	Markdown          *markdown.Options
	Dataset, ID, Slot string
	MaxWidth          float32
}

func ResolveMarkdownChunks(src string, opts VirtualizedMarkdownOptions) []string {
	if !ShouldVirtualizeMarkdown(src) {
		return []string{src}
	}
	if opts.Cache == nil {
		return SplitMarkdownChunks(src)
	}
	return opts.Cache.GetOrSplitMarkdown(opts.Dataset, opts.ID, opts.Slot, src)
}

func VirtualizedList(c *mygoui.Context, key string, listState *mygoui.ListState, scroll *mygoui.ScrollState, count int, keyFn func(int) any, renderRow func(mygoui.ListRow)) mygoui.Element {
	listState.Key = keyFn
	return mygoui.List(c.Key(key), listState, count).
		TrackScroll(scroll).
		Padding(Sp3, Sp10, Sp8, Sp10).Gap(0).
		Grow(1).MinHeight(0).FillWidth().
		Rows(renderRow)
}

func MarkdownChunkRow(c *mygoui.Context, key, text string, mdOpts *markdown.Options, maxWidth, padBottom float32) mygoui.Element {
	return mygoui.Column(c).FillWidth().MaxWidth(maxWidth).Padding(0, 0, padBottom, 0).Children(func() {
		markdown.View(c.Key(key), text, mdOpts)
	})
}

func RenderVirtualizedMarkdownList(c *mygoui.Context, key string, listState *mygoui.ListState, scroll *mygoui.ScrollState, src string, opts VirtualizedMarkdownOptions) mygoui.Element {
	chunks := ResolveMarkdownChunks(src, opts)
	maxWidth := opts.MaxWidth
	if maxWidth <= 0 {
		maxWidth = ReaderMaxW
	}
	return VirtualizedList(c, key, listState, scroll, len(chunks), func(index int) any { return index }, func(row mygoui.ListRow) {
		index := row.Index
		padBottom := Sp2 + 2
		if index+1 == len(chunks) {
			padBottom = 0
		} else if IsFenceSplitBoundary(chunks[index], chunks[index+1]) {
			padBottom = Sp1
		}
		MarkdownChunkRow(row.Context, "markdown", chunks[index], opts.Markdown, maxWidth, padBottom)
	})
}
