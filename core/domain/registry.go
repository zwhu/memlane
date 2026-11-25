package domain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type AccessLevel string

const (
	AccessNone     AccessLevel = "none"
	AccessRead     AccessLevel = "read"
	AccessMaintain AccessLevel = "maintain"
)

func ValidateAccessLevel(access AccessLevel) error {
	switch access {
	case AccessNone, AccessRead, AccessMaintain:
		return nil
	}
	return fmt.Errorf("invalid access level: %w", ErrInvalidArgument)
}

func IsProtectedProject(name, datasetName string) bool {
	return name == "ml-user-profiles" || datasetName == "ml-user-profiles"
}

type Project struct {
	ID, Name, DatasetID, DatasetName, CodeDatasetID string
	CodeEnabled, CodeConfigured, AutoCapture        bool
	AutoCaptureVersion                              int
	AutoCaptureUpdatedAt, Source, Status            string
	DesiredRevision                                 int
	OperationID                                     string
	ImportACL                                       bool
	ToolGraphJSON, AppliedJSON, ExtraJSON           string
	Aliases                                         []string
}

type Agent struct {
	ID, Kind, Name, PrincipalID, KeyFile, ControlTokenFile                                   string
	DefaultAccess                                                                            AccessLevel
	HostProfile, HostProfileFingerprint, CapabilitiesJSON, EntrypointsJSON, InstallationJSON string
	Archived                                                                                 bool
	RetiredAt, ExtraJSON                                                                     string
}

type ACLOverride struct {
	TargetKind, TargetID, AgentID string
	Access                        *AccessLevel
	Capture, AutoProcess          *bool
}

type RegistryDefaults struct {
	Access                                                          AccessLevel
	Capture, AutoProcess                                            bool
	Revision                                                        int
	ServiceNamespace                                                string
	RuntimeCatalogImported                                          bool
	ReservedDatasetsJSON, ToolGraphJSON, HostTargetsJSON, ExtraJSON string
}

type CommonConfig struct {
	ID, Name, Kind, DatasetID, DatasetName string
	CodeEnabled                            bool
	Status                                 string
	DesiredRevision                        int
	OperationID, AppliedJSON, ExtraJSON    string
}

type SessionBinding struct {
	SessionKey, ProjectID string
	UpdatedAt             float64
}

type EffectiveACL struct {
	Access               AccessLevel
	Capture, AutoProcess bool
	Permissions          []string
	CanCapture           bool
	CaptureScope         string
	ProjectAutoCapture   bool
	AutoCaptureVersion   int
	AutoCaptureUpdatedAt string
	CanAutoProcess       bool
}

func (s *Store) UpsertProject(ctx context.Context, p Project) error {
	if err := requireIDs(p.ID, p.Name); err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		inserted, err := tx.ExecContext(ctx, `INSERT INTO projects(id,name) VALUES(?,?) ON CONFLICT(id) DO NOTHING`, p.ID, p.Name)
		if err != nil {
			return err
		}
		if err := execSQL(ctx, tx, `UPDATE projects SET name=?,dataset_id=?,dataset_name=?,code_dataset_id=?,code_enabled=?,
			code_configured=?,auto_capture=?,auto_capture_version=?,auto_capture_updated_at=?,source=?,status=?,desired_revision=?,
			operation_id=?,import_acl=?,tool_graph_json=?,applied_json=?,extra_json=? WHERE id=?`, p.Name, p.DatasetID, p.DatasetName, p.CodeDatasetID,
			boolToInt(p.CodeEnabled), boolToInt(p.CodeConfigured), boolToInt(p.AutoCapture), max(1, p.AutoCaptureVersion), p.AutoCaptureUpdatedAt,
			p.Source, p.Status, p.DesiredRevision, p.OperationID, boolToInt(p.ImportACL), defaultText(p.ToolGraphJSON, "{}"),
			defaultText(p.AppliedJSON, "{}"), defaultText(p.ExtraJSON, "{}"), p.ID); err != nil {
			return err
		}
		if err := execSQL(ctx, tx, `DELETE FROM project_aliases WHERE project_id=?`, p.ID); err != nil {
			return err
		}
		for _, alias := range p.Aliases {
			if !filepath.IsAbs(alias) {
				return fmt.Errorf("project alias must be absolute: %w", ErrInvalidArgument)
			}
			if err := execSQL(ctx, tx, `INSERT INTO project_aliases VALUES(?,?) ON CONFLICT DO NOTHING`, p.ID, filepath.Clean(alias)); err != nil {
				return err
			}
		}
		n, err := inserted.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		return execSQL(ctx, tx, `INSERT INTO project_acl_overrides(target_kind,target_id,agent_id,access)
			SELECT 'project',?,id,'none' FROM agents WHERE archived=0 AND retired_at='' AND (? OR default_access='none')
			ON CONFLICT DO NOTHING`, p.ID, IsProtectedProject(p.Name, p.DatasetName))
	})
}

func projectWithAliases(ctx context.Context, db sqlReader, id string) (Project, error) {
	p, err := scanProject(db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE id=?`, id))
	if err != nil {
		return p, err
	}
	p.Aliases, err = queryList(ctx, db, func(row scanner) (string, error) {
		var alias string
		err := row.Scan(&alias)
		return alias, err
	}, `SELECT alias_path FROM project_aliases WHERE project_id=? ORDER BY alias_path`, id)
	return p, err
}

func (s *Store) GetProject(ctx context.Context, id string) (Project, error) {
	var p Project
	err := s.readTx(ctx, func(tx *sql.Tx) error {
		var err error
		p, err = projectWithAliases(ctx, tx, id)
		return err
	})
	return p, err
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	var projects []Project
	err := s.readTx(ctx, func(tx *sql.Tx) error {
		var err error
		projects, err = queryList(ctx, tx, scanProject, `SELECT `+projectColumns+` FROM projects ORDER BY id`)
		if err != nil {
			return err
		}
		aliases, err := tx.QueryContext(ctx, `SELECT project_id,alias_path FROM project_aliases ORDER BY project_id,alias_path`)
		if err != nil {
			return err
		}
		defer aliases.Close()
		byID := make(map[string]*Project, len(projects))
		for i := range projects {
			p := &projects[i]
			p.Aliases = []string{}
			byID[p.ID] = p
		}
		for aliases.Next() {
			var id, alias string
			if err := aliases.Scan(&id, &alias); err != nil {
				return err
			}
			if p := byID[id]; p != nil {
				p.Aliases = append(p.Aliases, alias)
			}
		}
		return aliases.Err()
	})
	return projects, err
}

func (s *Store) DeleteProject(ctx context.Context, id string) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := requireAffected(tx.ExecContext(ctx, `DELETE FROM projects WHERE id=?`, id)); err != nil {
			return err
		}
		if err := execSQL(ctx, tx, `DELETE FROM project_acl_overrides WHERE target_kind='project' AND target_id=?`, id); err != nil {
			return err
		}
		return execSQL(ctx, tx, `DELETE FROM session_bindings WHERE project_id=?`, id)
	})
}

func (s *Store) MatchProjectByPath(ctx context.Context, rawPath string) (Project, error) {
	if !filepath.IsAbs(rawPath) {
		return Project{}, ErrNotFound
	}
	var p Project
	err := s.readTx(ctx, func(tx *sql.Tx) error {
		for path := filepath.Clean(rawPath); ; path = filepath.Dir(path) {
			var id string
			err := tx.QueryRowContext(ctx, `SELECT project_id FROM project_aliases WHERE alias_path=? ORDER BY project_id LIMIT 1`, path).Scan(&id)
			if err == nil {
				p, err = projectWithAliases(ctx, tx, id)
				return err
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if filepath.Dir(path) == path {
				return ErrNotFound
			}
		}
	})
	return p, err
}

func (s *Store) SetProjectAutoCapture(ctx context.Context, projectID string, enabled bool) (Project, error) {
	var p Project
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := execSQL(ctx, tx, `UPDATE projects SET auto_capture=?,auto_capture_version=auto_capture_version+1,
			auto_capture_updated_at=? WHERE id=? AND auto_capture<>?`, boolToInt(enabled), NowUTCMicro(), projectID, boolToInt(enabled)); err != nil {
			return err
		}
		var err error
		p, err = projectWithAliases(ctx, tx, projectID)
		return err
	})
	return p, err
}

func (s *Store) ConnectAgent(ctx context.Context, a Agent) error {
	a.DefaultAccess = AccessNone
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := writeAgent(ctx, tx, a, false); err != nil {
			return err
		}
		return execSQL(ctx, tx, `INSERT INTO project_acl_overrides(target_kind,target_id,agent_id,access)
			SELECT 'project',id,?,'none' FROM projects UNION ALL SELECT 'common',id,?,'none' FROM common_config WHERE singleton=1
			ON CONFLICT DO NOTHING`, a.ID, a.ID)
	})
}

func (s *Store) UpsertAgent(ctx context.Context, a Agent) error {
	return writeAgent(ctx, s.db, a, true)
}

func writeAgent(ctx context.Context, db sqlExecer, a Agent, upsert bool) error {
	if err := requireIDs(a.ID, a.Name); err != nil {
		return err
	}
	if a.DefaultAccess == "" {
		a.DefaultAccess = AccessNone
	}
	if err := ValidateAccessLevel(a.DefaultAccess); err != nil {
		return err
	}
	query := `INSERT INTO agents (` + agentColumns + `) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	if upsert {
		query += ` ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,name=excluded.name,principal_id=excluded.principal_id,
			key_file=excluded.key_file,control_token_file=excluded.control_token_file,default_access=excluded.default_access,
			host_profile=excluded.host_profile,host_profile_fingerprint=excluded.host_profile_fingerprint,
			capabilities_json=excluded.capabilities_json,entrypoints_json=excluded.entrypoints_json,installation_json=excluded.installation_json,
			archived=excluded.archived,retired_at=excluded.retired_at,extra_json=excluded.extra_json`
	}
	return insertError(execSQL(ctx, db, query, a.ID, a.Kind, a.Name, a.PrincipalID, a.KeyFile, a.ControlTokenFile, a.DefaultAccess,
		a.HostProfile, a.HostProfileFingerprint, defaultText(a.CapabilitiesJSON, "{}"), defaultText(a.EntrypointsJSON, "[]"),
		defaultText(a.InstallationJSON, "{}"), boolToInt(a.Archived), a.RetiredAt, defaultText(a.ExtraJSON, "{}")))
}

func (s *Store) GetAgent(ctx context.Context, id string) (Agent, error) {
	return scanAgent(s.db.QueryRowContext(ctx, `SELECT `+agentColumns+` FROM agents WHERE id=?`, id))
}

func (s *Store) ListAgents(ctx context.Context, includeArchived bool) ([]Agent, error) {
	return queryList(ctx, s.db, scanAgent, `SELECT `+agentColumns+` FROM agents WHERE ? OR archived=0 ORDER BY id`, includeArchived)
}

const aclColumns = "target_kind,target_id,agent_id,access,capture,auto_process"

func scanACL(row scanner) (ACLOverride, error) {
	var a ACLOverride
	err := row.Scan(&a.TargetKind, &a.TargetID, &a.AgentID, &a.Access, &a.Capture, &a.AutoProcess)
	return a, rowError(err)
}

func (s *Store) SetACLOverride(ctx context.Context, a ACLOverride) error {
	if a.TargetKind != "project" && a.TargetKind != "common" {
		return ErrInvalidArgument
	}
	if err := requireIDs(a.TargetID, a.AgentID); err != nil {
		return err
	}
	if a.Access != nil {
		if err := ValidateAccessLevel(*a.Access); err != nil {
			return err
		}
	}
	return execSQL(ctx, s.db, `INSERT INTO project_acl_overrides (`+aclColumns+`) VALUES(?,?,?,?,?,?)
		ON CONFLICT(target_kind,target_id,agent_id) DO UPDATE SET access=excluded.access,capture=excluded.capture,
		auto_process=excluded.auto_process`, a.TargetKind, a.TargetID, a.AgentID, a.Access, a.Capture, a.AutoProcess)
}

func getACL(ctx context.Context, db sqlReader, kind, id, agentID string) (ACLOverride, error) {
	return scanACL(db.QueryRowContext(ctx, `SELECT `+aclColumns+` FROM project_acl_overrides WHERE target_kind=? AND target_id=? AND agent_id=?`, kind, id, agentID))
}

func (s *Store) GetACLOverride(ctx context.Context, kind, id, agentID string) (ACLOverride, error) {
	return getACL(ctx, s.db, kind, id, agentID)
}

func (s *Store) DeleteACLOverride(ctx context.Context, kind, id, agentID string) error {
	return execSQL(ctx, s.db, `DELETE FROM project_acl_overrides WHERE target_kind=? AND target_id=? AND agent_id=?`, kind, id, agentID)
}

func (s *Store) ListACLOverrides(ctx context.Context, kind, id string) ([]ACLOverride, error) {
	return queryList(ctx, s.db, scanACL, `SELECT `+aclColumns+` FROM project_acl_overrides WHERE target_kind=? AND target_id=? ORDER BY agent_id`, kind, id)
}

func (s *Store) GetRegistryDefaults(ctx context.Context) (RegistryDefaults, error) {
	return scanRegistryDefaults(s.db.QueryRowContext(ctx, `SELECT `+registryDefaultsColumns+` FROM defaults WHERE singleton=1`))
}

func (s *Store) GetCommonConfig(ctx context.Context) (CommonConfig, error) {
	return scanCommonConfig(s.db.QueryRowContext(ctx, `SELECT `+commonConfigColumns+` FROM common_config WHERE singleton=1`))
}

func (s *Store) EvaluateProjectACL(ctx context.Context, projectID, agentID string) (EffectiveACL, error) {
	var result EffectiveACL
	err := s.readTx(ctx, func(tx *sql.Tx) error {
		p, err := scanProject(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE id=?`, projectID))
		if err != nil {
			return err
		}
		result, err = evaluateACL(ctx, tx, "project", projectID, agentID, p)
		return err
	})
	return result, err
}

func (s *Store) EvaluateCommonACL(ctx context.Context, agentID string) (EffectiveACL, error) {
	var result EffectiveACL
	err := s.readTx(ctx, func(tx *sql.Tx) error {
		var id string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM common_config WHERE singleton=1`).Scan(&id); err != nil {
			return rowError(err)
		}
		var err error
		result, err = evaluateACL(ctx, tx, "common", id, agentID, Project{AutoCaptureVersion: 1})
		return err
	})
	return result, err
}

func evaluateACL(ctx context.Context, tx *sql.Tx, kind, id, agentID string, p Project) (EffectiveACL, error) {
	d, err := scanRegistryDefaults(tx.QueryRowContext(ctx, `SELECT `+registryDefaultsColumns+` FROM defaults WHERE singleton=1`))
	if err != nil {
		return EffectiveACL{}, err
	}
	if IsProtectedProject(p.Name, p.DatasetName) {
		d.Access, d.Capture, d.AutoProcess = AccessNone, false, false
	}
	var agentDefault AccessLevel
	var archived bool
	var retiredAt string
	err = tx.QueryRowContext(ctx, `SELECT default_access, archived, retired_at FROM agents WHERE id=?`, agentID).
		Scan(&agentDefault, &archived, &retiredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return EffectiveACL{}, fmt.Errorf("agent not registered: %w", ErrForbidden)
	}
	if err != nil {
		return EffectiveACL{}, err
	}
	if archived || strings.TrimSpace(retiredAt) != "" {
		return EffectiveACL{}, fmt.Errorf("agent archived or retired: %w", ErrForbidden)
	}
	o, err := getACL(ctx, tx, kind, id, agentID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return EffectiveACL{}, err
	}
	if o.Access != nil {
		d.Access = *o.Access
	} else if agentDefault == AccessNone {
		d.Access = AccessNone
	}
	if kind == "common" {
		acl := EffectiveACL{Access: AccessNone, Permissions: []string{}, CaptureScope: "project", AutoCaptureVersion: p.AutoCaptureVersion}
		if d.Access != AccessNone {
			acl.Access = AccessRead
			acl.Permissions = []string{"read"}
		}
		return acl, nil
	}
	if o.Capture != nil {
		d.Capture = *o.Capture
	}
	if o.AutoProcess != nil {
		d.AutoProcess = *o.AutoProcess
	}
	permissions := []string{}
	switch d.Access {
	case AccessRead:
		permissions = []string{"read"}
	case AccessMaintain:
		permissions = []string{"delete", "read", "write"}
	}
	scope := "ok"
	switch {
	case !p.AutoCapture:
		scope = "project"
	case !d.Capture && o.Capture != nil:
		scope = "agent"
	case !d.Capture:
		scope = "default"
	}
	return EffectiveACL{Access: d.Access, Capture: d.Capture, AutoProcess: d.AutoProcess, Permissions: permissions,
		CanCapture: p.AutoCapture && d.Capture && d.Access == AccessMaintain, CaptureScope: scope, ProjectAutoCapture: p.AutoCapture,
		AutoCaptureVersion: p.AutoCaptureVersion, AutoCaptureUpdatedAt: p.AutoCaptureUpdatedAt, CanAutoProcess: d.AutoProcess && d.Access == AccessMaintain}, nil
}

func scanSessionBinding(row scanner) (SessionBinding, error) {
	var b SessionBinding
	err := row.Scan(&b.SessionKey, &b.ProjectID, &b.UpdatedAt)
	return b, rowError(err)
}
func (s *Store) UpsertSessionBinding(ctx context.Context, sessionKey, projectID string, updatedAt float64) error {
	if err := requireIDs(sessionKey, projectID); err != nil {
		return err
	}
	return execSQL(ctx, s.db, `INSERT INTO session_bindings VALUES(?,?,?) ON CONFLICT(session_key) DO UPDATE SET
		project_id=excluded.project_id,updated_at=excluded.updated_at`, sessionKey, projectID, updatedAt)
}
func (s *Store) GetSessionBinding(ctx context.Context, sessionKey string) (SessionBinding, error) {
	return scanSessionBinding(s.db.QueryRowContext(ctx, `SELECT session_key,project_id,updated_at FROM session_bindings WHERE session_key=?`, sessionKey))
}
func (s *Store) ListProjectSessions(ctx context.Context, projectID string) ([]SessionBinding, error) {
	return queryList(ctx, s.db, scanSessionBinding, `SELECT session_key,project_id,updated_at FROM session_bindings WHERE project_id=? ORDER BY session_key`, projectID)
}

const projectColumns = `id, name, dataset_id, dataset_name, code_dataset_id, code_enabled, code_configured,
		auto_capture, auto_capture_version, auto_capture_updated_at, source, status, desired_revision, operation_id,
		import_acl, tool_graph_json, applied_json, extra_json`

func scanProject(row scanner) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.Name, &p.DatasetID, &p.DatasetName, &p.CodeDatasetID, &p.CodeEnabled, &p.CodeConfigured,
		&p.AutoCapture, &p.AutoCaptureVersion, &p.AutoCaptureUpdatedAt, &p.Source, &p.Status, &p.DesiredRevision, &p.OperationID,
		&p.ImportACL, &p.ToolGraphJSON, &p.AppliedJSON, &p.ExtraJSON)
	return p, rowError(err)
}

const agentColumns = `id, kind, name, principal_id, key_file, control_token_file, default_access,
		host_profile, host_profile_fingerprint, capabilities_json, entrypoints_json, installation_json, archived, retired_at,
		extra_json`

func scanAgent(row scanner) (Agent, error) {
	var p Agent
	err := row.Scan(&p.ID, &p.Kind, &p.Name, &p.PrincipalID, &p.KeyFile, &p.ControlTokenFile, &p.DefaultAccess,
		&p.HostProfile, &p.HostProfileFingerprint, &p.CapabilitiesJSON, &p.EntrypointsJSON, &p.InstallationJSON, &p.Archived, &p.RetiredAt,
		&p.ExtraJSON)
	return p, rowError(err)
}

const registryDefaultsColumns = `access, capture, auto_process, revision, service_namespace, runtime_catalog_imported, reserved_datasets_json,
		tool_graph_json, host_targets_json, extra_json`

func scanRegistryDefaults(row scanner) (RegistryDefaults, error) {
	var p RegistryDefaults
	err := row.Scan(&p.Access, &p.Capture, &p.AutoProcess, &p.Revision, &p.ServiceNamespace, &p.RuntimeCatalogImported, &p.ReservedDatasetsJSON,
		&p.ToolGraphJSON, &p.HostTargetsJSON, &p.ExtraJSON)
	return p, rowError(err)
}

func (s *Store) UpsertRegistryDefaults(ctx context.Context, p RegistryDefaults) error {
	if err := ValidateAccessLevel(p.Access); err != nil {
		return err
	}
	return execSQL(ctx, s.db, `INSERT INTO defaults (`+`singleton,`+registryDefaultsColumns+`)
		VALUES(1,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(singleton) DO UPDATE SET
		access=excluded.access, capture=excluded.capture, auto_process=excluded.auto_process,
		revision=excluded.revision, service_namespace=excluded.service_namespace, runtime_catalog_imported=excluded.runtime_catalog_imported,
		reserved_datasets_json=excluded.reserved_datasets_json, tool_graph_json=excluded.tool_graph_json, host_targets_json=excluded.host_targets_json,
		extra_json=excluded.extra_json`,
		p.Access, boolToInt(p.Capture), boolToInt(p.AutoProcess), p.Revision, p.ServiceNamespace, boolToInt(p.RuntimeCatalogImported), defaultText(p.ReservedDatasetsJSON, "[]"),
		defaultText(p.ToolGraphJSON, "{}"), defaultText(p.HostTargetsJSON, "{}"), defaultText(p.ExtraJSON, "{}"))
}

const commonConfigColumns = `id, name, kind, dataset_id, dataset_name, code_enabled, status,
		desired_revision, operation_id, applied_json, extra_json`

func scanCommonConfig(row scanner) (CommonConfig, error) {
	var p CommonConfig
	err := row.Scan(&p.ID, &p.Name, &p.Kind, &p.DatasetID, &p.DatasetName, &p.CodeEnabled, &p.Status,
		&p.DesiredRevision, &p.OperationID, &p.AppliedJSON, &p.ExtraJSON)
	return p, rowError(err)
}

func (s *Store) UpsertCommonConfig(ctx context.Context, p CommonConfig) error {
	if err := requireIDs(p.ID, p.Name); err != nil {
		return err
	}
	return execSQL(ctx, s.db, `INSERT INTO common_config (`+`singleton,`+commonConfigColumns+`)
		VALUES(1,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(singleton) DO UPDATE SET
		id=excluded.id, name=excluded.name, kind=excluded.kind,
		dataset_id=excluded.dataset_id, dataset_name=excluded.dataset_name, code_enabled=excluded.code_enabled,
		status=excluded.status, desired_revision=excluded.desired_revision, operation_id=excluded.operation_id,
		applied_json=excluded.applied_json, extra_json=excluded.extra_json`,
		p.ID, p.Name, defaultText(p.Kind, "common"), p.DatasetID, p.DatasetName, boolToInt(p.CodeEnabled), p.Status,
		p.DesiredRevision, p.OperationID, defaultText(p.AppliedJSON, "{}"), defaultText(p.ExtraJSON, "{}"))
}
