package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func validateProjectKnowledge(t records.ProjectTask, p records.ProjectKnowledgeProposal, sources []records.ProjectRepositorySource) error {
	invalid := access("invalid_project_result", "bounded proposal with host-read sources required")
	if p.Task != t.ID || p.Attempt != t.Attempt || len(p.Items) > 256 || strings.TrimSpace(p.Explanation) == "" || len(p.Explanation) > 8192 {
		return invalid
	}
	byID := map[string]records.ProjectRepositorySource{}
	total := 0
	for _, s := range sources {
		if s.Path == "" || filepath.IsAbs(s.Path) || filepath.ToSlash(filepath.Clean(s.Path)) != s.Path || strings.ContainsAny(s.Path, "\\\x00\r\n") || strings.HasPrefix(s.Path, "../") {
			return invalid
		}
		for _, part := range strings.Split(s.Path, "/") {
			switch strings.ToLower(part) {
			case ".git", ".codex", ".pi", ".trae", ".agents", ".claude", "auth.json", "credentials.json":
				return invalid
			}
			if strings.HasPrefix(strings.ToLower(part), ".env") {
				return invalid
			}
		}
		if len(s.Text) == 0 || len(s.Text) > 4*1024*1024 || !utf8.ValidString(s.Text) || strings.IndexByte(s.Text, 0) >= 0 {
			return invalid
		}
		total += len(s.Text)
		if total > 32*1024*1024 {
			return invalid
		}
		digest := sha256.Sum256([]byte(s.Text))
		if s.SHA256 != hex.EncodeToString(digest[:]) {
			return invalid
		}
		id := sha256.Sum256([]byte(s.Path + "\x00" + s.SHA256))
		if s.ID != hex.EncodeToString(id[:]) {
			return invalid
		}
		if _, ok := byID[s.ID]; ok {
			return invalid
		}
		byID[s.ID] = s
	}
	for _, item := range p.Items {
		if strings.TrimSpace(item.Title) == "" || len(item.Title) > 512 || strings.TrimSpace(item.Text) == "" || len(item.Text) > 65536 || len(item.Sources) == 0 || len(item.Sources) > 32 {
			return invalid
		}
		seen := map[string]bool{}
		for _, q := range item.Sources {
			s, ok := byID[q.Source]
			if !ok || seen[q.Source] || strings.TrimSpace(q.Quote) == "" || !strings.Contains(s.Text, q.Quote) {
				return invalid
			}
			seen[q.Source] = true
		}
	}
	return nil
}

func projectSourceMemory(project string, s records.ProjectRepositorySource) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("project-repository-source:"+project+":"+s.ID)).String()
}
func projectKnowledgeMemory(project string, item records.ProjectKnowledgeItem) string {
	item.Sources = append([]records.ProjectKnowledgeQuote(nil), item.Sources...)
	sort.Slice(item.Sources, func(i, j int) bool { return item.Sources[i].Source < item.Sources[j].Source })
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("project-knowledge:"+project+":"+UsageDigest(item))).String()
}

// SaveProjectKnowledge prepares a durable result and applies each new item in
// its own transaction. Cancellation and later failures keep earlier receipts.
func (s *Store) SaveProjectKnowledge(ctx context.Context, id string, attempt int64, p records.ProjectKnowledgeProposal, sources []records.ProjectRepositorySource) (records.ProjectTaskReceipt, error) {
	if err := s.PrepareProjectKnowledge(ctx, id, attempt, p, sources); err != nil {
		return records.ProjectTaskReceipt{}, err
	}
	return s.ApplyProjectKnowledge(ctx, id, attempt)
}

type projectIndexQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func projectReceiptIndexesQuery(ctx context.Context, query projectIndexQuery, r records.ProjectTaskReceipt) (indexed, failed, sourceIndexed, sourceFailed, pending int, err error) {
	all := append(append([]records.ProjectTaskSaved(nil), r.Added...), r.Sources...)
	for position, saved := range all {
		var current int64
		var indexedRevision int64
		var generation string
		var state, job string
		err = query.QueryRowContext(ctx, `SELECT m.current_revision,m.state,m.indexed_revision,m.generation,coalesce(j.state,'') FROM memories m LEFT JOIN jobs j ON j.memory=m.id AND j.revision=? WHERE m.id=?`, saved.Revision, saved.Memory).Scan(&current, &state, &indexedRevision, &generation, &job)
		if err != nil {
			return
		}
		if current != saved.Revision || state != "active" || job == "superseded" || job == "failed" {
			if position < len(r.Added) {
				failed++
			} else {
				sourceFailed++
			}
			continue
		}
		switch job {
		case "completed":
			if indexedRevision == saved.Revision && generation != "" {
				if position < len(r.Added) {
					indexed++
				} else {
					sourceIndexed++
				}
			} else {
				if position < len(r.Added) {
					failed++
				} else {
					sourceFailed++
				}
			}
		case "pending", "indexing":
			pending++
		default:
			err = fmt.Errorf("project saved index job missing")
			return
		}
	}
	return
}

func (s *Store) projectReceiptIndexes(ctx context.Context, r records.ProjectTaskReceipt) (int, int, int, int, int, error) {
	return projectReceiptIndexesQuery(ctx, s.DB, r)
}

func (s *Store) FinishProjectIndexes(ctx context.Context, id string, attempt int64) (bool, error) {
	t, err := s.ProjectTask(ctx, id)
	if err != nil {
		return false, err
	}
	if t.Receipt == nil {
		return false, access("project_save_incomplete", "persisted knowledge receipt required")
	}
	savedReceipt := *t.Receipt
	_, failed, _, sourceFailed, pending, err := s.projectReceiptIndexes(ctx, *t.Receipt)
	if err != nil {
		return false, err
	}
	if pending > 0 {
		return false, nil
	}
	if failed+sourceFailed > 0 || t.Receipt.SaveFailed > 0 {
		if t.Receipt.SaveFailed > 0 {
			return true, s.EndProjectRun(ctx, id, attempt, "failed", "project_save_partial")
		}
		return true, s.EndProjectRun(ctx, id, attempt, "failed", "project_index_failed")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	t, err = s.projectRunTx(ctx, tx, id, attempt, false)
	if err != nil {
		return false, err
	}
	if t.State != "indexing" {
		return false, access("project_task_closed", "current indexing task required")
	}
	// Recheck the actual jobs and current versions in the completion transaction.
	// An edit/delete between the observation above and this write is not success.
	_, failed, _, sourceFailed, pending, err = projectReceiptIndexesQuery(ctx, tx, savedReceipt)
	if err != nil {
		return false, err
	}
	if pending > 0 || failed+sourceFailed > 0 {
		return false, nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE project_tasks SET state='completed',error_code='' WHERE id=? AND attempt=?`, id, attempt); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE project_task_runs SET released=1,expires_ms=0 WHERE task=? AND attempt=?`, id, attempt); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
