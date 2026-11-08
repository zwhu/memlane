// Package codegraph owns bounded, read-only source snapshots and static code facts.
// Only the trusted service chooses enabled worktrees and publishes worker receipts.
package codegraph

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Lines  int    `json:"lines"`
}
type Manifest struct {
	Worktree  string `json:"worktree"`
	CommonDir string `json:"common_dir"`
	Commit    string `json:"commit"`
	Recipe    string `json:"recipe"`
	Digest    string `json:"digest"`
	Files     []File `json:"files"`
}
type Node struct {
	ExtractorID string `json:"extractor_id"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Language    string `json:"language"`
	SymbolKind  string `json:"symbol_kind"`
	FileSHA     string `json:"file_sha"`
	Unresolved  bool   `json:"unresolved"`
}
type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}
type Graph struct {
	Dataset   string   `json:"dataset"`
	Snapshot  string   `json:"snapshot"`
	Manifest  Manifest `json:"manifest"`
	Extractor string   `json:"extractor"`
	Nodes     []Node   `json:"nodes"`
	Edges     []Edge   `json:"edges"`
}
type Receipt struct {
	Dataset      string `json:"dataset"`
	Snapshot     string `json:"snapshot"`
	GraphDigest  string `json:"graph_digest"`
	SourceDigest string `json:"source_digest"`
	Nodes        int    `json:"nodes"`
	Edges        int    `json:"edges"`
	Extractor    string `json:"extractor"`
}
type Query struct {
	Dataset  string `json:"dataset"`
	Snapshot string `json:"snapshot"`
	Mode     string `json:"mode"`
	Text     string `json:"text"`
	Node     string `json:"node"`
	Limit    int    `json:"limit"`
	Hops     int    `json:"hops"`
}
type Result struct {
	Receipt   Receipt  `json:"receipt"`
	Manifest  Manifest `json:"manifest"`
	Nodes     []Node   `json:"nodes"`
	Edges     []Edge   `json:"edges"`
	Truncated bool     `json:"truncated"`
}
