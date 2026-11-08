package desktop

import (
	"context"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

// The official end hook acknowledges a background submission. Only the
// trusted material read can prove persistence; retries do not submit improve.
func awaitCogneeMaterial(ctx context.Context, query records.CogneeSync,
	read func(context.Context, records.CogneeSync) (records.CogneeSyncReceipt, error),
	interval time.Duration, progress func(records.CogneeSyncReceipt)) (records.CogneeSyncReceipt, error) {
	var receipt records.CogneeSyncReceipt
	for attempt := 0; attempt < 30; attempt++ {
		if err := ctx.Err(); err != nil {
			return receipt, err
		}
		var err error
		receipt, err = read(ctx, query)
		if err != nil {
			return receipt, err
		}
		// Distillation starts after the raw watermarks move. An empty
		// document list during that gap is not the end of the approved wait.
		if receipt.OfficialState == "persisted" && receipt.PendingDocuments == 0 && len(receipt.Knowledge) > 0 {
			return receipt, nil
		}
		if progress != nil {
			progress(receipt)
		}
		if attempt == 29 {
			break
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return receipt, ctx.Err()
		case <-timer.C:
		}
	}
	return receipt, nil
}
