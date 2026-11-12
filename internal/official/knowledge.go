package official

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/zwhu/memlane/internal/records"
)

var generatedDocumentName = regexp.MustCompile(`^text_[0-9a-f]{32,64}(\.[A-Za-z0-9]+)?$`)

func (r DocumentReader) ReadKnowledge(ctx context.Context, selected records.OfficialDocument, hint int) (records.OfficialKnowledge, error) {
	content, err := r.ReadText(ctx, selected, hint)
	if err != nil {
		return records.OfficialKnowledge{}, err
	}
	if content.SessionReference == "" && !importedKnowledge(content) {
		return records.OfficialKnowledge{}, ErrOfficialDocumentNoSession
	}
	status, err := r.readProcessing(ctx)
	if err != nil {
		return records.OfficialKnowledge{}, err
	}
	completed, ok := status[selected.ID]
	if !ok {
		return records.OfficialKnowledge{}, ErrOfficialDocumentChanged
	}
	after, err := r.find(ctx, selected.ID, hint)
	if err != nil {
		return records.OfficialKnowledge{}, err
	}
	if records.MergeDigest(after) != records.MergeDigest(content.Document) {
		return records.OfficialKnowledge{}, ErrOfficialDocumentChanged
	}
	if err := r.verify(ctx); err != nil {
		return records.OfficialKnowledge{}, err
	}
	state := "not_completed"
	if completed {
		state = "completed"
	}
	title, fallback := learningTitle(content)
	return records.OfficialKnowledge{Content: content, Title: title, TitleFromBody: fallback, ProcessingState: state}, nil
}

// LearningBody removes only the fixed SDK envelope, never a guessed heading.
func LearningBody(content records.OfficialDocumentText) string {
	if content.SessionReference == "" {
		return content.Text
	}
	prefix := "# Session learning (session " + content.SessionReference + ")\n\n"
	if strings.HasPrefix(content.Text, prefix) {
		return strings.TrimPrefix(content.Text, prefix)
	}
	if documentSessionReference(content.Document.Metadata, content.Text) == content.SessionReference {
		if _, body, ok := codingLessonEnvelope(content.Document.Metadata, content.Text, content.SessionReference); ok {
			return body
		}
	}
	return content.Text
}

func learningTitle(content records.OfficialDocumentText) (string, bool) {
	if label := content.Document.Label; label != nil && strings.TrimSpace(*label) != "" {
		return *label, false
	}
	if name := strings.TrimSpace(content.Document.Name); name != "" && !generatedDocumentName.MatchString(name) {
		return name, false
	}
	body := strings.TrimSpace(strings.SplitN(LearningBody(content), "\n\n", 2)[0])
	if body == "" {
		return "未命名知识", false
	}
	title := []rune(body)
	if len(title) > 64 {
		return string(title[:64]) + "…", true
	}
	return body, true
}

// readProcessing returns only the official per-item completion observation.
// Pending cannot be translated into failed, running or never-started.
func (r DocumentReader) readProcessing(ctx context.Context) (map[string]bool, error) {
	var status struct {
		Total     int `json:"total"`
		Completed int `json:"completed"`
		Pending   int `json:"pending"`
		Items     []struct {
			ID        string `json:"id"`
			Completed *bool  `json:"completed"`
		} `json:"items"`
	}
	err := Request(ctx, r.Endpoint, "/api/v1/datasets/"+r.Dataset+"/processing-status?pipeline=cognify_pipeline", "GET", r.Token, "", nil, &status)
	if err != nil {
		return nil, err
	}
	if status.Total < 0 || status.Total > 100000 || len(status.Items) != status.Total || status.Completed < 0 || status.Pending < 0 || status.Completed+status.Pending != status.Total {
		return nil, errors.New("official item processing counts changed")
	}
	out := map[string]bool{}
	completed := 0
	for _, item := range status.Items {
		if !canonicalSessionScope(item.ID) || item.Completed == nil {
			return nil, errors.New("official processing identity invalid")
		}
		if _, ok := out[item.ID]; ok {
			return nil, errors.New("official processing identity duplicated")
		}
		out[item.ID] = *item.Completed
		if *item.Completed {
			completed++
		}
	}
	if completed != status.Completed {
		return nil, errors.New("official processing completion counts changed")
	}
	return out, nil
}

// ListKnowledgePage keeps the official cursor even when this page consists of
// raw documents. It neither scans the entire dataset nor persists text/indexes.
func (r DocumentReader) ListKnowledgePage(ctx context.Context, limit, offset int) (out records.OfficialKnowledgePage, err error) {
	out = records.OfficialKnowledgePage{Items: []records.OfficialKnowledge{}, Limit: limit, Offset: offset}
	defer func() {
		if err != nil {
			out.Items = nil
		}
	}()
	if limit > 20 {
		return out, errors.New("bounded knowledge page required")
	}
	page, err := r.ListPage(ctx, limit, offset)
	if err != nil {
		return out, err
	}
	out.HasMore = page.HasMore
	var processing map[string]bool
	totalBytes := 0
	for index, row := range page.Documents {
		var metadata struct {
			NodeSets []string `json:"node_set"`
		}
		if len(row.Metadata) > 65536 || json.Unmarshal(row.Metadata, &metadata) != nil {
			continue
		}
		candidate := importKnowledgeMarker(row.Metadata)
		for _, tag := range metadata.NodeSets {
			candidate = candidate || tag == "session_learnings"
		}
		if !candidate {
			continue
		}
		content, err := r.ReadText(ctx, row, offset+index)
		if err != nil {
			return out, err
		}
		if content.SessionReference == "" && !importedKnowledge(content) {
			continue
		}
		totalBytes += len(content.Text)
		if totalBytes > 1024*1024 {
			return out, errors.New("knowledge page text budget exceeded")
		}
		if processing == nil {
			processing, err = r.readProcessing(ctx)
			if err != nil {
				return out, err
			}
		}
		completed, ok := processing[row.ID]
		if !ok {
			return out, ErrOfficialDocumentChanged
		}
		state := "not_completed"
		if completed {
			state = "completed"
		}
		title, fallback := learningTitle(content)
		out.Items = append(out.Items, records.OfficialKnowledge{Content: content, Title: title, TitleFromBody: fallback, ProcessingState: state})
	}
	// Recheck each returned metadata identity after status reads and other items.
	for _, item := range out.Items {
		fresh, err := r.find(ctx, item.Content.Document.ID, offset)
		if err != nil {
			return out, err
		}
		if records.MergeDigest(fresh) != records.MergeDigest(item.Content.Document) {
			return out, ErrOfficialDocumentChanged
		}
	}
	if err := r.verify(ctx); err != nil {
		return out, err
	}
	return out, nil
}

// An explicit import origin describes preserved knowledge, not AI-generated
// session lessons. Body verification prevents stale/mismatched classifications.
type importedKnowledgeIdentity struct {
	ImportID string `json:"import_id"`
	Library  string `json:"library"`
	Project  string `json:"project"`
	Memory   string `json:"memory"`
	Origin   string `json:"origin"`
	Digest   string `json:"digest"`
	Revision int    `json:"revision"`
}

func importKnowledgeMarker(metadata json.RawMessage) bool {
	var envelope struct {
		Import importedKnowledgeIdentity `json:"memlane_import_v1"`
	}
	if len(metadata) > 65536 || json.Unmarshal(metadata, &envelope) != nil {
		return false
	}
	i := envelope.Import
	digest, err := hex.DecodeString(i.Digest)
	return err == nil && len(digest) == 32 && strings.ToLower(i.Digest) == i.Digest && i.Origin == "knowledge" && i.Revision >= 1 && canonicalSessionScope(i.ImportID) && canonicalSessionScope(i.Library) && canonicalSessionScope(i.Project) && canonicalSessionScope(i.Memory)
}
func importedKnowledge(content records.OfficialDocumentText) bool {
	if !importKnowledgeMarker(content.Document.Metadata) {
		return false
	}
	var envelope struct {
		Import struct {
			Digest string `json:"digest"`
		} `json:"memlane_import_v1"`
	}
	if json.Unmarshal(content.Document.Metadata, &envelope) != nil {
		return false
	}
	sum := sha256.Sum256([]byte(content.Text))
	return hex.EncodeToString(sum[:]) == envelope.Import.Digest
}
