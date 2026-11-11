package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) writeCogneeToken(instance, dataset, token string) error {
	if parsed, err := uuid.Parse(instance); err != nil || parsed.String() != instance {
		return &records.AccessError{Code: "invalid_request", Message: "canonical service instance required"}
	}
	if parsed, err := uuid.Parse(dataset); err != nil || parsed.String() != dataset {
		return &records.AccessError{Code: "invalid_request", Message: "canonical dataset identity required"}
	}
	parent := filepath.Join(s.config.Root, "cognee-auth")
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return &records.AccessError{Code: "cognee_authorization_required", Message: "private Cognee authorization directory required"}
	}
	file, err := os.CreateTemp(parent, ".token-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.WriteString(token); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(file.Name(), filepath.Join(parent, instance+"-"+dataset+".token")); err != nil {
		return err
	}
	directory, err := os.Open(parent)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (s *Server) authorizeCognee(ctx context.Context, d records.CogneeAuthorization) (records.CogneeBinding, error) {
	b := d.Binding
	token := strings.TrimSpace(d.Credential)
	if len(token) > 8192 || token == "" || strings.ContainsAny(token, "\r\n") {
		return b, &records.AccessError{Code: "cognee_authorization_required", Message: "provide Cognee's independent authorization"}
	}
	s.agentAccess.RLock()
	err := s.store.AuthorizeCogneeGraphBinding(ctx, b)
	s.agentAccess.RUnlock()
	if err != nil {
		return b, err
	}
	var identity struct {
		Instance string `json:"instance"`
		Version  string `json:"cognee_version"`
		Adapter  string `json:"adapter"`
	}
	if err = readCognee(ctx, b.Endpoint, "/api/v1/memlane/identity", token, &identity); err != nil {
		return b, err
	}
	if identity.Version != "1.6.7" || identity.Adapter != "memlane-material-graph-v1" || (b.Instance != "" && b.Instance != identity.Instance) {
		return b, &records.AccessError{Code: "invalid_provenance", Message: "service identity or compatible reader changed"}
	}
	b.Instance = identity.Instance
	if parsed, e := uuid.Parse(b.Dataset); e != nil || parsed.String() != b.Dataset {
		return b, &records.AccessError{Code: "invalid_request", Message: "choose the actual Cognee dataset UUID"}
	}
	var schema map[string]any
	if err = readCognee(ctx, b.Endpoint, "/api/v1/datasets/"+b.Dataset+"/schema", token, &schema); err != nil {
		return b, err
	}
	s.agentAccess.RLock()
	defer s.agentAccess.RUnlock()
	// Refreshing the credential or retrying after a private-file write failure
	// reuses the same approved binding operation. A changed scope/generation
	// still conflicts; it never revives an earlier revoked authorization.
	bindings, e := s.store.CogneeBindings(ctx, b.Project)
	if e != nil {
		return b, e
	}
	for _, prior := range bindings {
		if prior.Instance == b.Instance && prior.Dataset == b.Dataset {
			candidate := b
			candidate.Operation = prior.Operation
			if candidate != prior {
				return b, &records.AccessError{Code: "scope_denied", Message: "approved binding scope or generation changed"}
			}
			b.Operation = prior.Operation
			break
		}
	}
	b, err = s.store.BindCognee(ctx, b)
	if err != nil {
		return b, err
	}
	if err = s.writeCogneeToken(b.Instance, b.Dataset, token); err != nil {
		return b, err
	}
	return b, nil
}

func (s *Server) syncCognee(ctx context.Context, q records.CogneeSync) (records.CogneeSyncReceipt, error) {
	out := records.CogneeSyncReceipt{Project: q.Project, Dataset: q.Dataset, Session: q.Session, Raw: []records.SaveReceipt{}, Knowledge: []records.SaveReceipt{}}
	for _, id := range []string{q.Project, q.Dataset} {
		if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
			return out, &records.AccessError{Code: "invalid_request", Message: "actual project and dataset required"}
		}
	}
	if q.Session == "" || len(q.Session) > 128 || strings.ContainsAny(q.Session, "/\\ \t\r\n") {
		return out, &records.AccessError{Code: "invalid_request", Message: "approved session identity required"}
	}
	bindings, err := s.store.CogneeBindings(ctx, q.Project)
	if err != nil {
		return out, err
	}
	var binding *records.CogneeBinding
	for i := range bindings {
		if bindings[i].Dataset == q.Dataset {
			binding = &bindings[i]
			break
		}
	}
	if binding == nil {
		return out, &records.AccessError{Code: "scope_denied", Message: "approve this service dataset in Memlane first"}
	}
	b := *binding
	s.agentAccess.RLock()
	err = s.store.AuthorizeCogneeBinding(ctx, b)
	s.agentAccess.RUnlock()
	if err != nil {
		return out, err
	}
	token, err := s.cogneeToken(b.Instance, b.Dataset)
	if err != nil {
		return out, err
	}
	var material records.CogneeMaterial
	path := "/api/v1/memlane/datasets/" + b.Dataset + "/sessions/" + url.PathEscape(q.Session) + "?expected_instance=" + b.Instance
	if err = readCognee(ctx, b.Endpoint, path, token, &material); err != nil {
		return out, err
	}
	if material.Instance != b.Instance || material.Dataset != b.Dataset || material.Session != q.Session || material.CogneeVersion != "1.6.7" || len(material.Raw) > 256 || len(material.Documents) > 256 {
		return out, &records.AccessError{Code: "invalid_provenance", Message: "material response identity or budget differs"}
	}
	out.OfficialState = material.State
	if material.State != "persisted" {
		return out, nil
	}
	// Validate the entire material envelope before writing even its first item.
	seen := map[string]bool{}
	for _, raw := range material.Raw {
		if id, e := uuid.Parse(raw.ID); e != nil || id.String() != raw.ID || seen[raw.ID] || !raw.Persisted || raw.Sequence < 0 || strings.TrimSpace(raw.Text) == "" || len(raw.Text) > records.CogneeRawLimit || !utf8.ValidString(raw.Text) || (raw.Kind != "qa" && raw.Kind != "trace") {
			return out, &records.AccessError{Code: "invalid_provenance", Message: "invalid official original material"}
		}
		seen[raw.ID] = true
	}
	for _, document := range material.Documents {
		if id, e := uuid.Parse(document.ID); e != nil || id.String() != document.ID || seen[document.ID] || !strings.HasPrefix(document.Text, "# Session learning (session "+q.Session+")\n\n") || len(document.Text) > records.MergeInputLimit || !utf8.ValidString(document.Text) {
			return out, &records.AccessError{Code: "invalid_provenance", Message: "invalid official learning document"}
		}
		seen[document.ID] = true
	}
	if len(material.Raw) == 0 {
		return out, nil
	}
	if len(material.Raw) > 16 && len(material.Documents) > 0 {
		return out, &records.AccessError{Code: "resource_limit", Message: "complete originals exceed the current 16-source knowledge linkage budget"}
	}
	base := records.CogneeImport{ConnectionID: b.ConnectionID, Generation: b.Generation, Workspace: b.Workspace, Project: b.Project}
	controller := official.Controller{Root: filepath.Join(filepath.Dir(s.config.Root), "official")}
	if _, e := controller.LoadAgent(b.Project); e == nil {
		text, original, e := controller.Transcript(b.Project, q.Session, b.Workspace)
		if e != nil {
			return out, &records.AccessError{Code: "original_unavailable", Message: "完整本轮 Codex 原文未确认：" + e.Error()}
		}
		original.Instance, original.Dataset = b.Instance, b.Dataset
		d := base
		d.ExternalID = "cognee://" + b.Dataset + "/sessions/" + q.Session + "/native/" + strings.TrimPrefix(q.Session, "codex_")
		d.Origin, d.Title, d.Text, d.Native = "raw", "Codex 完整会话原文", text, &original
		s.agentAccess.RLock()
		saved, e := s.store.ImportCognee(ctx, d)
		s.agentAccess.RUnlock()
		if e != nil {
			return out, e
		}
		out.SessionOriginal, out.OriginalState = &saved, "captured"
	} else {
		out.OriginalState = "not_app_managed"
	}
	refs := []records.MergeSource{}
	for _, raw := range material.Raw {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		d := base
		d.ExternalID = "cognee://" + b.Dataset + "/sessions/" + q.Session + "/" + raw.Kind + "/" + raw.ID
		d.Origin = "raw"
		d.Text = raw.Text
		d.Title = fmt.Sprintf("Codex 会话 · %s %d", raw.Kind, raw.Sequence+1)
		d.Material = &records.CogneeOriginal{Instance: b.Instance, Session: q.Session, Kind: raw.Kind, Sequence: raw.Sequence, CapturedAt: raw.CapturedAt, OfficialState: "persisted"}
		metadata, _ := json.Marshal(d.Material)
		d.Fragment = string(metadata)
		s.agentAccess.RLock()
		receipt, e := s.store.ImportCognee(ctx, d)
		s.agentAccess.RUnlock()
		if e != nil {
			return out, e
		}
		out.Raw = append(out.Raw, receipt)
		quote := raw.Text
		if len(quote) > records.MergeInputLimit {
			quote = quote[:records.MergeInputLimit]
			for !utf8.ValidString(quote) {
				quote = quote[:len(quote)-1]
			}
		}
		refs = append(refs, records.MergeSource{Memory: receipt.Record.Memory, Revision: receipt.SavedRevision, Quote: quote})
	}
	for _, document := range material.Documents {
		if !document.Processed {
			out.PendingDocuments++
			continue
		}
		if err = ctx.Err(); err != nil {
			return out, err
		}
		d := base
		d.ExternalID = "cognee://" + b.Dataset + "/sessions/" + q.Session + "/data/" + document.ID
		d.Origin = "knowledge"
		d.Text = document.Text
		d.Title = "Cognee 提炼 · " + titleForLearning(document.Text, q.Session)
		d.Sources = refs
		d.Material = &records.CogneeOriginal{Instance: b.Instance, Session: q.Session, Kind: "data", Sequence: 0, OfficialState: "persisted"}
		metadata, _ := json.Marshal(d.Material)
		d.Fragment = string(metadata)
		s.agentAccess.RLock()
		receipt, e := s.store.ImportCognee(ctx, d)
		s.agentAccess.RUnlock()
		if e != nil {
			return out, e
		}
		out.Knowledge = append(out.Knowledge, receipt)
	}
	select {
	case s.work <- struct{}{}:
	default:
	}
	return out, nil
}
func titleForLearning(text, session string) string {
	text = strings.TrimSpace(strings.TrimPrefix(text, "# Session learning (session "+session+")\n\n"))
	runes := []rune(text)
	return string(runes[:min(48, len(runes))])
}
