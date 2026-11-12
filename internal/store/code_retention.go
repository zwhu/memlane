package store

import (
	"context"
	"errors"
)

// CodeRetention preserves the current graph, including disabled worktrees,
// plus the most recent distinct completed graph. Receipts/sources stay in SQL.
func (s *Store) CodeRetention(ctx context.Context) (map[string][]string, error) {
	states, err := s.Codes(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, c := range states {
		keep := []string{}
		if c.Snapshot != "" {
			keep = append(keep, c.Snapshot)
		}
		rows, err := s.DB.QueryContext(ctx, `SELECT receipt FROM code_jobs WHERE worktree_id=? AND state='completed' ORDER BY finished_ms DESC,created_ms DESC,id DESC`, c.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() && len(keep) < 2 {
			var raw string
			if err = rows.Scan(&raw); err != nil {
				break
			}
			receipt, official, decodeErr := decodeCodeReceipt(raw)
			if decodeErr != nil {
				err = decodeErr
				break
			}
			if official != nil {
				continue
			}
			if receipt == nil || receipt.Dataset != c.Dataset || receipt.Snapshot == "" {
				err = errors.New("invalid completed code retention receipt")
				break
			}
			if len(keep) == 0 || keep[0] != receipt.Snapshot {
				keep = append(keep, receipt.Snapshot)
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
		out[c.Dataset] = keep
	}
	return out, nil
}

func (s *Store) CompletedCodeExtractions(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM code_jobs WHERE state IN ('completed','failed','cancelled','interrupted') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
