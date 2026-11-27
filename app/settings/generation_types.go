package settings

import (
	"encoding/json"

	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/internal/bootstrap"
)

type GenerationRef struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

type GenerationProfile struct {
	Mode                  string   `json:"mode"`
	Provider              string   `json:"provider,omitempty"`
	BridgeKind            string   `json:"bridge_kind,omitempty"`
	BridgeProvider        string   `json:"bridge_provider,omitempty"`
	Model                 string   `json:"model"`
	Endpoint              string   `json:"endpoint,omitempty"`
	Temperature           *float64 `json:"temperature,omitempty"`
	MaxConcurrentRequests *int     `json:"max_concurrent_requests,omitempty"`
	MaxOutputTokens       *int     `json:"max_output_tokens,omitempty"`
	CredentialVersion     string   `json:"credential_version,omitempty"`
	CredentialConfigured  bool     `json:"credential_configured,omitempty"`
}

type GenerationRow struct {
	Revision int64             `json:"revision"`
	Digest   string            `json:"digest"`
	Profile  GenerationProfile `json:"profile"`
}

type GenerationValidation struct {
	Phase              string              `json:"phase,omitempty"`
	FailedPhase        string              `json:"failed_phase,omitempty"`
	DiagnosticCode     string              `json:"diagnostic_code,omitempty"`
	DiagnosticStage    string              `json:"diagnostic_stage,omitempty"`
	NetworkError       string              `json:"network_error,omitempty"`
	BridgeErrorCode    string              `json:"bridge_error_code,omitempty"`
	HTTPStatus         int                 `json:"http_status,omitempty"`
	ProcessExitCode    *int                `json:"process_exit_code,omitempty"`
	ElapsedMs          float64             `json:"elapsed_ms,omitempty"`
	DeadlineSeconds    int                 `json:"deadline_seconds,omitempty"`
	StartedAt          float64             `json:"started_at,omitempty"`
	AgentDefaults      *AgentModelDefaults `json:"agent_defaults,omitempty"`
	Reference          *GenerationRef      `json:"reference,omitempty"`
	Status             string              `json:"status"`
	Digest             string              `json:"digest"`
	ErrorCode          string              `json:"error_code"`
	Plain              bool                `json:"plain_passed"`
	Schema             bool                `json:"schema_passed"`
	Quality            bool                `json:"quality_passed"`
	RegisteredRevision int64               `json:"registered_revision"`
	StructuredMode     string              `json:"structured_mode"`
	ModeEvidence       string              `json:"structured_mode_evidence"`
}

type AgentModelDefaults struct {
	Model               string `json:"model"`
	Provider            string `json:"provider"`
	Source              string `json:"source,omitempty"`
	ConfigurationSource string `json:"configuration_source,omitempty"`
	ReasoningEffort     string `json:"reasoning_effort,omitempty"`
	ThinkingLevel       string `json:"thinking_level,omitempty"`
	ServiceTier         string `json:"service_tier,omitempty"`
}

type GenerationDirectory struct {
	OutputLanguage         string                        `json:"output_language"`
	ModelStatus            *api.ModelStatus              `json:"model_status"`
	DetectedProcessingCLIs []string                      `json:"detected_processing_clis,omitempty"`
	AgentDefaults          map[string]AgentModelDefaults `json:"agent_defaults"`
	AgentBridges           map[string]GenerationProfile  `json:"agent_bridges"`

	HandoffAvailable bool                            `json:"handoff_available"`
	ActiveHandoff    *GenerationHandoffReceipt       `json:"active_handoff,omitempty"`
	Revision         int64                           `json:"revision"`
	Active           *GenerationRef                  `json:"active"`
	Next             *GenerationRef                  `json:"next"`
	Profiles         map[string][]GenerationRow      `json:"profiles"`
	Validations      map[string]GenerationValidation `json:"validations"`
	NextError        string                          `json:"next_error"`
	StartupError     string                          `json:"startup_error"`
}

func (g *GenerationDirectory) UnmarshalJSON(data []byte) error {
	type desktopDirectory GenerationDirectory
	var envelope struct {
		*desktopDirectory
		State    *api.GenerationState `json:"state"`
		Profiles json.RawMessage      `json:"profiles"`
	}
	var result GenerationDirectory
	envelope.desktopDirectory = (*desktopDirectory)(&result)
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if envelope.State == nil {
		if len(envelope.Profiles) > 0 {
			if err := json.Unmarshal(envelope.Profiles, &result.Profiles); err != nil {
				return err
			}
		}
	} else {
		var profiles []struct {
			GenerationProfile
			ProfileID string `json:"profile_id"`
			Revision  int64  `json:"revision"`
			Digest    string `json:"digest"`
			Removed   bool   `json:"removed"`
		}
		if err := json.Unmarshal(envelope.Profiles, &profiles); err != nil {
			return err
		}
		result.Profiles = make(map[string][]GenerationRow)
		for _, p := range profiles {
			if !p.Removed {
				result.Profiles[p.ProfileID] = append(result.Profiles[p.ProfileID], GenerationRow{p.Revision, p.Digest, p.GenerationProfile})
			}
		}
		state := envelope.State
		result.Revision, result.StartupError = int64(state.Revision), state.StartupError
		if state.ActiveProfileID != "" && state.ActiveRevision > 0 {
			result.Active = &GenerationRef{state.ActiveProfileID, int64(state.ActiveRevision)}
		}
		if state.NextProfileID != "" && state.NextRevision > 0 {
			result.Next = &GenerationRef{state.NextProfileID, int64(state.NextRevision)}
		}
		if result.OutputLanguage == "" && result.ModelStatus != nil {
			result.OutputLanguage = result.ModelStatus.OutputLanguage
		}
	}
	*g = result
	return nil
}

func isProcessingCLIKind(kind string) bool {
	switch kind {
	case "codex", "pi", "traex", "antigravity":
		return true
	default:
		return false
	}
}

func (a *Controller) isDetectedProcessingCLI(kind string) bool {
	if !isProcessingCLIKind(kind) {
		return false
	}
	s := &a.integrations
	for _, detected := range s.Generation.DetectedProcessingCLIs {
		if detected == kind {
			return true
		}
	}
	if s.GenerationLoaded && s.GenerationError == "" && s.Generation.DetectedProcessingCLIs != nil {
		return false
	}
	for _, row := range s.Connections.Connections {
		if row.Detected && row.Kind == kind {
			return true
		}
	}
	for _, host := range a.environment.Hosts {
		if host.Detected && host.SupportsProcessingCLI && host.Executable != "" && host.Kind == kind {
			return true
		}
	}
	return false
}

func (a *Controller) hasDetectedProcessingCLI() bool {
	s := &a.integrations
	for _, kind := range s.Generation.DetectedProcessingCLIs {
		if isProcessingCLIKind(kind) {
			return true
		}
	}
	if s.GenerationLoaded && s.GenerationError == "" && s.Generation.DetectedProcessingCLIs != nil {
		return false
	}
	for _, row := range s.Connections.Connections {
		if row.Detected && isProcessingCLIKind(row.Kind) {
			return true
		}
	}
	for _, host := range a.environment.Hosts {
		if host.Detected && host.SupportsProcessingCLI && host.Executable != "" && isProcessingCLIKind(host.Kind) {
			return true
		}
	}
	return false
}

func (a *Controller) detectedUnconfiguredProcessingCLIs() []bootstrap.RecommendedProcessingCLI {
	var unconfigured []bootstrap.RecommendedProcessingCLI
	for _, cli := range bootstrap.RecommendedProcessingCLIs() {
		if a.isDetectedProcessingCLI(cli.Kind) && !a.integrations.availableProcessingBridge(cli.Kind) {
			unconfigured = append(unconfigured, cli)
		}
	}
	return unconfigured
}

func (g GenerationDirectory) row(ref *GenerationRef) (GenerationRow, bool) {
	if ref == nil {
		return GenerationRow{}, false
	}
	for _, row := range g.Profiles[ref.ID] {
		if row.Revision == ref.Revision {
			return row, true
		}
	}
	return GenerationRow{}, false
}

func (g GenerationDirectory) validated(ref *GenerationRef) bool {
	row, ok := g.row(ref)
	if !ok {
		return false
	}
	var latest int64
	for _, v := range g.Validations {
		if v.Digest == row.Digest && v.RegisteredRevision > latest {
			latest = v.RegisteredRevision
		}
	}
	found := false
	for _, v := range g.Validations {
		if v.Digest != row.Digest || v.RegisteredRevision != latest {
			continue
		}
		found = true
		if v.Status != "passed" || !v.Plain || !v.Schema || !v.Quality {
			return false
		}
		if row.Profile.Mode == "agent_bridge" {
			if actual, present := g.AgentDefaults[row.Profile.BridgeKind]; present &&
				(v.AgentDefaults == nil || actual != *v.AgentDefaults) {
				return false
			}
		}
	}
	return found
}

func (g GenerationDirectory) latestValidation(digest string) (string, GenerationValidation) {
	if digest == "" {
		return "", GenerationValidation{}
	}
	var latest int64
	for _, row := range g.Validations {
		if row.Digest == digest && row.RegisteredRevision > latest {
			latest = row.RegisteredRevision
		}
	}
	operation := ""
	var result GenerationValidation
	for id, row := range g.Validations {
		if row.Digest != digest || row.RegisteredRevision != latest {
			continue
		}
		if operation != "" {
			return "", GenerationValidation{}
		}
		operation, result = id, row
	}
	return operation, result
}
