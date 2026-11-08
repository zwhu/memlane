package daemon

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) verifyNativeOriginal(ctx context.Context, b records.CogneeBinding, v records.Version) (string, error) {
	n := v.SourceDetail.Native
	if n.Instance != b.Instance || n.Dataset != b.Dataset || n.Workspace != b.Workspace {
		return "upstream_changed", nil
	}
	token, err := s.cogneeToken(b.Instance, b.Dataset)
	if err != nil {
		return "", err
	}
	var identity struct {
		Instance string `json:"instance"`
	}
	if err = readCognee(ctx, b.Endpoint, "/api/v1/memlane/identity", token, &identity); err != nil {
		return "", err
	}
	if identity.Instance != b.Instance {
		return "upstream_changed", nil
	}
	var schema map[string]any
	if err = readCognee(ctx, b.Endpoint, "/api/v1/datasets/"+b.Dataset+"/schema", token, &schema); err != nil {
		return "", err
	}
	controller := official.Controller{Root: filepath.Join(filepath.Dir(s.config.Root), "official")}
	text, current, err := controller.Transcript(b.Project, n.Session, b.Workspace)
	if err != nil {
		return "upstream_unavailable", nil
	}
	if current.Digest != n.Digest || text != v.Text {
		return "upstream_changed", nil
	}
	return "available", nil
}

func (s *Server) verifyOfficialOriginal(ctx context.Context, b records.CogneeBinding, v records.Version) (string, error) {
	if v.SourceDetail == nil || v.SourceDetail.Official == nil || len(v.SourceDetail.References) == 0 {
		return "available", nil
	}
	u, err := url.Parse(v.SourceDetail.References[0])
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if u.Scheme != "cognee" || u.Host != b.Dataset || len(parts) != 4 || parts[0] != "sessions" || parts[1] != v.SourceDetail.Official.Session || parts[2] != v.SourceDetail.Official.Kind {
		return "", &records.AccessError{Code: "invalid_provenance", Message: "official original identity differs from its approved binding"}
	}
	token, err := s.cogneeToken(b.Instance, b.Dataset)
	if err != nil {
		return "", err
	}
	var material records.CogneeMaterial
	path := "/api/v1/memlane/datasets/" + b.Dataset + "/sessions/" + url.PathEscape(parts[1]) + "?expected_instance=" + b.Instance
	if err = readCognee(ctx, b.Endpoint, path, token, &material); err != nil {
		return "", err
	}
	if material.Instance != b.Instance || material.Dataset != b.Dataset || material.Session != parts[1] || material.CogneeVersion != "1.6.7" {
		return "", &records.AccessError{Code: "invalid_provenance", Message: "official material identity changed"}
	}
	for _, raw := range material.Raw {
		if raw.ID != parts[3] {
			continue
		}
		if !raw.Persisted {
			return "upstream_unavailable", nil
		}
		if raw.Kind != parts[2] || raw.Sequence != v.SourceDetail.Official.Sequence || raw.Text != v.Text {
			return "upstream_changed", nil
		}
		return "available", nil
	}
	return "upstream_unavailable", nil
}

func (s *Server) readSource(ctx context.Context, q records.SourceRead) (records.SourceRecord, error) {
	s.agentAccess.RLock()
	out, err := s.store.ReadSource(ctx, q)
	s.agentAccess.RUnlock()
	if err != nil || out.State != "available" || out.Record.SourceDetail == nil || (out.Record.SourceDetail.Official == nil && out.Record.SourceDetail.Native == nil) {
		return out, err
	}
	if len(out.Record.SourceDetail.References) == 0 {
		return records.SourceRecord{}, &records.AccessError{Code: "invalid_provenance", Message: "official source reference is missing"}
	}
	bindings, err := s.store.CogneeBindings(ctx, q.Project)
	if err != nil {
		return records.SourceRecord{}, err
	}
	var binding *records.CogneeBinding
	instance := ""
	if out.Record.SourceDetail.Official != nil {
		instance = out.Record.SourceDetail.Official.Instance
	} else {
		instance = out.Record.SourceDetail.Native.Instance
	}
	for _, b := range bindings {
		if b.Instance == instance && strings.HasPrefix(out.Record.SourceDetail.References[0], "cognee://"+b.Dataset+"/") {
			binding = &b
			break
		}
	}
	if binding == nil {
		return records.SourceRecord{}, &records.AccessError{Code: "scope_denied", Message: "official source instance is not currently authorized"}
	}
	var state string
	if out.Record.SourceDetail.Native != nil {
		state, err = s.verifyNativeOriginal(ctx, *binding, out.Record)
	} else {
		state, err = s.verifyOfficialOriginal(ctx, *binding, out.Record)
	}
	if err != nil {
		return records.SourceRecord{}, err
	}
	var original *records.Version
	originalState := "not_captured"
	if state == "available" && out.Record.SourceDetail.Official != nil {
		session := out.Record.SourceDetail.Official.Session
		ref := "cognee://" + binding.Dataset + "/sessions/" + session + "/native/" + strings.TrimPrefix(session, "codex_")
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("cognee-import-v1\x00"+q.Project+"\x00"+ref)).String()
		if v, e := s.store.Current(ctx, id); e == nil && v.State == "active" && v.Dataset == q.Project && v.SourceDetail != nil && v.SourceDetail.Native != nil && v.SourceDetail.Native.Session == session {
			originalState, err = s.verifyNativeOriginal(ctx, *binding, v)
			if err != nil {
				return records.SourceRecord{}, err
			}
			if originalState == "available" {
				original = &v
			}
		}
	}
	// Recheck authorization, owner and concrete source version after network
	// I/O. A disconnect or edit while the response was in flight wins.
	s.agentAccess.RLock()
	defer s.agentAccess.RUnlock()
	fresh, err := s.store.ReadSource(ctx, q)
	if err != nil || fresh.State != "available" {
		return fresh, err
	}
	if state != "available" {
		fresh.State = state
		fresh.Record = records.Version{}
		fresh.Quote = ""
		fresh.QuoteOffset = -1
	}
	if original != nil {
		verified, e := s.store.ReadSource(ctx, records.SourceRead{Project: q.Project, Owner: original.Memory, OwnerRevision: original.Revision, Memory: original.Memory, Revision: original.Revision})
		if e != nil {
			return records.SourceRecord{}, e
		}
		if verified.State == "available" {
			fresh.SessionOriginal = &verified.Record
		} else {
			originalState = verified.State
		}
	}
	fresh.OriginalState = originalState
	return fresh, nil
}
