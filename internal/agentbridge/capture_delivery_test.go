package agentbridge

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestCapturedDeliveryAcknowledgesOnlyCommittedOriginal(t *testing.T) {
	root := t.TempDir()
	config := Config{Root: root, ConnectionID: uuid.NewString(), Generation: 1, Workspace: t.TempDir()}
	fragment, _ := json.Marshal(records.CaptureOrigin{Agent: "codex", Consent: uuid.NewString()})
	args := records.AgentArguments{OperationID: "synthetic-automatic-original", Text: "synthetic original", Origin: "raw", Source: &records.AgentSource{Fragment: string(fragment)}}
	retained, err := RetainCapturedRaw(config, args)
	if err != nil {
		t.Fatal(err)
	}
	query := records.CaptureDeliveryQuery{EntryID: retained.ID, ReviewDigest: retained.ReviewDigest}
	var receipt records.CaptureDeliveryReceipt
	save := func(source records.DeliveryAssignmentSource) (records.CaptureDeliveryReceipt, error) {
		if source.Review.Arguments.Text != args.Text {
			t.Fatal("original changed")
		}
		if receipt.Memory == "" {
			receipt = records.CaptureDeliveryReceipt{EntryID: retained.ID, RequestDigest: retained.RequestDigest, ReviewDigest: retained.ReviewDigest, ConnectionID: config.ConnectionID, OperationID: args.OperationID, LibraryID: uuid.NewString(), RouteID: uuid.NewString(), Project: uuid.NewString(), Memory: uuid.NewString(), Revision: 1, Persisted: true}
		}
		return receipt, nil
	}
	out, err := ProcessCapturedRaw(root, query, save)
	if err != nil || out.AckPending || !out.Persisted {
		t.Fatal(out, err)
	}
	review, err := ReviewDelivery(root, retained.ID)
	if err != nil || !review.Item.Status.PersistedConfirmed || review.Item.Status.Memory != receipt.Memory {
		t.Fatal(review, err)
	}
	replay, err := ProcessCapturedRaw(root, query, save)
	if err != nil || replay != out {
		t.Fatal(replay, err)
	}
	query.ReviewDigest = "changed"
	if _, err = ProcessCapturedRaw(root, query, save); err == nil {
		t.Fatal("changed source accepted")
	}
}
