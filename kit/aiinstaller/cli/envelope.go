// Package cli defines an installer CLI response protocol and security helpers.
package cli

import (
	"encoding/json"
	"io"
	"maps"
	"slices"
)

const (
	ExitOK               = 0
	ExitActionRequired   = 10
	ExitHumanRequired    = 11
	ExitInProgressOrBusy = 20
	ExitPreflightBlocked = 30
	ExitNeedsReview      = 40
	ExitFatal            = 50
	ExitInvalidArguments = 64

	DefaultRetryMillis = 10000
)

var statuses = map[int]string{
	ExitOK: "ok", ExitActionRequired: "action_required", ExitHumanRequired: "human_required",
	ExitInProgressOrBusy: "in_progress_or_busy", ExitPreflightBlocked: "preflight_blocked",
	ExitNeedsReview: "needs_review", ExitFatal: "fatal", ExitInvalidArguments: "invalid_arguments",
}

var exitPriority = map[int]int{
	ExitOK: 0, ExitHumanRequired: 1, ExitActionRequired: 2, ExitPreflightBlocked: 3,
	ExitInProgressOrBusy: 4, ExitNeedsReview: 5, ExitFatal: 6, ExitInvalidArguments: 7,
}

// StatusMap returns the exit code descriptions for a capabilities manifest.
func StatusMap() map[int]string {
	return maps.Clone(statuses)
}

// StatusName returns the wire status for an exit code.
func StatusName(code int) string {
	return statuses[code]
}

// ExitPriority returns the precedence used when multiple blockers are recorded.
func ExitPriority(code int) int {
	return exitPriority[code]
}

// Envelope defines the common installer CLI response fields.
type Envelope struct {
	SchemaVersion int              `json:"schema_version"`
	Command       string           `json:"command"`
	Mode          string           `json:"mode"`
	OK            bool             `json:"ok"`
	Status        string           `json:"status"`
	ExitCode      int              `json:"exit_code"`
	OperationID   string           `json:"operation_id"`
	RetryAfterMS  int              `json:"retry_after_ms"`
	App           AppSummary       `json:"app"`
	Blockers      []Blocker        `json:"blockers"`
	NextActions   []NextAction     `json:"next_actions"`
	HumanSteps    []string         `json:"human_steps"`
	Capabilities  *CommandManifest `json:"capabilities"`
	AgentGuidance string           `json:"agent_guidance"`
}

// AppSummary identifies the application that generated the response.
type AppSummary struct {
	Version            string `json:"version"`
	Executable         string `json:"executable"`
	BundledRuntimeHash string `json:"bundled_runtime_hash"`
	Platform           string `json:"platform"`
}

// Blocker describes a condition that affects the command result.
type Blocker struct {
	Code   string `json:"code"`
	Status string `json:"status"`
}

// NextAction describes a command the caller may run and its prerequisite.
type NextAction struct {
	Args     []string `json:"args"`
	Requires string   `json:"requires"`
}

// CommandManifest lists publicly supported commands, repair targets, and exit codes.
type CommandManifest struct {
	SchemaVersion int                 `json:"schema_version"`
	Commands      map[string][]string `json:"commands"`
	RepairTargets []string            `json:"repair_targets"`
	ExitCodes     map[int]string      `json:"exit_codes"`
}

// NewEnvelope initializes the common fields for a command response.
func NewEnvelope(command, mode, agentGuidance string) Envelope {
	return Envelope{
		SchemaVersion: 1,
		Command:       command,
		Mode:          mode,
		Blockers:      []Blocker{},
		NextActions:   []NextAction{},
		HumanSteps:    []string{},
		AgentGuidance: agentGuidance,
	}
}

// Block records a blocker and retains the highest-priority exit code.
func (e *Envelope) Block(code int, reason string) {
	e.Blockers = append(e.Blockers, Blocker{Code: reason, Status: StatusName(code)})
	if ExitPriority(code) > ExitPriority(e.ExitCode) {
		e.ExitCode = code
	}
}

// Busy records a retryable blocker and a retry delay in milliseconds.
func (e *Envelope) Busy(reason string) {
	e.Block(ExitInProgressOrBusy, reason)
	e.RetryAfterMS = DefaultRetryMillis
}

// SetOperation identifies the operation associated with the response.
func (e *Envelope) SetOperation(id string) {
	e.OperationID = id
}

// AddAction records an allowed follow-up action once.
func (e *Envelope) AddAction(requires string, args ...string) {
	for _, action := range e.NextActions {
		if action.Requires == requires && slices.Equal(action.Args, args) {
			return
		}
	}
	e.NextActions = append(e.NextActions, NextAction{Args: slices.Clone(args), Requires: requires})
}

// Finalize derives the wire status and success flag from the exit code.
func (e *Envelope) Finalize() {
	e.OK, e.Status = e.ExitCode == ExitOK, StatusName(e.ExitCode)
}

// EncodeJSON writes the installer CLI JSON format.
func EncodeJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}
