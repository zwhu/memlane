package simpledesktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/zwhu/memlane/internal/controlplane"
)

func connectionActionPath(id, action string) string {
	return "/v1/connections/" + url.PathEscape(id) + "/" + action
}

type pinnedPreviewVersion struct {
	status   string
	revision int64
}

func (a *App) preloadPinnedConnectionPreviews() {
	s := &a.integrations
	wizard := &a.environment
	if !a.initialSetup || wizard.InitialStep != 3 || !wizard.RuntimeStarted || wizard.staleRuntimeOwner() ||
		!s.Loaded || s.Loading || s.Busy || a.Update == nil || len(s.PinnedAgentKinds) == 0 {
		return
	}
	if _, ok := a.accessClient(); !ok {
		return
	}
	if wizard.pinnedPreviewRequested == nil {
		wizard.pinnedPreviewRequested = make(map[string]pinnedPreviewVersion)
	}
	var ids []string
	for _, row := range s.Connections.Connections {
		if row.ID == "" || !slices.Contains(s.PinnedAgentKinds, row.Kind) || s.pendingConnection(row.ID) != nil || needsTraeHooksRepair(row) {
			continue
		}
		if !connectionActionAllowed(row, "prepare") && row.Status != "prepared" {
			delete(s.ConnectionPreviews, row.ID)
			continue
		}
		version := pinnedPreviewVersion{status: row.Status, revision: s.Connections.Revision}
		if previous, ok := wizard.pinnedPreviewRequested[row.ID]; !ok || previous != version {
			wizard.pinnedPreviewRequested[row.ID] = version
			ids = append(ids, row.ID)
		}
	}
	a.preloadConnectionPreviews(ids)
}

func (a *App) preloadConnectionPreviews(ids []string) {
	s := &a.integrations
	client, ok := a.accessClient()
	if !ok || s.Busy || s.Loading || a.Update == nil || len(ids) == 0 {
		return
	}
	ids = append([]string(nil), ids...)
	s.Busy = true
	s.Error, s.Message = "", ""
	for _, id := range ids {
		delete(s.ConnectionPreviews, id)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		previews := make(map[string]controlplane.ConnectionPreview, len(ids))
		var firstErr error
		for _, id := range ids {
			var preview controlplane.ConnectionPreview
			err := client.request(ctx, http.MethodGet, connectionActionPath(id, "preview"), nil, &preview)
			if err == nil && preview.InstanceID != id {
				err = fmt.Errorf("接入预览未确认，请重新预览")
			}
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			previews[id] = preview
		}
		a.Update(func() {
			s.Busy = false
			if s.ConnectionPreviews == nil {
				s.ConnectionPreviews = make(map[string]controlplane.ConnectionPreview)
			}
			for id, preview := range previews {
				s.ConnectionPreviews[id] = preview
			}
			s.Error = controlUserError(firstErr)
		})
	}()
}

// A preview can authorize activation only when it includes the proposed digest.
// A host that supplies the digest during preparation requires a second confirmation.
func previewEligibleForConfirm(preview controlplane.ConnectionPreview) bool {
	return preview.Status == "not_connected" || preview.Status == "prepared" || preview.Status == "staging_not_complete"
}

func (a *App) confirmPrepareAndActivateConnection(id, previewSHA string) {
	s := &a.integrations
	preview, present := s.ConnectionPreviews[id]
	client, ok := a.accessClient()
	if !ok || !present || !previewEligibleForConfirm(preview) || !preview.OK || preview.NeedsHookRepair || preview.InstanceID != id || preview.EffectiveProposedSHA() != previewSHA ||
		s.pendingConnection(id) != nil || s.Busy || s.Loading || a.Update == nil {
		return
	}
	delete(s.ConnectionPreviews, id)
	s.Busy = true
	s.Error, s.Message = "", ""
	root := a.generationOperationsDir()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		type actionResult struct {
			Revision   int64           `json:"revision"`
			Connection AgentConnection `json:"connection"`
		}
		run := func(action string, revision int64, sha string) (actionResult, error) {
			o := generationUIOperation{Operation: integrationOperation(), Kind: "connection_" + action, Profile: id, Revision: revision}
			var result actionResult
			if _, err := recordGenerationOperationAt(root, o); err != nil {
				return result, err
			}
			payload := map[string]any{"operation_id": o.Operation, "expected_revision": revision}
			if action == "activate" {
				payload["proposed_sha256"] = sha
			}
			err := client.request(ctx, http.MethodPost, connectionActionPath(id, action), payload, &result)
			complete := err == nil && result.Connection.ID == id
			if complete || client.confirmedConnectionRejection(ctx, o, err) {
				if finishErr := controlplane.FinishUIOperation(root, o.Operation); finishErr != nil {
					return result, fmt.Errorf("原回执已确认，但操作记录尚未清理，请重新核对")
				}
			} else if err == nil {
				err = fmt.Errorf("Agent 原操作回执未确认，请核对")
			}
			return result, err
		}
		var err error
		var nextPreviewID string
		var refreshedPreview *controlplane.ConnectionPreview
		if preview.Status == "prepared" {
			if previewSHA == "" {
				err = fmt.Errorf("接入预览缺少确认信息，请重新预览")
			} else {
				_, err = run("activate", preview.Revision, previewSHA)
			}
		} else {
			var prepared actionResult
			prepared, err = run("prepare", preview.Revision, "")
			if err == nil {
				switch {
				case previewSHA == "" || preview.Kind == "opencode":
					nextPreviewID = id
				case prepared.Connection.Status != "prepared" || prepared.Connection.Preview == nil || prepared.Connection.Preview.ProposedSHA != previewSHA ||
					prepared.Connection.Preview.BaselineSHA256 != preview.BaselineSHA256:
					refreshed, readErr := client.controlPlaneClient().PreviewConnection(ctx, id)
					if readErr == nil && refreshed.InstanceID == id {
						refreshedPreview = &refreshed
					}
					err = fmt.Errorf("准备后的接入配置或宿主文件已变化，请重新确认连接")
				default:
					_, err = run("activate", prepared.Revision, previewSHA)
				}
			}
		}
		a.updateConnectionMutation(client, root, "", nextPreviewID, refreshedPreview, err)
	}()
}

// Refresh both the journal and the connection rows even after an uncertain result.
func (a *App) updateConnectionMutation(client ProjectControlClient, root, message, nextPreviewID string, refreshedPreview *controlplane.ConnectionPreview, mutationErr error) {
	pending, journalErr := loadGenerationOperationsFromDir(root)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var directory ConnectionsDirectory
	readErr := client.request(ctx, http.MethodGet, "/v1/connections", nil, &directory)
	a.Update(func() {
		s := &a.integrations
		s.Busy = false
		s.Message = message
		if refreshedPreview != nil {
			if s.ConnectionPreviews == nil {
				s.ConnectionPreviews = make(map[string]controlplane.ConnectionPreview)
			}
			s.ConnectionPreviews[refreshedPreview.InstanceID] = *refreshedPreview
		}
		s.ConnectionsError = controlUserError(readErr)
		if readErr == nil {
			s.Connections = directory
		}
		if journalErr == nil {
			s.PendingOperations = pending
			s.Error = controlUserError(mutationErr)
			if s.Error == "" {
				s.Error = controlUserError(readErr)
			}
		} else {
			s.Error = "无法读取待核对操作，请重新检测连接"
		}
		a.RefreshProjectDirectory()
		if mutationErr == nil && readErr == nil && journalErr == nil {
			if nextPreviewID != "" {
				a.preloadConnectionPreviews([]string{nextPreviewID})
			} else {
				a.preloadPinnedConnectionPreviews()
			}
		}
	})
}

func (s *integrationState) clearTraeRepairPreview(id string) {
	delete(s.TraeRepairPreview, id)
	delete(s.TraeRepairHasRevision, id)
}

func (a *App) previewTraeHooksRepair(id string) {
	s := &a.integrations
	client, ok := a.accessClient()
	if !ok || s.Busy || s.Loading || a.Update == nil || s.pendingConnection(id) != nil {
		return
	}
	s.clearTraeRepairPreview(id)
	s.Busy = true
	s.Error, s.Message = "", ""
	revision := s.Connections.Revision
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var raw json.RawMessage
		var preview controlplane.TraeHooksRepairResult
		var fields struct {
			Revision *int64 `json:"revision"`
		}
		err := client.request(ctx, http.MethodPost, connectionActionPath(id, "repair-trae-hooks"), map[string]bool{"dry_run": true}, &raw)
		if err == nil {
			if json.Unmarshal(raw, &preview) != nil || json.Unmarshal(raw, &fields) != nil {
				err = fmt.Errorf("TRAE Hooks 修复预览未确认，请重新检查")
			}
		}
		a.Update(func() {
			s.Busy = false
			if err != nil {
				s.Error = controlUserError(err)
				return
			}
			if s.TraeRepairPreview == nil {
				s.TraeRepairPreview = make(map[string]controlplane.TraeHooksRepairResult)
			}
			if s.TraeRepairHasRevision == nil {
				s.TraeRepairHasRevision = make(map[string]bool)
			}
			hasRevision := fields.Revision != nil
			if !hasRevision {
				preview.Revision = revision
			}
			s.TraeRepairPreview[id] = preview
			s.TraeRepairHasRevision[id] = hasRevision
		})
	}()
}

func (a *App) applyTraeHooksRepair(id string) {
	s := &a.integrations
	client, ok := a.accessClient()
	preview, present := s.TraeRepairPreview[id]
	if !ok || !present || !preview.Repairable || len(preview.Blockers) != 0 || preview.ReviewedSHA == "" ||
		s.Busy || s.Loading || a.Update == nil || s.pendingConnection(id) != nil {
		return
	}
	if !s.TraeRepairHasRevision[id] {
		preview.Revision = s.Connections.Revision
	}
	s.clearTraeRepairPreview(id)
	s.Busy = true
	s.Error, s.Message = "", ""
	root := a.generationOperationsDir()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		o := generationUIOperation{Operation: integrationOperation(), Kind: "connection_repair_trae_hooks", Profile: id, Revision: preview.Revision}
		_, err := recordGenerationOperationAt(root, o)
		message := ""
		if err == nil {
			var result controlplane.TraeHooksRepairResult
			payload := map[string]any{"expected_revision": preview.Revision, "operation_id": o.Operation, "reviewed_sha": preview.ReviewedSHA}
			err = client.request(ctx, http.MethodPost, connectionActionPath(id, "repair-trae-hooks"), payload, &result)
			repaired := err == nil && result.Repaired && result.OperationID == o.Operation &&
				(result.Connection == nil || result.Connection.ID == id)
			if repaired || client.confirmedConnectionRejection(ctx, o, err) {
				if finishErr := controlplane.FinishUIOperation(root, o.Operation); finishErr != nil {
					err = fmt.Errorf("修复回执已确认，但操作记录尚未清理，请重新核对")
				}
			} else if err == nil {
				err = fmt.Errorf("TRAE Hooks 修复回执未确认，请核对原操作")
			}
			if repaired && err == nil {
				message = "TRAE Hooks 已修复，原配置已备份。"
			}
		}
		a.updateConnectionMutation(client, root, message, "", nil, err)
	}()
}
