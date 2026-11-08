package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/codegraph"
)

func (c *Client) CodeBuild(ctx context.Context, g codegraph.Graph) (codegraph.Receipt, error) {
	var out codegraph.Receipt
	if c.CodeRecipe != "enola-0.4.30-v351" {
		return out, errors.New("worker component does not support the selected code graph recipe")
	}
	data, e := json.Marshal(g)
	if e != nil {
		return out, e
	}
	if len(data) > codegraph.MaxBytes {
		return out, errors.New("code graph import exceeds budget")
	}
	dir := filepath.Join(c.config.DataDir, "code-imports")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return out, e
	}
	id := uuid.NewString()
	path := filepath.Join(dir, id+".json")
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return out, e
	}
	defer os.Remove(path)
	_, e = f.Write(data)
	if e == nil {
		e = f.Sync()
	}
	closed := f.Close()
	if e != nil {
		return out, e
	}
	if closed != nil {
		return out, closed
	}
	e = c.Call(ctx, "code_build", map[string]any{"dataset": g.Dataset, "code_import": id}, &out)
	if e == nil && (out.Dataset != g.Dataset || out.Snapshot != g.Snapshot || out.SourceDigest != g.Manifest.Digest || out.Nodes != len(g.Nodes) || out.Edges != len(g.Edges) || out.Extractor != g.Extractor) {
		e = errors.New("code graph receipt mismatch")
	}
	return out, e
}
func (c *Client) CodeQuery(ctx context.Context, q codegraph.Query) (codegraph.Result, error) {
	var out codegraph.Result
	if c.CodeRecipe != "enola-0.4.30-v351" {
		return out, errors.New("worker code graph capability unavailable")
	}
	e := c.Call(ctx, "code_query", map[string]any{"code_query": q}, &out)
	if e == nil && (out.Receipt.Dataset != q.Dataset || out.Receipt.Snapshot != q.Snapshot) {
		e = errors.New("code query receipt mismatch")
	}
	return out, e
}
