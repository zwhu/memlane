// Package memory coordinates durable originals and traceable current search hits.
package memory

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/store"
)

type Service struct {
	Store  *store.Store
	Engine engine.Worker
}
type Hit struct {
	Version   store.Version    `json:"version"`
	Candidate engine.Candidate `json:"candidate"`
}

func (s *Service) IndexCurrent(ctx context.Context, id string) error {
	v, err := s.Store.Current(ctx, id)
	if err != nil {
		return err
	}
	if v.State != "active" {
		return store.ErrConflict
	}
	if s.Engine == nil {
		cause := errors.New("engine unavailable; original remains saved")
		return errors.Join(cause, s.Store.IndexFailed(context.WithoutCancel(ctx), v.Record, cause))
	}
	if err = s.Store.IndexStarted(ctx, id, v.Revision); err != nil {
		return err
	}
	r, err := s.Engine.Index(ctx, v.Record)
	if err != nil {
		// Persist failure independently of a cancelled request context.
		persistErr := s.Store.IndexFailed(context.WithoutCancel(ctx), v.Record, err)
		return fmt.Errorf("index unavailable: %w", errors.Join(err, persistErr))
	}
	return s.Store.MarkIndexed(ctx, v.Record, r)
}

// datasets must be an explicit, authorized list from the caller; an empty list
// is an error, never a request to query every project.
func (s *Service) Search(ctx context.Context, datasets []string, query string, limit int) ([]Hit, error) {
	if len(datasets) == 0 || limit < 1 || limit > 100 {
		return nil, errors.New("explicit datasets and valid limit required")
	}
	if s.Engine == nil {
		return nil, errors.New("engine unavailable")
	}
	out := []Hit{}
	seen := map[string]bool{}
	for _, dataset := range datasets {
		id, err := uuid.Parse(dataset)
		if err != nil {
			return nil, errors.New("invalid dataset")
		}
		dataset = id.String()
		window := max(10, limit)
		for {
			candidates, err := s.Engine.Search(ctx, dataset, query, window)
			if err != nil {
				return nil, fmt.Errorf("search unavailable: %w", err)
			}
			for _, candidate := range candidates {
				if candidate.Dataset != dataset {
					return nil, errors.New("engine returned a different dataset")
				}
				v, valid, err := s.Store.Resolve(ctx, candidate)
				if err != nil {
					return nil, err
				}
				if valid && !seen[v.Memory] {
					out = append(out, Hit{Version: v, Candidate: candidate})
					seen[v.Memory] = true
				}
			}
			if len(out) >= limit || len(candidates) < window || window == 1000 {
				break
			}
			window = min(1000, window*2)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
