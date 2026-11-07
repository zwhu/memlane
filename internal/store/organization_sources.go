package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/zwhu/memlane/internal/records"
)

// evidenceRoots follows immutable lineage, including user revisions whose
// original lineage remains historical. Roots are references, never a quality
// or independence score; different versions of one source remain one origin.
func (s *Store) evidenceRoots(ctx context.Context, tx *sql.Tx, memory string, revision int64) ([]records.MergeTarget, error) {
	roots := map[string]records.MergeTarget{}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var walk func(string, int64, int) error
	walk = func(id string, rev int64, depth int) error {
		key := fmt.Sprintf("%s:%d", id, rev)
		if visiting[key] || depth > 32 || len(visited) > 256 {
			return records.MergeError("invalid_provenance")
		}
		if visited[key] {
			return nil
		}
		visiting[key] = true
		defer delete(visiting, key)
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM versions WHERE memory=? AND revision=?`, id, rev).Scan(&exists); err != nil {
			return err
		}
		if exists != 1 {
			return records.MergeError("invalid_provenance")
		}
		links := []records.MergeTarget{}
		tables := []string{"merge_sources"}
		if s.Schema >= 4 {
			tables = append(tables, "organization_sources")
		}
		for _, table := range tables {
			// Table names are fixed internal schema constants, never Agent input.
			rows, err := tx.QueryContext(ctx, `SELECT source_memory,source_revision FROM `+table+` WHERE memory=? AND revision=(SELECT max(revision) FROM `+table+` WHERE memory=? AND revision<=?) ORDER BY source_memory`, id, id, rev)
			if err != nil {
				return err
			}
			for rows.Next() {
				var ref records.MergeTarget
				if err = rows.Scan(&ref.Memory, &ref.Revision); err != nil {
					rows.Close()
					return err
				}
				links = append(links, ref)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		}
		if len(links) == 0 {
			roots[key] = records.MergeTarget{Memory: id, Revision: rev}
		} else {
			for _, ref := range links {
				if err := walk(ref.Memory, ref.Revision, depth+1); err != nil {
					return err
				}
			}
		}
		visited[key] = true
		return nil
	}
	if err := walk(memory, revision, 0); err != nil {
		return nil, err
	}
	keys := []string{}
	for key := range roots {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := []records.MergeTarget{}
	for _, key := range keys {
		out = append(out, roots[key])
	}
	return out, nil
}
func (s *Store) OrganizationSources(ctx context.Context, memory string, revision int64) ([]records.OrganizationSource, error) {
	out := []records.OrganizationSource{}
	if s.Schema < 4 {
		return out, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT source_memory,source_revision,project,snapshot,quote FROM organization_sources WHERE memory=? AND revision=? ORDER BY source_memory`, memory, revision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v records.OrganizationSource
		var raw string
		if err = rows.Scan(&v.Memory, &v.Revision, &v.Project, &raw, &v.Quote); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &v.Input); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// enrichVersions batches metadata queries after all version rows have closed.
// It does not embed complete frozen source texts into paginated list responses.
func (s *Store) enrichVersions(ctx context.Context, versions []*records.Version) error {
	if s.Schema < 4 || len(versions) == 0 {
		return nil
	}
	byID := map[string]*records.Version{}
	args := []any{}
	marks := []string{}
	for _, v := range versions {
		byID[v.Memory] = v
		args = append(args, v.Memory)
		marks = append(marks, "?")
	}
	suffix := strings.Join(marks, ",")
	rows, err := s.DB.QueryContext(ctx, `SELECT memory,tag FROM memory_tags WHERE memory IN (`+suffix+`) ORDER BY memory,tag`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, tag string
		if err = rows.Scan(&id, &tag); err != nil {
			rows.Close()
			return err
		}
		byID[id].Tags = append(byID[id].Tags, tag)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = s.DB.QueryContext(ctx, `SELECT memory,revision,state FROM derived_memories WHERE memory IN (`+suffix+`)`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, state string
		var revision int64
		if err = rows.Scan(&id, &revision, &state); err != nil {
			rows.Close()
			return err
		}
		v := byID[id]
		if v.Revision == revision {
			v.Derived = &records.DerivedInfo{Kind: "summary", State: state}
		}
	}
	err = rows.Err()
	rows.Close()
	return err
}
func (s *Store) followOrganizationVersion(ctx context.Context, tx *sql.Tx, id string, revision int64) error {
	if s.Schema < 4 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO organization_sources(memory,revision,source_memory,source_revision,project,snapshot,quote)
 SELECT memory,?,source_memory,source_revision,project,snapshot,quote FROM organization_sources WHERE memory=? AND revision=(SELECT max(revision) FROM organization_sources WHERE memory=? AND revision<?) ON CONFLICT DO NOTHING`, revision, id, id, revision)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE derived_memories SET revision=?,state='user_confirmed' WHERE memory=?`, revision, id)
	return err
}
func organizationCurrent(ctx context.Context, tx *sql.Tx, project string, sources []records.MergeSource) (bool, error) {
	for _, ref := range sources {
		var count int
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM memories m WHERE m.id=? AND m.dataset=? AND m.current_revision=? AND m.state='active' AND NOT EXISTS(SELECT 1 FROM derived_memories d WHERE d.memory=m.id AND d.revision=m.current_revision AND d.state='stale')`, ref.Memory, project, ref.Revision).Scan(&count)
		if err != nil {
			return false, err
		}
		if count != 1 {
			return false, nil
		}
	}
	return true, nil
}
