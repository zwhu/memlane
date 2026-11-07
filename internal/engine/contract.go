// Package engine defines the raw-text contract of the independently managed worker.
package engine

import "context"

type Record struct {
	Dataset  string `json:"dataset"`
	Memory   string `json:"memory"`
	Revision int64  `json:"revision"`
	Text     string `json:"text"`
}

type Receipt struct {
	ChunkIDs   []string `json:"chunk_ids"`
	Generation string   `json:"generation"`
}

type Candidate struct {
	ChunkID    string  `json:"chunk_id"`
	Dataset    string  `json:"dataset"`
	Memory     string  `json:"memory"`
	Revision   int64   `json:"revision"`
	Generation string  `json:"generation"`
	Text       string  `json:"text"`
	StartByte  int     `json:"start_byte"`
	EndByte    int     `json:"end_byte"`
	Score      float32 `json:"score"`
}

type Worker interface {
	Index(context.Context, Record) (Receipt, error)
	Search(context.Context, string, string, int) ([]Candidate, error)
}
