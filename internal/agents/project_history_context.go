package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/zwhu/memlane/internal/records"
)

type ProjectHistoryContextReader func(context.Context, string) (records.ProjectRepositorySource, error)
type projectHistoryContext struct {
	reader   ProjectHistoryContextReader
	selected map[string]string
	read     map[string]records.ProjectRepositorySource
	sent     map[string]bool
	bytes    int64
}

func newProjectHistoryContext(reader ProjectHistoryContextReader, selections []records.ProjectSessionSource) *projectHistoryContext {
	r := &projectHistoryContext{reader: reader, selected: map[string]string{}, read: map[string]records.ProjectRepositorySource{}, sent: map[string]bool{}}
	for _, selection := range selections {
		r.selected[selection.ID] = selection.Digest
	}
	return r
}
func (r *projectHistoryContext) page(ctx context.Context, id string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.reader == nil || r.selected[id] == "" {
		return nil, errors.New("session outside frozen historical scope")
	}
	source, err := r.reader(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source.History == nil || source.History.Session != id || source.History.Digest != r.selected[id] {
		return nil, errors.New("historical source differs from selected version")
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	if cached, ok := r.read[id]; ok {
		prior, _ := json.Marshal(cached)
		if string(prior) != string(encoded) {
			return nil, errors.New("frozen historical payload changed")
		}
	} else {
		if len(encoded) > 6*1024*1024 || r.bytes+int64(len(encoded)) > 48*1024*1024 {
			return nil, errors.New("historical tool responses exceed budget; nothing truncated")
		}
		r.read[id] = source
		r.bytes += int64(len(encoded))
	}
	return encoded, nil
}
func (r *projectHistoryContext) requireComplete() error {
	if len(r.read) != len(r.selected) || len(r.sent) != len(r.selected) {
		return errors.New("selected history was not completely read and sent")
	}
	return nil
}
func (r *projectHistoryContext) markSent(id string) {
	if _, ok := r.read[id]; ok {
		r.sent[id] = true
	}
}
func (r *projectHistoryContext) sources() []records.ProjectRepositorySource {
	ids := make([]string, 0, len(r.read))
	for id := range r.read {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]records.ProjectRepositorySource, 0, len(ids))
	for _, id := range ids {
		out = append(out, r.read[id])
	}
	return out
}
func (r *projectHistoryContext) proof() *records.ProjectHistoryContextReceipt {
	if len(r.selected) == 0 {
		return nil
	}
	digest := sha256.New()
	for _, source := range r.sources() {
		b, _ := json.Marshal(source)
		digest.Write(b)
		digest.Write([]byte{'\n'})
	}
	return &records.ProjectHistoryContextReceipt{Selected: len(r.selected), Read: len(r.read), Sent: len(r.sent), Bytes: r.bytes, Digest: hex.EncodeToString(digest.Sum(nil))}
}
