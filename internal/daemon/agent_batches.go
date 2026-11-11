package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Server) connectionBatch(ctx context.Context, id string) (records.AgentConnectionBatch, error) {
	b, err := s.store.AgentConnectionBatch(ctx, id)
	if id == "" && errors.Is(err, sql.ErrNoRows) {
		return b, nil
	}
	return b, err
}

func (s *Server) connectionBatchState(ctx context.Context, id, operation string) (records.AgentConnectionBatchState, error) {
	identity, err := s.store.AgentConnectionBatchIdentity(ctx)
	state := records.AgentConnectionBatchState{Identity: identity}
	if err != nil {
		return state, err
	}
	b, err := s.connectionBatch(ctx, id)
	if operation != "" {
		b, err = s.store.AgentConnectionBatchByOperation(ctx, operation)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
	}
	if err == nil && b.ID != "" {
		state.Batch = &b
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return state, err
}

func (s *Server) startConnectionBatch(ctx context.Context, d records.AgentConnectionBatchStart) (records.AgentConnectionBatch, error) {
	s.agentBatchMu.Lock()
	defer s.agentBatchMu.Unlock()
	if s.lifecycle.Err() != nil {
		return records.AgentConnectionBatch{}, s.lifecycle.Err()
	}
	b, created, err := s.store.CreateAgentConnectionBatch(ctx, d)
	if err == nil && created {
		s.launchConnectionBatchLocked(b.ID)
	}
	return b, err
}

func (s *Server) actionConnectionBatch(ctx context.Context, d records.AgentConnectionBatchAction) (records.AgentConnectionBatch, error) {
	s.agentBatchMu.Lock()
	defer s.agentBatchMu.Unlock()
	var b records.AgentConnectionBatch
	if _, err := uuid.Parse(d.OperationID); err != nil || (d.Kind != "verify" && d.Kind != "retry" && d.Kind != "cancel") {
		return b, &records.AccessError{Code: "invalid_request", Message: "explicit batch action and operation UUID required"}
	}
	if _, err := uuid.Parse(d.BatchID); err != nil {
		return b, &records.AccessError{Code: "invalid_request", Message: "explicit batch UUID required"}
	}
	if s.lifecycle.Err() != nil {
		return b, s.lifecycle.Err()
	}
	b, err := s.store.AgentConnectionBatch(ctx, d.BatchID)
	if err != nil {
		return b, err
	}
	identity, err := s.store.AgentConnectionBatchIdentity(ctx)
	if err != nil {
		return b, err
	}
	if d.Identity != identity || b.Identity != identity {
		return b, &records.AccessError{Code: "restore_reconnect_required", Message: "connection batch identity changed"}
	}
	if b.State == "retired" {
		return b, &records.AccessError{Code: "restore_reconnect_required", Message: "restored connection batch is retired"}
	}
	if d.Workspace != "" && d.Kind != "verify" {
		return b, &records.AccessError{Code: "invalid_request", Message: "workspace is only available for explicit verification"}
	}
	workspace, workspaceDigest, workspaceProject := "", "", ""
	if d.Workspace != "" {
		workspace, err = filepath.EvalSymlinks(d.Workspace)
		if err != nil || !filepath.IsAbs(d.Workspace) {
			return b, &records.AccessError{Code: "invalid_request", Message: "absolute registered verification workspace required"}
		}
		workspaceDigest, err = agents.CodexWorkspaceIdentity(workspace)
		if err != nil {
			return b, err
		}
		project, e := s.store.ProjectForWorkspace(ctx, workspace)
		if e != nil {
			return b, e
		}
		workspaceProject = project.ID
	}
	var verificationTargets []any
	if d.Kind == "verify" {
		for _, item := range b.Items {
			verificationTargets = append(verificationTargets, []any{item.ConnectionID, item.Generation})
		}
	}
	actionDigest := records.MergeDigest([]any{d.Identity, d.BatchID, d.Kind, workspace, workspaceDigest, workspaceProject, verificationTargets})
	if kind, seen := b.Actions[d.OperationID]; seen {
		priorDigest := b.ActionDigests[d.OperationID]
		if kind != d.Kind || priorDigest != "" && priorDigest != actionDigest || priorDigest == "" && workspace != "" {
			return b, &records.AccessError{Code: "conflict", Message: "batch action operation already used"}
		}
		return b, nil
	}
	if len(b.Actions) >= 64 {
		return b, &records.AccessError{Code: "invalid_request", Message: "batch action limit reached; start a new batch"}
	}
	if d.Kind != "cancel" {
		if s.agentBatchRuns[b.ID] != nil {
			return b, &records.AccessError{Code: "conflict", Message: "connection batch is already active"}
		}
		var other int
		if err = s.store.DB.QueryRowContext(ctx, `SELECT count(*) FROM agent_connection_batches WHERE id!=? AND state IN ('running','waiting','cancelling')`, b.ID).Scan(&other); err != nil {
			return b, err
		}
		if other != 0 {
			return b, &records.AccessError{Code: "conflict", Message: "another connection batch is active"}
		}
	}
	changed := false
	for index := range b.Items {
		i := &b.Items[index]
		switch d.Kind {
		case "verify":
			if i.State == "awaiting_reload" {
				if i.Target.Agent == "codex" {
					project, e := s.validateCodexVerificationWorkspace(ctx, *i, workspace, workspaceDigest)
					if e != nil {
						return b, e
					}
					i.VerificationWorkspace, i.VerificationProject, i.VerificationWorkspaceDigest = workspace, project, workspaceDigest
				}
				i.State = "verifying"
				i.Phase = "verify"
				i.Error = ""
				i.Attempt++
				changed = true
			}
		case "retry":
			if i.State == "failed" || i.State == "interrupted" || i.State == "cancelled" {
				i.State = "queued"
				if i.Phase == "verify" {
					i.State = "verifying"
				}
				i.Error = ""
				i.Attempt++
				changed = true
			}
		case "cancel":
			if i.State == "queued" || i.State == "awaiting_reload" {
				i.State = "cancelled"
				changed = true
			}
		}
	}
	if b.Actions == nil {
		b.Actions = map[string]string{}
	}
	b.Actions[d.OperationID] = d.Kind
	if b.ActionDigests == nil {
		b.ActionDigests = map[string]string{}
	}
	b.ActionDigests[d.OperationID] = actionDigest
	if d.Kind == "cancel" {
		if s.agentBatchRuns[b.ID] != nil {
			b.State = "cancelling"
		} else if changed {
			b.State = "cancelled"
		}
	} else if changed {
		b.State = "running"
	}
	b, err = s.store.SaveAgentConnectionBatch(ctx, b)
	if err != nil {
		return b, err
	}
	if d.Kind == "cancel" {
		if cancel := s.agentBatchRuns[b.ID]; cancel != nil {
			cancel()
		}
	} else if changed {
		s.launchConnectionBatchLocked(b.ID)
	}
	return b, nil
}

func (s *Server) launchConnectionBatchLocked(id string) {
	if s.agentBatchRuns == nil {
		s.agentBatchRuns = map[string]context.CancelFunc{}
	}
	ctx, cancel := context.WithCancel(s.lifecycle)
	s.agentBatchRuns[id] = cancel
	s.agentBatchWG.Add(1)
	go func() {
		defer s.agentBatchWG.Done()
		defer cancel()
		defer func() { s.agentBatchMu.Lock(); delete(s.agentBatchRuns, id); s.agentBatchMu.Unlock() }()
		s.runConnectionBatch(ctx, id)
	}()
}

func summarizeConnectionBatch(b records.AgentConnectionBatch) string {
	if b.State == "cancelling" {
		return "cancelled"
	}
	waiting, failed := false, false
	for _, i := range b.Items {
		switch i.State {
		case "queued", "installing", "verifying":
			return "running"
		case "awaiting_reload":
			waiting = true
		case "failed", "unsupported", "interrupted", "cancelled":
			failed = true
		}
	}
	if waiting {
		return "waiting"
	}
	if failed {
		return "partial_failed"
	}
	return "completed"
}

func (s *Server) runConnectionBatch(ctx context.Context, id string) {
	for {
		s.agentBatchMu.Lock()
		b, err := s.store.AgentConnectionBatch(context.Background(), id)
		if err != nil || b.State == "retired" {
			s.agentBatchMu.Unlock()
			return
		}
		position := -1
		for index := range b.Items {
			if b.Items[index].State == "queued" || b.Items[index].State == "verifying" {
				position = index
				break
			}
		}
		if position < 0 || ctx.Err() != nil {
			if ctx.Err() != nil {
				for index := range b.Items {
					if b.Items[index].State == "queued" || b.Items[index].State == "verifying" {
						b.Items[index].State = "interrupted"
						if b.State == "cancelling" {
							b.Items[index].State = "cancelled"
						}
					}
				}
				if b.State != "cancelling" {
					b.State = "interrupted"
				}
			}
			if b.State != "interrupted" {
				b.State = summarizeConnectionBatch(b)
			}
			_, err = s.store.SaveAgentConnectionBatch(context.Background(), b)
			s.agentBatchMu.Unlock()
			if err != nil && s.stop != nil {
				s.stop()
			}
			return
		}
		item := b.Items[position]
		verify := item.State == "verifying"
		if !verify {
			item.State = "installing"
			item.Phase = "install"
		}
		b.Items[position] = item
		_, err = s.store.SaveAgentConnectionBatch(context.Background(), b)
		s.agentBatchMu.Unlock()
		if err != nil {
			if s.stop != nil {
				s.stop()
			}
			return
		}
		if verify {
			item, err = s.verifyConnectionBatchItem(ctx, item)
		} else {
			item, err = s.installConnectionBatchItem(ctx, item)
		}
		s.agentBatchMu.Lock()
		b, readErr := s.store.AgentConnectionBatch(context.Background(), id)
		if readErr != nil || b.State == "retired" {
			s.agentBatchMu.Unlock()
			return
		}
		if err != nil {
			item.State = "failed"
			item.Error = agents.BatchSafeReason(err.Error())
			if ctx.Err() != nil {
				item.State = "interrupted"
				if b.State == "cancelling" {
					item.State = "cancelled"
				}
			}
		}
		b.Items[position] = item
		_, saveErr := s.store.SaveAgentConnectionBatch(context.Background(), b)
		s.agentBatchMu.Unlock()
		if saveErr != nil {
			if s.stop != nil {
				s.stop()
			}
			return
		}
	}
}

func (s *Server) installConnectionBatchItem(ctx context.Context, item records.AgentConnectionBatchItem) (records.AgentConnectionBatchItem, error) {
	if item.Target.Agent != "pi" && item.Target.Agent != "codex" {
		item.State = "unsupported"
		item.Error = "此 Agent 的电脑级安装尚待适配。"
		return item, nil
	}
	if s.agentBatchInstall != nil {
		return s.agentBatchInstall(ctx, item)
	}
	probe := agents.Probe(ctx, item.Target.Agent, item.Target.Path)
	if err := probe.ConnectionError(); err != nil {
		return item, err
	}
	if err := agents.ComputerConnectionError(probe); err != nil {
		item.State = "unsupported"
		item.Error = err.Error()
		return item, nil
	}
	s.agentAccess.Lock()
	defer s.agentAccess.Unlock()
	if err := ctx.Err(); err != nil {
		return item, err
	}
	connections, err := s.store.Connections(ctx)
	if err != nil {
		return item, err
	}
	for _, c := range connections {
		if c.Enabled && c.Agent == item.Target.Agent && c.ID != item.ConnectionID {
			if c.Agent == "codex" {
				root := s.config.ClientRoot
				if root == "" {
					root = filepath.Dir(s.config.Root)
				}
				prior, e := agents.LoadInstallation(root, c.ID)
				if e != nil || !prior.Options.ComputerLevel || prior.Options.AgentPath != item.Target.Path || prior.Options.CodexDirectory != item.Target.Directory {
					continue
				}
				item.Existing, item.ConnectionID, item.Generation, item.State = true, c.ID, c.Generation, "awaiting_reload"
				return item, nil
			}
			item.Existing = true
			item.ConnectionID = c.ID
			item.Generation = c.Generation
			item.State = "already_connected"
			return item, nil
		}
	}
	c, err := s.store.Connection(ctx, item.ConnectionID)
	if errors.Is(err, sql.ErrNoRows) {
		c, err = s.store.ConfigureConnection(ctx, records.ConnectionDraft{ID: item.ConnectionID, Agent: item.Target.Agent, Name: item.Target.Agent, ConnectionOnly: true})
	}
	if err != nil {
		return item, err
	}
	if !c.Enabled || c.Agent != item.Target.Agent || c.Generation != 1 || c.Project != "" || c.AutoProject || len(c.AllowedScopes) != 0 || c.ID != item.ConnectionID {
		return item, errors.New("连接身份已变化，请新建连接批次。")
	}
	item.Generation = c.Generation
	clientRoot := s.config.ClientRoot
	if clientRoot == "" {
		clientRoot = filepath.Dir(s.config.Root)
	}
	bridge := filepath.Join(filepath.Dir(s.proposalGuard), "memlane")
	if prior, e := agents.LoadInstallation(clientRoot, c.ID); e == nil {
		directory := prior.Options.PiDirectory
		if c.Agent == "codex" {
			directory = prior.Options.CodexDirectory
		}
		if prior.Connection.Generation != c.Generation || directory != item.Target.Directory || prior.Options.AgentPath != item.Target.Path {
			return item, errors.New("安装位置或连接代次已变化，请检查原配置。")
		}
		if prior.AgentVersion == probe.Version && agents.CheckInstallation(prior) == nil {
			item.State = "awaiting_reload"
			return item, nil
		}
	}
	options := agents.InstallOptions{ClientRoot: clientRoot, ServiceRoot: s.config.Root, Bridge: bridge, AgentPath: item.Target.Path, PiDirectory: item.Target.Directory}
	if c.Agent == "codex" {
		options.PiDirectory = ""
		options.CodexDirectory = item.Target.Directory
		options.ComputerLevel = true
	}
	_, err = agents.Install(ctx, c, options)
	if err == nil {
		item.State = "awaiting_reload"
	}
	return item, err
}

func (s *Server) verifyConnectionBatchItem(ctx context.Context, item records.AgentConnectionBatchItem) (records.AgentConnectionBatchItem, error) {
	if s.agentBatchVerify != nil {
		return s.agentBatchVerify(ctx, item)
	}
	c, err := s.store.Connection(ctx, item.ConnectionID)
	if err != nil {
		return item, err
	}
	if !c.Enabled || c.Generation != item.Generation || c.AutoProject || c.PendingProject != "" || c.TrustedWorkspace != "" || c.Agent != "codex" && !agents.PiProtocolConnectionEligible(c) {
		return item, errors.New("连接已变化，请重新检查。")
	}
	clientRoot := s.config.ClientRoot
	if clientRoot == "" {
		clientRoot = filepath.Dir(s.config.Root)
	}
	i, err := agents.LoadInstallation(clientRoot, c.ID)
	if err != nil {
		return item, err
	}
	directory := i.Options.PiDirectory
	if c.Agent == "codex" {
		directory = i.Options.CodexDirectory
	}
	if i.Connection.Generation != c.Generation || i.Options.AgentPath != item.Target.Path || directory != item.Target.Directory {
		return item, errors.New("安装回执与批次不符。")
	}
	d := agents.Probe(ctx, c.Agent, item.Target.Path)
	if d.Version != i.AgentVersion {
		return item, errors.New("Agent 版本已变化，请重新安装接入配置。")
	}
	if c.Agent == "codex" {
		project, e := s.validateCodexVerificationWorkspace(ctx, item, item.VerificationWorkspace, item.VerificationWorkspaceDigest)
		if e != nil {
			return item, e
		}
		if project != item.VerificationProject {
			return item, errors.New("verification project changed")
		}
	}
	execution, err := agents.VerifyProtocolConnection(ctx, d, i, agents.RunOptions{Guard: s.proposalGuard, Workspace: item.VerificationWorkspace})
	item.Verification, _ = json.Marshal(execution)
	if err == nil {
		current, e := s.store.Connection(ctx, c.ID)
		if e != nil || !current.Enabled || current.Generation != c.Generation {
			return item, errors.New("验证期间连接已变化。")
		}
		item.State = "connected"
	}
	return item, err
}

func (s *Server) validateCodexVerificationWorkspace(ctx context.Context, item records.AgentConnectionBatchItem, workspace, digest string) (string, error) {
	c, err := s.store.Connection(ctx, item.ConnectionID)
	if err != nil {
		return "", err
	}
	if !c.Enabled || c.Agent != "codex" || c.Generation != item.Generation {
		return "", &records.AccessError{Code: "connection_changed", Message: "connection generation changed"}
	}
	if workspace == "" {
		scopes, e := s.store.Scopes(ctx)
		if e != nil {
			return "", e
		}
		for _, scope := range scopes {
			if scope.Kind != "project" {
				continue
			}
			for _, id := range c.AllowedScopes {
				if id == scope.ID {
					return "", &records.AccessError{Code: "scope_required", Message: "select an explicitly authorized project workspace"}
				}
			}
		}
		if c.Project != "" {
			return "", &records.AccessError{Code: "scope_required", Message: "bound project verification requires its workspace"}
		}
		return "", nil
	}
	actual, e := agents.CodexWorkspaceIdentity(workspace)
	if e != nil || actual != digest {
		return "", &records.AccessError{Code: "workspace_mismatch", Message: "verification directory identity changed"}
	}
	project, e := s.store.ProjectForWorkspace(ctx, workspace)
	if e != nil {
		return "", e
	}
	allowed := false
	for _, id := range c.AllowedScopes {
		if id == project.ID {
			allowed = true
		}
	}
	if !allowed || c.Project != "" && c.Project != project.ID {
		return "", &records.AccessError{Code: "scope_denied", Message: "verification workspace is not explicitly authorized"}
	}
	if e = s.store.CheckBoundWorkspace(ctx, project.ID, workspace); e != nil {
		return "", e
	}
	return project.ID, nil
}
