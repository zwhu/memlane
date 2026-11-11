package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type AgentBatchIntent struct {
	ServiceRoot string                              `json:"service_root"`
	Start       *records.AgentConnectionBatchStart  `json:"start,omitempty"`
	Action      *records.AgentConnectionBatchAction `json:"action,omitempty"`
}

type AgentBatchUI struct {
	ServiceReachable    bool
	State               records.AgentConnectionBatchState
	Selected            map[string]bool
	Intent              AgentBatchIntent
	Error               string
	Loading, Submitting bool
	Sequence            uint64
}

func (s *AgentBatchUI) pending() bool { return s.Intent.Start != nil || s.Intent.Action != nil }
func (s *AgentBatchUI) frozen() bool {
	if s.Submitting || s.pending() {
		return true
	}
	if s.State.Batch == nil {
		return false
	}
	return s.State.Batch.State == "running" || s.State.Batch.State == "waiting" || s.State.Batch.State == "cancelling"
}

func (a *App) loadAgentBatchIntent() {
	s := &a.agents
	data, err := os.ReadFile(filepath.Join(s.Root, "agent-connection-intent.json"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil || len(data) > 65536 {
		s.Batch.Error = "上次连接操作无法读取，请检查客户端目录权限。"
		return
	}
	var intent AgentBatchIntent
	if json.Unmarshal(data, &intent) != nil || intent.ServiceRoot != a.library.Root || (intent.Start != nil && intent.Action != nil) {
		s.Batch.Error = "上次连接操作不属于当前服务，未自动重放。"
		return
	}
	s.Batch.Intent = intent
}

func (a *App) persistAgentBatchIntent(intent AgentBatchIntent) error {
	if err := serviceipc.WriteJSON(filepath.Join(a.agents.Root, "agent-connection-intent.json"), intent); err != nil {
		return err
	}
	a.agents.Batch.Intent = intent
	return nil
}

func (a *App) clearAgentBatchIntent() {
	if err := a.persistAgentBatchIntent(AgentBatchIntent{ServiceRoot: a.library.Root}); err != nil {
		a.agents.Batch.Error = "操作回执已取得，但本机草稿未能清理：" + err.Error()
	}
}

func (a *App) refreshAgentBatch() {
	s := &a.agents
	b := &s.Batch
	if s.Context == nil || b.Loading || b.Submitting {
		return
	}
	b.Loading = true
	sequence := b.Sequence
	intent := b.Intent
	owner := s.Context
	library := a.library
	id := ""
	if b.State.Batch != nil {
		id = b.State.Batch.ID
	}
	if intent.Action != nil {
		id = intent.Action.BatchID
	}
	go func() {
		ctx, cancel := context.WithTimeout(owner, 15*time.Second)
		defer cancel()
		var state records.AgentConnectionBatchState
		var err error
		if intent.Start != nil {
			state, err = library.AgentConnectionBatchByOperation(ctx, intent.Start.OperationID)
		} else {
			state, err = library.AgentConnectionBatch(ctx, id)
		}
		if owner.Err() != nil {
			return
		}
		s.Dispatch(func() {
			b.Loading = false
			if owner != s.Context || sequence != b.Sequence {
				return
			}
			if err != nil {
				b.ServiceReachable = false
				b.Error = "连接服务暂不可用。请在「环境与组件」中检查或启动服务，再重新检测。"
				return
			}
			b.State = state
			b.ServiceReachable = true
			b.Error = ""
			identity := ""
			if intent.Start != nil {
				identity = intent.Start.Identity
			}
			if intent.Action != nil {
				identity = intent.Action.Identity
			}
			if identity != "" && identity != state.Identity {
				a.clearAgentBatchIntent()
				b.Error = "记忆库已恢复，旧连接操作已退休，请重新选择。"
				return
			}
			if state.Batch != nil {
				if intent.Start != nil && state.Batch.OperationID == intent.Start.OperationID {
					a.clearAgentBatchIntent()
					b.Selected = map[string]bool{}
				}
				if intent.Action != nil && state.Batch.Actions[intent.Action.OperationID] == intent.Action.Kind {
					a.clearAgentBatchIntent()
				}
			}
		})
	}()
}

func (a *App) machineConnectionEligible(d agents.Discovery) bool {
	if agents.ComputerConnectionError(d) != nil || !filepath.IsAbs(d.Path) || !filepath.IsAbs(a.machineConnectionDirectory(d.Agent)) {
		return false
	}
	for _, c := range a.agents.Connections {
		if c.Agent == d.Agent && c.Enabled {
			return false
		}
	}
	return true
}

func (a *App) machineConnectionDirectory(agent string) string {
	if agent == "pi" {
		return a.agents.PiDir
	}
	if agent == "codex" {
		if directory := os.Getenv("CODEX_HOME"); directory != "" {
			return directory
		}
		return filepath.Join(a.agents.Home, ".codex")
	}
	return ""
}

func (a *App) selectedConnectionTargets() []records.AgentConnectionTarget {
	var targets []records.AgentConnectionTarget
	for _, d := range a.agents.Found {
		if a.agents.Batch.Selected[d.Agent] && a.machineConnectionEligible(d) {
			targets = append(targets, records.AgentConnectionTarget{Agent: d.Agent, Path: d.Path, Directory: a.machineConnectionDirectory(d.Agent)})
		}
	}
	return targets
}

func (a *App) selectAllAgentConnections() {
	if a.agents.Batch.frozen() {
		return
	}
	if a.agents.Batch.Selected == nil {
		a.agents.Batch.Selected = map[string]bool{}
	}
	for _, d := range a.agents.Found {
		if a.machineConnectionEligible(d) {
			a.agents.Batch.Selected[d.Agent] = true
		}
	}
}

func (a *App) startSelectedAgentBatch() {
	b := &a.agents.Batch
	if b.frozen() || b.State.Identity == "" {
		return
	}
	targets := a.selectedConnectionTargets()
	if len(targets) == 0 {
		return
	}
	intent := AgentBatchIntent{ServiceRoot: a.library.Root, Start: &records.AgentConnectionBatchStart{Identity: b.State.Identity, OperationID: uuid.NewString(), Targets: targets}}
	if err := a.persistAgentBatchIntent(intent); err != nil {
		b.Error = "连接请求未保存，尚未开始：" + err.Error()
		return
	}
	a.submitAgentBatchIntent()
}

func (a *App) actAgentBatch(kind string) {
	b := &a.agents.Batch
	if b.Submitting || b.pending() || b.State.Batch == nil {
		return
	}
	intent := AgentBatchIntent{ServiceRoot: a.library.Root, Action: &records.AgentConnectionBatchAction{Identity: b.State.Identity, BatchID: b.State.Batch.ID, OperationID: uuid.NewString(), Kind: kind}}
	if err := a.persistAgentBatchIntent(intent); err != nil {
		b.Error = "操作尚未开始：" + err.Error()
		return
	}
	a.submitAgentBatchIntent()
}

func (a *App) submitAgentBatchIntent() {
	s := &a.agents
	b := &s.Batch
	if s.Context == nil || b.Submitting || !b.pending() {
		return
	}
	intent := b.Intent
	owner := s.Context
	library := a.library
	b.Submitting = true
	b.Sequence++
	sequence := b.Sequence
	go func() {
		ctx, cancel := context.WithTimeout(owner, 20*time.Second)
		defer cancel()
		var result records.AgentConnectionBatch
		var err error
		if intent.Start != nil {
			result, err = library.StartAgentConnectionBatch(ctx, *intent.Start)
		} else {
			result, err = library.ActAgentConnectionBatch(ctx, *intent.Action)
		}
		if owner.Err() != nil {
			return
		}
		s.Dispatch(func() {
			if owner != s.Context || sequence != b.Sequence {
				return
			}
			b.Submitting = false
			if err != nil {
				if failure, ok := err.(*serviceipc.ServiceError); ok && (failure.Code == "conflict" || failure.Code == "invalid_request" || failure.Code == "confirmation_required" || failure.Code == "restore_reconnect_required") {
					a.clearAgentBatchIntent()
					b.State.Batch = nil
					b.Error = "连接操作未执行：" + failure.Error()
					a.refreshAgentBatch()
					return
				}
				b.Error = "上次操作未取得确定回执，请核对后重试：" + err.Error()
				a.refreshAgentBatch()
				return
			}
			b.State.Batch = &result
			b.ServiceReachable = true
			b.State.Identity = result.Identity
			b.Selected = map[string]bool{}
			b.Error = ""
			a.clearAgentBatchIntent()
			a.refreshAgents(false)
			a.refreshAgentBatch()
		})
	}()
}

func agentBatchItemState(state string) string {
	labels := map[string]string{"queued": "等待连接", "installing": "正在安装", "awaiting_reload": "待重载验证", "verifying": "正在验证", "connected": "已连接", "already_connected": "已配置，保留原连接", "failed": "连接失败", "unsupported": "电脑级接入待适配", "cancelled": "已取消", "interrupted": "连接已中断"}
	if label := labels[state]; label != "" {
		return label
	}
	return state
}
