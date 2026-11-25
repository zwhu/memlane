package profiles

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

const markerPrefix = "<!-- memlane-user-profile "
const markerEnd = " -->\n\n"

func randomID() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func validateBody(body string) error {
	if len(body) > MaxDocumentBytes || !utf8.ValidString(body) || strings.TrimSpace(body) == "" || strings.ContainsRune(body, 0) {
		return ErrInvalidDocument
	}
	return nil
}

func (s *Service) save(ctx context.Context, row domain.UserProfile, body string) (domain.DocumentRef, string, error) {
	fail := func(err error) (domain.DocumentRef, string, error) {
		return domain.DocumentRef{}, "", errors.Join(ErrSaveUnconfirmed, err)
	}
	meta := map[string]any{"memlane_kind": "user_profile", "profile_source": row.InstanceID, "profile_operation": row.OperationID,
		"profile_method": row.Method, "generated_at": row.StartedAt}
	marker, _ := json.Marshal(struct {
		Source    string `json:"source"`
		Operation string `json:"operation"`
	}{row.InstanceID, row.OperationID})
	marked := markerPrefix + string(marker) + markerEnd + body
	sha, md := sha256.Sum256([]byte(marked)), md5.Sum([]byte(marked))
	digest, legacyDigest := hex.EncodeToString(sha[:]), hex.EncodeToString(md[:])
	matches := func(hash string) bool {
		return hash == "" || strings.EqualFold(hash, digest) || strings.EqualFold(hash, legacyDigest)
	}
	scope := engine.Scope{DatasetID: row.DatasetID}
	receipt, err := s.cfg.Engine.Ingest(ctx, engine.IngestRequest{Scope: scope, Kind: engine.IngestKindDocument,
		IdempotencyKey: row.OperationID, Content: marked, SourceMeta: engine.SourceMeta{ExternalMetadata: meta}})
	if err != nil || !receipt.OK || strings.TrimSpace(receipt.DocumentID) == "" || !matches(receipt.ContentHash) {
		return fail(err)
	}
	item, err := s.cfg.Engine.GetItem(ctx, scope, receipt.DocumentID)
	if err != nil || item.ID != "" && item.ID != receipt.DocumentID || item.DatasetID != "" && item.DatasetID != scope.DatasetID ||
		!matches(item.ContentHash) {
		return fail(err)
	}
	body, err = s.itemBody(ctx, scope, receipt.DocumentID, item)
	if err != nil || body != "" && body != marked ||
		body == "" && item.ContentHash == "" && (receipt.Deduplicated || receipt.ContentHash == "") {
		return fail(err)
	}
	return domain.DocumentRef{DatasetID: scope.DatasetID, DataID: receipt.DocumentID}, digest, nil
}

func (s *Service) itemBody(ctx context.Context, scope engine.Scope, id string, item engine.Item) (string, error) {
	if item.Content != "" || item.RawDataLocation == "" {
		return item.Content, nil
	}
	if reader, ok := engine.AsCatalogProjector(s.cfg.Engine); ok {
		return reader.ReadLocalBody(ctx, engine.LocalBodyRef{DatasetID: scope.DatasetID, DocumentID: id,
			Location: item.RawDataLocation, MimeType: item.MimeType, MaxBytes: MaxDocumentBytes + 4096})
	}
	return "", nil
}

func stripMarker(body string) string {
	if rest, ok := strings.CutPrefix(body, markerPrefix); ok {
		metadata, markdown, found := strings.Cut(rest, markerEnd)
		var marker struct{ Source, Operation string }
		if found && json.Unmarshal([]byte(metadata), &marker) == nil && marker.Source != "" && marker.Operation != "" {
			return markdown
		}
	}
	return body
}

func (s *Service) profile(ctx context.Context, source string) (domain.UserProfile, error) {
	id := sourceID(source, "agent")
	row, err := s.cfg.Store.GetUserProfile(ctx, id)
	if errors.Is(err, domain.ErrNotFound) && strings.EqualFold(strings.TrimSpace(source), "claude") {
		return s.cfg.Store.GetUserProfile(ctx, "manual:claude")
	}
	return row, err
}

func (s *Service) Get(ctx context.Context, source string) (domain.UserProfile, string, error) {
	if !knownSource(sourceID(source, "agent")) {
		return domain.UserProfile{}, "", domain.ErrNotFound
	}
	row, err := s.profile(ctx, source)
	if err != nil {
		return row, "", err
	}
	if row.Document.DataID == "" {
		return row, "", domain.ErrNotFound
	}
	scope := engine.Scope{DatasetID: row.Document.DatasetID}
	item, err := s.cfg.Engine.GetItem(ctx, scope, row.Document.DataID)
	if err != nil {
		return row, "", err
	}
	body, err := s.itemBody(ctx, scope, row.Document.DataID, item)
	if err != nil {
		return row, "", err
	}
	body = stripMarker(body)
	return row, body, validateBody(body)
}

func (s *Service) Delete(ctx context.Context, source string) error {
	if !knownSource(sourceID(source, "agent")) {
		return domain.ErrNotFound
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return engine.ErrUnavailable
	}
	id := sourceID(source, "agent")
	if s.active[id] != nil {
		s.mu.Unlock()
		return domain.ErrConflict
	}
	row, err := s.profile(ctx, source)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if s.active[row.InstanceID] != nil {
		s.mu.Unlock()
		return domain.ErrConflict
	}
	op := &operation{row: domain.UserProfile{Status: "saving"}, done: make(chan struct{})}
	s.active[row.InstanceID] = op
	s.mu.Unlock()
	defer s.finish(row.InstanceID, op)
	ctx = context.WithoutCancel(ctx)
	if row.Document.DataID != "" {
		err = s.cfg.Engine.DeleteItem(ctx, engine.Scope{DatasetID: row.Document.DatasetID}, row.Document.DataID)
		if err != nil && !errors.Is(err, engine.ErrNotFound) {
			return err
		}
	}
	return s.cfg.Store.DeleteUserProfile(ctx, row.InstanceID)
}
