package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) ReadSource(ctx context.Context, query records.SourceRead) (records.SourceRecord, error) {
	var out records.SourceRecord
	err := Call(ctx, l.Root, Request{Op: "source_read", SourceRead: &query}, &out)
	return out, err
}
