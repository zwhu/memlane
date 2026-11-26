package gateway

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/zwhu/memlane/core/engine"
)

const (
	defaultRecallContextMaxChars = 6000
	defaultRecallPassageMaxChars = 1200
	minTruncatedPassageChars     = 80
)

var (
	noiseIsAEdgeRE       = regexp.MustCompile(`(?i)^.+?\s+is\s+an?\s+[a-z]+(?:[ _-][a-z]+)?\.?$`)
	noiseBelongsToEdgeRE = regexp.MustCompile(`(?i)^.+\s+belongs\s+to\s+set\s+\S+\.?$`)
)

type recallFormatOptions struct {
	CommonDatasetID string
	SessionID       string
	Purpose         string
	MaxChars        int
	MaxPassageChars int
}

// authorizedGraphContext filters graph search output strictly to authorized dataset IDs.
// It returns the filtered context (nil if no content remains) and whether the engine degraded to chunks.
func authorizedGraphContext(gc *engine.GraphSearchContext, scopes []engine.Scope) (*engine.GraphSearchContext, bool) {
	if gc == nil {
		return nil, false
	}
	degraded := gc.DegradedToChunks
	datasets := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		if scope.DatasetID != "" {
			datasets[scope.DatasetID] = true
		}
	}

	allowedEntityConflicts := make(map[string]bool)
	droppedEntityConflicts := make(map[string]bool)
	entities := make([]engine.GraphSearchEntity, 0, len(gc.Entities))
	for _, entity := range gc.Entities {
		if !datasets[entity.DatasetID] {
			for _, conflict := range entity.Conflicts {
				if trimmed := strings.TrimSpace(conflict); trimmed != "" {
					droppedEntityConflicts[trimmed] = true
				}
			}
			continue
		}
		cleaned := entity
		if len(entity.Edges) > 0 {
			cleaned.Edges = append([]engine.GraphSearchEdge(nil), entity.Edges...)
		}
		if len(entity.Conflicts) > 0 {
			cleaned.Conflicts = dedupeTrimmedStrings(entity.Conflicts)
			for _, conflict := range cleaned.Conflicts {
				allowedEntityConflicts[conflict] = true
			}
		}
		entities = append(entities, cleaned)
	}

	facts := make([]engine.GraphSearchFact, 0, len(gc.Facts))
	for _, fact := range gc.Facts {
		if datasets[fact.DatasetID] && strings.TrimSpace(fact.Text) != "" {
			facts = append(facts, fact)
		}
	}

	codeFacts := make([]engine.CodeSearchFact, 0, len(gc.CodeFacts))
	for _, cf := range gc.CodeFacts {
		if !datasets[cf.DatasetID] || strings.TrimSpace(cf.Name) == "" {
			continue
		}
		cloned := cf
		if len(cf.Relations) > 0 {
			cloned.Relations = append([]engine.CodeSearchRelation(nil), cf.Relations...)
		}
		codeFacts = append(codeFacts, cloned)
	}

	conflicts := make([]string, 0, len(gc.Conflicts))
	seenConflicts := make(map[string]bool, len(gc.Conflicts))
	for _, raw := range gc.Conflicts {
		text := strings.TrimSpace(raw)
		if text == "" || seenConflicts[text] {
			continue
		}
		if droppedEntityConflicts[text] && !allowedEntityConflicts[text] {
			continue
		}
		seenConflicts[text] = true
		conflicts = append(conflicts, text)
	}

	guidance := strings.TrimSpace(gc.SessionGuidance)
	if len(entities) == 0 && len(facts) == 0 && len(conflicts) == 0 && guidance == "" && len(codeFacts) == 0 {
		return nil, degraded
	}
	return &engine.GraphSearchContext{
		Entities:         entities,
		Facts:            facts,
		Conflicts:        conflicts,
		SessionGuidance:  guidance,
		CodeFacts:        codeFacts,
		DegradedToChunks: degraded,
	}, degraded
}

func isNoiseGraphEdge(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	return noiseIsAEdgeRE.MatchString(trimmed) || noiseBelongsToEdgeRE.MatchString(trimmed)
}

func filterCurrentSessionHits(hits []engine.SearchHit, sessionID, purpose string) []engine.SearchHit {
	if purpose != "" || strings.TrimSpace(sessionID) == "" || len(hits) == 0 {
		return hits
	}
	filtered := make([]engine.SearchHit, 0, len(hits))
	for _, hit := range hits {
		if isCurrentSessionHit(hit, sessionID) {
			continue
		}
		filtered = append(filtered, hit)
	}
	return filtered
}

func isCurrentSessionHit(hit engine.SearchHit, sessionID string) bool {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return false
	}
	if hit.Metadata != nil {
		for _, key := range []string{"session_id", "source_session_id"} {
			if raw, ok := hit.Metadata[key].(string); ok && strings.TrimSpace(raw) == sid {
				return true
			}
		}
	}
	if lineMatchesSessionID(hit.Title, sid) {
		return true
	}
	head := snippetHead(hit.Snippet, 512)
	for _, line := range strings.Split(head, "\n") {
		if lineMatchesSessionID(line, sid) {
			return true
		}
	}
	return false
}

func lineMatchesSessionID(line, sessionID string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if idx := strings.Index(lower, "session id:"); idx >= 0 {
		rest := strings.TrimSpace(trimmed[idx+len("session id:"):])
		token := leadingToken(rest)
		if token == sessionID {
			return true
		}
	}
	if strings.Contains(lower, "session learning") && strings.Contains(trimmed, "(session "+sessionID+")") {
		return true
	}
	return false
}

func leadingToken(s string) string {
	end := strings.IndexAny(s, " \t\r\n,);]|")
	if end < 0 {
		return s
	}
	return s[:end]
}

func snippetHead(text string, maxRunes int) string {
	trimmed := strings.TrimSpace(text)
	if utf8.RuneCountInString(trimmed) <= maxRunes {
		return trimmed
	}
	return truncateRunes(trimmed, maxRunes)
}

func isRawTranscriptHit(hit engine.SearchHit) bool {
	if hit.Metadata != nil {
		if v, ok := hit.Metadata["lesson_type"].(string); ok && strings.TrimSpace(v) != "" {
			return false
		}
		if v, ok := hit.Metadata["state_key"].(string); ok && strings.TrimSpace(v) != "" {
			return false
		}
		if v, ok := hit.Metadata["kind"].(string); ok {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "knowledge":
				return false
			case "raw_doc", "capture_qa", "capture_trace", "transcript", "raw":
				return true
			}
		}
		if tags := extractStringSlice(hit.Metadata["node_set"]); len(tags) > 0 {
			for _, tag := range tags {
				switch tag {
				case "session_learnings", "user_context", "memory":
					return false
				case "user_sessions_from_cache", "agent_trace_feedbacks":
					return true
				}
			}
		}
	}
	head := snippetHead(hit.Snippet, 512)
	lowerHead := strings.ToLower(head)
	if strings.Contains(lowerHead, "lesson-type:") || strings.Contains(lowerHead, "# session learning") {
		return false
	}
	if strings.Contains(lowerHead, "session id:") {
		return true
	}
	return false
}

func extractStringSlice(raw any) []string {
	switch values := raw.(type) {
	case []string:
		return values
	case []any:
		out := make([]string, 0, len(values))
		for _, item := range values {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return nil
	}
}

func dedupeTrimmedStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}

func truncateWithEllipsis(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	if maxRunes <= 3 {
		return truncateRunes(s, maxRunes)
	}
	return strings.TrimRight(truncateRunes(s, maxRunes-3), " \t\n\r") + "..."
}

type budgetedMarkdown struct {
	buf       strings.Builder
	usedRunes int
	maxRunes  int
}

func newBudgetedMarkdown(maxRunes int) *budgetedMarkdown {
	return &budgetedMarkdown{maxRunes: maxRunes}
}

func (b *budgetedMarkdown) remaining() int {
	return b.maxRunes - b.usedRunes
}

func (b *budgetedMarkdown) writeRaw(text string) {
	b.buf.WriteString(text)
	b.usedRunes += utf8.RuneCountInString(text)
}

func (b *budgetedMarkdown) addSection(header string, items []string, allowTruncateLast bool) {
	if len(items) == 0 || b.remaining() <= 0 {
		return
	}
	prefix := header + "\n"
	if b.usedRunes > 0 {
		prefix = "\n\n" + prefix
	}
	prefixRunes := utf8.RuneCountInString(prefix)
	if b.remaining() <= prefixRunes {
		return
	}

	wroteHeader := false
	for _, rawItem := range items {
		item := strings.TrimSpace(rawItem)
		if item == "" {
			continue
		}
		sep := ""
		if wroteHeader {
			sep = "\n"
		}
		needPrefix := 0
		if !wroteHeader {
			needPrefix = prefixRunes
		}
		avail := b.remaining() - needPrefix - utf8.RuneCountInString(sep)
		if avail <= 0 {
			return
		}
		itemRunes := utf8.RuneCountInString(item)
		if itemRunes <= avail {
			if !wroteHeader {
				b.writeRaw(prefix)
				wroteHeader = true
			} else {
				b.writeRaw(sep)
			}
			b.writeRaw(item)
			continue
		}
		if allowTruncateLast && avail >= minTruncatedPassageChars {
			clipped := truncateWithEllipsis(item, avail)
			if clipped != "" {
				if !wroteHeader {
					b.writeRaw(prefix)
				} else {
					b.writeRaw(sep)
				}
				b.writeRaw(clipped)
			}
		}
		return
	}
}

func formatRecallContext(hits []engine.SearchHit, gc *engine.GraphSearchContext, opts recallFormatOptions) string {
	maxChars := opts.MaxChars
	if maxChars <= 0 {
		maxChars = defaultRecallContextMaxChars
	}
	maxPassage := opts.MaxPassageChars
	if maxPassage <= 0 {
		maxPassage = defaultRecallPassageMaxChars
	}

	filteredHits := filterCurrentSessionHits(hits, opts.SessionID, opts.Purpose)
	doc := newBudgetedMarkdown(maxChars)

	// 1. Conflicts / Updates (highest priority, deduplicated).
	if gc != nil {
		var rawConflicts []string
		rawConflicts = append(rawConflicts, gc.Conflicts...)
		for _, entity := range gc.Entities {
			rawConflicts = append(rawConflicts, entity.Conflicts...)
		}
		conflicts := dedupeTrimmedStrings(rawConflicts)
		if len(conflicts) > 0 {
			lines := make([]string, 0, len(conflicts))
			for _, conflict := range conflicts {
				lines = append(lines, "- "+truncateWithEllipsis(conflict, maxPassage))
			}
			doc.addSection("## Conflicts & Updates", lines, true)
		}
	}

	// 2. Relevant entities & Related facts (denoised; project dataset ordered ahead of common).
	if gc != nil {
		renderedEdges := make(map[string]bool)
		orderedEntities := orderEntitiesProjectFirst(gc.Entities, opts.CommonDatasetID)
		entityBlocks := make([]string, 0, len(orderedEntities))
		for _, entity := range orderedEntities {
			name := strings.TrimSpace(entity.Name)
			if name == "" {
				continue
			}
			edgeLines := make([]string, 0, len(entity.Edges))
			seenEntityEdge := make(map[string]bool, len(entity.Edges))
			for _, edge := range entity.Edges {
				text := strings.TrimSpace(edge.Text)
				if isNoiseGraphEdge(text) {
					continue
				}
				if edge.Superseded && !strings.Contains(strings.ToLower(text), "superseded") {
					text += " [superseded]"
				}
				if edge.Authoritative && !strings.Contains(strings.ToLower(text), "authoritative") {
					text += " [authoritative]"
				}
				if seenEntityEdge[text] {
					continue
				}
				seenEntityEdge[text] = true
				renderedEdges[strings.TrimSpace(edge.Text)] = true
				renderedEdges[text] = true
				edgeLines = append(edgeLines, "  - "+truncateWithEllipsis(text, maxPassage))
			}
			if len(edgeLines) == 0 {
				continue
			}
			header := "- **" + name + "**"
			if kind := strings.TrimSpace(entity.Type); kind != "" {
				header += " (" + kind + ")"
			}
			entityBlocks = append(entityBlocks, header+"\n"+strings.Join(edgeLines, "\n"))
		}
		doc.addSection("## Relevant Entities", entityBlocks, true)

		orderedFacts := orderFactsProjectFirst(gc.Facts, opts.CommonDatasetID)
		factLines := make([]string, 0, len(orderedFacts))
		seenFacts := make(map[string]bool, len(orderedFacts))
		for _, fact := range orderedFacts {
			text := strings.TrimSpace(fact.Text)
			if isNoiseGraphEdge(text) || seenFacts[text] || renderedEdges[text] {
				continue
			}
			seenFacts[text] = true
			factLines = append(factLines, "- "+truncateWithEllipsis(text, maxPassage))
		}
		doc.addSection("## Related Facts", factLines, true)
	}

	// 3. Active session guidance.
	if gc != nil {
		if guidance := strings.TrimSpace(gc.SessionGuidance); guidance != "" {
			doc.addSection("## Active Session Guidance", []string{truncateWithEllipsis(guidance, maxPassage)}, true)
		}
	}

	// 4. Code graph facts.
	if gc != nil && len(gc.CodeFacts) > 0 {
		codeLines := make([]string, 0, len(gc.CodeFacts))
		for _, cf := range gc.CodeFacts {
			if line := formatCodeFact(cf); line != "" {
				codeLines = append(codeLines, "- "+truncateWithEllipsis(line, maxPassage))
			}
		}
		doc.addSection("## Code Graph", codeLines, true)
	}

	// 5. Project distilled memories before raw transcripts; 6. Cross-project common reference.
	var distilledProject, rawProject, commonHits []engine.SearchHit
	for _, hit := range filteredHits {
		if opts.CommonDatasetID != "" && hit.DatasetID == opts.CommonDatasetID {
			commonHits = append(commonHits, hit)
			continue
		}
		if isRawTranscriptHit(hit) {
			rawProject = append(rawProject, hit)
		} else {
			distilledProject = append(distilledProject, hit)
		}
	}

	if lines := formatHitPassages(distilledProject, maxPassage); len(lines) > 0 {
		doc.addSection("## Project Memories", lines, true)
	}
	if lines := formatHitPassages(rawProject, maxPassage); len(lines) > 0 {
		doc.addSection("## Project Transcripts", lines, true)
	}
	if lines := formatHitPassages(commonHits, maxPassage); len(lines) > 0 {
		header := "## Common Reference\n> Project conventions take precedence over common reference."
		doc.addSection(header, lines, true)
	}

	return strings.TrimSpace(doc.buf.String())
}

func orderEntitiesProjectFirst(entities []engine.GraphSearchEntity, commonDatasetID string) []engine.GraphSearchEntity {
	if commonDatasetID == "" || len(entities) <= 1 {
		return entities
	}
	project := make([]engine.GraphSearchEntity, 0, len(entities))
	common := make([]engine.GraphSearchEntity, 0)
	for _, entity := range entities {
		if entity.DatasetID == commonDatasetID {
			common = append(common, entity)
		} else {
			project = append(project, entity)
		}
	}
	return append(project, common...)
}

func orderFactsProjectFirst(facts []engine.GraphSearchFact, commonDatasetID string) []engine.GraphSearchFact {
	if commonDatasetID == "" || len(facts) <= 1 {
		return facts
	}
	project := make([]engine.GraphSearchFact, 0, len(facts))
	common := make([]engine.GraphSearchFact, 0)
	for _, fact := range facts {
		if fact.DatasetID == commonDatasetID {
			common = append(common, fact)
		} else {
			project = append(project, fact)
		}
	}
	return append(project, common...)
}

func formatCodeFact(cf engine.CodeSearchFact) string {
	name := strings.TrimSpace(cf.Name)
	if name == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("`")
	b.WriteString(name)
	b.WriteString("`")
	var meta []string
	if kind := strings.TrimSpace(cf.Kind); kind != "" {
		meta = append(meta, kind)
	}
	if file := strings.TrimSpace(cf.File); file != "" {
		if cf.Line > 0 {
			meta = append(meta, fmt.Sprintf("%s:%d", file, cf.Line))
		} else {
			meta = append(meta, file)
		}
	}
	if len(meta) > 0 {
		b.WriteString(" (")
		b.WriteString(strings.Join(meta, ", "))
		b.WriteString(")")
	}
	var rels []string
	for _, rel := range cf.Relations {
		relType, target := strings.TrimSpace(rel.Type), strings.TrimSpace(rel.Target)
		if relType != "" && target != "" {
			rels = append(rels, relType+" -> "+target)
		}
	}
	if len(rels) > 0 {
		b.WriteString(": ")
		b.WriteString(strings.Join(rels, "; "))
	}
	return b.String()
}

func formatHitPassages(hits []engine.SearchHit, maxPassage int) []string {
	lines := make([]string, 0, len(hits))
	for _, hit := range hits {
		title := strings.TrimSpace(hit.Title)
		snippet := strings.TrimSpace(hit.Snippet)
		if title == "" && snippet == "" {
			continue
		}
		var passage string
		switch {
		case title != "" && snippet != "":
			passage = "- **" + title + "**: " + snippet
		case title != "":
			passage = "- **" + title + "**"
		default:
			passage = "- " + snippet
		}
		lines = append(lines, truncateWithEllipsis(passage, maxPassage))
	}
	return lines
}
