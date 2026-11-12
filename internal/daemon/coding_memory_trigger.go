package daemon

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func (o *sessionProcessingOwner) authorizeTrigger(capability string, q records.CodingMemoryTrigger) error {
	if o == nil || o.capability == "" || subtle.ConstantTimeCompare([]byte(o.capability), []byte(capability)) != 1 {
		return records.MergeError("service_authorization_required")
	}
	for _, id := range []string{q.Operation, q.Instance, q.Dataset, q.User} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			return records.MergeError("invalid_request")
		}
	}
	if len(q.Session) < 1 || len(q.Session) > 128 || strings.ContainsFunc(q.Session, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return records.MergeError("invalid_request")
	}
	return nil
}

func triggerMatches(q records.CodingMemoryTrigger, task records.SessionProcessingTask) bool {
	r := task.Request
	return r.AutomaticGrant != nil && r.Operation == q.Operation && r.Instance == q.Instance && r.Dataset == q.Dataset && r.User == q.User && r.Session == q.Session
}

func (o *sessionProcessingOwner) StartAutomatic(ctx context.Context, capability string, q records.CodingMemoryTrigger) (records.SessionProcessingTask, error) {
	var empty records.SessionProcessingTask
	if err := o.authorizeTrigger(capability, q); err != nil {
		return empty, err
	}
	// Replay the same official invocation before reading mutable input/model
	// state. A lost response or service restart never restarts that operation.
	f, err := o.server.store.LoadSessionProcessing(ctx, q.Operation)
	if err == nil {
		if !triggerMatches(q, f.Task) {
			return empty, records.ErrConflict
		}
		return f.Task, nil
	}
	if err != sql.ErrNoRows {
		return empty, err
	}
	s := o.server
	grant, err := s.store.CodingMemoryGrantForDataset(ctx, q.Instance, q.Dataset)
	if err != nil || grant.User != q.User {
		return empty, records.MergeError("scope_denied")
	}
	if err = s.store.AuthorizeCodingMemoryGrant(ctx, grant); err != nil {
		return empty, err
	}
	request := records.SessionProcessingRequest{Operation: q.Operation, Instance: q.Instance, Dataset: q.Dataset, User: q.User, Session: q.Session, Project: grant.Binding.Project, AutomaticGrant: &grant}
	value, err := s.readOfficialSession(ctx, sessionProcessingQuery(request), true)
	if err != nil {
		return empty, err
	}
	detail, ok := value.(records.SessionDetail)
	if !ok {
		return empty, records.MergeError("stale_source")
	}
	request.ObservedDigest = records.SessionProcessingSourceDigest(detail)
	configuration, err := s.store.ProcessingConfiguration(ctx)
	if err != nil {
		return empty, err
	}
	if !configuration.Current.Active || configuration.Current.Revision == nil {
		return empty, records.MergeError("configuration_required")
	}
	request.Processing = records.ProcessingTaskAuthorization{Revision: configuration.Current.Revision.ID, Generation: configuration.Current.Generation}
	return o.Start(ctx, request)
}

func (o *sessionProcessingOwner) AutomaticTask(ctx context.Context, capability string, q records.CodingMemoryTrigger) (records.SessionProcessingTask, error) {
	if err := o.authorizeTrigger(capability, q); err != nil {
		return records.SessionProcessingTask{}, err
	}
	f, err := o.server.store.LoadSessionProcessing(ctx, q.Operation)
	if err != nil {
		return records.SessionProcessingTask{}, err
	}
	if !triggerMatches(q, f.Task) {
		return records.SessionProcessingTask{}, records.ErrConflict
	}
	return f.Task, nil
}
