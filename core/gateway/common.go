package gateway

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

func validCommonCategory(category string) bool {
	return category == "preference" || category == "experience"
}

func (s *Server) commonList(w http.ResponseWriter, r *http.Request) {
	s.knowledgePage(w, r, "common", "")
}

func (s *Server) commonConfig(ctx context.Context) (domain.CommonConfig, error) {
	c, err := s.cfg.Store.GetCommonConfig(ctx)
	if err == nil && c.DatasetID == "" {
		err = domain.ErrNotFound
	}
	return c, err
}

func (s *Server) commonDocument(w http.ResponseWriter, r *http.Request) {
	c, err := s.commonConfig(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	s.serveDocument(w, r, c.DatasetID, r.PathValue("doc"))
}

type commonInput struct {
	Category *string `json:"common_category"`
	Title    *string `json:"title"`
	Label    *string `json:"label"`
	Content  *string `json:"content"`
}

func (s *Server) saveCommon(w http.ResponseWriter, r *http.Request) {
	var in commonInput
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	s.commonMu.Lock()
	defer s.commonMu.Unlock()
	ctx := r.Context()
	common, err := s.commonConfig(ctx)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	var category, title, content string
	old := r.PathValue("doc")
	if old != "" {
		record, body, _, err := s.loadDocument(ctx, common.DatasetID, old)
		if err != nil {
			writeEngineError(w, err)
			return
		}
		category, title, content = record.CommonCategory, record.Title, body
	}
	if in.Category != nil {
		category = *in.Category
	}
	if in.Label != nil {
		title = *in.Label
	}
	if in.Title != nil {
		title = *in.Title
	}
	if in.Content != nil {
		content = *in.Content
	}
	title = strings.TrimSpace(title)
	if !validCommonCategory(category) || title == "" || strings.TrimSpace(content) == "" || len(content) > maxDocumentBody {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	scope := engine.Scope{DatasetID: common.DatasetID}
	key := rand.Text()
	if old != "" {
		key = tupleDigest("common-replacement", common.DatasetID, old, category, title, content)
	}
	result, err := s.cfg.Engine.Ingest(ctx, engine.IngestRequest{Scope: scope, Kind: engine.IngestKindDocument, IdempotencyKey: key,
		Content: content, SourceMeta: engine.SourceMeta{NodeSet: []string{"memlane_common"},
			ExternalMetadata: map[string]any{"memlane_kind": "common_knowledge", "category": category, "common_category": category, "title": title, "label": title}}})
	if err != nil {
		writeEngineError(w, err)
		return
	}
	doc := result.DocumentID
	if doc == "" {
		doc = result.EntryID
	}
	if !result.OK || doc == "" {
		writeEngineError(w, errors.New("common ingest was not accepted"))
		return
	}
	if old != "" && old != doc {
		if err := s.cfg.Engine.DeleteItem(ctx, scope, old); err != nil && !errors.Is(err, engine.ErrNotFound) {
			writeEngineError(w, err)
			return
		}
	}
	if err := s.publishCommon(ctx, scope.DatasetID, doc, old, category, title); err != nil {
		writeEngineError(w, err)
		return
	}
	status := 201
	if old != "" {
		status = 200
	}
	writeJSON(w, status, map[string]any{"ok": true, "dataset_id": scope.DatasetID, "document_id": doc, "doc_id": doc,
		"common_category": category, "title": title, "content": content})
}

func (s *Server) publishCommon(ctx context.Context, ds, doc, old, category, title string) error {
	if s.cfg.Projector != nil {
		_, err := s.cfg.Projector.SyncCatalog(ctx, s.cfg.Store)
		return err
	}
	now := time.Now()
	if err := s.cfg.Store.UpsertCatalogDoc(ctx, domain.CatalogDoc{DatasetID: ds, DocID: doc, Kind: "common", IsKnowledge: true,
		CommonCategory: category, DocLabel: title, CreatedAtRaw: domain.FormatUTCNS(now), CreatedAtNS: domain.FormatUTCNS(now),
		EffectiveDate: now.Format(time.DateOnly), SortTSDescKey: -now.UnixNano()}); err != nil {
		return err
	}
	if old != "" && old != doc {
		return s.syncDocumentDeletion(ctx, ds, old)
	}
	return nil
}

func (s *Server) deleteCommon(w http.ResponseWriter, r *http.Request) {
	s.commonMu.Lock()
	defer s.commonMu.Unlock()
	common, err := s.commonConfig(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if err := s.removeDocument(r.Context(), common.DatasetID, r.PathValue("doc")); err != nil {
		writeEngineError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) commonACL(w http.ResponseWriter, r *http.Request) {
	var in aclBody
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	if in.Access == nil || *in.Access != domain.AccessRead && *in.Access != domain.AccessNone || in.Capture != nil || in.AutoProcess != nil {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	ctx := r.Context()
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	in.AgentID = r.PathValue("agent_id")
	if err := s.activeAgent(ctx, in.AgentID); err != nil {
		writeDomainError(w, err)
		return
	}
	common, err := s.commonConfig(ctx)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if err := s.cfg.Store.SetACLOverride(ctx, domain.ACLOverride{TargetKind: "common", TargetID: common.ID, AgentID: in.AgentID, Access: in.Access}); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, in)
}

func commonSummary(ctx context.Context, tx *sql.Tx) (*projectSummary, error) {
	var out projectSummary
	err := tx.QueryRowContext(ctx, "SELECT id,name,dataset_id FROM common_config WHERE dataset_id<>''").Scan(&out.ProjectID, &out.Name, &out.DatasetID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	err = catalogCounts(ctx, tx, out.DatasetID, &out)
	return &out, err
}
