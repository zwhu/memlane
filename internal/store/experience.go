package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Store) CopyExperience(ctx context.Context, d records.ExperienceCopy) (records.OrganizationReceipt, error) {
	var receipt records.OrganizationReceipt
	if d.Title != nil || d.Conditions != nil {
		if d.Title == nil || d.Conditions == nil || strings.TrimSpace(*d.Title) == "" || strings.TrimSpace(*d.Conditions) == "" || len(*d.Title) > 512 || len(*d.Conditions) > 4096 {
			return receipt, records.MergeError("invalid_request")
		}
	}
	if strings.TrimSpace(d.OperationID) == "" || len(d.OperationID) > 128 || d.Revision < 1 {
		return receipt, records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return receipt, err
	}
	defer tx.Rollback()
	var scope, kind string
	if err = tx.QueryRowContext(ctx, `SELECT id,kind FROM scopes WHERE id=?`, d.Project).Scan(&scope, &kind); err != nil || kind != "project" {
		return receipt, records.MergeError("scope_denied")
	}
	var global string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM scopes WHERE kind='experience'`).Scan(&global); err != nil {
		return receipt, err
	}
	var title, body string
	if err = tx.QueryRowContext(ctx, `SELECT title,text FROM versions WHERE memory=? AND revision=?`, d.Memory, d.Revision).Scan(&title, &body); err != nil {
		return receipt, err
	}
	operation := "experience-copy:" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(d.OperationID)).String()
	memoryID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(operation)).String()
	source := "用户：原文复制到全局"
	conditions := ""
	copyTitle := title
	if d.Title != nil {
		copyTitle, conditions = *d.Title, *d.Conditions
		source = "用户：原文复制到通用知识"
	}
	m := Mutation{Kind: "save", OperationID: operation, Memory: memoryID, Dataset: global, Text: body, Title: copyTitle, Source: source, Origin: "knowledge", SourceDetail: &records.AgentSource{References: []string{d.Project + "/" + d.Memory + "@v" + stringRevision(d.Revision)}}}
	var prior bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operations WHERE id=?)`, operation).Scan(&prior); err != nil {
		return receipt, err
	}
	if prior && d.Conditions != nil {
		var savedConditions string
		if err = tx.QueryRowContext(ctx, `SELECT conditions FROM global_experiences WHERE memory=? AND revision=1`, memoryID).Scan(&savedConditions); err != nil {
			return receipt, err
		}
		if savedConditions != conditions {
			return receipt, ErrConflict
		}
	}
	if !prior {
		ok, e := organizationCurrent(ctx, tx, d.Project, []records.MergeSource{{Memory: d.Memory, Revision: d.Revision}})
		if e != nil {
			return receipt, e
		}
		if !ok {
			return receipt, records.MergeError("proposal_expired")
		}
	}
	saved, err := s.applyTx(ctx, tx, m)
	if err != nil {
		return receipt, err
	}
	receipt = records.OrganizationReceipt{ID: operation, Memory: saved.Memory, Revision: saved.Revision}
	if prior {
		return receipt, nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO global_experiences(memory,revision,kind,conditions) VALUES(?,?,'copy',?)`, saved.Memory, saved.Revision, conditions); err != nil {
		return receipt, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO derived_memories(memory,revision,state) VALUES(?,?,'user_confirmed')`, saved.Memory, saved.Revision); err != nil {
		return receipt, err
	}
	v, err := scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+` WHERE m.id=?`, d.Memory))
	if err != nil {
		return receipt, err
	}
	roots, err := s.evidenceRoots(ctx, tx, d.Memory, d.Revision)
	if err != nil {
		return receipt, err
	}
	input := records.OrganizationInput{MergeInput: records.MergeInput{Memory: d.Memory, Revision: d.Revision, Title: title, Text: body, Source: v.Source, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano)}, Origin: v.Origin, Roots: roots}
	raw, _ := json.Marshal(input)
	if _, err = tx.ExecContext(ctx, `INSERT INTO organization_sources(memory,revision,source_memory,source_revision,project,snapshot,quote) VALUES(?,?,?,?,?,?,?)`, saved.Memory, saved.Revision, d.Memory, d.Revision, d.Project, string(raw), body); err != nil {
		return receipt, err
	}
	return receipt, tx.Commit()
}
func (s *Store) applyGlobalExperience(ctx context.Context, tx *sql.Tx, t records.OrganizationTask, e records.OrganizationEntry, id string) (records.OrganizationReceipt, error) {
	item := e.Item
	receipt := records.OrganizationReceipt{ID: id}
	refs := []string{}
	for _, ref := range item.Sources {
		refs = append(refs, t.Project+"/"+ref.Memory+"@v"+stringRevision(ref.Revision))
	}
	v, err := s.applyTx(ctx, tx, Mutation{Kind: "save", OperationID: "experience-review:" + e.ID, Memory: e.ID, Dataset: item.Destination, Text: item.Text, Title: item.Title, Source: "traex：用户确认全局提炼", Origin: "knowledge", SourceDetail: &records.AgentSource{References: refs}})
	if err != nil {
		return receipt, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO global_experiences(memory,revision,kind,conditions) VALUES(?,?,'distill',?)`, v.Memory, v.Revision, item.Conditions); err != nil {
		return receipt, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO derived_memories(memory,revision,state) VALUES(?,?,'user_confirmed')`, v.Memory, v.Revision); err != nil {
		return receipt, err
	}
	for _, ref := range item.Sources {
		var input records.OrganizationInput
		for _, in := range t.Snapshot.Inputs {
			if in.Memory == ref.Memory {
				input = in
			}
		}
		raw, _ := json.Marshal(input)
		if _, err = tx.ExecContext(ctx, `INSERT INTO organization_sources(memory,revision,source_memory,source_revision,project,snapshot,quote) VALUES(?,?,?,?,?,?,?)`, v.Memory, v.Revision, ref.Memory, ref.Revision, t.Project, string(raw), ref.Quote); err != nil {
			return receipt, err
		}
	}
	receipt.Memory = v.Memory
	receipt.Revision = v.Revision
	return receipt, nil
}
func (s *Store) followExperienceVersion(ctx context.Context, tx *sql.Tx, m Mutation, revision int64) error {
	if s.Schema < 6 {
		return nil
	}
	selected := revision - 1
	if m.Kind == "restore" {
		selected = m.RestoreRevision
	}
	var kind, conditions string
	err := tx.QueryRowContext(ctx, `SELECT kind,conditions FROM global_experiences WHERE memory=? AND revision=(SELECT max(revision) FROM global_experiences WHERE memory=? AND revision<=?)`, m.Memory, m.Memory, selected).Scan(&kind, &conditions)
	if err == sql.ErrNoRows {
		if m.Conditions != nil {
			return records.MergeError("invalid_request")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if m.Conditions != nil {
		if m.Kind != "edit" || len(*m.Conditions) > 4096 {
			return records.MergeError("invalid_request")
		}
		conditions = *m.Conditions
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO global_experiences(memory,revision,kind,conditions) VALUES(?,?,?,?)`, m.Memory, revision, kind, conditions)
	return err
}

func (s *Store) enrichExperienceVersions(ctx context.Context, versions []*records.Version) error {
	if s.Schema < 6 || len(versions) == 0 {
		return nil
	}
	values := []string{}
	args := []any{}
	byID := map[string]*records.Version{}
	for _, v := range versions {
		values = append(values, "(?,?)")
		args = append(args, v.Memory, v.Revision)
		byID[v.Memory] = v
	}
	rows, err := s.DB.QueryContext(ctx, `WITH requested(memory,revision) AS (VALUES `+strings.Join(values, ",")+`) SELECT g.memory,g.kind,g.conditions FROM requested r JOIN global_experiences g ON g.memory=r.memory AND g.revision=(SELECT max(revision) FROM global_experiences x WHERE x.memory=r.memory AND x.revision<=r.revision)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind, conditions string
		if err = rows.Scan(&id, &kind, &conditions); err != nil {
			return err
		}
		v := byID[id]
		v.Experience = &records.ExperienceInfo{Kind: kind, Conditions: conditions}
		if v.Derived != nil {
			v.Derived.Kind = "global_" + kind
		}
	}
	return rows.Err()
}
