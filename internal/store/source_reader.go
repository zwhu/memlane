package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// ReadSource takes one SQLite snapshot for owner, lineage and source state.
// It never follows external URLs or fetches a file named by a citation.
func (s *Store) ReadSource(ctx context.Context, q records.SourceRead) (records.SourceRecord, error) {
	out := records.SourceRecord{Project: q.Project, Memory: q.Memory, Revision: q.Revision, QuoteOffset: -1}
	for _, id := range []string{q.Project, q.Owner, q.Memory} {
		if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
			return out, access("invalid_request", "explicit source and project identities required")
		}
	}
	if q.Revision < 1 || q.OwnerRevision < 1 {
		return out, access("invalid_request", "specific source and owner versions required")
	}
	// New official bindings keep material reads behind the same connection
	// retirement/generation checks as graph reads. Legacy local sources retain
	// the host's existing browsing behavior. The daemon holds the barrier.
	bindings, err := s.CogneeBindings(ctx, q.Project)
	if err != nil {
		return out, err
	}
	v, err := s.Current(ctx, q.Memory)
	if err != nil {
		return out, err
	}
	if v.SourceDetail != nil && len(v.SourceDetail.References) > 0 {
		matched := false
		for _, b := range bindings {
			if strings.HasPrefix(v.SourceDetail.References[0], "cognee://"+b.Dataset+"/") {
				if v.SourceDetail.Official != nil && v.SourceDetail.Official.Instance != b.Instance {
					continue
				}
				if v.SourceDetail.Native != nil && v.SourceDetail.Native.Instance != b.Instance {
					continue
				}
				if err = s.AuthorizeCogneeBinding(ctx, b); err != nil {
					return out, err
				}
				matched = true
			}
		}
		if (v.SourceDetail.Official != nil || v.SourceDetail.Native != nil) && !matched {
			return out, access("scope_denied", "original service instance is not bound to this project")
		}
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	owner, err := scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+`WHERE m.id=?`, q.Owner))
	if err != nil {
		return out, err
	}
	if owner.Dataset != q.Project {
		return out, access("scope_denied", "source owner is outside selected project")
	}
	if owner.Revision != q.OwnerRevision {
		out.State = "owner_changed"
		return out, nil
	}
	if q.Owner != q.Memory {
		var sourceProject string
		err = tx.QueryRowContext(ctx, `SELECT project,quote FROM organization_sources WHERE memory=? AND revision=(SELECT max(revision) FROM organization_sources WHERE memory=? AND revision<=?) AND source_memory=? AND source_revision=?`, q.Owner, q.Owner, q.OwnerRevision, q.Memory, q.Revision).Scan(&sourceProject, &out.Quote)
		if err != nil {
			if err == sql.ErrNoRows {
				return out, access("invalid_provenance", "source is not part of this knowledge version")
			}
			return out, err
		}
		if sourceProject != q.Project {
			return out, access("scope_denied", "source material requires its own project authorization")
		}
	} else if owner.Origin != "raw" || q.OwnerRevision != q.Revision {
		return out, access("invalid_provenance", "direct source reading requires an original record")
	}
	source, err := scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+`WHERE m.id=?`, q.Memory))
	if err != nil {
		return out, err
	}
	if source.Dataset != q.Project {
		return out, access("scope_denied", "source is outside selected project")
	}
	out.CurrentRevision = source.Revision
	if source.Revision != q.Revision || source.State != "active" {
		out.State = "source_changed"
		return out, nil
	}
	if source.Origin != "raw" {
		return out, access("invalid_provenance", "reader requires original material")
	}
	if out.Quote != "" {
		out.QuoteOffset = strings.Index(source.Text, out.Quote)
		if out.QuoteOffset < 0 {
			return out, access("invalid_provenance", "frozen excerpt is absent from original version")
		}
	}
	out.State, out.Record = "available", source
	return out, tx.Commit()
}
