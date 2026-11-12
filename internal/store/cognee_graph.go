package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func migrateCogneeBrowsing(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE cognee_bindings(instance TEXT NOT NULL,dataset TEXT NOT NULL,project TEXT NOT NULL REFERENCES scopes(id),operation TEXT NOT NULL UNIQUE,digest TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(instance,dataset));PRAGMA user_version=15;`)
	return err
}

func (s *Store) AuthorizeCogneeBinding(ctx context.Context, b records.CogneeBinding) error {
	if b.MaterialSubmission {
		return access("scope_denied", "material authorization does not grant historical session import")
	}
	c, scopes, err := s.ResolveAgentScopes(ctx, records.AgentRequest{Protocol: 1, ConnectionID: b.ConnectionID, Generation: b.Generation, Workspace: b.Workspace, Tool: "memory_save", Arguments: records.AgentArguments{Scope: b.Project}})
	if err != nil {
		return err
	}
	if c.Agent != "codex" || c.ContextProject != b.Project || len(scopes) != 1 || scopes[0] != b.Project {
		return access("scope_denied", "Cognee material graph requires its approved Codex workspace")
	}
	return nil
}

func (s *Store) BindCognee(ctx context.Context, b records.CogneeBinding) (records.CogneeBinding, error) {
	var codeScopes int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM official_code_scopes WHERE instance=? AND dataset=?`, b.Instance, b.Dataset).Scan(&codeScopes); err != nil {
		return b, err
	}
	if codeScopes != 0 {
		return b, access("scope_denied", "code datasets do not grant knowledge/session access")
	}
	for _, id := range []string{b.Operation, b.Instance, b.Dataset, b.Project} {
		if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
			return b, access("invalid_request", "canonical binding identities required")
		}
	}
	u, err := url.Parse(b.Endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return b, access("invalid_request", "explicit local Cognee endpoint required")
	}
	if err = s.AuthorizeCogneeGraphBinding(ctx, b); err != nil {
		return b, err
	}
	encoded, err := json.Marshal(b)
	if err != nil {
		return b, err
	}
	sum := sha256.Sum256(encoded)
	digest := hex.EncodeToString(sum[:])
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return b, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM official_code_scopes WHERE instance=? AND dataset=?`, b.Instance, b.Dataset).Scan(&codeScopes); err != nil {
		return b, err
	}
	if codeScopes != 0 {
		return b, access("scope_denied", "code datasets do not grant knowledge/session access")
	}
	var prior, payload string
	err = tx.QueryRowContext(ctx, `SELECT digest,payload FROM cognee_bindings WHERE operation=? OR (instance=? AND dataset=?) UNION ALL SELECT digest,payload FROM pending_cognee_bindings WHERE operation=? OR (instance=? AND dataset=?)`, b.Operation, b.Instance, b.Dataset, b.Operation, b.Instance, b.Dataset).Scan(&prior, &payload)
	if err == nil {
		if prior != digest {
			return b, ErrConflict
		}
		err = json.Unmarshal([]byte(payload), &b)
		return b, err
	}
	if err != sql.ErrNoRows {
		return b, err
	}
	if b.ReadReviewDigest != "" || b.ReadPreviousOperation != "" {
		return b, access("invalid_request", "read reauthorization metadata requires its explicit review operation")
	}
	// The actual original-import ledger owns dataset→project association.
	prefix := "cognee://" + b.Dataset + "/"
	var foreign int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM memory_metadata md JOIN memories m ON m.id=md.memory JOIN operations o ON o.id='cognee-import-v1:'||m.id WHERE substr(json_extract(md.source_detail,'$.references[0]'),1,?)=? AND m.dataset!=?`, len(prefix), prefix, b.Project).Scan(&foreign)
	if err != nil {
		return b, err
	}
	if foreign > 0 {
		return b, access("scope_denied", "Cognee dataset already belongs to another project")
	}
	table := "cognee_bindings"
	var project int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM scopes WHERE id=? AND kind='project'`, b.Project).Scan(&project); err != nil {
		return b, err
	}
	if project == 0 {
		table = "pending_cognee_bindings"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO `+table+`(instance,dataset,project,operation,digest,payload) VALUES(?,?,?,?,?,?)`, b.Instance, b.Dataset, b.Project, b.Operation, digest, string(encoded))
	if err != nil {
		return b, err
	}
	return b, tx.Commit()
}

func (s *Store) CogneeBindings(ctx context.Context, project string) ([]records.CogneeBinding, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT payload FROM cognee_bindings WHERE project=? UNION ALL SELECT payload FROM pending_cognee_bindings WHERE project=? ORDER BY payload`, project, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []records.CogneeBinding{}
	for rows.Next() {
		var raw string
		var b records.CogneeBinding
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ProjectGraph is a read-only projection. Exact document identity and chunk
// text bind each assertion to imported current knowledge; no title matching,
// co-occurrence inference, model output or local extraction is used.
func (s *Store) ProjectCogneeGraph(ctx context.Context, b records.CogneeBinding, g records.CogneeGraph) (records.CogneeGraph, error) {
	out := records.CogneeGraph{Instance: b.Instance, Dataset: b.Dataset, Project: b.Project, CogneeVersion: g.CogneeVersion, State: "empty", Entities: []records.CogneeEntity{}, Mentions: []records.CogneeMention{}, Relations: []records.CogneeRelation{}, UnsupportedRelations: g.UnsupportedRelations}
	if err := s.AuthorizeCogneeGraphBinding(ctx, b); err != nil {
		return out, err
	}
	if g.Instance != b.Instance || g.Dataset != b.Dataset || g.CogneeVersion != "1.6.7" || len(g.Entities) > 4000 || len(g.Mentions) > 16000 || len(g.Relations) > 16000 {
		return out, access("invalid_provenance", "bounded graph from approved service required")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT m.id FROM memories m JOIN memory_metadata md ON md.memory=m.id JOIN operations o ON o.id='cognee-import-v1:'||m.id WHERE m.dataset=? AND m.state='active' AND md.origin='knowledge' AND substr(json_extract(md.source_detail,'$.references[0]'),1,?)=?`, b.Project, len("cognee://"+b.Dataset+"/"), "cognee://"+b.Dataset+"/")
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	documents := map[string]records.Version{}
	for _, id := range ids {
		if b.MaterialSubmission {
			continue
		} // new grants do not authorize legacy imported/session material
		v, err := scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+`WHERE m.id=?`, id))
		if err != nil {
			return out, err
		}
		var derived string
		err = tx.QueryRowContext(ctx, `SELECT state FROM derived_memories WHERE memory=? AND revision=?`, id, v.Revision).Scan(&derived)
		if err != nil && err != sql.ErrNoRows {
			return out, err
		}
		if v.State != "active" || v.Dataset != b.Project || derived != "valid" || v.Operation != "save" {
			continue
		}
		if v.SourceDetail == nil || len(v.SourceDetail.References) == 0 {
			continue
		}
		if v.SourceDetail.Official == nil || v.SourceDetail.Official.Instance != b.Instance || v.SourceDetail.Official.Kind != "data" {
			continue
		}
		u, err := url.Parse(v.SourceDetail.References[0])
		if err != nil {
			continue
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 4 && parts[2] == "data" {
			documents[parts[3]] = v
		}
	}
	// Independently submitted current materials carry a distinct durable proof.
	submitted, err := submittedCogneeDocuments(ctx, tx, b)
	if err != nil {
		return out, err
	}
	for document, v := range submitted {
		if prior, exists := documents[document]; exists && (prior.Memory != v.Memory || prior.Revision != v.Revision) {
			return out, access("invalid_provenance", "ambiguous official document")
		}
		documents[document] = v
	}
	bindSource := func(source records.CogneeGraphSource) (records.CogneeGraphSource, bool) {
		v, ok := documents[source.Document]
		if !ok || source.Quote == "" || len(source.Quote) > records.MergeInputLimit || !strings.Contains(v.Text, source.Quote) {
			return source, false
		}
		if _, err := uuid.Parse(source.Chunk); err != nil {
			return source, false
		}
		source.Memory, source.Revision, source.Origin = v.Memory, v.Revision, v.Origin
		return source, true
	}
	entities := map[string]records.CogneeEntity{}
	for _, entity := range g.Entities {
		if parsed, err := uuid.Parse(entity.ID); err != nil || parsed.String() != entity.ID || len(entity.Name) > 1024 || len(entity.Description) > 16384 || len(entity.Types) > 32 {
			return out, access("invalid_provenance", "invalid entity identity or text")
		}
		for _, kind := range entity.Types {
			if strings.TrimSpace(kind) == "" || len(kind) > 1024 {
				return out, access("invalid_provenance", "invalid entity type")
			}
		}
		entities[entity.ID] = entity
	}
	used := map[string]bool{}
	seenMentions := map[string]bool{}
	for _, mention := range g.Mentions {
		if _, ok := entities[mention.Entity]; !ok {
			continue
		}
		source, ok := bindSource(mention.CogneeGraphSource)
		if !ok {
			continue
		}
		key := mention.Entity + ":" + source.Chunk
		if seenMentions[key] {
			continue
		}
		seenMentions[key] = true
		mention.CogneeGraphSource = source
		out.Mentions = append(out.Mentions, mention)
		used[mention.Entity] = true
	}
	seenRelations := map[string]bool{}
	for _, relation := range g.Relations {
		if !used[relation.Source] || !used[relation.Target] || len(relation.Label) > 1024 {
			continue
		}
		if _, err := uuid.Parse(relation.ID); err != nil {
			return out, access("invalid_provenance", "invalid relation identity")
		}
		sources := []records.CogneeGraphSource{}
		for _, source := range relation.Sources {
			if source, ok := bindSource(source); ok {
				sources = append(sources, source)
			}
		}
		if len(sources) == 0 || seenRelations[relation.ID] {
			continue
		}
		seenRelations[relation.ID] = true
		relation.Sources = sources
		out.Relations = append(out.Relations, relation)
	}
	for _, entity := range g.Entities {
		if used[entity.ID] {
			out.Entities = append(out.Entities, entity)
		}
	}
	if len(out.Entities) > 0 {
		out.State = "available"
	}
	return out, tx.Commit()
}
