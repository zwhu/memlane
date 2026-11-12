package daemon

import (
	"context"
	"net/url"
	"strings"

	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) readOfficialSourceSession(ctx context.Context, q records.SourceRead) (records.SourceSession, error) {
	var out records.SourceSession
	verified, err := s.readSource(ctx, q)
	if err != nil {
		return out, err
	}
	if verified.State != "available" {
		return out, &records.AccessError{Code: "stale_source", Message: "knowledge or source version changed"}
	}
	stamp := verified.Record.SourceDetail
	if stamp == nil || stamp.Official == nil || len(stamp.References) == 0 {
		return out, &records.AccessError{Code: "invalid_provenance", Message: "official session provenance required"}
	}
	ref, err := url.Parse(stamp.References[0])
	if err != nil {
		return out, &records.AccessError{Code: "invalid_provenance", Message: "official source reference required"}
	}
	parts := strings.Split(strings.Trim(ref.Path, "/"), "/")
	if ref.Scheme != "cognee" || len(parts) != 4 || parts[0] != "sessions" || parts[1] != stamp.Official.Session || parts[2] != stamp.Official.Kind || (parts[2] != "qa" && parts[2] != "trace") || parts[3] == "" {
		return out, &records.AccessError{Code: "invalid_provenance", Message: "official source reference changed"}
	}
	query := records.SessionQuery{Project: q.Project, Instance: stamp.Official.Instance, Dataset: ref.Host}
	binding, err := s.sessionBinding(ctx, query)
	if err != nil {
		return out, err
	}
	token, err := s.cogneeToken(binding.Instance, binding.Dataset)
	if err != nil {
		return out, err
	}
	reader := official.SessionReader{Endpoint: binding.Endpoint, Instance: binding.Instance, Dataset: binding.Dataset, Token: token}
	detail, err := reader.ReadUniqueSession(ctx, parts[1])
	if err != nil {
		return out, sessionReadError(err)
	}
	// Keep the lineage/owner-version check active on every refresh, in addition
	// to the official principal/dataset/session checks above.
	fresh, err := s.readSource(ctx, q)
	if err != nil {
		return out, err
	}
	if fresh.State != "available" || records.MergeDigest(fresh.Record) != records.MergeDigest(verified.Record) {
		return out, &records.AccessError{Code: "stale_source", Message: "knowledge or source version changed during read"}
	}
	current, err := s.sessionBinding(ctx, query)
	if err != nil {
		return out, err
	}
	if current != binding {
		return out, &records.AccessError{Code: "scope_changed", Message: "official source binding changed"}
	}
	out = records.SourceSession{Binding: binding, Detail: detail, TargetKind: parts[2], TargetID: parts[3]}
	if parts[2] == "qa" {
		for _, qa := range detail.QAs {
			if qa.ID == parts[3] {
				out.TargetLoaded = true
			}
		}
	} else {
		for _, trace := range detail.Traces {
			if trace.ID == parts[3] {
				out.TargetLoaded = true
			}
		}
	}
	return out, nil
}
