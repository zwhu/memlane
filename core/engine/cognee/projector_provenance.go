package cognee

import (
	"context"
	"errors"
	"slices"

	"github.com/zwhu/memlane/core/domain"
)

func lockLessonProvenance(ctx context.Context, store *domain.Store, docs []projectedDoc) (int, error) {
	count := 0
	for i := range docs {
		doc := &docs[i]
		if doc.provenance == nil {
			continue
		}
		locked, current, err := currentLessonProvenance(ctx, store, *doc.provenance)
		if err != nil {
			return count, err
		}
		if !current {
			locked, err = store.LockDocumentProvenance(ctx, *doc.provenance)
			if err != nil {
				return count, err
			}
			count++
		}
		doc.provenance = &locked
	}
	return count, nil
}

func currentLessonProvenance(ctx context.Context, store *domain.Store, desired domain.DocumentProvenance) (domain.DocumentProvenance, bool, error) {
	existing, err := store.GetDocumentProvenance(ctx, desired.DatasetID, desired.DocumentID)
	if errors.Is(err, domain.ErrNotFound) {
		return existing, false, nil
	}
	if err != nil || existing.LessonType != desired.LessonType || existing.StateKey != desired.StateKey {
		return existing, false, err
	}
	existingDay, desiredDay := existing.FirstSourceDate, desired.FirstSourceDate
	existingRank := domain.ProvenanceStatusRank(existing.SourceDateStatus)
	if existingDay == desiredDay {
		if existingRank < domain.ProvenanceStatusRank(desired.SourceDateStatus) {
			return existing, false, nil
		}
	} else if existingDay == "" || desiredDay != "" && existingDay > desiredDay {
		return existing, false, nil
	}
	for _, id := range desired.SourceQAIDs {
		if !slices.Contains(existing.SourceQAIDs, id) {
			return existing, false, nil
		}
	}
	if desired.LessonType == "" || desired.StateKey == "" || existingDay == "" {
		return existing, true, nil
	}
	keyed, err := store.GetKeyedProvenance(ctx, desired.DatasetID, desired.LessonType, desired.StateKey)
	if errors.Is(err, domain.ErrNotFound) {
		return existing, false, nil
	}
	return existing, err == nil && keyed.FirstSourceDate == existingDay &&
		domain.ProvenanceStatusRank(keyed.SourceDateStatus) >= existingRank, err
}
