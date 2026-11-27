package memlane

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const architectureModule = "github.com/zwhu/memlane"

func checkGoFileArchitecture(relPath string, src []byte) []string {
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, relPath, src, parser.ImportsOnly)
	if err != nil {
		return []string{err.Error()}
	}

	var violations []string
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		position := files.PositionFor(spec.Path.Pos(), false)
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s: invalid import: %v", position, err))
			continue
		}
		inPackage := func(pkg string) bool {
			prefix := architectureModule + "/" + pkg
			return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
		}
		var reason string
		switch {
		case path.Dir(relPath) == "app/settings" && !settingsImportAllowed(importPath):
			reason = "settings dependencies must stay inside its host and transport boundaries"
		case path.Dir(relPath) == "app/shell" && inPackage("app"):
			reason = "shell cannot depend on application controllers"
		case crossesAppFeatureBoundary(relPath, importPath):
			reason = "application features cannot depend on sibling features or the composition root"
		case inPackage("core/engine/cognee") &&
			!strings.HasPrefix(relPath, "core/engine/cognee/") &&
			path.Dir(relPath) != "core/engine" && !strings.HasPrefix(relPath, "cmd/"):
			reason = "Cognee imports are restricted to the adapter, engine factory, and cmd"
		case strings.HasPrefix(relPath, "kit/") &&
			(importPath == architectureModule || inPackage("core") || inPackage("app") || inPackage("internal")):
			reason = "kit cannot depend on Memlane business packages"
		case strings.HasPrefix(relPath, "core/") && (inPackage("app") || inPackage("kit/ui")):
			reason = "core cannot depend on UI packages"
		case strings.HasPrefix(relPath, "core/engine/") && inPackage("core/domain"):
			reason = "engine cannot depend on domain"
		}
		if reason != "" {
			violations = append(violations, fmt.Sprintf("%s: %s: %q", position, reason, importPath))
		}
	}
	return violations
}

func appFeature(name string) string {
	rest, ok := strings.CutPrefix(name, "app/")
	if !ok {
		return ""
	}
	feature, _, _ := strings.Cut(rest, "/")
	switch feature {
	case "overview", "reading", "search", "graph", "settings":
		return feature
	default:
		return ""
	}
}

func crossesAppFeatureBoundary(relPath, importPath string) bool {
	source := appFeature(path.Dir(relPath))
	if source == "" {
		return false
	}
	localPath, ok := strings.CutPrefix(importPath, architectureModule+"/")
	if !ok {
		return false
	}
	target := appFeature(localPath)
	return localPath == "app" || (target != "" && source != target)
}

func settingsImportAllowed(importPath string) bool {
	switch importPath {
	case architectureModule + "/app/api",
		architectureModule + "/app/shell",
		architectureModule + "/kit/ui",
		architectureModule + "/kit/agents",
		architectureModule + "/kit/aiinstaller",
		architectureModule + "/core/setup",
		architectureModule + "/internal/bootstrap",
		architectureModule + "/internal/controlplane",
		"github.com/egoist/mygo",
		"github.com/egoist/mygo/ui",
		"github.com/egoist/mygo/ui/markdown":
		return true
	}
	if strings.Contains(strings.SplitN(importPath, "/", 2)[0], ".") {
		return false
	}
	pkg, err := build.Default.Import(importPath, "", build.FindOnly)
	return err == nil && pkg.Goroot
}

var (
	sqliteWAL          = regexp.MustCompile(`(?i)journal_mode\s*=\s*['"]?wal['"]?\b|journal_mode\s*\(\s*['"]?wal['"]?\s*\)`)
	sqliteWALSizeLimit = regexp.MustCompile(`(?i)journal_size_limit\b`)
)

func isTierAGoDirectory(relPath string) bool {
	dir, _, _ := strings.Cut(relPath, "/")
	switch dir {
	case "kit", "core", "app", "internal", "cmd":
		return true
	default:
		return false
	}
}

func isTierAGoProduction(relPath string) bool {
	return isTierAGoDirectory(relPath) &&
		strings.HasSuffix(relPath, ".go") && !strings.HasSuffix(relPath, "_test.go")
}

func checkGoFileSQLiteWAL(relPath string, src []byte) []string {
	if !isTierAGoProduction(relPath) {
		return nil
	}
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, relPath, src, 0)
	if err != nil {
		return []string{err.Error()}
	}

	var walPos token.Pos
	hasLimit := false
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.BasicLit:
			if n.Kind == token.STRING {
				value, err := strconv.Unquote(n.Value)
				if err == nil {
					if !walPos.IsValid() && sqliteWAL.MatchString(value) {
						walPos = n.Pos()
					}
					hasLimit = hasLimit || sqliteWALSizeLimit.MatchString(value)
				}
			}
		case *ast.CallExpr:
			var name string
			switch fn := ast.Unparen(n.Fun).(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			}
			hasLimit = hasLimit || name == "OpenPrivateSQLite" || name == "OpenSQLite"
		}
		return !hasLimit
	})
	if walPos.IsValid() && !hasLimit {
		return []string{fmt.Sprintf("%s: SQLite WAL requires journal_size_limit or a shared SQLite opener",
			files.PositionFor(walPos, false))}
	}
	return nil
}

func walkGoGuardFiles(t *testing.T, skipDir, checkFile func(string) bool, check func(string, []byte) []string) {
	t.Helper()
	err := filepath.WalkDir(".", func(filename string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relPath := filepath.ToSlash(filename)
		if entry.IsDir() {
			if relPath != "." && skipDir(relPath) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(filename) != ".go" || !checkFile(relPath) {
			return nil
		}
		src, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		for _, violation := range check(relPath, src) {
			t.Error(violation)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk Go files: %v", err)
	}
}

func TestArchitectureImportBoundaries(t *testing.T) {
	walkGoGuardFiles(t, func(dir string) bool {
		switch path.Base(dir) {
		case ".git", ".local", ".cache", "build", "vendor", "node_modules", "docs",
			"testdata", "web", "assets", "scripts", "engines":
			return true
		case "engine":
			// core/engine is the in-repo Go engine layer.
			return dir == "engine"
		default:
			return false
		}
	}, func(file string) bool {
		return file != "arch_guard_test.go"
	}, checkGoFileArchitecture)
}

func TestGoSQLiteWALJournalSizeLimit(t *testing.T) {
	walkGoGuardFiles(t, func(dir string) bool {
		return !isTierAGoDirectory(dir)
	}, isTierAGoProduction, checkGoFileSQLiteWAL)
}

func TestArchitectureGuardSelfTest(t *testing.T) {
	const module = architectureModule
	const cognee = module + "/core/engine/cognee"
	for _, tc := range []struct {
		file, imported string
		blocked        bool
	}{
		{"kit/example/file.go", module, true},
		{"kit/example/file.go", module + "/core", true},
		{"kit/example/file.go", module + "/core/domain", true},
		{"kit/example/file.go", module + "/app", true},
		{"kit/example/file.go", module + "/app/shell", true},
		{"kit/example/file.go", module + "/internal", true},
		{"kit/example/file.go", module + "/internal/bootstrap", true},
		{"kit/example/file.go", module + "/kit/ui", false},
		{"kit/example/file.go", module + "/coreutils", false},
		{"kit/example/file.go", module + "-other/core", false},
		{"core/domain/file.go", module + "/app", true},
		{"core/domain/file.go", module + "/app/shell", true},
		{"core/domain/file.go", module + "/kit/ui", true},
		{"core/domain/file.go", module + "/kit/ui/theme", true},
		{"core/domain/file.go", module + "/kit/uikit", false},
		{"core/domain/file.go", cognee, true},
		{"core/domain/file.go", module + "/core/engine/cogneeother", false},
		{"core/gateway/file.go", cognee + "/client", true},
		{"core/engine/other/file.go", cognee, true},
		{"app/shell/file.go", cognee, true},
		{"app/shell/contracts.go", module + "/app/settings", true},
		{"app/shell/contracts.go", module + "/app/api", true},
		{"app/shell/contracts.go", module + "/app", true},
		{"app/overview/controller.go", module + "/app/reading", true},
		{"app/reading/common_test.go", module + "/app/search", true},
		{"app/search/controller.go", module + "/app/graph", true},
		{"app/graph/controller.go", module + "/app/settings", true},
		{"app/overview/controller.go", module + "/app", true},
		{"app/settings/controller.go", module + "/app", true},
		{"app/reading/nested/file.go", module + "/app/search/nested", true},
		{"app/graph/controller.go", module + "/app/shell", false},
		{"app/overview/controller.go", module + "/app/api", false},
		{"app/app.go", module + "/app/settings", false},
		{"app/app.go", module + "/app/overview", false},
		{"app/app.go", module + "/app/graph", false},
		{"app/settings/controller.go", module, true},
		{"app/settings/controller.go", module + "/app/overview", true},
		{"app/settings/controller.go", module + "/app/reading", true},
		{"app/settings/controller.go", module + "/app/search", true},
		{"app/settings/controller.go", module + "/app/graph", true},
		{"app/settings/controller.go", cognee, true},
		{"app/settings/controller.go", module + "/app/shell", false},
		{"app/settings/controller.go", module + "/kit/ui", false},
		{"app/settings/controller_test.go", "net/http/httptest", false},
		{"app/settings/controller.go", "github.com/egoist/mygo/ui/markdown", false},
		{"app/settings/controller.go", "github.com/egoist/mygo/cli", true},
		{"kit/example/file.go", cognee, true},
		{"internal/example/file.go", cognee, true},
		{"root_test.go", cognee, true},
		{"core/engine/cognee_extra/file.go", cognee, true},
		{"core/engine/cognee/file.go", cognee + "/client", false},
		{"core/engine/cognee/client/file.go", cognee, false},
		{"core/engine/factory.go", cognee, false},
		{"core/engine/capabilities.go", module + "/core/domain", true},
		{"core/engine/cognee/projector.go", module + "/core/domain", true},
		{"core/engine/cognee/projector_test.go", module + "/core/domain", true},
		{"core/engine/cognee/adapter.go", module + "/core/engine", false},
		{"cmd/memlane/file.go", cognee + "/client", false},
	} {
		t.Run(tc.file+"/"+tc.imported, func(t *testing.T) {
			src := fmt.Appendf(nil, "package example\nimport (\n alias %q // trailing comment\n)\n", tc.imported)
			if got := checkGoFileArchitecture(tc.file, src); (len(got) > 0) != tc.blocked {
				t.Errorf("blocked = %t, want %t, violations = %v", len(got) > 0, tc.blocked, got)
			}
		})
	}
	for _, tc := range []struct {
		name, file, body string
		blocked          bool
	}{
		{"assignment", "kit/data/db.go", `var _ = "journal_mode=WAL"`, true},
		{"raw literal", "core/domain/db.go", "var _ = `PRAGMA Journal_Mode = 'WaL';`", true},
		{"parenthesized", "app/db.go", `var _ = "journal_mode( \"WAL\" )"`, true},
		{"escaped literal", "internal/db.go", `var _ = "journal_mode=\x57AL"`, true},
		{"comment limit", "cmd/main.go", "var _ = \"journal_mode=WAL\" // journal_size_limit", true},
		{"size limit prefix", "cmd/main.go", `var _, _ = "journal_mode=WAL", "journal_size_limited"`, true},
		{"reference only", "cmd/main.go", `var _ = "journal_mode=WAL"; var _ = OpenPrivateSQLite`, true},
		{"opener name in string", "cmd/main.go", `var _, _ = "journal_mode=WAL", "OpenSQLite"`, true},
		{"different opener", "cmd/main.go", `func f() { differentOpenSQLite("journal_mode=WAL") }`, true},
		{"size limit", "core/domain/db.go", `var _, _ = "journal_mode=WAL", "PRAGMA JOURNAL_SIZE_LIMIT=4194304"`, false},
		{"direct opener", "core/domain/db.go", `func f() { OpenPrivateSQLite("journal_mode=WAL") }`, false},
		{"selector opener", "core/domain/db.go", `func f() { domain.OpenSQLite("journal_mode(WAL)") }`, false},
		{"other mode", "core/domain/db.go", `var _ = "journal_mode=walrus"`, false},
		{"WAL comment", "core/domain/db.go", `// journal_mode=WAL`, false},
		{"Go test file", "core/domain/db_test.go", `var _ = "journal_mode=WAL"`, false},
		{"outside Tier A", "root.go", `var _ = "journal_mode=WAL"`, false},
	} {
		t.Run("WAL/"+tc.name, func(t *testing.T) {
			src := []byte("package example\n" + tc.body + "\n")
			if got := checkGoFileSQLiteWAL(tc.file, src); (len(got) > 0) != tc.blocked {
				t.Errorf("blocked = %t, want %t, violations = %v", len(got) > 0, tc.blocked, got)
			}
		})
	}
}
