// memlane-bootstrap is the dev command for the native first-install core. It
// intentionally has no Python prerequisite and does not perform implicit installs.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/zwhu/memlane/engine"
	"github.com/zwhu/memlane/internal/bootstrap"
	"os"
	"runtime"
)

func main() {
	python := flag.String("python", "", "manually selected existing Python path")
	source := flag.String("source", "", "existing fixed Cognee source directory")
	archive := flag.String("archive", "", "existing official source archive")
	manifest := flag.String("manifest", "", "component manifest for source verification")
	lock := flag.String("lock", "", "fixed requirements.lock to check against selected existing Python")
	diagnose := flag.Bool("diagnose", false, "generate report and complete Agent prompt from installer JSON on stdin")
	inventory := flag.Bool("inventory", false, "detect the complete native component plan without installing")
	componentRoot := flag.String("component-root", "", "managed component destination (detection does not create it)")
	modelCache := flag.String("model-cache", "", "existing HF_HOME model cache to verify and reuse")
	plugin := flag.String("plugin", "", "existing fixed managed Agent plugin to verify and reuse")
	flag.Parse()
	if *inventory {
		environment, err := bootstrap.NewEnvironment(context.Background(), engine.BootstrapAssets, bootstrap.EnvironmentOptions{Root: *componentRoot, Python: *python, Source: *source, Archive: *archive, ModelCache: *modelCache, Plugin: *plugin})
		if err != nil {
			fatal(err)
		}
		plan, err := environment.Detect(context.Background())
		if err != nil {
			fatal(err)
		}
		emit(map[string]any{"plan": plan, "components_ready": plan.Complete(), "downloads_performed": false, "storage_initialized": false})
		return
	}
	if *diagnose {
		var input bootstrap.DiagnosticInput
		if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
			fatal(err)
		}
		report, prompt, err := bootstrap.Diagnostics(input)
		if err != nil {
			fatal(err)
		}
		emit(map[string]string{"report": report, "prompt": prompt})
		return
	}
	explicit := []string{}
	if *python != "" {
		explicit = append(explicit, *python)
	}
	observations := []bootstrap.Observation{}
	for _, p := range bootstrap.PythonCandidates(explicit) {
		o, err := bootstrap.ProbePython(context.Background(), p)
		if err != nil {
			o.Status, o.Reason = bootstrap.Incompatible, err.Error()
		}
		observations = append(observations, o)
	}
	result := map[string]any{"platform": runtime.GOOS + "/" + runtime.GOARCH, "python": observations, "downloads_performed": false, "storage_initialized": false}
	if *lock != "" {
		if *python == "" {
			fatal(fmt.Errorf("dependency verification requires an explicit --python selection"))
		}
		b, err := os.ReadFile(*lock)
		if err != nil {
			fatal(err)
		}
		o, err := bootstrap.ProbeDependencies(context.Background(), *python, b)
		if err != nil {
			fatal(err)
		}
		result["runtime_dependencies"] = o
	}
	if *source != "" || *archive != "" || *manifest != "" {
		if *source == "" || *archive == "" || *manifest == "" {
			fatal(fmt.Errorf("source verification requires --source, --archive and --manifest"))
		}
		var m struct {
			Service struct {
				Commit   string   `json:"commit"`
				SHA      string   `json:"archive_sha256"`
				Packages []string `json:"packages"`
			} `json:"service"`
		}
		b, err := os.ReadFile(*manifest)
		if err != nil {
			fatal(err)
		}
		if err := json.Unmarshal(b, &m); err != nil {
			fatal(err)
		}
		o := bootstrap.Observation{Status: bootstrap.Available, Path: *source, Version: m.Service.Commit, Fingerprint: m.Service.SHA}
		if err := bootstrap.VerifySource(*archive, *source, m.Service.SHA, m.Service.Packages); err != nil {
			o.Status, o.Reason = bootstrap.Incompatible, err.Error()
		}
		result["cognee_source"] = o
	}
	emit(result)
}
func emit(v any) {
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	if err := e.Encode(v); err != nil {
		fatal(err)
	}
}
func fatal(err error) {
	fmt.Fprintln(os.Stderr, bootstrap.RedactInstallerText(err.Error()))
	os.Exit(1)
}
