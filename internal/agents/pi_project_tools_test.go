//go:build darwin || linux

package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

func TestPiProjectToolsAreScopedAuthenticatedAndFinishIsReadBarrier(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("Synthetic repository contract"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".git", ".pi"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "auth.json"), []byte("not-readable"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	source, err := openProjectSourceReader(root)
	if err != nil {
		t.Fatal(err)
	}
	defer source.root.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	knowledge := newProjectContextReader(func(context.Context, string) (records.ProjectKnowledgeContextPage, error) {
		return records.ProjectKnowledgeContextPage{Total: 0}, nil
	})
	tools, err := newPiProjectTools(ctx, source, knowledge)
	if err != nil {
		t.Fatal(err)
	}
	defer tools.close()
	call := func(q piProjectToolRequest, token string) (int, string) {
		t.Helper()
		b, _ := json.Marshal(q)
		req, e := http.NewRequest("POST", tools.endpoint, bytes.NewReader(b))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		body, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		return res.StatusCode, string(body)
	}
	if code, _ := call(piProjectToolRequest{Tool: "memlane_project_tree"}, "wrong"); code != 403 {
		t.Fatal("unauthenticated access", code)
	}
	if code, _ := call(piProjectToolRequest{Tool: "memlane_project_finish"}, tools.token); code == 200 {
		t.Fatal("finish without reads accepted")
	}
	code, body := call(piProjectToolRequest{Tool: "memlane_project_tree"}, tools.token)
	if code != 200 || !strings.Contains(body, "README.md") || strings.Contains(body, "auth.json") || strings.Contains(body, ".git") || strings.Contains(body, "outside") {
		t.Fatal("unsafe tree", code, body)
	}
	if code, _ := call(piProjectToolRequest{Tool: "memlane_project_knowledge"}, tools.token); code != 200 {
		t.Fatal("knowledge read failed")
	}
	for _, path := range []string{"../README.md", "outside/file", ".pi/auth.json", "auth.json"} {
		if code, _ := call(piProjectToolRequest{Tool: "memlane_project_source", Path: path}, tools.token); code == 200 {
			t.Fatal("unsafe source accepted", path)
		}
	}
	code, body = call(piProjectToolRequest{Tool: "memlane_project_source", Path: "README.md"}, tools.token)
	if code != 200 || !strings.Contains(body, "Synthetic repository contract") || len(source.sources) != 1 {
		t.Fatal("real source missing", code, body)
	}

	if code, _ := call(piProjectToolRequest{Tool: "memlane_project_knowledge"}, tools.token); code != 200 {
		t.Fatal("knowledge read failed")
	}
	if code, _ := call(piProjectToolRequest{Tool: "memlane_project_finish"}, tools.token); code != 200 {
		t.Fatal("finish failed after complete reads")
	}
	if code, _ := call(piProjectToolRequest{Tool: "memlane_project_source", Path: "README.md"}, tools.token); code != 409 {
		t.Fatal("source released after read barrier", code)
	}
}
