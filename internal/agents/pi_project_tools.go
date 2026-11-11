//go:build darwin || linux

package agents

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// A task-private read boundary, never the daily memory bridge. Only the trusted
// ephemeral pi Extension receives its capability; no user session is loaded.
// The source reader retains directory descriptors and rejects symlink escapes.
type piProjectTools struct {
	config           *piProjectConfiguration
	ctx              context.Context
	source           *projectSourceReader
	knowledge        *projectContextReader
	token, endpoint  string
	server           *http.Server
	mu               sync.Mutex
	finished, listed bool
}

type piProjectToolRequest struct {
	Tool   string `json:"tool"`
	Path   string `json:"path,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

func newPiProjectTools(ctx context.Context, source *projectSourceReader, knowledge *projectContextReader) (*piProjectTools, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	t := &piProjectTools{ctx: ctx, source: source, knowledge: knowledge, token: uuid.NewString(), endpoint: "http://" + listener.Addr().String() + "/tool"}
	t.server = &http.Server{Handler: http.HandlerFunc(t.handle), ReadHeaderTimeout: time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, MaxHeaderBytes: 4096}
	go func() { _ = t.server.Serve(listener) }()
	return t, nil
}
func (t *piProjectTools) close() {
	_ = t.server.Close()
	t.mu.Lock()
	t.mu.Unlock()
}
func (t *piProjectTools) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || r.URL.Path != "/tool" || r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+t.token)) != 1 {
		http.Error(w, "task capability required", http.StatusForbidden)
		return
	}
	var q piProjectToolRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8193))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&q) != nil {
		http.Error(w, "invalid task tool request", 400)
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		http.Error(w, "invalid task tool request", 400)
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ctx.Err() != nil || t.finished {
		http.Error(w, "task read phase ended", 409)
		return
	}
	var result any
	var err error
	switch q.Tool {
	case "memlane_project_tree":
		if q.Cursor != "" {
			err = errors.New("unexpected task cursor")
			break
		}
		result, err = t.source.tree(t.ctx, q.Path)
		if err == nil {
			t.listed = true
		}
	case "memlane_project_source":
		if q.Cursor != "" {
			err = errors.New("unexpected task cursor")
			break
		}
		if err = t.knowledge.requireComplete(); err != nil {
			break
		}
		result, err = t.source.read(t.ctx, q.Path)
	case "memlane_project_knowledge":
		if q.Path != "" {
			err = errors.New("unexpected task path")
			break
		}
		var body []byte
		body, err = t.knowledge.page(t.ctx, q.Cursor)
		result = json.RawMessage(body)
	case "memlane_project_finish":
		if q.Path != "" || q.Cursor != "" || !t.listed {
			err = errors.New("repository listing and completed knowledge context required")
			break
		}
		err = t.knowledge.requireComplete()
		if err == nil {
			t.finished = true
			result = map[string]any{"ready": true, "read_phase_ended": true}
		}
	default:
		err = errors.New("task tool not permitted")
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err = json.NewEncoder(w).Encode(result); err == nil && q.Tool == "memlane_project_knowledge" {
		t.knowledge.markSent(q.Cursor)
	}
}

// List a single directory through openat, not a path-based recursive scan.
// The Agent chooses further directories explicitly; resource limits fail closed.
func (r *projectSourceReader) tree(ctx context.Context, path string) ([]map[string]string, error) {
	if filepath.IsAbs(path) {
		relative, err := filepath.Rel(r.root.Name(), path)
		if err != nil {
			return nil, errors.New("repository directory is outside authorized root")
		}
		path = filepath.ToSlash(relative)
	}
	if path == "." {
		path = ""
	}
	if path != "" && !projectSourcePath(path) {
		return nil, errors.New("repository directory path is not permitted")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(r.root.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if path != "" {
		for _, part := range splitProjectDirectory(path) {
			next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			unix.Close(fd)
			if e != nil {
				return nil, errors.New("repository directory unavailable or symlinked")
			}
			fd = next
		}
	}
	directory := os.NewFile(uintptr(fd), path)
	defer directory.Close()
	entries, err := directory.ReadDir(4097)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > 4096 {
		return nil, errors.New("repository directory listing exceeded budget")
	}
	result := []map[string]string{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if path != "" {
			name = path + "/" + name
		}
		if !projectSourcePath(name) || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		kind := "file"
		if entry.IsDir() {
			kind = "directory"
		} else if !entry.Type().IsRegular() {
			continue
		}
		result = append(result, map[string]string{"path": name, "kind": kind})
	}
	sort.Slice(result, func(i, j int) bool { return result[i]["path"] < result[j]["path"] })
	return result, nil
}
func splitProjectDirectory(path string) []string { return strings.Split(path, "/") }

//go:embed pi-project-tools.ts
var piProjectToolExtension string

// The tool phase has no native output schema. An explicit host-acknowledged
// finish removes all tools, then the one final provider request uses the exact
// existing strict full-schema path. Model-only completion stays tool-free.
func (t *piProjectTools) extension(schemaExtension string) string {
	endpoint, _ := json.Marshal(t.endpoint)
	token, _ := json.Marshal(t.token)
	tools := strings.ReplaceAll(piProjectToolExtension, "__MEMLANE_PROJECT_ENDPOINT__", string(endpoint))
	tools = strings.ReplaceAll(tools, "__MEMLANE_PROJECT_TOKEN__", string(token))
	schemaExtension = strings.Replace(schemaExtension, "export default function (pi) {", "export default function (pi) {\n let projectFinalPhase = false;\n installProjectTools(pi, () => { projectFinalPhase = true; });", 1)
	schemaExtension = strings.Replace(schemaExtension, "const payload = event.payload;", `const payload = event.payload;
    if (!projectFinalPhase) {
      if (!payload || !Array.isArray(payload.tools) || payload.tools.length === 0 || payload.tools.some(tool => !projectToolNames.includes(tool.name ?? tool.function?.name))) {
        throw new Error("project tool payload must contain only its task read tools");
      }
      return payload;
    }`, 1)
	schemaExtension = strings.Replace(schemaExtension, `pi.on("after_provider_response", (event) => {`, `pi.on("after_provider_response", (event) => {
    if (!projectFinalPhase) return;`, 1)
	return tools + "\n" + schemaExtension
}

func (t *piProjectTools) phaseEnded() bool { t.mu.Lock(); defer t.mu.Unlock(); return t.finished }
