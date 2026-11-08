package records

// A host-confirmed service/dataset binding. Endpoint and project come from
// the trusted App, never from an Agent graph query or extraction result.
type CogneeBinding struct {
	Operation    string `json:"operation"`
	Instance     string `json:"instance"`
	Dataset      string `json:"dataset"`
	Project      string `json:"project"`
	Endpoint     string `json:"endpoint"`
	ConnectionID string `json:"connection_id"`
	Generation   int64  `json:"generation"`
	Workspace    string `json:"workspace"`
}

type CogneeEntity struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}
type CogneeGraphSource struct {
	Document   string `json:"document"`
	Chunk      string `json:"chunk"`
	ChunkIndex *int   `json:"chunk_index,omitempty"`
	Quote      string `json:"quote"`
	Memory     string `json:"memory,omitempty"`
	Revision   int64  `json:"revision,omitempty"`
}
type CogneeMention struct {
	CogneeGraphSource
	Entity string `json:"entity"`
}
type CogneeRelation struct {
	ID      string              `json:"id"`
	Source  string              `json:"source"`
	Target  string              `json:"target"`
	Label   string              `json:"label"`
	Sources []CogneeGraphSource `json:"sources"`
}
type CogneeGraph struct {
	Instance             string           `json:"instance"`
	Dataset              string           `json:"dataset"`
	CogneeVersion        string           `json:"cognee_version"`
	Project              string           `json:"project,omitempty"`
	State                string           `json:"state,omitempty"`
	Entities             []CogneeEntity   `json:"entities"`
	Mentions             []CogneeMention  `json:"mentions"`
	Relations            []CogneeRelation `json:"relations"`
	UnsupportedRelations int              `json:"unsupported_relations"`
}

type CogneeGraphQuery struct {
	Project string `json:"project"`
	Dataset string `json:"dataset,omitempty"`
}

// Credential is transient host input. It must never enter a binding, source,
// database operation digest, UI history or response.
type CogneeAuthorization struct {
	Binding    CogneeBinding `json:"binding"`
	Credential string        `json:"credential"`
}
type CogneeSync struct {
	Project string `json:"project"`
	Dataset string `json:"dataset"`
	Session string `json:"session"`
}
type CogneeMaterial struct {
	Instance       string           `json:"instance"`
	Dataset        string           `json:"dataset"`
	Session        string           `json:"session"`
	CogneeVersion  string           `json:"cognee_version"`
	State          string           `json:"state"`
	Raw            []CogneeRaw      `json:"raw"`
	Documents      []CogneeDocument `json:"documents"`
	PersistedQA    int              `json:"persisted_qa"`
	PersistedTrace int              `json:"persisted_trace"`
}
type CogneeRaw struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Text       string `json:"text"`
	Sequence   int    `json:"sequence"`
	CapturedAt string `json:"captured_at"`
	Persisted  bool   `json:"persisted"`
}
type CogneeDocument struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
	Processed bool   `json:"processed"`
}
type CogneeSyncReceipt struct {
	SessionOriginal  *SaveReceipt  `json:"session_original,omitempty"`
	OriginalState    string        `json:"original_state,omitempty"`
	Project          string        `json:"project"`
	Dataset          string        `json:"dataset"`
	Session          string        `json:"session"`
	OfficialState    string        `json:"official_state"`
	Raw              []SaveReceipt `json:"raw"`
	Knowledge        []SaveReceipt `json:"knowledge"`
	PendingDocuments int           `json:"pending_documents"`
}
