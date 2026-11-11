package agentbridge

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"time"
)

// A journal precedes a claim and any RPC. Its frozen user intent and original
// Agent payload are separate from the Agent operation and its save status.
type deliveryAssignmentJournal struct {
	Intent    records.DeliveryAssignmentIntent   `json:"intent"`
	Source    deliveryPayload                    `json:"source"`
	Digest    string                             `json:"digest"`
	State     string                             `json:"state"`
	CreatedMS int64                              `json:"created_ms"`
	UpdatedMS int64                              `json:"updated_ms"`
	Receipt   *records.DeliveryAssignmentReceipt `json:"receipt,omitempty"`
}

func assignmentError(code string) error { return &records.AccessError{Code: code, Message: code} }
func assignmentIntentValid(q records.DeliveryAssignmentIntent) bool {
	return q.OperationID != "" && len(q.OperationID) <= 128 && q.LibraryID != "" && q.EntryID != "" && q.RequestDigest != "" && q.ReviewDigest != "" && q.Project != "" && q.WorkspaceDigest != ""
}
func assignmentJournalDigest(j deliveryAssignmentJournal) string {
	return deliveryHash(struct {
		Intent records.DeliveryAssignmentIntent
		Source deliveryPayload
	}{j.Intent, j.Source})
}
func sameAssignmentIntent(a, b records.DeliveryAssignmentIntent) bool { return a == b }
func validateDeliveryAssignments(d *deliveryFile, root string) error {
	if d.ledger.Assignments == nil {
		d.ledger.Assignments = map[string]deliveryAssignmentJournal{}
	}
	if len(d.ledger.Assignments) > 0 && d.ledger.Version != 2 {
		return assignmentError("delivery_format_unsupported")
	}
	if len(d.ledger.Assignments) > deliveryLimit {
		return assignmentError("delivery_corrupt")
	}
	for op, j := range d.ledger.Assignments {
		if !assignmentIntentValid(j.Intent) || op != j.Intent.OperationID || j.Source.Root != root || j.Source.Grant != nil || j.Intent.OperationID == j.Source.Arguments.OperationID || j.Intent.RequestDigest != originalRequestDigest(j.Source) || j.Intent.ReviewDigest != payloadDigest(j.Source) || j.Digest != assignmentJournalDigest(j) {
			return assignmentError("delivery_corrupt")
		}
		e := d.find(j.Intent.EntryID)
		if e == nil || e.AssignmentOperation != op || e.RequestDigest != j.Intent.RequestDigest || deliveryKey(Config{ConnectionID: j.Source.Connection}, j.Source.Arguments) != e.ID {
			return assignmentError("delivery_corrupt")
		}
		switch j.State {
		case "journaled":
			if j.Receipt != nil {
				return assignmentError("delivery_corrupt")
			}
		case "claimed", "saved":
			if e.Payload.Grant != nil || e.Attempts != 0 || e.Digest != j.Intent.ReviewDigest || e.InitialOwner != "" {
				return assignmentError("delivery_corrupt")
			}
			if j.State == "saved" {
				if j.Receipt == nil || !assignmentReceiptMatches(j, *j.Receipt) {
					return assignmentError("delivery_corrupt")
				}
			} else if j.Receipt != nil {
				return assignmentError("delivery_corrupt")
			}
		default:
			return assignmentError("delivery_corrupt")
		}
	}
	for _, e := range d.ledger.Entries {
		if e.AssignmentOperation != "" {
			j, ok := d.ledger.Assignments[e.AssignmentOperation]
			if !ok || j.Intent.EntryID != e.ID {
				return assignmentError("delivery_corrupt")
			}
		}
	}
	return nil
}
func deliveryAssignmentItem(d *deliveryFile, e *deliveryEntry) records.AgentDeliveryItem {
	item := deliveryItem(e)
	if j, ok := d.ledger.Assignments[e.AssignmentOperation]; ok {
		link := &records.DeliveryAssignmentSpoolStatus{OperationID: j.Intent.OperationID, LibraryID: j.Intent.LibraryID, Project: j.Intent.Project, State: j.State}
		if j.Receipt != nil {
			link.Memory = j.Receipt.Memory
			link.Revision = j.Receipt.Revision
		}
		item.Assignment = link
	}
	return item
}
func assignmentSource(d *deliveryFile, e *deliveryEntry, j *deliveryAssignmentJournal) records.DeliveryAssignmentSource {
	p := e.Payload
	if j != nil {
		p = j.Source
	}
	item := deliveryAssignmentItem(d, e)
	if j != nil {
		item.Status.RequestDigest = j.Intent.RequestDigest
		item.Status.ReviewDigest = j.Intent.ReviewDigest
	}
	return records.DeliveryAssignmentSource{Review: records.AgentDeliveryReview{Item: item, Arguments: p.Arguments}, WorkspacePhysical: p.WorkspacePhysical}
}
func assignmentEligible(e *deliveryEntry) bool {
	return e.Payload.WorkspacePhysical != "" && e.Payload.Grant == nil && e.Attempts == 0 && e.ActiveAttempt == "" && (e.State == "pending_unverified" || e.State == "blocked") && e.Memory == "" && e.SavedRevision == 0
}

// ReadDeliveryAssignmentSource returns only owned retained data, including a
// claimed/unknown original source. It performs no network or authorization.
func ReadDeliveryAssignmentSource(root, entryID, reviewDigest string) (records.DeliveryAssignmentSource, error) {
	d, err := openDelivery(root)
	if err != nil {
		return records.DeliveryAssignmentSource{}, err
	}
	defer d.close()
	e := d.find(entryID)
	if e == nil {
		return records.DeliveryAssignmentSource{}, assignmentError("delivery_not_found")
	}
	if j, ok := d.ledger.Assignments[e.AssignmentOperation]; ok {
		if reviewDigest != j.Intent.ReviewDigest {
			return records.DeliveryAssignmentSource{}, assignmentError("delivery_review_required")
		}
		return assignmentSource(d, e, &j), nil
	}
	if reviewDigest == "" || e.Digest != reviewDigest {
		return records.DeliveryAssignmentSource{}, assignmentError("delivery_review_required")
	}
	return assignmentSource(d, e, nil), nil
}

// JournalDeliveryAssignment persists the exact user request before the claim.
// Existing journals are immutable even if their initial claim never completed.
func JournalDeliveryAssignment(root string, q records.DeliveryAssignmentIntent) (records.DeliveryAssignmentSource, error) {
	d, err := openDelivery(root)
	if err != nil {
		return records.DeliveryAssignmentSource{}, err
	}
	defer d.close()
	return journalDeliveryAssignment(d, q)
}
func journalDeliveryAssignment(d *deliveryFile, q records.DeliveryAssignmentIntent) (records.DeliveryAssignmentSource, error) {
	if !assignmentIntentValid(q) {
		return records.DeliveryAssignmentSource{}, assignmentError("invalid_request")
	}
	e := d.find(q.EntryID)
	if e == nil {
		return records.DeliveryAssignmentSource{}, assignmentError("delivery_not_found")
	}
	if e.AssignmentOperation != "" {
		j := d.ledger.Assignments[e.AssignmentOperation]
		if !sameAssignmentIntent(j.Intent, q) {
			return records.DeliveryAssignmentSource{}, assignmentError("delivery_assignment_conflict")
		}
		return assignmentSource(d, e, &j), nil
	}
	if existing, ok := d.ledger.Assignments[q.OperationID]; ok {
		_ = existing
		return records.DeliveryAssignmentSource{}, assignmentError("delivery_assignment_conflict")
	}
	if q.OperationID == e.Payload.Arguments.OperationID {
		return records.DeliveryAssignmentSource{}, assignmentError("delivery_assignment_conflict")
	}
	if !assignmentEligible(e) {
		return records.DeliveryAssignmentSource{}, assignmentError("delivery_assignment_ineligible")
	}
	if e.RequestDigest != q.RequestDigest || e.Digest != q.ReviewDigest {
		return records.DeliveryAssignmentSource{}, assignmentError("delivery_review_required")
	}
	raw, _ := json.Marshal(e.Payload)
	var source deliveryPayload
	if json.Unmarshal(raw, &source) != nil {
		return records.DeliveryAssignmentSource{}, assignmentError("delivery_corrupt")
	}
	now := time.Now().UnixMilli()
	j := deliveryAssignmentJournal{Intent: q, Source: source, State: "journaled", CreatedMS: now, UpdatedMS: now}
	j.Digest = assignmentJournalDigest(j)
	d.ledger.Assignments[q.OperationID] = j
	d.ledger.Version = 2
	e.AssignmentOperation = q.OperationID
	if err := d.save(); err != nil {
		return records.DeliveryAssignmentSource{}, err
	}
	return assignmentSource(d, e, &j), nil
}

// Claim is a second fsync after the journal. Retiring InitialOwner prevents a
// late first authorization response from binding/sending this Agent operation.
func ClaimDeliveryAssignment(root string, q records.DeliveryAssignmentIntent) (records.DeliveryAssignmentSource, error) {
	d, err := openDelivery(root)
	if err != nil {
		return records.DeliveryAssignmentSource{}, err
	}
	defer d.close()
	if _, err = journalDeliveryAssignment(d, q); err != nil {
		return records.DeliveryAssignmentSource{}, err
	}
	e := d.find(q.EntryID)
	j := d.ledger.Assignments[q.OperationID]
	if j.State == "claimed" || j.State == "saved" {
		return assignmentSource(d, e, &j), nil
	}
	if !assignmentEligible(e) || e.Digest != q.ReviewDigest || e.RequestDigest != q.RequestDigest {
		return records.DeliveryAssignmentSource{}, assignmentError("delivery_assignment_ineligible")
	}
	e.InitialOwner = ""
	j.State = "claimed"
	j.UpdatedMS = time.Now().UnixMilli()
	d.ledger.Assignments[q.OperationID] = j
	if err = d.save(); err != nil {
		return records.DeliveryAssignmentSource{}, err
	}
	return assignmentSource(d, e, &j), nil
}
func PrepareDeliveryAssignment(root string, q records.DeliveryAssignmentIntent) error {
	_, err := ClaimDeliveryAssignment(root, q)
	return err
}
func ReadDeliveryAssignmentIntent(root, entryID string) (*records.DeliveryAssignmentIntent, error) {
	d, err := openDelivery(root)
	if err != nil {
		return nil, err
	}
	defer d.close()
	e := d.find(entryID)
	if e == nil {
		return nil, assignmentError("delivery_not_found")
	}
	j, ok := d.ledger.Assignments[e.AssignmentOperation]
	if !ok {
		return nil, nil
	}
	out := j.Intent
	return &out, nil
}
func ReadDeliveryAssignmentClaim(root string, q records.DeliveryAssignmentIntent) (records.DeliveryAssignmentSource, error) {
	d, err := openDelivery(root)
	if err != nil {
		return records.DeliveryAssignmentSource{}, err
	}
	defer d.close()
	e := d.find(q.EntryID)
	if e == nil {
		return records.DeliveryAssignmentSource{}, assignmentError("delivery_not_found")
	}
	j, ok := d.ledger.Assignments[e.AssignmentOperation]
	if !ok || !sameAssignmentIntent(j.Intent, q) {
		return records.DeliveryAssignmentSource{}, assignmentError("delivery_assignment_conflict")
	}
	if j.State != "claimed" && j.State != "saved" {
		return records.DeliveryAssignmentSource{}, assignmentError("delivery_assignment_not_claimed")
	}
	return assignmentSource(d, e, &j), nil
}
func assignmentReceiptMatches(j deliveryAssignmentJournal, r records.DeliveryAssignmentReceipt) bool {
	q := j.Intent
	return r.OperationID == q.OperationID && r.LibraryID == q.LibraryID && r.EntryID == q.EntryID && r.RequestDigest == q.RequestDigest && r.ReviewDigest == q.ReviewDigest && r.Project == q.Project && r.WorkspaceDigest == q.WorkspaceDigest && r.OriginalOperationID == j.Source.Arguments.OperationID && r.OriginalConnectionID == j.Source.Connection && r.Source == "user_assignment" && r.State == "saved" && r.Revision > 0 && r.CreatedMS > 0 && uuid.Validate(r.Memory) == nil
}
func normalizedAssignmentReceipt(r records.DeliveryAssignmentReceipt) records.DeliveryAssignmentReceipt {
	raw, _ := json.Marshal(r)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	delete(fields, "spool_ack_pending")
	raw, _ = json.Marshal(fields)
	var normalized records.DeliveryAssignmentReceipt
	_ = json.Unmarshal(raw, &normalized)
	return normalized
}

// Ack links the independent SQL user-assignment receipt. It never sets the
// original Agent operation to persisted/confirmed or changes its namespace.
func AckDeliveryAssignment(root string, q records.DeliveryAssignmentIntent, r records.DeliveryAssignmentReceipt) error {
	d, err := openDelivery(root)
	if err != nil {
		return err
	}
	defer d.close()
	e := d.find(q.EntryID)
	if e == nil {
		return assignmentError("delivery_not_found")
	}
	j, ok := d.ledger.Assignments[e.AssignmentOperation]
	if !ok || !sameAssignmentIntent(j.Intent, q) || j.State == "journaled" || !assignmentReceiptMatches(j, r) {
		return assignmentError("delivery_assignment_receipt_mismatch")
	}
	r = normalizedAssignmentReceipt(r)
	if j.Receipt != nil {
		if deliveryHash(*j.Receipt) != deliveryHash(r) {
			return assignmentError("delivery_assignment_receipt_mismatch")
		}
		return nil
	}
	j.State = "saved"
	j.UpdatedMS = time.Now().UnixMilli()
	j.Receipt = &r
	d.ledger.Assignments[q.OperationID] = j
	return d.save()
}
