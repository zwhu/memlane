package codegraph

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Contract fixtures mirror captured Enola v351 root facts, including its shared
// TS/Python module ID. Actual extractor/worker acceptance remains separate.
func TestRootModuleSharedLanguageIdentityAndStrictSources(t *testing.T) {
	dataset := uuid.New()
	sha := strings.Repeat("a", 64)
	m := Manifest{Worktree: t.TempDir(), Recipe: Recipe, Digest: sha, Files: []File{{Path: "sample.ts", SHA256: sha, Lines: 1}, {Path: "sample.py", SHA256: sha, Lines: 2}}}
	module := func(language string) fact {
		return fact{ID: "2bd4a5dc07b9b3fde5eeae24c3f29a59", Kind: "module", Name: ".", File: ".", Props: map[string]any{"language": language}}
	}
	syms := []fact{
		{ID: "ded62daa9f6c27225f5c68fb41e540f8", Kind: "symbol", Name: "..upgradeGraph", File: "sample.ts", Line: 1, Props: map[string]any{"language": "typescript", "symbol_kind": "function"}},
		{ID: "cfec643101df32d5c667810211b3bf8c", Kind: "symbol", Name: "sample.upgrade_graph", File: "sample.py", Line: 1, Props: map[string]any{"language": "python", "symbol_kind": "function"}},
	}
	facts := append([]fact{module("python"), module("typescript")}, syms...)
	g, err := normalize(dataset, m, facts)
	if err != nil {
		t.Fatal(err)
	}
	roots, symbols := 0, 0
	for _, n := range g.Nodes {
		if n.Kind == "module" {
			roots++
			if n.File != "." || n.Name != "." || n.Language != "" || n.FileSHA != "" || n.ExtractorID != facts[0].ID {
				t.Fatal("shared module identity lost", n)
			}
		}
		if n.Kind == "symbol" {
			symbols++
			if n.FileSHA != sha {
				t.Fatal("symbol source SHA lost", n)
			}
		}
	}
	if roots != 1 || symbols != 2 {
		t.Fatal(roots, symbols)
	}
	bad := module("typescript")
	bad.Props["symbol_kind"] = "conflicting"
	if _, err := normalize(dataset, m, []fact{module("python"), bad}); err == nil {
		t.Fatal("conflicting module merged")
	}
	if _, err := normalize(dataset, m, []fact{syms[0], syms[0]}); err == nil {
		t.Fatal("duplicate symbols accepted")
	}
	bad = syms[0]
	bad.File = "."
	if _, err := normalize(dataset, m, []fact{bad}); err == nil {
		t.Fatal("dot accepted as a source file")
	}
	if _, err := normalize(dataset, Manifest{Recipe: Recipe, Digest: sha}, []fact{module("typescript")}); err == nil {
		t.Fatal("root module without source accepted")
	}
}
