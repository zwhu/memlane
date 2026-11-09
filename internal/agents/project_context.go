package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

type ProjectKnowledgeContextReader func(context.Context, string) (records.ProjectKnowledgeContextPage, error)

type projectContextReader struct {
	read    ProjectKnowledgeContextReader
	pages   map[string][]byte
	issued  map[string]bool
	items   map[string]bool
	sent    map[string]bool
	total   int
	receipt records.ProjectKnowledgeContextReceipt
	digest  []byte
}

func newProjectContextReader(read ProjectKnowledgeContextReader) *projectContextReader {
	return &projectContextReader{read: read, pages: map[string][]byte{}, issued: map[string]bool{"": true}, items: map[string]bool{}, sent: map[string]bool{}, total: -1}
}

func (r *projectContextReader) page(ctx context.Context, cursor string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.read == nil || !r.issued[cursor] {
		return nil, errors.New("project knowledge context cursor not issued")
	}
	page, err := r.read(ctx, cursor)
	if err != nil {
		return nil, err
	}
	if body, ok := r.pages[cursor]; ok {
		current, e := json.Marshal(page)
		if e != nil || !bytes.Equal(current, body) {
			return nil, errors.New("project knowledge page changed after first read")
		}
		return body, nil
	}
	if page.Total < 0 || r.total >= 0 && page.Total != r.total || len(page.Items) == 0 && page.Next != "" {
		return nil, errors.New("project knowledge context changed or invalid page")
	}
	r.total = page.Total
	last := cursor
	for _, item := range page.Items {
		parsed, e := uuid.Parse(item.Memory)
		if e != nil || parsed.String() != item.Memory || item.Revision < 1 || item.Memory <= last || r.items[item.Memory] {
			return nil, errors.New("project knowledge context identity invalid")
		}
		last = item.Memory
	}
	if page.Next != "" && (len(page.Items) == 0 || page.Next != last) {
		return nil, errors.New("project knowledge continuation invalid")
	}
	if page.Next == "" && len(r.items)+len(page.Items) != page.Total {
		return nil, errors.New("project knowledge context incomplete")
	}
	body, err := json.Marshal(page)
	if err != nil || len(body) > 2*1024*1024 || r.receipt.Bytes+int64(len(body)) > 32*1024*1024 {
		return nil, errors.New("project knowledge context read budget exceeded")
	}
	for _, item := range page.Items {
		r.items[item.Memory] = true
	}
	r.issued[page.Next] = true
	r.pages[cursor] = body
	r.receipt.Pages++
	r.receipt.Items += len(page.Items)
	r.receipt.Bytes += int64(len(body))
	h := sha256.New()
	h.Write(r.digest)
	h.Write(body)
	r.digest = h.Sum(nil)
	r.receipt.Digest = hex.EncodeToString(r.digest)
	r.receipt.Complete = page.Next == ""
	return body, nil
}

func (r *projectContextReader) proof() *records.ProjectKnowledgeContextReceipt {
	if r.read == nil {
		return nil
	}
	value := r.receipt
	return &value
}

func (r *projectContextReader) markSent(cursor string) {
	if _, ok := r.pages[cursor]; ok && !r.sent[cursor] {
		r.sent[cursor] = true
		r.receipt.SentPages++
	}
}

func (r *projectContextReader) requireComplete() error {
	if r.read != nil && (!r.receipt.Complete || r.receipt.SentPages != r.receipt.Pages) {
		return fmt.Errorf("current project knowledge context must be read before repository citations and final additions")
	}
	return nil
}
