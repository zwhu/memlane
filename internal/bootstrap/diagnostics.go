package bootstrap

import (
	"github.com/zwhu/memlane/core/setup"
	"github.com/zwhu/memlane/kit/aiinstaller"
)

type DiagnosticInput struct {
	Operation    string   `json:"operation"`
	Issue        string   `json:"issue"`
	Stage        string   `json:"stage"`
	Platform     string   `json:"platform"`
	ExitCode     *int     `json:"exit_code"`
	Error        string   `json:"error"`
	InstallerLog string   `json:"installer_log"`
	Actions      []string `json:"attempted_actions"`
	Journal      *Journal `json:"installation,omitempty"`
	Plan         *Plan    `json:"detection,omitempty"`
}

func (input DiagnosticInput) toInstallerInput() aiinstaller.DiagnosticInput {
	out := aiinstaller.DiagnosticInput{Operation: input.Operation, Issue: input.Issue,
		Stage: input.Stage, Platform: input.Platform, ExitCode: input.ExitCode,
		Error: input.Error, InstallerLog: input.InstallerLog, Actions: input.Actions}
	if input.Plan != nil {
		p := input.Plan.toInstallerPlan()
		out.Plan = &p
	}
	if input.Journal != nil {
		j := input.Journal.toInstallerJournal()
		out.Journal = &j
	}
	return out
}

func RedactInstallerText(s string) string { return aiinstaller.RedactText(s) }

func Diagnostics(input DiagnosticInput) (string, string, error) {
	return setup.Diagnostics(input.toInstallerInput())
}
func DiagnosticArchive(input DiagnosticInput) ([]byte, error) {
	return setup.DiagnosticArchive(input.toInstallerInput())
}
func ExportDiagnostic(path string, input DiagnosticInput) error {
	return setup.ExportDiagnostic(path, input.toInstallerInput())
}
