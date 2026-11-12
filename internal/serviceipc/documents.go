package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) OfficialDocumentSource(ctx context.Context, q records.OfficialDocumentQuery) (records.OfficialDocumentSource, error) {
	var out records.OfficialDocumentSource
	err := Call(ctx, l.Root, Request{Op: "official_document_source", DocumentQuery: &q}, &out)
	return out, err
}

func (l Library) OfficialKnowledge(ctx context.Context, q records.OfficialDocumentQuery) (records.OfficialKnowledgePage, error) {
	var out records.OfficialKnowledgePage
	err := Call(ctx, l.Root, Request{Op: "official_knowledge", DocumentQuery: &q}, &out)
	return out, err
}

func (l Library) OfficialKnowledgeText(ctx context.Context, q records.OfficialDocumentQuery) (records.OfficialKnowledge, error) {
	var out records.OfficialKnowledge
	err := Call(ctx, l.Root, Request{Op: "official_knowledge_text", DocumentQuery: &q}, &out)
	return out, err
}

func (l Library) OfficialDocuments(ctx context.Context, q records.OfficialDocumentQuery) (records.OfficialDocumentPage, error) {
	var out records.OfficialDocumentPage
	err := Call(ctx, l.Root, Request{Op: "official_documents", DocumentQuery: &q}, &out)
	return out, err
}

func (l Library) OfficialDocumentText(ctx context.Context, q records.OfficialDocumentQuery) (records.OfficialDocumentText, error) {
	var out records.OfficialDocumentText
	err := Call(ctx, l.Root, Request{Op: "official_document_text", DocumentQuery: &q}, &out)
	return out, err
}

func (l Library) OfficialDocumentSession(ctx context.Context, q records.OfficialDocumentQuery) (records.OfficialDocumentSession, error) {
	var out records.OfficialDocumentSession
	err := Call(ctx, l.Root, Request{Op: "official_document_session", DocumentQuery: &q}, &out)
	return out, err
}
