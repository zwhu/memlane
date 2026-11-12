package official

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/zwhu/memlane/internal/records"
)

var ErrOfficialDocumentChanged = errors.New("official document metadata changed")
var ErrOfficialDocumentUnsupported = errors.New("official document has no supported UTF-8 text representation")
var ErrOfficialDocumentNoSession = errors.New("official document has no consistent session reference")

// DocumentReader uses the same authorized principal/service/dataset boundary as
// sessions. It does not create records, versions, material copies or an index.
type DocumentReader struct{ SessionReader }

func (r DocumentReader) ListPage(ctx context.Context, limit, offset int) (records.OfficialDocumentPage, error) {
	var out records.OfficialDocumentPage
	if limit < 1 || limit > 500 || offset < 0 || offset > 1_000_000 {
		return out, errors.New("bounded official document page required")
	}
	if err := r.verify(ctx); err != nil {
		return out, err
	}
	query := url.Values{"limit": {strconv.Itoa(limit + 1)}, "offset": {strconv.Itoa(offset)}}
	var rows []records.OfficialDocument
	if err := Request(ctx, r.Endpoint, "/api/v1/datasets/"+r.Dataset+"/data?"+query.Encode(), "GET", r.Token, "", nil, &rows); err != nil {
		return out, err
	}
	if len(rows) > limit+1 {
		return out, errors.New("official document pagination changed")
	}
	seen := map[string]bool{}
	for _, d := range rows {
		if !canonicalSessionScope(d.ID) || d.Dataset != r.Dataset || seen[d.ID] || d.Size != nil && *d.Size < 0 {
			return out, errors.New("official document identity changed")
		}
		seen[d.ID] = true
	}
	if err := r.verify(ctx); err != nil {
		return out, err
	}
	out = records.OfficialDocumentPage{Documents: rows, Limit: limit, Offset: offset, HasMore: len(rows) > limit}
	if out.HasMore {
		out.Documents = rows[:limit]
	}
	if out.Documents == nil {
		out.Documents = []records.OfficialDocument{}
	}
	return out, nil
}

// There is no GET metadata-by-id in the pinned API. First check the selected
// row's catalog position, then follow standard pagination if inserts moved it.
// Never use the owner-only content_hash filter for ACL-granted readers.
func (r DocumentReader) find(ctx context.Context, id string, hint int) (records.OfficialDocument, error) {
	if hint < 0 || hint > 1_000_000 {
		return records.OfficialDocument{}, errors.New("official document position invalid")
	}
	page, err := r.ListPage(ctx, 1, hint)
	if err != nil {
		return records.OfficialDocument{}, err
	}
	if len(page.Documents) > 0 && page.Documents[0].ID == id {
		return page.Documents[0], nil
	}
	for offset := 0; offset <= 1_000_000; offset += 500 {
		page, err = r.ListPage(ctx, 500, offset)
		if err != nil {
			return records.OfficialDocument{}, err
		}
		for _, d := range page.Documents {
			if d.ID == id {
				return d, nil
			}
		}
		if !page.HasMore {
			return records.OfficialDocument{}, errors.New("official document absent from current catalog")
		}
	}
	return records.OfficialDocument{}, errors.New("official document catalog exceeds upstream pagination boundary")
}

// ReadText returns only bounded UTF-8 content from the official download route.
// Metadata is checked on both sides of the read; the caller must also recheck
// its project authorization afterwards, as it does for official sessions.
func (r DocumentReader) ReadText(ctx context.Context, selected records.OfficialDocument, hint int) (records.OfficialDocumentText, error) {
	var out records.OfficialDocumentText
	if !canonicalSessionScope(selected.ID) || selected.Dataset != r.Dataset {
		return out, errors.New("selected official document identity required")
	}
	before, err := r.find(ctx, selected.ID, hint)
	if err != nil {
		return out, err
	}
	if records.MergeDigest(before) != records.MergeDigest(selected) {
		return out, ErrOfficialDocumentChanged
	}
	kind, _, err := mime.ParseMediaType(before.MIMEType)
	if err != nil || !strings.HasPrefix(kind, "text/") && kind != "application/json" && kind != "application/yaml" {
		return out, ErrOfficialDocumentUnsupported
	}
	body, err := requestBytes(ctx, r.Endpoint, "/api/v1/datasets/"+r.Dataset+"/data/"+selected.ID+"/raw", "GET", r.Token, "", nil)
	if err != nil {
		return out, err
	}
	if !utf8.Valid(body) || strings.ContainsRune(string(body), '\x00') {
		return out, ErrOfficialDocumentUnsupported
	}
	after, err := r.find(ctx, selected.ID, hint)
	if err != nil {
		return out, err
	}
	if records.MergeDigest(before) != records.MergeDigest(after) {
		return out, ErrOfficialDocumentChanged
	}
	text := string(body)
	return records.OfficialDocumentText{Document: after, Text: text, SessionReference: documentSessionReference(after.Metadata, text)}, nil
}

// Resolve only within the already authorized dataset and prove a unique owner.
// No Memlane memory/version row or historical mirror is needed for this read.
func (r DocumentReader) ReadSession(ctx context.Context, selected records.OfficialDocument, hint int) (records.OfficialDocumentSession, error) {
	document, err := r.ReadText(ctx, selected, hint)
	if err != nil {
		return records.OfficialDocumentSession{}, err
	}
	if document.SessionReference == "" {
		return records.OfficialDocumentSession{}, ErrOfficialDocumentNoSession
	}
	detail, err := r.ReadUniqueSession(ctx, document.SessionReference)
	if err != nil {
		return records.OfficialDocumentSession{}, err
	}
	after, err := r.find(ctx, selected.ID, hint)
	if err != nil {
		return records.OfficialDocumentSession{}, err
	}
	if records.MergeDigest(document.Document) != records.MergeDigest(after) {
		return records.OfficialDocumentSession{}, ErrOfficialDocumentChanged
	}
	return records.OfficialDocumentSession{Document: after, Detail: detail}, nil
}

// Recognize the pinned legacy and coding-mode envelopes only when node sets,
// rendering and coding identity agree. Metadata is caller-supplied: this is a
// reference, never an attestation of AI generation or a session access grant.
func documentSessionReference(metadata json.RawMessage, text string) string {
	var tags struct {
		NodeSets []string `json:"node_set"`
	}
	if len(metadata) > 64*1024 || json.Unmarshal(metadata, &tags) != nil || len(tags.NodeSets) > 128 {
		return ""
	}
	marked, session := false, ""
	for _, tag := range tags.NodeSets {
		if tag == "session_learnings" {
			marked = true
		}
		if !strings.HasPrefix(tag, "session_learnings:") {
			continue
		}
		candidate := strings.TrimPrefix(tag, "session_learnings:")
		if candidate == "" || len(candidate) > 128 || strings.ContainsFunc(candidate, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return ""
		}
		if session != "" && session != candidate {
			return ""
		}
		session = candidate
	}
	if !marked || session == "" {
		return ""
	}
	if strings.HasPrefix(text, "# Session learning (session "+session+")\n\n") {
		return session
	}
	if _, _, ok := codingLessonEnvelope(metadata, text, session); ok {
		return session
	}
	return ""
}
