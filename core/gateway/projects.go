package gateway

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zwhu/memlane/core/domain"
)

func (s *Server) RegistryLocker() sync.Locker { return &s.registryMu }

type projectSummary struct {
	ProjectID          string `json:"project_id"`
	Name               string `json:"name"`
	DatasetID          string `json:"dataset_id"`
	Protected          bool   `json:"protected,omitempty"`
	AutoCapture        bool   `json:"auto_capture"`
	AutoCaptureVersion int    `json:"auto_capture_version"`
	KnowledgeCount     int    `json:"knowledge_count"`
	RawDocCount        int    `json:"raw_doc_count"`
	SessionCount       int    `json:"session_count"`
	UnreadCount        int    `json:"unread_count"`
	UnreadStateRev     int64  `json:"unread_state_rev"`
	CatalogVersion     string `json:"catalog_version"`
	InboxPending       int    `json:"inbox_pending"`
	InboxProcessing    int    `json:"inbox_processing"`
	InboxFailed        int    `json:"inbox_failed"`
	LastError          string `json:"last_error"`

	AutoCaptureUpdatedAt string `json:"auto_capture_updated_at"`
	AutoCaptureReason    string `json:"auto_capture_reason"`
	AutoCaptureActor     string `json:"auto_capture_actor"`
	LastErrorAt          string `json:"last_error_at"`
	FilteredCount        int    `json:"filtered_count"`
}

type aclBody struct {
	AgentID     string              `json:"agent_id"`
	Access      *domain.AccessLevel `json:"access,omitempty"`
	Capture     *bool               `json:"capture,omitempty"`
	AutoProcess *bool               `json:"auto_process,omitempty"`
}

type projectView struct {
	projectSummary
	ID              string                    `json:"id"`
	DatasetName     string                    `json:"dataset_name"`
	CodeDatasetID   string                    `json:"code_dataset_id"`
	CodeEnabled     bool                      `json:"code_enabled"`
	Status          string                    `json:"status"`
	Aliases         []string                  `json:"aliases"`
	Overrides       map[string]aclBody        `json:"overrides"`
	DesiredRevision int64                     `json:"desired_revision"`
	Applied         map[string]projectApplied `json:"applied"`
	CodeConfigured  bool                      `json:"code_configured"`
	Inbox           InboxSummary              `json:"inbox"`

	RecentFiltered []CaptureFilterEventView `json:"recent_filtered,omitempty"`
}

type projectApplied struct {
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
}

func (s *Server) populateProjectAccess(ctx context.Context, view *projectView, kind, appliedJSON string) error {
	view.Overrides = map[string]aclBody{}
	overrides, err := s.cfg.Store.ListACLOverrides(ctx, kind, view.ID)
	if err != nil {
		return err
	}
	for _, a := range overrides {
		view.Overrides[a.AgentID] = aclBody{a.AgentID, a.Access, a.Capture, a.AutoProcess}
	}
	view.Applied = map[string]projectApplied{}
	if view.DatasetID == "" {
		return nil
	}
	if view.Status == "" {
		view.Status = "verified"
	}
	var previous map[string]projectApplied
	if view.Status != "verified" && appliedJSON != "" {
		if err := json.Unmarshal([]byte(appliedJSON), &previous); err != nil {
			return err
		}
	}
	agents, err := s.activeAgents(ctx)
	if err != nil {
		return err
	}
	for _, a := range agents {
		if view.Status == "verified" {
			view.Applied[a.ID] = projectApplied{view.Status, view.DesiredRevision}
		} else if value, ok := previous[a.ID]; ok {
			view.Applied[a.ID] = value
		} else {
			view.Applied[a.ID] = projectApplied{view.Status, view.DesiredRevision}
		}
	}
	return nil
}

func (s *Server) projectView(ctx context.Context, p domain.Project) (projectView, error) {
	view := projectView{projectSummary: projectSummary{ProjectID: p.ID, Name: p.Name, DatasetID: p.DatasetID,
		Protected:   domain.IsProtectedProject(p.Name, p.DatasetName) || domain.IsProtectedProject(p.ID, p.DatasetID),
		AutoCapture: p.AutoCapture, AutoCaptureVersion: p.AutoCaptureVersion, AutoCaptureUpdatedAt: p.AutoCaptureUpdatedAt,
		AutoCaptureReason: p.AutoCaptureReason, AutoCaptureActor: p.AutoCaptureActor}, ID: p.ID, DatasetName: p.DatasetName,
		CodeDatasetID: p.CodeDatasetID, CodeEnabled: p.CodeEnabled, Status: p.Status, Aliases: append([]string{}, p.Aliases...),
		DesiredRevision: int64(p.DesiredRevision), CodeConfigured: p.CodeConfigured}
	err := catalogCounts(ctx, s.cfg.Store.DB(), p.DatasetID, &view.projectSummary)
	if err != nil {
		return view, err
	}
	view.Inbox, err = s.inbox.Summary(ctx, p.ID)
	if err != nil {
		return view, err
	}
	view.InboxPending, view.InboxProcessing, view.InboxFailed = view.Inbox.Pending, view.Inbox.Processing, view.Inbox.Failed
	view.FilteredCount, view.RecentFiltered = view.Inbox.FilteredCount, view.Inbox.RecentFiltered
	if len(view.Inbox.RecentFailures) > 0 {
		view.LastError = view.Inbox.RecentFailures[0].LastError
		view.LastErrorAt = view.Inbox.RecentFailures[0].LastErrorAt
	}
	err = s.populateProjectAccess(ctx, &view, "project", p.AppliedJSON)
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
	defaults, err := s.cfg.Store.GetRegistryDefaults(ctx)
	if err != nil {
		writeDomainError(w, err)
		return
	}
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
	agents, err := s.activeAgents(ctx)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	var common *projectView
	c, err := s.commonConfig(ctx)
	if err == nil {
		common = &projectView{projectSummary: projectSummary{ProjectID: c.ID, Name: c.Name, DatasetID: c.DatasetID},
			ID: c.ID, DatasetName: c.DatasetName, Status: c.Status, Aliases: []string{}, DesiredRevision: int64(c.DesiredRevision)}
		err = catalogCounts(ctx, s.cfg.Store.DB(), c.DatasetID, &common.projectSummary)
		if err == nil {
			err = s.populateProjectAccess(ctx, common, "common", c.AppliedJSON)
		}
	} else if errors.Is(err, domain.ErrNotFound) {
		err = nil
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"projects": views, "agents": agents, "common": common,
		"defaults": map[string]any{"access": defaults.Access, "capture": defaults.Capture, "auto_process": defaults.AutoProcess}, "revision": defaults.Revision})
}

func (s *Server) updateDefaults(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ExpectedRevision *int    `json:"expected_revision"`
		Fields           aclBody `json:"fields"`
	}
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if err := s.checkRegistryRevision(r.Context(), in.ExpectedRevision); err != nil {
		writeDomainError(w, err)
		return
	}
	// The Go gateway only supports explicit, per-agent grants.
	if in.Fields.Access != nil && *in.Fields.Access != domain.AccessNone ||
		in.Fields.Capture != nil && *in.Fields.Capture || in.Fields.AutoProcess != nil && *in.Fields.AutoProcess {
		writeDomainError(w, domain.ErrForbidden)
		return
	}
	s.listProjects(w, r)
}

func (s *Server) checkRegistryRevision(ctx context.Context, expected *int) error {
	if expected == nil {
		return nil
	}
	d, err := s.cfg.Store.GetRegistryDefaults(ctx)
	if err == nil && d.Revision != *expected {
		return domain.ErrConflict
	}
	return err
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
	Workspace     string    `json:"workspace"`
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
		p.AutoCaptureActor, p.AutoCaptureReason = "user", "user_disabled"
		if p.AutoCapture {
			p.AutoCaptureReason = "user_enabled"
		}
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
		p.CodeDatasetID = domain.CanonicalUUID(*in.CodeDatasetID)
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
	canonDS := domain.CanonicalUUID(p.DatasetID)
	canonCodeDS := domain.CanonicalUUID(p.CodeDatasetID)
	if canonDS != "" && canonDS == canonCodeDS {
		return domain.ErrConflict
	}
	if canonDS == "" && canonCodeDS == "" {
		return nil
	}
	existingRows, err := readRows(ctx, s.cfg.Store.DB(), `SELECT dataset_id FROM projects WHERE id<>? AND dataset_id<>''
		UNION ALL SELECT code_dataset_id FROM projects WHERE id<>? AND code_dataset_id<>''
		UNION ALL SELECT dataset_id FROM common_config WHERE dataset_id<>''`,
		[]any{p.ID, p.ID}, func(row rowScanner) (string, error) {
			var ds string
			err := row.Scan(&ds)
			return ds, err
		})
	if err != nil {
		return err
	}
	for _, rawExisting := range existingRows {
		existingCanon := domain.CanonicalUUID(rawExisting)
		if existingCanon == "" {
			continue
		}
		if (canonDS != "" && existingCanon == canonDS) || (canonCodeDS != "" && existingCanon == canonCodeDS) {
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
	reconnected := strings.TrimSpace(in.DatasetID) != ""
	p := domain.Project{ID: id, DatasetID: domain.CanonicalUUID(in.DatasetID), DatasetName: in.DatasetName, AutoCapture: true}
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
	if in.Workspace != "" {
		p.Aliases = append(p.Aliases, in.Workspace)
	}
	if p.DatasetName == "" {
		p.DatasetName = p.Name
	}
	p.AutoCaptureVersion = 1
	ctx := r.Context()
	s.registryMu.Lock()
	if _, err := s.cfg.Store.GetProject(ctx, id); !errors.Is(err, domain.ErrNotFound) {
		s.registryMu.Unlock()
		if err == nil {
			err = domain.ErrConflict
		}
		writeDomainError(w, err)
		return
	}
	if err := s.validProject(ctx, p); err != nil {
		s.registryMu.Unlock()
		writeDomainError(w, err)
		return
	}
	if err := s.cfg.Store.UpsertProject(ctx, p); err != nil {
		s.registryMu.Unlock()
		writeDomainError(w, err)
		return
	}
	if reconnected && p.DatasetID != "" {
		if err := s.cfg.Store.ClearRemovedSessionOwners(ctx, p.DatasetID); err != nil {
			log.Printf("gateway project reconnect clear removed session owners: %s", sanitizeError(err))
		}
	}
	s.registryMu.Unlock()
	if reconnected && s.cfg.Projector != nil {
		if _, err := s.SyncDatasetCatalog(ctx, p.DatasetID); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("gateway project reconnect catalog sync: %s", sanitizeError(err))
		}
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
		AutoCapture *bool  `json:"auto_capture"`
		Enabled     *bool  `json:"enabled"`
		Reason      string `json:"reason"`
		Actor       string `json:"actor"`
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
	p, err := s.cfg.Store.SetProjectAutoCaptureWithMeta(r.Context(), r.PathValue("id"), *enabled,
		sanitizeError(errors.New(in.Actor)), sanitizeError(errors.New(in.Reason)))
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
	id := r.PathValue("id")
	expectedDS := strings.TrimSpace(r.URL.Query().Get("dataset_id"))
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if expectedDS != "" {
		p, err := s.cfg.Store.GetProject(r.Context(), id)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		if domain.CanonicalUUID(p.DatasetID) != domain.CanonicalUUID(expectedDS) {
			writeDomainError(w, domain.ErrConflict)
			return
		}
	}
	if err := s.cfg.Store.DeleteProject(r.Context(), id); err != nil {
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
	var request struct {
		aclBody
		ExpectedRevision *int     `json:"expected_revision"`
		Fields           *aclBody `json:"fields"`
	}
	if r.Method != http.MethodDelete && !decodeJSON(w, r, s.cfg.MaxBodyBytes, &request) {
		return
	}
	in := request.aclBody
	ctx := r.Context()
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	id := r.PathValue("id")
	targetKind := "project"
	directory := r.Pattern == "PUT /v1/projects/{id}/overrides"
	if _, err := s.cfg.Store.GetProject(ctx, id); err != nil {
		if !directory || !errors.Is(err, domain.ErrNotFound) {
			writeDomainError(w, err)
			return
		}
		common, err := s.commonConfig(ctx)
		if err == nil && common.ID != id {
			err = domain.ErrNotFound
		}
		if err != nil {
			writeDomainError(w, err)
			return
		}
		targetKind = "common"
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
	if request.Fields != nil {
		if err := s.checkRegistryRevision(ctx, request.ExpectedRevision); err != nil {
			writeDomainError(w, err)
			return
		}
		previous, err := s.cfg.Store.GetACLOverride(ctx, targetKind, id, in.AgentID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			writeDomainError(w, err)
			return
		}
		in.Access = cmp.Or(request.Fields.Access, previous.Access)
		in.Capture = cmp.Or(request.Fields.Capture, previous.Capture)
		in.AutoProcess = cmp.Or(request.Fields.AutoProcess, previous.AutoProcess)
	}
	if request.Fields == nil && in.Access == nil {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	if targetKind == "common" && (in.Access == nil ||
		(*in.Access != domain.AccessNone && *in.Access != domain.AccessRead)) {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	if err := s.cfg.Store.UpdateACLOverride(ctx, domain.ACLOverride{TargetKind: targetKind, TargetID: id, AgentID: in.AgentID,
		Access: in.Access, Capture: in.Capture, AutoProcess: in.AutoProcess}, request.ExpectedRevision); err != nil {
		writeDomainError(w, err)
		return
	}
	if directory {
		s.listProjects(w, r)
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
