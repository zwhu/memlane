package store

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
	"sort"
)

// DeletionImpact reports direct persisted lineage of active current knowledge.
// It predicts neither semantic dependence nor changes to another item's body.
// All reads share one SQLite snapshot; no review, indexing or knowledge writes.
func (s *Store) DeletionImpact(ctx context.Context, q records.DeletionImpactQuery) (records.DeletionImpact, error) {
	out := records.DeletionImpact{Memory: q.Memory, Dataset: q.Dataset, References: []records.DeletionReference{}}
	if s.Schema < 6 || q.Memory == "" || len(q.Memory) > 128 || q.Dataset == "" || len(q.Dataset) > 128 || q.ExpectedRevision < 1 || len(q.Datasets) == 0 || len(q.Datasets) > 256 {
		return out, access("invalid_request", "exact knowledge target and authorized scopes required")
	}
	allowed := map[string]bool{}
	for _, d := range q.Datasets {
		if d == "" || len(d) > 128 {
			return out, access("invalid_request", "invalid authorized scope")
		}
		allowed[d] = true
	}
	if !allowed[q.Dataset] {
		return out, access("scope_denied", "knowledge scope not authorized")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	scopeRows, e := tx.QueryContext(ctx, `SELECT id FROM scopes ORDER BY id`)
	if e != nil {
		return out, e
	}
	effective := map[string]bool{}
	for scopeRows.Next() {
		var id string
		if e = scopeRows.Scan(&id); e != nil {
			scopeRows.Close()
			return out, e
		}
		if allowed[id] {
			effective[id] = true
			out.CoverageDatasets = append(out.CoverageDatasets, id)
		}
	}
	e = scopeRows.Err()
	scopeRows.Close()
	if e != nil {
		return out, e
	}
	allowed = effective
	if !allowed[q.Dataset] {
		return out, access("scope_denied", "knowledge scope not authorized")
	}
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT dataset,current_revision,state FROM memories WHERE id=?`, q.Memory).Scan(&out.Dataset, &out.Revision, &state); err != nil {
		return out, err
	}
	if out.Dataset != q.Dataset || out.Revision != q.ExpectedRevision {
		return out, access("version_changed", "knowledge version or scope changed")
	}
	if state != "active" {
		return out, access("version_changed", "knowledge is no longer active")
	}
	refs := []records.DeletionReference{}
	memories := map[string]bool{}
	type referenceKey struct {
		Memory                   string
		Revision, SourceRevision int64
	}
	positions := map[referenceKey]int{}
	for _, table := range []string{"merge_sources", "organization_sources"} {
		// Fixed internal table constants only. Current lineage follows the same
		// latest-at-or-before rule as evidenceRoots; superseded old links do not count.
		rows, e := tx.QueryContext(ctx, `SELECT m.id,m.current_revision,m.dataset,v.title,p.source_revision,COALESCE(g.kind,''),COALESCE(d.state,'') FROM `+table+` p JOIN memories m ON m.id=p.memory JOIN versions v ON v.memory=m.id AND v.revision=m.current_revision LEFT JOIN global_experiences g ON g.memory=m.id AND g.revision=(SELECT max(x.revision) FROM global_experiences x WHERE x.memory=m.id AND x.revision<=m.current_revision) LEFT JOIN derived_memories d ON d.memory=m.id AND d.revision=m.current_revision WHERE p.source_memory=? AND m.state='active' AND p.revision=(SELECT max(x.revision) FROM `+table+` x WHERE x.memory=m.id AND x.revision<=m.current_revision) ORDER BY m.id,p.source_revision`, q.Memory)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var r records.DeletionReference
			var kind string
			if e = rows.Scan(&r.Memory, &r.Revision, &r.Dataset, &r.Title, &r.SourceRevision, &kind, &r.DerivedState); e != nil {
				rows.Close()
				return out, e
			}
			if !allowed[r.Dataset] {
				continue
			}
			relation := "merge"
			if table == "organization_sources" {
				relation = "organization"
				if kind == "copy" || kind == "distill" {
					relation = "experience_" + kind
				}
			}
			key := referenceKey{r.Memory, r.Revision, r.SourceRevision}
			if index, ok := positions[key]; ok {
				refs[index].Relations = append(refs[index].Relations, relation)
			} else {
				r.Relations = []string{relation}
				positions[key] = len(refs)
				refs = append(refs, r)
			}
			memories[r.Memory] = true
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		a, b := refs[i], refs[j]
		if a.Dataset != b.Dataset {
			return a.Dataset < b.Dataset
		}
		if a.Memory != b.Memory {
			return a.Memory < b.Memory
		}
		return a.SourceRevision < b.SourceRevision
	})
	out.DirectMemoryCount = len(memories)
	out.ReferenceCount = len(refs)
	out.Truncated = len(refs) > 50
	if out.Truncated {
		refs = refs[:50]
	}
	out.References = refs
	return out, tx.Commit()
}
