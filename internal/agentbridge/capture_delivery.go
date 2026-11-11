package agentbridge

import (
	"errors"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// A real automatic save acknowledges the original operation. Independent
// manual user-assignment receipts continue to use their separate path.
func AckCapturedRaw(root string, r records.CaptureDeliveryReceipt) error {
	if !r.Persisted || r.Revision != 1 || r.LibraryID == "" || r.RouteID == "" || r.Project == "" {
		return errors.New("capture_receipt_invalid")
	}
	if id, err := uuid.Parse(r.Memory); err != nil || id.String() != r.Memory {
		return errors.New("capture_receipt_invalid")
	}
	d, err := openDelivery(root)
	if err != nil {
		return err
	}
	defer d.close()
	e := d.find(r.EntryID)
	return ackCapturedEntry(d, e, r)
}

func ackCapturedEntry(d *deliveryFile, e *deliveryEntry, r records.CaptureDeliveryReceipt) error {
	if !r.Persisted || r.Revision != 1 {
		return errors.New("capture_receipt_invalid")
	}
	for _, value := range []string{r.Memory, r.LibraryID, r.RouteID, r.Project} {
		if id, err := uuid.Parse(value); err != nil || id.String() != value {
			return errors.New("capture_receipt_invalid")
		}
	}
	if e == nil {
		return errors.New("delivery_not_found")
	}
	if _, ok := records.ReadCaptureOrigin(e.Payload.Arguments); !ok || e.Payload.Connection != r.ConnectionID || e.Payload.Arguments.OperationID != r.OperationID || e.RequestDigest != r.RequestDigest || e.Digest != r.ReviewDigest || e.AssignmentOperation != "" || e.Payload.Grant != nil {
		return errors.New("capture_receipt_mismatch")
	}
	if e.State == "confirmed" && (e.Memory != r.Memory || e.SavedRevision != r.Revision) {
		return errors.New("capture_receipt_mismatch")
	}
	e.State, e.Code, e.Memory, e.SavedRevision = "confirmed", "", r.Memory, r.Revision
	return d.save()
}

// Serialize automatic delivery against manual claims using the existing
// private spool owner. The callback performs local SQL only, never IPC.
func ProcessCapturedRaw(root string, q records.CaptureDeliveryQuery, save func(records.DeliveryAssignmentSource) (records.CaptureDeliveryReceipt, error)) (records.CaptureDeliveryReceipt, error) {
	var out records.CaptureDeliveryReceipt
	d, err := openDelivery(root)
	if err != nil {
		return out, err
	}
	defer d.close()
	e := d.find(q.EntryID)
	if e == nil {
		return out, errors.New("delivery_not_found")
	}
	if e.Digest != q.ReviewDigest || e.AssignmentOperation != "" {
		return out, errors.New("capture_source_changed")
	}
	if _, ok := records.ReadCaptureOrigin(e.Payload.Arguments); !ok {
		return out, errors.New("capture_source_invalid")
	}
	source := assignmentSource(d, e, nil)
	out, err = save(source)
	if err != nil {
		return out, err
	}
	if !out.Persisted || out.Revision != 1 || out.Memory == "" {
		return records.CaptureDeliveryReceipt{}, errors.New("capture_receipt_invalid")
	}
	if err = ackCapturedEntry(d, e, out); err != nil {
		out.AckPending = true
	}
	return out, nil
}
