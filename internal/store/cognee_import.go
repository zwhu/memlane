package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// ImportCognee reuses original versions, the operation ledger, index jobs and
// immutable organization_sources. No schema change or external network call is
// needed inside the transaction. The daemon holds its authorization barrier.
func (s *Store) ImportCognee(ctx context.Context, d records.CogneeImport) (records.SaveReceipt, error) {
	var receipt records.SaveReceipt
	invalid := func() error {
		return access("invalid_request", "bounded Cognee result and immutable source references required")
	}
	u, err := url.Parse(d.ExternalID)
	parts := []string{}
	if u != nil {
		parts = strings.Split(strings.Trim(u.Path, "/"), "/")
	}
	if err != nil || u.Scheme != "cognee" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(parts) != 4 || parts[0] != "sessions" || len(parts[1]) == 0 || len(parts[1]) > 128 || len(d.ExternalID) > 512 {
		return receipt, invalid()
	}
	if _, err = uuid.Parse(u.Host); err != nil {
		return receipt, invalid()
	}
	if _, err = uuid.Parse(parts[3]); err != nil {
		return receipt, invalid()
	}
	datasetID, _ := uuid.Parse(u.Host)
	entryID, _ := uuid.Parse(parts[3])
	canonical := "cognee://" + datasetID.String() + "/sessions/" + parts[1] + "/" + parts[2] + "/" + entryID.String()
	if d.ExternalID != canonical || strings.ContainsAny(parts[1], " \t\r\n\\") {
		return receipt, invalid()
	}
	if d.Origin == "raw" {
		if (parts[2] != "qa" && parts[2] != "trace" && (parts[2] != "native" || d.Native == nil)) || len(d.Sources) != 0 {
			return receipt, invalid()
		}
	} else if d.Origin != "knowledge" || parts[2] != "data" || len(d.Sources) == 0 || len(d.Sources) > 16 {
		return receipt, invalid()
	}
	textLimit := records.MergeInputLimit
	if d.Origin == "raw" {
		textLimit = records.CogneeRawLimit
	}
	if strings.TrimSpace(d.Text) == "" || len(d.Text) > textLimit || len(d.Fragment) > records.MergeInputLimit || len(d.Title) > 512 || !utf8.ValidString(d.Text) || !utf8.ValidString(d.Fragment) || d.Workspace == "" {
		return receipt, invalid()
	}
	r := records.AgentRequest{Protocol: 1, ConnectionID: d.ConnectionID, Generation: d.Generation, Workspace: d.Workspace, Tool: "memory_save", Arguments: records.AgentArguments{Scope: d.Project}}
	c, scopes, err := s.ResolveAgentScopes(ctx, r)
	if err != nil {
		return receipt, err
	}
	if c.Agent != "codex" || c.ContextProject == "" || c.ContextProject != d.Project || len(scopes) != 1 || scopes[0] != d.Project {
		return receipt, access("scope_denied", "Cognee import requires its approved Codex workspace project")
	}
	refs := append([]records.MergeSource(nil), d.Sources...)
	sort.Slice(refs, func(i, j int) bool { return refs[i].Memory < refs[j].Memory })
	detail := &records.AgentSource{Fragment: d.Fragment, References: []string{d.ExternalID}}
	if d.Native != nil {
		hash := sha256.Sum256([]byte(d.Text))
		if d.Origin != "raw" || parts[2] != "native" || d.Material != nil || d.Native.Dataset != u.Host || d.Native.Session != parts[1] || d.Native.Workspace != d.Workspace || d.Native.CLIVersion != "0.167.0-alpha.3.2" || d.Native.Digest != hex.EncodeToString(hash[:]) {
			return receipt, invalid()
		}
		if id, e := uuid.Parse(d.Native.Instance); e != nil || id.String() != d.Native.Instance {
			return receipt, invalid()
		}
		detail.Native = d.Native
	}
	if d.Material != nil {
		if d.Material.Session != parts[1] || d.Material.Kind != parts[2] || d.Material.Sequence < 0 || d.Material.OfficialState != "persisted" {
			return receipt, invalid()
		}
		if parsed, e := uuid.Parse(d.Material.Instance); e != nil || parsed.String() != d.Material.Instance {
			return receipt, invalid()
		}
		if d.Material.CapturedAt != "" {
			if _, e := time.Parse(time.RFC3339Nano, d.Material.CapturedAt); e != nil {
				return receipt, invalid()
			}
		}
		detail.Official = d.Material
	}
	for i, ref := range refs {
		if _, err = uuid.Parse(ref.Memory); err != nil || ref.Revision < 1 || strings.TrimSpace(ref.Quote) == "" || len(ref.Quote) > records.MergeInputLimit || !utf8.ValidString(ref.Quote) || (i > 0 && refs[i-1].Memory == ref.Memory) {
			return receipt, invalid()
		}
		h := sha256.Sum256([]byte(ref.Quote))
		// The quoted content is bound into the existing mutation digest, while
		// readable links keep identity/version separate from displayed prose.
		detail.References = append(detail.References, "memlane://"+ref.Memory+"@v"+stringRevision(ref.Revision)+"/"+hex.EncodeToString(h[:]))
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("cognee-import-v1\x00"+d.Project+"\x00"+d.ExternalID)).String()
	m := Mutation{OperationID: "cognee-import-v1:" + id, Kind: "save", Memory: id, Dataset: d.Project, ExpectedRevision: 0, Text: d.Text, Title: d.Title, Source: c.Agent, Origin: d.Origin, SourceDetail: detail}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return receipt, err
	}
	defer tx.Rollback()
	if c.PendingProject != "" {
		if err = s.acceptFirstProjectTx(ctx, tx, c); err != nil {
			return receipt, err
		}
	}
	// A dataset is bound by the first original received under an approved
	// workspace. Original records cannot be moved by the review tools, so this
	// immutable metadata remains authoritative when derived knowledge moves.
	// Check inside the same transaction as creation to serialize first imports.
	namespace := "cognee://" + datasetID.String() + "/"
	var foreign int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM memory_metadata md JOIN memories m ON m.id=md.memory JOIN operations o ON o.memory=m.id AND o.id='cognee-import-v1:'||m.id WHERE md.origin='raw' AND substr(json_extract(md.source_detail,'$.references[0]'),1,?)=? AND m.dataset!=?`, len(namespace), namespace, d.Project).Scan(&foreign); err != nil {
		return receipt, err
	}
	if foreign != 0 {
		return receipt, access("scope_denied", "Cognee dataset is already bound to another approved project")
	}
	var prior int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM operations WHERE id=?`, m.OperationID).Scan(&prior); err != nil {
		return receipt, err
	}
	inputs := []records.OrganizationInput{}
	if prior == 0 {
		for _, ref := range refs {
			v, e := scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+`WHERE m.id=?`, ref.Memory))
			if e != nil {
				return receipt, e
			}
			if v.Dataset != d.Project || v.Revision != ref.Revision || v.State != "active" || v.Origin != "raw" || !strings.Contains(v.Text, ref.Quote) {
				return receipt, access("source_stale", "Cognee knowledge needs current original records in the same project")
			}
			var trusted int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM operations WHERE memory=? AND id=?`, ref.Memory, "cognee-import-v1:"+ref.Memory).Scan(&trusted); err != nil {
				return receipt, err
			}
			if trusted != 1 || v.SourceDetail == nil || len(v.SourceDetail.References) == 0 || !strings.HasPrefix(v.SourceDetail.References[0], namespace+"sessions/"+parts[1]+"/") {
				return receipt, access("invalid_provenance", "Cognee session learning requires trusted originals from its own dataset and session")
			}
			roots, e := s.evidenceRoots(ctx, tx, v.Memory, v.Revision)
			if e != nil {
				return receipt, e
			}
			inputs = append(inputs, records.OrganizationInput{MergeInput: records.MergeInput{Memory: v.Memory, Revision: v.Revision, Title: v.Title, Text: v.Text, Source: v.Source, SourceDetail: v.SourceDetail, CapturedAt: v.CreatedAt}, Origin: v.Origin, Roots: roots})
		}
	}
	v, err := s.applyTx(ctx, tx, m)
	if err != nil {
		return receipt, err
	}
	if prior == 0 && d.Origin == "knowledge" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO derived_memories(memory,revision,state) VALUES(?,?,'valid')`, id, v.Revision); err != nil {
			return receipt, err
		}
		for i, ref := range refs {
			b, e := json.Marshal(inputs[i])
			if e != nil {
				return receipt, e
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO organization_sources(memory,revision,source_memory,source_revision,project,snapshot,quote) VALUES(?,?,?,?,?,?,?)`, id, v.Revision, ref.Memory, ref.Revision, d.Project, string(b), ref.Quote); err != nil {
				return receipt, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return receipt, err
	}
	current, err := s.Current(ctx, id)
	if err != nil {
		return receipt, err
	}
	return records.SaveReceipt{Persisted: true, SavedRevision: v.Revision, Record: current}, nil
}
