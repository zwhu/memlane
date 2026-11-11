package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"strings"
)

// BatchSafeReason bounds persisted installation diagnostics and redacts common
// credential forms using the same policy as Agent execution diagnostics.
func BatchSafeReason(reason string) string { return safeReason(reason) }

// ConnectionVerification identifies a connection proof, not model capability
// or a project grant. Preserve legacy tokens so existing dependency hashes stay
// unchanged; protocol receipts occupy their own namespace.
func ConnectionVerification(i Installation, generation int64) (string, bool) {
	if !i.Connection.Enabled || i.Connection.Generation != generation {
		return "", false
	}
	if !(i.Connection.Agent == "codex" && i.Options.ComputerLevel) && i.VerifiedGeneration == generation && i.VerifiedAt != "" && i.VerificationError == "" {
		return i.VerifiedAt, true
	}
	c := i.Connection
	emptyPi := c.Agent == "pi" && c.Project == "" && len(c.AllowedScopes) == 0
	settledPi := PiProtocolConnectionEligible(c) && i.ProtocolScopeDigest == protocolScopeDigest(c)
	computerCodex := c.Agent == "codex" && i.Options.ComputerLevel && ComputerConnectionError(Discovery{Agent: c.Agent, Version: i.AgentVersion}) == nil && i.ProtocolScopeDigest == protocolScopeDigest(c)
	if (emptyPi || settledPi || computerCodex) && !c.AutoProject && c.PendingProject == "" && c.TrustedWorkspace == "" && i.ProtocolGeneration == generation && i.ProtocolVerifiedAt != "" && i.ProtocolError == "" {
		return "protocol:" + i.ProtocolVerifiedAt, true
	}
	return "", false
}
func protocolScopeDigest(c records.Connection) string {
	return records.MergeDigest([]any{c.Project, c.AllowedScopes})
}

// PiProtocolConnectionEligible accepts only a settled, exact pi identity. It
// grants no project access and does not prove model capability.
func PiProtocolConnectionEligible(c records.Connection) bool {
	id, err := uuid.Parse(c.ID)
	if err != nil || id.String() != c.ID || c.Agent != "pi" || !c.Enabled || c.Generation < 1 || c.AutoProject || c.PendingProject != "" || c.TrustedWorkspace != "" {
		return false
	}
	scopes := map[string]bool{}
	for _, scope := range c.AllowedScopes {
		id, err := uuid.Parse(scope)
		if err != nil || id.String() != scope || strings.TrimSpace(scope) != scope || scopes[scope] {
			return false
		}
		scopes[scope] = true
	}
	return c.Project == "" || scopes[c.Project]
}

// VerifyProtocolConnection verifies settled pi identity and grant metadata. It neither
// writes synthetic memories nor upgrades the historical read/write receipt.
func VerifyProtocolConnection(ctx context.Context, d Discovery, i Installation, o RunOptions) (Execution, error) {
	o.ProtocolOnly = true
	o.Prompt = "/memlane-connection-check"
	var execution Execution
	var proof ProtocolVerificationContext
	err := CheckInstallation(i)
	if err == nil {
		if i.Connection.Agent == "codex" {
			execution, err = runCodexConnectionProtocol(ctx, d, i, o)
		} else {
			execution, err = Run(ctx, d, i, o)
		}
	}
	if err == nil {
		if i.Connection.Agent == "codex" {
			proof, err = checkCodexProtocolTranscript(execution, i, o.Workspace)
		} else {
			err = checkPiProtocolTranscript(execution, i.Connection)
			proof.ScopeDigest = protocolScopeDigest(i.Connection)
		}
	}
	failure := ""
	if err != nil {
		failure = err.Error()
	}
	persistErr := SaveProtocolVerification(i.Options.ClientRoot, i.Connection.ID, i.Connection.Generation, failure, proof)
	if err == nil {
		err = persistErr
	}
	return execution, err
}

func checkProtocolTranscript(e Execution) error {
	return checkPiProtocolTranscript(e, records.Connection{})
}
func checkPiProtocolTranscript(e Execution, c records.Connection) error {
	if e.ProtocolReceipt == nil {
		return errors.New("Agent 未返回连接协议回执")
	}
	if e.Failure != "" || len(e.Calls) != 0 || e.Events["agent_start"] != 0 || e.Events["turn_start"] != 0 || e.Events["tool_execution_start"] != 0 || e.Events["error"] != 0 {
		return errors.New("连接验证意外启动了模型或工具执行")
	}
	{
		if !e.ProtocolReceipt.OK || e.ProtocolReceipt.Protocol != 1 {
			if e.ProtocolReceipt.Error != nil {
				return fmt.Errorf("连接协议验证失败：%s", safeReason(e.ProtocolReceipt.Error.Code))
			}
			return errors.New("Agent 连接验证包含非预期工具或失败回执")
		}
		var reply struct {
			Scopes   *[]records.Scope `json:"scopes"`
			Bound    *string          `json:"bound_project"`
			Current  *string          `json:"current_project"`
			Requires *bool            `json:"requires_scope"`
		}
		if json.Unmarshal(e.ProtocolReceipt.Result, &reply) != nil || reply.Scopes == nil || reply.Bound == nil || *reply.Bound != c.Project || reply.Current == nil || *reply.Current != c.Project || reply.Requires == nil || *reply.Requires != (c.Project == "") || len(*reply.Scopes) != len(c.AllowedScopes) {
			return errors.New("连接协议未返回当前授权元数据")
		}
		expected := map[string]bool{}
		for _, id := range c.AllowedScopes {
			expected[id] = true
		}
		seen := map[string]bool{}
		for _, scope := range *reply.Scopes {
			if !expected[scope.ID] || seen[scope.ID] || scope.Kind != "project" && scope.Kind != "preference" && scope.Kind != "experience" {
				return errors.New("连接协议授权范围不匹配")
			}
			seen[scope.ID] = true
		}
	}
	return nil
}
