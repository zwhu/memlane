package memlane_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func resolveLintPython() string {
	candidate := filepath.Join(".local", "cognee-python", "bin", "python")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
		return candidate
	}
	return "python3"
}

func recordLintInputs(t *testing.T) {
	t.Helper()
	for _, root := range []string{"scripts", filepath.Join("engine", "python"), filepath.Join("internal", "simpledesktop")} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if entry.Name() == "__pycache__" {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(entry.Name())
			if ext == ".py" || ext == ".go" {
				if _, err := os.Stat(path); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk lint inputs under %s: %v", root, err)
		}
	}
}

func TestStaticLintGate(t *testing.T) {
	recordLintInputs(t)
	python := resolveLintPython()
	for _, args := range [][]string{
		{"scripts/lint.py"},
		{"-m", "unittest", "discover", "-s", "scripts", "-p", "test_*.py"},
	} {
		cmd := exec.Command(python, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v failed: %v\n%s", python, args, err, string(out))
		}
	}
}
