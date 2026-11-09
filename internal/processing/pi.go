// Package processing resolves trusted runtime dependencies separately from
// immutable saved configuration. A saved candidate grants no execution.
package processing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

type PiBinding struct {
	Spec                         records.ProcessingSpec
	Executable, AuthHome, Helper string
	AgentVersion, Dependency     string
}

// Dependency hashes only enter the service-owned capability ledger. Neither
// credential values nor original provider files are stored in it or returned.
func fileDigest(path string, optional bool) (string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) && optional {
		return "absent", nil
	}
	if err != nil {
		return "", records.MergeError("dependency_unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return "", records.MergeError("invalid_dependency")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, 1024*1024+1))
	if err != nil || n > 1024*1024 {
		return "", records.MergeError("invalid_dependency")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func ResolvePi(ctx context.Context, clientRoot, serviceRoot, helper string, spec records.ProcessingSpec) (PiBinding, error) {
	var out PiBinding
	id, err := uuid.Parse(spec.Connection)
	if err != nil || id.String() != spec.Connection || spec.Mode != "agent" || spec.Agent != "pi" || spec.Model == "" || spec.Provider == "" || spec.ConnectionGeneration < 1 {
		return out, records.MergeError("unsupported_processing")
	}
	if !filepath.IsAbs(clientRoot) || !filepath.IsAbs(serviceRoot) || !filepath.IsAbs(helper) {
		return out, records.MergeError("invalid_dependency")
	}
	i, err := agents.LoadInstallation(clientRoot, spec.Connection)
	if err != nil || i.Connection.ID != spec.Connection || i.Connection.Agent != "pi" ||
		!i.Connection.Enabled || i.Connection.Generation != spec.ConnectionGeneration ||
		i.ToolsetVersion != 2 ||
		i.VerifiedGeneration != spec.ConnectionGeneration || i.VerifiedAt == "" || i.VerificationError != "" ||
		i.Options.ClientRoot != clientRoot || i.Options.ServiceRoot != serviceRoot || i.Options.Bridge == "" ||
		!filepath.IsAbs(i.Options.PiDirectory) || !filepath.IsAbs(i.Options.AgentPath) {
		return out, records.MergeError("connection_verification_required")
	}
	if err = agents.CheckInstallation(i); err != nil {
		return out, records.MergeError("connection_changed")
	}
	probe := agents.Probe(ctx, "pi", i.Options.AgentPath)
	if probe.ConnectionError() != nil || probe.Version != "0.84.1" {
		return out, records.MergeError("unsupported_agent_version")
	}
	if spec.Provider != "deepseek" && spec.Provider != "ollama" {
		return out, records.MergeError("unsupported_schema_binding")
	}
	dependencies := []string{"memlane-pi-completion-v1", records.MergeDigest(spec), probe.Version,
		i.Options.AgentPath, i.Options.PiDirectory, helper, i.ExtensionHash, i.VerifiedAt}
	for _, name := range []string{"settings.json", "models.json", "auth.json"} {
		digest, err := fileDigest(filepath.Join(i.Options.PiDirectory, name), name != "settings.json")
		if err != nil {
			return out, err
		}
		dependencies = append(dependencies, name, digest)
	}
	out = PiBinding{Spec: spec, Executable: i.Options.AgentPath, AuthHome: i.Options.PiDirectory,
		Helper: helper, AgentVersion: probe.Version, Dependency: records.MergeDigest(dependencies)}
	return out, nil
}

// VerifyPi is a bounded synthetic capability probe, not fact-quality evidence
// or permission to ingest knowledge. Only the service owner may seal its result.
func VerifyPi(ctx context.Context, b PiBinding, onStart func(int) error, onEvent func(string)) (agents.PiCompletionExecution, error) {
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["marker","value"],"properties":{"marker":{"type":"string","const":"memlane-model-probe"},"value":{"type":"integer","const":7}}}`)
	return agents.RunPiCompletion(ctx, agents.PiCompletionOptions{Executable: b.Executable, AuthHome: b.AuthHome,
		Provider: b.Spec.Provider, Model: b.Spec.Model, GuardExecutable: b.Helper,
		SystemPrompt: "You are performing a synthetic model capability probe. Return the requested JSON only.",
		Prompt:       "Return marker memlane-model-probe and integer value 7. This probe contains no user knowledge.",
		OutputSchema: schema, OnStart: onStart, OnEvent: onEvent})
}
