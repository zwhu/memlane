package daemon

import (
	"context"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) reauthorizeCogneeRead(ctx context.Context, q records.CogneeReadReauthorization) (records.CogneeBinding, error) {
	if q.Previous.MaterialSubmission {
		return s.reauthorizeCapturedRead(ctx, q)
	}
	s.agentAccess.RLock()
	candidate, err := s.store.PrepareCogneeReadReauthorization(ctx, q)
	s.agentAccess.RUnlock()
	if err != nil {
		return records.CogneeBinding{}, err
	}
	token, err := s.cogneeToken(candidate.Instance, candidate.Dataset)
	if err != nil {
		return records.CogneeBinding{}, err
	}
	reader := official.DocumentReader{SessionReader: official.SessionReader{Endpoint: candidate.Endpoint, Instance: candidate.Instance, Dataset: candidate.Dataset, Token: token}}
	// This is a dataset-authorized metadata read, not a graph/import/model run.
	if _, err = reader.ListPage(ctx, 1, 0); err != nil {
		return records.CogneeBinding{}, sessionReadError(err)
	}
	s.agentAccess.Lock()
	defer s.agentAccess.Unlock()
	return s.store.ReauthorizeCogneeRead(ctx, q)
}

// Refreshing a credential after reconnecting does not restore permission to
// read. Only the separate, explicit review can commit a captured-read grant.
func (s *Server) refreshCapturedReadCredential(ctx context.Context, b records.CogneeBinding, token string) (records.CogneeBinding, error) {
	s.agentAccess.RLock()
	c, err := s.store.Connection(ctx, b.ConnectionID)
	q := records.CogneeReadReauthorization{Previous: b, Operation: uuid.NewString(), Generation: c.Generation}
	var rows []records.OfficialCaptureDelivery
	if err == nil {
		rows, err = s.store.PiCaptureReadReviewDeliveries(ctx, q, piCapturePolicyDigest)
	}
	grant, checkErr := s.store.PreparePiCaptureReadReauthorization(ctx, q, piCapturePolicyDigest)
	s.agentAccess.RUnlock()
	if err != nil {
		return records.CogneeBinding{}, err
	}
	if checkErr != nil {
		return records.CogneeBinding{}, checkErr
	}
	if len(rows) == 0 {
		return records.CogneeBinding{}, &records.AccessError{Code: "scope_denied", Message: "original saved captured entries required"}
	}
	reader := official.SessionReader{Endpoint: b.Endpoint, Instance: b.Instance, Dataset: b.Dataset, Token: token}
	if err = reader.VerifyCaptureService(ctx); err != nil {
		return records.CogneeBinding{}, sessionReadError(err)
	}
	if _, _, err = reader.ReadSavedCapture(ctx, rows[0]); err != nil {
		return records.CogneeBinding{}, sessionReadError(err)
	}
	s.agentAccess.Lock()
	defer s.agentAccess.Unlock()
	current, err := s.store.PreparePiCaptureReadReauthorization(ctx, q, piCapturePolicyDigest)
	if err != nil {
		return records.CogneeBinding{}, err
	}
	if current != grant {
		return records.CogneeBinding{}, &records.AccessError{Code: "scope_changed", Message: "captured credential scope changed"}
	}
	if err = s.writeCogneeToken(b.Instance, b.Dataset, token); err != nil {
		return records.CogneeBinding{}, err
	}
	return b, nil
}

func (s *Server) reauthorizeCapturedRead(ctx context.Context, q records.CogneeReadReauthorization) (records.CogneeBinding, error) {
	s.agentAccess.RLock()
	grant, err := s.store.PreparePiCaptureReadReauthorization(ctx, q, piCapturePolicyDigest)
	var rows []records.OfficialCaptureDelivery
	if err == nil {
		rows, err = s.store.PiCaptureReadReviewDeliveries(ctx, q, piCapturePolicyDigest)
	}
	s.agentAccess.RUnlock()
	if err != nil {
		return records.CogneeBinding{}, err
	}
	token, err := s.cogneeToken(q.Previous.Instance, q.Previous.Dataset)
	if err != nil {
		return records.CogneeBinding{}, err
	}
	reader := official.SessionReader{Endpoint: q.Previous.Endpoint, Instance: q.Previous.Instance, Dataset: q.Previous.Dataset, Token: token}
	if err = reader.VerifyCaptureService(ctx); err != nil {
		return records.CogneeBinding{}, sessionReadError(err)
	}
	if len(rows) == 0 {
		return records.CogneeBinding{}, &records.AccessError{Code: "scope_denied", Message: "confirmed saved capture receipts required"}
	}
	// Exact own-user, dataset and original-operation read, not a history scan.
	if _, _, err = reader.ReadSavedCapture(ctx, rows[0]); err != nil {
		return records.CogneeBinding{}, sessionReadError(err)
	}
	s.agentAccess.Lock()
	defer s.agentAccess.Unlock()
	current, err := s.store.PreparePiCaptureReadReauthorization(ctx, q, piCapturePolicyDigest)
	if err != nil {
		return records.CogneeBinding{}, err
	}
	if current != grant {
		return records.CogneeBinding{}, &records.AccessError{Code: "scope_changed", Message: "captured read identity changed during review"}
	}
	if _, err = s.store.ReauthorizePiCaptureRead(ctx, q, piCapturePolicyDigest); err != nil {
		return records.CogneeBinding{}, err
	}
	return q.Previous, nil
}
