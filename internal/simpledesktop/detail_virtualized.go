package simpledesktop

import (
	"fmt"
	"hash/maphash"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

const (
	detailVirtualizeMinBytes   = 6144
	detailVirtualizeMinLines   = 100
	detailChunkTargetBytes     = 1600
	detailChunkTargetLines     = 18
	detailChunkMaxBytes        = 2800
	detailChunkMaxLines        = 28
	detailCodeChunkMaxLines    = 28
	detailTraceInlineBytes     = 3072
	detailTraceInlineLines     = 40
	maxDetailChunkCacheEntries = 64
	maxDetailChunkCacheBytes   = 8 * 1024 * 1024
)

var (
	detailChunkSeed1 = maphash.MakeSeed()
	detailChunkSeed2 = maphash.MakeSeed()
)

type detailContentKey struct {
	len1, len2, len3 int
	h1, h2           uint64
}

func markdownContentKey(src string) detailContentKey {
	var h1, h2 maphash.Hash
	h1.SetSeed(detailChunkSeed1)
	h2.SetSeed(detailChunkSeed2)
	_, _ = h1.WriteString(src)
	_, _ = h2.WriteString(src)
	return detailContentKey{len1: len(src), h1: h1.Sum64(), h2: h2.Sum64()}
}

func traceContentKey(t Trace) detailContentKey {
	var h1, h2 maphash.Hash
	h1.SetSeed(detailChunkSeed1)
	h2.SetSeed(detailChunkSeed2)
	_, _ = h1.Write(t.Params)
	_ = h1.WriteByte(0)
	_, _ = h1.Write(t.Result)
	_ = h1.WriteByte(0)
	_, _ = h1.WriteString(t.Error)
	_, _ = h2.Write(t.Params)
	_ = h2.WriteByte(0)
	_, _ = h2.Write(t.Result)
	_ = h2.WriteByte(0)
	_, _ = h2.WriteString(t.Error)
	return detailContentKey{
		len1: len(t.Params),
		len2: len(t.Result),
		len3: len(t.Error),
		h1:   h1.Sum64(),
		h2:   h2.Sum64(),
	}
}

func detailChunksBytes(chunks []string) int {
	total := 0
	for _, ch := range chunks {
		total += len(ch)
	}
	return total
}

type detailChunkEntry struct {
	dataset string
	id      string
	slot    string
	content detailContentKey
	chunks  []string
	bytes   int
	stamp   uint64
}

type detailChunkCache struct {
	mu         sync.Mutex
	entries    []detailChunkEntry
	totalBytes int
	maxEntries int
	maxBytes   int
	clock      uint64
}

func newDetailChunkCache() *detailChunkCache {
	return &detailChunkCache{
		maxEntries: maxDetailChunkCacheEntries,
		maxBytes:   maxDetailChunkCacheBytes,
	}
}

func (c *detailChunkCache) limits() (maxEntries, maxBytes int) {
	maxEntries = c.maxEntries
	if maxEntries <= 0 {
		maxEntries = maxDetailChunkCacheEntries
	}
	maxBytes = c.maxBytes
	if maxBytes <= 0 {
		maxBytes = maxDetailChunkCacheBytes
	}
	return maxEntries, maxBytes
}

func (c *detailChunkCache) removeAtLocked(idx int) {
	last := len(c.entries) - 1
	c.totalBytes -= c.entries[idx].bytes
	if idx != last {
		c.entries[idx] = c.entries[last]
	}
	c.entries[last] = detailChunkEntry{}
	c.entries = c.entries[:last]
}

func (c *detailChunkCache) evictOverflowLocked(maxEntries, maxBytes int, keepDataset, keepID, keepSlot string) {
	for len(c.entries) > maxEntries || c.totalBytes > maxBytes {
		oldest := -1
		for idx := range c.entries {
			e := &c.entries[idx]
			if len(c.entries) > 1 && e.dataset == keepDataset && e.id == keepID && e.slot == keepSlot {
				continue
			}
			if oldest < 0 || e.stamp < c.entries[oldest].stamp {
				oldest = idx
			}
		}
		if oldest < 0 {
			break
		}
		c.removeAtLocked(oldest)
	}
}

func (c *detailChunkCache) getOrSplit(dataset, id, slot string, content detailContentKey, split func() []string) []string {
	if c == nil {
		return split()
	}
	c.mu.Lock()
	for idx := range c.entries {
		e := &c.entries[idx]
		if e.dataset == dataset && e.id == id && e.slot == slot && e.content == content {
			c.clock++
			e.stamp = c.clock
			chunks := e.chunks
			c.mu.Unlock()
			return chunks
		}
	}
	c.mu.Unlock()

	chunks := split()
	entryBytes := detailChunksBytes(chunks)

	c.mu.Lock()
	defer c.mu.Unlock()
	maxEntries, maxBytes := c.limits()
	for idx := range c.entries {
		e := &c.entries[idx]
		if e.dataset == dataset && e.id == id && e.slot == slot {
			if entryBytes > maxBytes {
				c.removeAtLocked(idx)
				return chunks
			}
			c.clock++
			c.totalBytes = c.totalBytes - e.bytes + entryBytes
			e.content = content
			e.chunks = chunks
			e.bytes = entryBytes
			e.stamp = c.clock
			c.evictOverflowLocked(maxEntries, maxBytes, dataset, id, slot)
			return chunks
		}
	}
	if entryBytes > maxBytes {
		return chunks
	}
	c.clock++
	c.entries = append(c.entries, detailChunkEntry{
		dataset: dataset,
		id:      id,
		slot:    slot,
		content: content,
		chunks:  chunks,
		bytes:   entryBytes,
		stamp:   c.clock,
	})
	c.totalBytes += entryBytes
	c.evictOverflowLocked(maxEntries, maxBytes, dataset, id, slot)
	return chunks
}

func (c *detailChunkCache) getOrSplitMarkdown(dataset, id, slot, src string) []string {
	return c.getOrSplit(dataset, id, slot, markdownContentKey(src), func() []string {
		return splitMarkdownChunks(src)
	})
}

func (c *detailChunkCache) getOrSplitTrace(dataset, id, slot string, t Trace) []string {
	return c.getOrSplit(dataset, id, slot, traceContentKey(t), func() []string {
		return splitTraceBodyChunks(t)
	})
}

func (c *detailChunkCache) Invalidate(dataset string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if dataset == "" {
		clear(c.entries)
		c.entries = nil
		c.totalBytes = 0
		return
	}
	kept := c.entries[:0]
	total := 0
	for _, e := range c.entries {
		if e.dataset != dataset {
			kept = append(kept, e)
			total += e.bytes
		}
	}
	clear(c.entries[len(kept):])
	c.entries = kept
	c.totalBytes = total
}

func (c *detailChunkCache) InvalidateObject(dataset, id string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.entries[:0]
	total := 0
	for _, e := range c.entries {
		if !(e.dataset == dataset && e.id == id) {
			kept = append(kept, e)
			total += e.bytes
		}
	}
	clear(c.entries[len(kept):])
	c.entries = kept
	c.totalBytes = total
}

func projectDetailScroll(s *pane, key string) *ui.ScrollState {
	if s.DetailScroll == nil {
		s.DetailScroll = map[string]*ui.ScrollState{}
	}
	scroll := s.DetailScroll[key]
	if scroll == nil {
		scroll = &ui.ScrollState{}
		s.DetailScroll[key] = scroll
	}
	return scroll
}

func projectDetailList(s *pane, key string) *ui.ListState {
	if s.DetailLists == nil {
		s.DetailLists = map[string]*ui.ListState{}
	}
	list := s.DetailLists[key]
	if list == nil {
		list = &ui.ListState{}
		s.DetailLists[key] = list
	}
	return list
}

func countNewlinesUpTo(s string, limit int) int {
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

func shouldVirtualizeMarkdown(src string) bool {
	return len(src) > detailVirtualizeMinBytes || countNewlinesUpTo(src, detailVirtualizeMinLines+1) > detailVirtualizeMinLines
}

func splitUTF8Chunk(s string, maxBytes int) []string {
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

// splitMarkdownChunks splits large Markdown text into renderable chunks without
// breaking fenced code blocks, tables, or UTF-8 rune boundaries.
func splitMarkdownChunks(src string) []string {
	if !shouldVirtualizeMarkdown(src) {
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
				for _, part := range splitUTF8Chunk(line, detailChunkMaxBytes) {
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
			for _, part := range splitUTF8Chunk(line, detailChunkMaxBytes) {
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

func appendCodeFenceChunks(out *[]string, lang, content string) {
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
			for _, part := range splitUTF8Chunk(line, detailChunkTargetBytes) {
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

func splitTraceBodyChunks(t Trace) []string {
	params := string(t.Params)
	result := string(t.Result)
	totalBytes := len(params) + len(result) + len(t.Error)
	if totalBytes <= detailTraceInlineBytes {
		totalLines := countNewlinesUpTo(params, detailTraceInlineLines+1) +
			countNewlinesUpTo(result, detailTraceInlineLines+1) +
			countNewlinesUpTo(t.Error, detailTraceInlineLines+1)
		if totalLines <= detailTraceInlineLines {
			return []string{"```json\n" + params + "\n```\n\n```json\n" + result + "\n```\n\n" + t.Error}
		}
	}
	var chunks []string
	appendCodeFenceChunks(&chunks, "json", params)
	appendCodeFenceChunks(&chunks, "json", result)
	if strings.TrimSpace(t.Error) != "" {
		chunks = append(chunks, splitMarkdownChunks(t.Error)...)
	}
	if len(chunks) == 0 {
		return []string{""}
	}
	return chunks
}

type detailRowKind uint8

const (
	detailRowDocChunk detailRowKind = iota
	detailRowUnrecognized
	detailRowTail
	detailRowSessionCoverage
	detailRowSessionEmpty
	detailRowQAPrompt
	detailRowQAAnswer
	detailRowTraceHeader
	detailRowTraceBody
	detailRowSubagentIntro
	detailRowSubagentCard
	detailRowSessionFooter
)

type detailRow struct {
	kind         detailRowKind
	key          string
	text         string
	roleLabel    string
	toolKey      string
	trace        Trace
	child        Item
	expanded     bool
	first        bool
	last         bool
	truncated    bool
	showLoadFull bool
	codeSplit    bool
}

func hasDetailTail(i Item) bool {
	return len(knowledgeBasisFields(i)) > 1 || i.Source != "" || i.SourceDocument != "" || i.Kind == "知识"
}

func isFenceSplitBoundary(prev, next string) bool {
	return (strings.HasPrefix(next, "```") && strings.HasSuffix(prev, "```")) ||
		(strings.HasPrefix(next, "~~~") && strings.HasSuffix(prev, "~~~"))
}

func buildDocumentDetailRows(a *App, i Item, pres Presentation) []detailRow {
	var chunks []string
	if shouldVirtualizeMarkdown(pres.VisibleBody) {
		chunks = a.detailChunks.getOrSplitMarkdown(i.Dataset, i.ID, "doc", pres.VisibleBody)
	} else {
		chunks = []string{pres.VisibleBody}
	}
	rows := make([]detailRow, 0, len(chunks)+2)
	keyPrefix := i.key()
	for idx, ch := range chunks {
		codeSplit := idx+1 < len(chunks) && isFenceSplitBoundary(ch, chunks[idx+1])
		rows = append(rows, detailRow{
			kind:      detailRowDocChunk,
			key:       fmt.Sprintf("doc:%s:%d", keyPrefix, idx),
			text:      ch,
			first:     idx == 0,
			last:      idx == len(chunks)-1,
			codeSplit: codeSplit,
		})
	}
	if pres.Unrecognized {
		rows = append(rows, detailRow{kind: detailRowUnrecognized, key: "unrecognized:" + keyPrefix})
	}
	if hasDetailTail(i) {
		rows = append(rows, detailRow{kind: detailRowTail, key: "tail:" + keyPrefix})
	}
	return rows
}

func appendTraceRows(rows []detailRow, a *App, s *pane, i Item, traces []Trace, uniqueKey func(string) string) []detailRow {
	for _, trace := range traces {
		t := trace
		toolKey := i.key() + "/" + t.ID
		expanded := s.Tools != nil && s.Tools[toolKey]
		headKey := uniqueKey("th:" + t.ID)
		rows = append(rows, detailRow{
			kind:     detailRowTraceHeader,
			key:      headKey,
			toolKey:  toolKey,
			trace:    t,
			expanded: expanded,
		})
		if expanded {
			chunks := a.detailChunks.getOrSplitTrace(i.Dataset, i.ID, "tr:"+headKey, t)
			for idx, ch := range chunks {
				rows = append(rows, detailRow{
					kind:  detailRowTraceBody,
					key:   fmt.Sprintf("%s:b:%d", headKey, idx),
					text:  ch,
					first: idx == 0,
					last:  idx == len(chunks)-1,
				})
			}
		}
	}
	return rows
}

func buildSessionDetailRows(a *App, s *pane, i Item, paged bool) []detailRow {
	seen := map[string]int{}
	uniqueKey := func(base string) string {
		n := seen[base]
		seen[base] = n + 1
		if n == 0 {
			return base
		}
		return fmt.Sprintf("%s#%d", base, n)
	}

	if paged && s.SessionSubagents && len(i.Subagents) > 0 {
		rows := make([]detailRow, 0, len(i.Subagents)+2)
		rows = append(rows, detailRow{kind: detailRowSubagentIntro, key: "sub-intro:" + i.key()})
		for _, child := range i.Subagents {
			rows = append(rows, detailRow{
				kind:  detailRowSubagentCard,
				key:   uniqueKey("sub:" + child.key()),
				child: child,
			})
		}
		if hasDetailTail(i) {
			rows = append(rows, detailRow{kind: detailRowTail, key: "tail:" + i.key()})
		}
		return rows
	}

	if paged && s.SessionTools {
		rows := make([]detailRow, 0, len(i.Detail.Traces)+3)
		if i.Detail.ToolCount == 0 && len(i.Detail.Traces) == 0 {
			rows = append(rows, detailRow{
				kind: detailRowSessionEmpty,
				key:  "empty-tools:" + i.key(),
				text: "此会话尚无已保存的工具记录。",
			})
		} else {
			rows = appendTraceRows(rows, a, s, i, i.Detail.Traces, uniqueKey)
		}
		rows = append(rows, detailRow{kind: detailRowSessionFooter, key: "footer-tools:" + i.key()})
		if hasDetailTail(i) {
			rows = append(rows, detailRow{kind: detailRowTail, key: "tail:" + i.key()})
		}
		return rows
	}

	rows := make([]detailRow, 0, len(i.Detail.QAs)*2+4)
	if !paged {
		showLoadFull := len(i.Detail.QAs) < i.Detail.Count || len(i.Detail.Traces) < i.Detail.ToolCount
		rows = append(rows, detailRow{
			kind:         detailRowSessionCoverage,
			key:          "coverage:" + i.key(),
			showLoadFull: showLoadFull,
		})
	}
	if len(i.Detail.QAs) == 0 {
		message := "此会话尚无已保存的问答；下方仅为已保存的工具记录。"
		if paged {
			message = "此会话尚无已保存的问答，可切换到工具记录查看。"
		}
		rows = append(rows, detailRow{
			kind: detailRowSessionEmpty,
			key:  "empty-qas:" + i.key(),
			text: message,
		})
	}
	promptRole := "用户"
	if i.Session != nil && i.Session.IsSubagent {
		promptRole = "上游消息"
	}
	for _, q := range i.Detail.QAs {
		qBase := uniqueKey("q:" + q.ID)
		var qChunks []string
		if shouldVirtualizeMarkdown(q.Question) {
			qChunks = a.detailChunks.getOrSplitMarkdown(i.Dataset, i.ID, qBase, q.Question)
		} else {
			qChunks = []string{q.Question}
		}
		for idx, ch := range qChunks {
			rows = append(rows, detailRow{
				kind:      detailRowQAPrompt,
				key:       fmt.Sprintf("%s:%d", qBase, idx),
				text:      ch,
				roleLabel: promptRole,
				first:     idx == 0,
				last:      idx == len(qChunks)-1,
				truncated: q.QuestionTruncated,
			})
		}
		aBase := uniqueKey("a:" + q.ID)
		var aChunks []string
		if shouldVirtualizeMarkdown(q.Answer) {
			aChunks = a.detailChunks.getOrSplitMarkdown(i.Dataset, i.ID, aBase, q.Answer)
		} else {
			aChunks = []string{q.Answer}
		}
		for idx, ch := range aChunks {
			rows = append(rows, detailRow{
				kind:      detailRowQAAnswer,
				key:       fmt.Sprintf("%s:%d", aBase, idx),
				text:      ch,
				first:     idx == 0,
				last:      idx == len(aChunks)-1,
				truncated: q.AnswerTruncated,
			})
		}
	}
	if !paged {
		rows = appendTraceRows(rows, a, s, i, i.Detail.Traces, uniqueKey)
	}
	if paged {
		rows = append(rows, detailRow{kind: detailRowSessionFooter, key: "footer-qa:" + i.key()})
	}
	if hasDetailTail(i) {
		rows = append(rows, detailRow{kind: detailRowTail, key: "tail:" + i.key()})
	}
	return rows
}

func (a *App) renderDetailRowList(c *ui.Context, s *pane, i Item, scrollKey string, scroll *ui.ScrollState, rows []detailRow) {
	listState := projectDetailList(s, scrollKey)
	listState.Key = func(n int) any { return rows[n].key }
	mdOpts := a.v4MarkdownOptions(fsReading)
	ui.List(c.Key("detail-list-"+scrollKey), listState, len(rows)).
		TrackScroll(scroll).
		Padding(sp3, sp10, sp8, sp10).Gap(0).
		Grow(1).MinHeight(0).FillWidth().
		Rows(func(row ui.ListRow) {
			a.renderDetailRow(row.Context, s, i, rows[row.Index], mdOpts)
		})
}

func (a *App) renderDetailRow(c *ui.Context, s *pane, i Item, r detailRow, mdOpts *markdown.Options) {
	switch r.kind {
	case detailRowDocChunk:
		padBot := sp2 + 2
		if r.last {
			padBot = 0
		} else if r.codeSplit {
			padBot = sp1
		}
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(0, 0, padBot, 0).Children(func() {
			markdown.View(c.Key(r.key), r.text, mdOpts)
		})
	case detailRowUnrecognized:
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(sp2, 0, 0, 0).Children(func() {
			ui.Text(c, "原文格式未识别，保留原文").FontSize(fsCaption).TextColor(tok.Text3)
		})
	case detailRowTail:
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Children(func() {
			a.projectDetailTail(c, s, &i)
		})
	case detailRowSessionCoverage:
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Gap(sp2).Padding(0, 0, sp3, 0).Children(func() {
			homeText(c, sessionCoverage(*i.Detail), homeBody).FontSize(fsFootnote)
			if r.showLoadFull {
				homeText(c, "当前是最近记录，可读取按时间排列的完整会话。", homeMeta)
				projectLink(c, "读取完整会话", func() { a.loadSessionPage(s, 0, 0) }).Disabled(s.Reading)
			}
		})
	case detailRowSessionEmpty:
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(0, 0, sp3, 0).Children(func() {
			homeText(c, r.text, homeMeta)
		})
	case detailRowQAPrompt:
		padOut := float32(0)
		if r.last {
			padOut = sp3
		}
		padTop, padBot := float32(2), float32(2)
		bwTop, bwBot := float32(0), float32(0)
		if r.first {
			padTop, bwTop = 11, 1
		}
		if r.last {
			padBot, bwBot = 11, 1
		}
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(0, 0, padOut, 0).Children(func() {
			card := ui.Column(c).FillWidth().Padding(padTop, 14, padBot, 14).Background(tok.Fill1).
				BorderWidth(bwTop, 1, bwBot, 1).BorderColor(tok.Border).Gap(6)
			if r.first && r.last {
				card.Radius(radius)
			}
			card.Children(func() {
				if r.first {
					ui.Text(c, r.roleLabel).Font("system-ui").FontSize(fsCaption).FontWeight(500).TextColor(tok.Text3)
				}
				markdown.View(c.Key(r.key), r.text, mdOpts)
				if r.last && r.truncated {
					ui.Text(c, "官方内容已截断").FontSize(fsCaption).TextColor(tok.Text3)
				}
			})
		})
	case detailRowQAAnswer:
		padBot := sp2 + 2
		if r.last {
			padBot = sp4
		}
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(0, 0, padBot, 0).Gap(sp2).Children(func() {
			if r.first {
				sourceAgentLabel(c, i)
			}
			markdown.View(c.Key(r.key), r.text, mdOpts)
			if r.last && r.truncated {
				ui.Text(c, "官方内容已截断").FontSize(fsCaption).TextColor(tok.Text3)
			}
		})
	case detailRowTraceHeader:
		t := r.trace
		statusCol := tok.OK
		if strings.Contains(strings.ToLower(t.Status), "err") || strings.Contains(strings.ToLower(t.Status), "fail") || t.Error != "" {
			statusCol = tok.Danger
		}
		padOut := sp2
		bwBot := float32(1)
		if r.expanded {
			padOut = 0
			bwBot = 0
		}
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(0, 0, padOut, 0).Children(func() {
			card := ui.Column(c).FillWidth().BorderWidth(1, 1, bwBot, 1).BorderColor(tok.Border).Background(tok.BgElevated)
			if !r.expanded {
				card.Radius(radius)
			}
			card.Children(func() {
				head := ui.ButtonBase(c.Key("tool-head-"+r.key)).Role(ui.RoleButton).Label(t.Name + " · " + t.Status).
					Cursor(ui.CursorPointer).FillWidth().Padding(sp2, sp3).Gap(sp2).AlignItems(ui.Center).Justify(ui.Start).BorderWidth(0)
				if !r.expanded {
					head.Radius(radius)
				}
				if head.Hovered() {
					head.Background(tok.Hover)
				} else {
					head.Background(ui.Transparent)
				}
				toolKey := r.toolKey
				head.Children(func() {
					ico := v4IconChev
					if r.expanded {
						ico = v4IconDown
					}
					ui.Icon(c, ico).FontSize(12).TextColor(tok.Text3).Shrink(0)
					ui.Box(c).Size(6, 6).Radius(3).Background(statusCol).Shrink(0)
					ui.Text(c, t.Name).Font("ui-monospace").FontSize(fsFootnote).TextColor(tok.Text1).SingleLine().Grow(1).MinWidth(0)
					if t.Status != "" {
						homeText(c, t.Status, homeMeta).SingleLine().Shrink(0)
					}
				}).OnClick(func() {
					if s.Tools == nil {
						s.Tools = map[string]bool{}
					}
					s.Tools[toolKey] = !s.Tools[toolKey]
				})
			})
		})
	case detailRowTraceBody:
		padOut := float32(0)
		padTop, padBot := sp1, sp1
		bwTop, bwBot := float32(0), float32(0)
		if r.first {
			padTop, bwTop = sp2, 1
		}
		if r.last {
			padBot, bwBot, padOut = sp3, 1, sp2
		}
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(0, 0, padOut, 0).Children(func() {
			ui.Column(c).FillWidth().Padding(padTop, sp3, padBot, sp3).
				BorderWidth(bwTop, 1, bwBot, 1).BorderColor(tok.Border).Background(tok.BgElevated).
				Children(func() {
					markdown.View(c.Key(r.key), r.text, mdOpts)
				})
		})
	case detailRowSubagentIntro:
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(0, 0, sp3, 0).Children(func() {
			homeText(c, "以下子会话由当前主会话派生，可能包含 Agent 之间的问答对话与工具调用，点击查看完整记录。", homeMeta)
		})
	case detailRowSubagentCard:
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(0, 0, sp3, 0).Children(func() {
			a.renderSubagentCard(c, s, i, r.child)
		})
	case detailRowSessionFooter:
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(sp1, 0, sp3, 0).Children(func() {
			a.sessionLoadMore(c, s, i)
		})
	}
}
