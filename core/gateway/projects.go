package gateway

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/zwhu/memlane/core/domain"
)

type projectSummary struct {
	ProjectID          string `json:"project_id"`
	Name               string `json:"name"`
	DatasetID          string `json:"dataset_id"`
	AutoCapture        bool   `json:"auto_capture"`
	AutoCaptureVersion int    `json:"auto_capture_version"`
	KnowledgeCount     int    `json:"knowledge_count"`
	RawDocCount        int    `json:"raw_doc_count"`
	SessionCount       int    `json:"session_count"`
	UnreadCount        int    `json:"unread_count"`
	UnreadStateRev     int64  `json:"unread_state_rev"`
	CatalogVersion     string `json:"catalog_version"`
	InboxPending       int    `json:"inbox_pending"`
	InboxFailed        int    `json:"inbox_failed"`
	LastError          string `json:"last_error"`
}

type aclBody struct {
	AgentID     string              `json:"agent_id"`
	Access      *domain.AccessLevel `json:"access,omitempty"`
	Capture     *bool               `json:"capture,omitempty"`
	AutoProcess *bool               `json:"auto_process,omitempty"`
}

type projectView struct {
	projectSummary
	ID            string       `json:"id"`
	DatasetName   string       `json:"dataset_name"`
	CodeDatasetID string       `json:"code_dataset_id"`
	CodeEnabled   bool         `json:"code_enabled"`
	Status        string       `json:"status"`
	Aliases       []string     `json:"aliases"`
	Overrides     []aclBody    `json:"overrides"`
	Inbox         InboxSummary `json:"inbox"`
}

func (s *Server) projectView(ctx context.Context, p domain.Project) (projectView, error) {
	view := projectView{projectSummary: projectSummary{ProjectID: p.ID, Name: p.Name, DatasetID: p.DatasetID,
		AutoCapture: p.AutoCapture, AutoCaptureVersion: p.AutoCaptureVersion}, ID: p.ID, DatasetName: p.DatasetName,
		CodeDatasetID: p.CodeDatasetID, CodeEnabled: p.CodeEnabled, Status: p.Status, Aliases: append([]string{}, p.Aliases...),
		Overrides: []aclBody{}}
	err := catalogCounts(ctx, s.cfg.Store.DB(), p.DatasetID, &view.projectSummary)
	if err != nil {
		return view, err
	}
	view.Inbox, err = s.inbox.Summary(ctx, p.ID)
	if err != nil {
		return view, err
	}
	view.InboxPending, view.InboxFailed = view.Inbox.Pending, view.Inbox.Failed
	if len(view.Inbox.RecentFailures) > 0 {
		view.LastError = view.Inbox.RecentFailures[0].LastError
	}
	overrides, err := s.cfg.Store.ListACLOverrides(ctx, "project", p.ID)
	for _, a := range overrides {
		view.Overrides = append(view.Overrides, aclBody{a.AgentID, a.Access, a.Capture, a.AutoProcess})
	}
	return view, err
}

func catalogCounts(ctx context.Context, db sqlRead, ds string, out *projectSummary) error {
	if ds == "" {
		return nil
	}
	return db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM catalog_docs WHERE dataset_id=?1 AND is_knowledge=1),
		(SELECT count(*) FROM catalog_docs WHERE dataset_id=?1 AND kind='raw_doc'),
		(SELECT count(*) FROM catalog_sessions WHERE dataset_id=?1),
		COALESCE((SELECT catalog_version FROM catalog_sync_state WHERE dataset_id=?1),'')`, ds).
		Scan(&out.KnowledgeCount, &out.RawDocCount, &out.SessionCount, &out.CatalogVersion)
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projects, err := s.cfg.Store.ListProjects(ctx)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	views := make([]projectView, 0, len(projects))
	for _, p := range projects {
		view, err := s.projectView(ctx, p)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		views = append(views, view)
	}
	writeJSON(w, 200, map[string]any{"projects": views})
}

func (s *Server) respondProject(w http.ResponseWriter, r *http.Request, p domain.Project, status int) {
	view, err := s.projectView(r.Context(), p)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, status, view)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.cfg.Store.GetProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	s.respondProject(w, r, p, 200)
}

type projectInput struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	Name          *string   `json:"name"`
	DatasetID     string    `json:"dataset_id"`
	DatasetName   string    `json:"dataset_name"`
	AutoCapture   *bool     `json:"auto_capture"`
	Aliases       *[]string `json:"aliases"`
	Status        *string   `json:"status"`
	CodeEnabled   *bool     `json:"code_enabled"`
	CodeDatasetID *string   `json:"code_dataset_id"`
}

func (in projectInput) apply(p *domain.Project) {
	if in.Name != nil {
		p.Name = strings.TrimSpace(*in.Name)
	}
	if in.AutoCapture != nil && p.AutoCapture != *in.AutoCapture {
		p.AutoCapture = *in.AutoCapture
		p.AutoCaptureVersion++
		p.AutoCaptureUpdatedAt = domain.NowUTCMicro()
	}
	if in.Aliases != nil {
		p.Aliases = *in.Aliases
	}
	if in.Status != nil {
		p.Status = *in.Status
	}
	if in.CodeEnabled != nil {
		p.CodeEnabled = *in.CodeEnabled
	}
	if in.CodeDatasetID != nil {
		p.CodeDatasetID = strings.TrimSpace(*in.CodeDatasetID)
	}
}

func (s *Server) validProject(ctx context.Context, p domain.Project) error {
	if p.Name == "" || strings.ContainsAny(p.ID, "/\\") || len(p.Name) > 512 {
		return domain.ErrInvalidArgument
	}
	for _, alias := range p.Aliases {
		if !filepath.IsAbs(alias) || strings.ContainsRune(alias, 0) {
			return domain.ErrInvalidArgument
		}
	}
	if p.DatasetID != "" && p.DatasetID == p.CodeDatasetID {
		return domain.ErrConflict
	}
	for _, ds := range []string{p.DatasetID, p.CodeDatasetID} {
		if ds == "" {
			continue
		}
		var existing int
		err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects
			WHERE id<>? AND (dataset_id=? OR code_dataset_id=?) UNION ALL
			SELECT 1 FROM common_config WHERE dataset_id=?)`, p.ID, ds, ds, ds).Scan(&existing)
		if err != nil {
			return err
		}
		if existing != 0 {
			return domain.ErrConflict
		}
	}
	return nil
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var in projectInput
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = strings.TrimSpace(in.ProjectID)
	}
	if id == "" {
		id = rand.Text()
	}
	p := domain.Project{ID: id, DatasetID: strings.TrimSpace(in.DatasetID), DatasetName: in.DatasetName, AutoCapture: true}
	if p.DatasetID == "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			writeDomainError(w, err)
			return
		}
		id[6], id[8] = (id[6]&0x0f)|0x40, (id[8]&0x3f)|0x80
		p.DatasetID = fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	}
	in.apply(&p)
	if p.DatasetName == "" {
		p.DatasetName = p.Name
	}
	p.AutoCaptureVersion = 1
	ctx := r.Context()
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if _, err := s.cfg.Store.GetProject(ctx, id); !errors.Is(err, domain.ErrNotFound) {
		if err == nil {
			err = domain.ErrConflict
		}
		writeDomainError(w, err)
		return
	}
	if err := s.validProject(ctx, p); err != nil {
		writeDomainError(w, err)
		return
	}
	if err := s.cfg.Store.UpsertProject(ctx, p); err != nil {
		writeDomainError(w, err)
		return
	}
	s.getCreatedProject(w, r, id)
}

func (s *Server) getCreatedProject(w http.ResponseWriter, r *http.Request, id string) {
	p, err := s.cfg.Store.GetProject(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	s.respondProject(w, r, p, 201)
}

func (s *Server) changeProject(w http.ResponseWriter, r *http.Request, change func(*domain.Project)) {
	ctx := r.Context()
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	p, err := s.cfg.Store.GetProject(ctx, r.PathValue("id"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	change(&p)
	if err := s.validProject(ctx, p); err != nil {
		writeDomainError(w, err)
		return
	}
	if err := s.cfg.Store.UpsertProject(ctx, p); err != nil {
		writeDomainError(w, err)
		return
	}
	s.getProject(w, r)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	var in projectInput
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	if r.Method == http.MethodPut {
		if in.Name == nil {
			writeDomainError(w, domain.ErrInvalidArgument)
			return
		}
		in = projectInput{Name: in.Name}
	}
	s.changeProject(w, r, in.apply)
}

func (s *Server) toggleCapture(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AutoCapture *bool `json:"auto_capture"`
		Enabled     *bool `json:"enabled"`
	}
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	enabled := cmp.Or(in.AutoCapture, in.Enabled)
	if enabled == nil || in.AutoCapture != nil && in.Enabled != nil && *in.AutoCapture != *in.Enabled {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	p, err := s.cfg.Store.SetProjectAutoCapture(r.Context(), r.PathValue("id"), *enabled)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	s.respondProject(w, r, p, 200)
}

func (s *Server) addAlias(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path      string `json:"path"`
		Alias     string `json:"alias"`
		AliasPath string `json:"alias_path"`
		Workspace string `json:"workspace"`
	}
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	alias := cmp.Or(in.Path, in.AliasPath, in.Alias, in.Workspace)
	s.changeProject(w, r, func(p *domain.Project) { p.Aliases = append(p.Aliases, alias) })
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if err := s.cfg.Store.DeleteProject(r.Context(), r.PathValue("id")); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) activeAgent(ctx context.Context, id string) error {
	if id == "" {
		return domain.ErrInvalidArgument
	}
	a, err := s.cfg.Store.GetAgent(ctx, id)
	if err == nil && (a.Archived || strings.TrimSpace(a.RetiredAt) != "") {
		return domain.ErrForbidden
	}
	return err
}

func (s *Server) projectACL(w http.ResponseWriter, r *http.Request) {
	var in aclBody
	if r.Method != http.MethodDelete && !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	ctx := r.Context()
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	id := r.PathValue("id")
	if _, err := s.cfg.Store.GetProject(ctx, id); err != nil {
		writeDomainError(w, err)
		return
	}
	agentID := r.PathValue("agent_id")
	if r.Method == http.MethodDelete {
		if err := s.cfg.Store.DeleteACLOverride(ctx, "project", id, agentID); err != nil {
			writeDomainError(w, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	if agentID != "" {
		in.AgentID = agentID
	}
	if err := s.activeAgent(ctx, in.AgentID); err != nil {
		writeDomainError(w, err)
		return
	}
	if in.Access == nil {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	if err := s.cfg.Store.SetACLOverride(ctx, domain.ACLOverride{TargetKind: "project", TargetID: id, AgentID: in.AgentID,
		Access: in.Access, Capture: in.Capture, AutoProcess: in.AutoProcess}); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, in)
}

func (s *Server) retryProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.cfg.Store.GetProject(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	ids, err := s.inbox.RetryFailed(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"project_id": id, "retried_count": len(ids), "item_ids": ids})
}

type datasetRef struct {
	ID        string `json:"id"`
	ProjectID string `json:"project"`
	Kind      string `json:"kind"`
	ScopeHash string `json:"scope"`
}

func datasetRefs(ctx context.Context, db sqlRead, identity string) ([]datasetRef, error) {
	return readRows(ctx, db, `SELECT dataset_id,id,'project' FROM projects WHERE dataset_id<>''
		UNION ALL SELECT code_dataset_id,id,'code' FROM projects WHERE code_enabled=1 AND code_dataset_id<>''
		UNION ALL SELECT dataset_id,id,'common' FROM common_config WHERE dataset_id<>'' ORDER BY 1,2,3`, nil, func(row rowScanner) (datasetRef, error) {
		var ref datasetRef
		err := row.Scan(&ref.ID, &ref.ProjectID, &ref.Kind)
		if ref.Kind == "project" && identity != "" {
			ref.ScopeHash = domain.KnowledgeReadScopeHash(identity, ref.ProjectID, ref.ID)
		}
		return ref, err
	})
}

func (s *Server) managedDataset(ctx context.Context, ds string) error {
	if ds == "" {
		return domain.ErrForbidden
	}
	var found bool
	err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects
		WHERE dataset_id=?1 OR (code_enabled=1 AND code_dataset_id=?1)
		UNION ALL SELECT 1 FROM common_config WHERE dataset_id=?1)`, ds).Scan(&found)
	if err != nil {
		return err
	}
	if !found {
		return domain.ErrForbidden
	}
	return nil
}

func snapshot(ctx context.Context, store *domain.Store, read func(*sql.Tx) error) error {
	tx, err := store.DB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := read(tx); err != nil {
		return err
	}
	return tx.Commit()
}
