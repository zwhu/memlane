package official

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/zwhu/memlane/internal/records"
)

var ErrOfficialDocumentNoSource = errors.New("official document has no consistent imported source")

// ImportedKnowledgeSources identifies possible links only. Access and the
// target's preserved identity/body are verified again by ReadImportedSource.
func ImportedKnowledgeSources(content records.OfficialDocumentText) []records.OfficialDocumentReference {
	if !importedKnowledge(content) {
		return nil
	}
	var envelope struct {
		Import struct {
			Project string                              `json:"project"`
			Sources []records.OfficialDocumentReference `json:"sources"`
		} `json:"memlane_import_v1"`
	}
	if json.Unmarshal(content.Document.Metadata, &envelope) != nil || len(envelope.Import.Sources) > 64 {
		return nil
	}
	seen := map[string]bool{}
	for _, ref := range envelope.Import.Sources {
		digest, err := hex.DecodeString(ref.Digest)
		if !canonicalSessionScope(ref.Document) || !canonicalSessionScope(ref.Memory) || ref.Project != envelope.Import.Project || ref.Revision < 1 || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != ref.Digest || seen[ref.Document] {
			return nil
		}
		seen[ref.Document] = true
	}
	return envelope.Import.Sources
}

func (r DocumentReader) ReadImportedSource(ctx context.Context, selected records.OfficialDocument, target string, hint int) (records.OfficialDocumentSource, error) {
	var out records.OfficialDocumentSource
	parent, err := r.ReadText(ctx, selected, hint)
	if err != nil {
		return out, err
	}
	var ref *records.OfficialDocumentReference
	for _, candidate := range ImportedKnowledgeSources(parent) {
		if candidate.Document == target {
			c := candidate
			ref = &c
		}
	}
	if ref == nil {
		return out, ErrOfficialDocumentNoSource
	}
	row, err := r.find(ctx, target, 0)
	if err != nil {
		return out, err
	}
	content, err := r.ReadText(ctx, row, 0)
	if err != nil {
		return out, err
	}
	var owner, source struct {
		Import importedKnowledgeIdentity `json:"memlane_import_v1"`
	}
	if json.Unmarshal(parent.Document.Metadata, &owner) != nil || json.Unmarshal(content.Document.Metadata, &source) != nil {
		return out, ErrOfficialDocumentNoSource
	}
	i := source.Import
	digest := sha256.Sum256([]byte(content.Text))
	if i.Library != owner.Import.Library || i.Project != ref.Project || i.Memory != ref.Memory || i.Revision != ref.Revision || i.Origin != "raw" || !canonicalSessionScope(i.ImportID) || i.Digest != ref.Digest || hex.EncodeToString(digest[:]) != ref.Digest {
		return out, ErrOfficialDocumentNoSource
	}
	// Parent metadata must still contain exactly the link that authorized this
	// target. Recheck after reading the target, then check service identity.
	after, err := r.find(ctx, selected.ID, hint)
	if err != nil {
		return out, err
	}
	if records.MergeDigest(after) != records.MergeDigest(parent.Document) {
		return out, ErrOfficialDocumentChanged
	}
	if err := r.verify(ctx); err != nil {
		return out, err
	}
	return records.OfficialDocumentSource{Document: after, Reference: *ref, Content: content}, nil
}
