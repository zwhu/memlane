package daemon

import (
	"context"
	"errors"

	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) readOfficialDocument(ctx context.Context, q records.OfficialDocumentQuery, detail bool) (any, error) {
	mode := "list"
	if detail {
		mode = "text"
	}
	return s.readOfficialDocumentMode(ctx, q, mode)
}

func (s *Server) readOfficialDocumentMode(ctx context.Context, q records.OfficialDocumentQuery, mode string) (any, error) {
	scope := records.SessionQuery{Project: q.Project, Instance: q.Instance, Dataset: q.Dataset}
	b, err := s.sessionBinding(ctx, scope)
	if err != nil {
		return nil, err
	}
	token, err := s.cogneeToken(b.Instance, b.Dataset)
	if err != nil {
		return nil, err
	}
	r := official.DocumentReader{SessionReader: official.SessionReader{Endpoint: b.Endpoint, Instance: b.Instance, Dataset: b.Dataset, Token: token}}
	var out any
	if mode == "knowledge" {
		out, err = r.ListKnowledgePage(ctx, q.Limit, q.Offset)
	} else if mode == "text" || mode == "session" || mode == "knowledge_text" || mode == "import_source" {
		if q.Selected == nil {
			return nil, &records.AccessError{Code: "invalid_request", Message: "selected official document required"}
		}
		if mode == "import_source" {
			out, err = r.ReadImportedSource(ctx, *q.Selected, q.SourceDocument, q.Offset)
		} else if mode == "knowledge_text" {
			out, err = r.ReadKnowledge(ctx, *q.Selected, q.Offset)
		} else if mode == "session" {
			out, err = r.ReadSession(ctx, *q.Selected, q.Offset)
		} else {
			out, err = r.ReadText(ctx, *q.Selected, q.Offset)
		}
	} else {
		out, err = r.ListPage(ctx, q.Limit, q.Offset)
	}
	if err != nil {
		if errors.Is(err, official.ErrOfficialDocumentNoSource) {
			return nil, &records.AccessError{Code: "source_not_found", Message: "official document has no verified imported source"}
		}
		if errors.Is(err, official.ErrOfficialDocumentNoSession) {
			return nil, &records.AccessError{Code: "source_not_found", Message: "official document has no verified session reference"}
		}
		if errors.Is(err, official.ErrOfficialDocumentUnsupported) {
			return nil, &records.AccessError{Code: "unsupported_content", Message: "this official document cannot be previewed as text"}
		}
		if errors.Is(err, official.ErrOfficialDocumentChanged) {
			return nil, &records.AccessError{Code: "stale_source", Message: "official document changed; refresh its metadata"}
		}
		mapped := sessionReadError(err)
		var access *records.AccessError
		if errors.As(mapped, &access) {
			return nil, &records.AccessError{Code: access.Code, Message: "official document read could not be verified"}
		}
		return nil, mapped
	}
	fresh, err := s.sessionBinding(ctx, scope)
	if err != nil {
		return nil, err
	}
	if fresh != b {
		return nil, &records.AccessError{Code: "scope_changed", Message: "official document authorization changed"}
	}
	return out, nil
}
