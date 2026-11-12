package daemon

import (
	"context"
	"encoding/json"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) captureReadScope(ctx context.Context, q records.SessionQuery) (records.CogneeBinding, []records.OfficialCaptureDelivery, error) {
	var b records.CogneeBinding
	s.agentAccess.RLock()
	defer s.agentAccess.RUnlock()
	bindings, err := s.store.CogneeBindings(ctx, q.Project)
	if err != nil {
		return b, nil, err
	}
	matched := 0
	for _, candidate := range bindings {
		if candidate.Project == q.Project && candidate.Instance == q.Instance && candidate.Dataset == q.Dataset && candidate.MaterialSubmission {
			b = candidate
			matched++
		}
	}
	if matched != 1 {
		return b, nil, &records.AccessError{Code: "scope_denied", Message: "current captured material binding required"}
	}
	rows, err := s.store.CapturedDeliveries(ctx, b, piCapturePolicyDigest)
	return b, rows, err
}

func (s *Server) readCapturedSession(ctx context.Context, q records.SessionQuery, detail bool) (any, error) {
	fail := &records.AccessError{Code: "scope_denied", Message: "only confirmed captured entries may be read"}
	if (!detail && (q.Limit < 1 || q.Limit > 100 || q.Offset < 0)) || (detail && (q.Selected == nil || q.Selected.Dataset != q.Dataset)) {
		return nil, fail
	}
	b, rows, err := s.captureReadScope(ctx, q)
	if err != nil {
		return nil, err
	}
	token, err := s.cogneeToken(b.Instance, b.Dataset)
	if err != nil {
		return nil, err
	}
	reader := official.SessionReader{Endpoint: b.Endpoint, Instance: b.Instance, Dataset: b.Dataset, Token: token}
	// Even an empty local receipt set is not a verified empty service while
	// the official endpoint is unavailable or has changed identity.
	if err = reader.VerifyCaptureService(ctx); err != nil {
		return nil, sessionReadError(err)
	}
	sessions := []string{}
	grouped := map[string][]records.OfficialCaptureDelivery{}
	for _, r := range rows {
		if _, ok := grouped[r.Session]; !ok {
			sessions = append(sessions, r.Session)
		}
		grouped[r.Session] = append(grouped[r.Session], r)
	}
	used := []records.OfficialCaptureDelivery{}
	read := func(row records.OfficialCaptureDelivery) (string, json.RawMessage, error) {
		user, entry, e := reader.ReadSavedCapture(ctx, row)
		if e != nil {
			return "", nil, sessionReadError(e)
		}
		used = append(used, row)
		return user, entry, nil
	}
	var out any
	if detail {
		selected := q.Selected
		entries := grouped[selected.Session]
		if len(entries) == 0 || len(entries) > 256 {
			return nil, fail
		}
		d := records.SessionDetail{SessionSummary: records.SessionSummary{Session: selected.Session, Dataset: b.Dataset, Agent: "pi"}, QAs: []records.SessionQA{}, Traces: []records.SessionTrace{}}
		ids := map[string]bool{}
		for i := len(entries) - 1; i >= 0; i-- {
			row := entries[i]
			user, entry, e := read(row)
			if e != nil {
				return nil, e
			}
			if user != selected.User || (d.User != "" && d.User != user) || ids[row.OfficialID] {
				return nil, fail
			}
			d.User = user
			ids[row.OfficialID] = true
			if row.Kind == "qa" {
				var qa records.SessionQA
				if json.Unmarshal(entry, &qa) != nil {
					return nil, fail
				}
				qa.ID = row.OfficialID
				flags := savedCaptureTruncation(entry, "context")
				qa.QuestionTruncated, qa.AnswerTruncated = flags.Question, flags.Answer
				d.QAs = append(d.QAs, qa)
			} else {
				var trace records.SessionTrace
				if json.Unmarshal(entry, &trace) != nil {
					return nil, fail
				}
				trace.ID = row.OfficialID
				flags := savedCaptureTruncation(entry, "memory_context")
				trace.ParamsTruncated, trace.ResultTruncated, trace.ErrorTruncated = flags.Params, flags.Result, flags.Error
				d.Traces = append(d.Traces, trace)
			}
		}
		d.QACount, d.ToolCount = len(d.QAs), len(d.Traces)
		out = d
	} else {
		page := records.SessionPage{Sessions: []records.SessionSummary{}, Total: len(sessions), Limit: q.Limit, Offset: q.Offset}
		if q.Offset < len(sessions) {
			end := min(len(sessions), q.Offset+q.Limit)
			page.HasMore = end < len(sessions)
			for _, session := range sessions[q.Offset:end] {
				user, _, e := read(grouped[session][0])
				if e != nil {
					return nil, e
				}
				page.Sessions = append(page.Sessions, records.SessionSummary{Session: session, User: user, Dataset: b.Dataset, Agent: "pi"})
			}
		}
		out = page
	}
	// A grant change while reading wins. New independent receipts are allowed;
	// every adopted row must still be present with exactly the same metadata.
	current, fresh, err := s.captureReadScope(ctx, q)
	if err != nil {
		return nil, err
	}
	if current != b {
		return nil, fail
	}
	valid := map[string]records.OfficialCaptureDelivery{}
	for _, r := range fresh {
		valid[r.Operation] = r
	}
	for _, r := range used {
		if valid[r.Operation] != r {
			return nil, fail
		}
	}
	return out, nil
}
