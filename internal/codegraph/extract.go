package codegraph

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
)

type Extraction struct {
	Manifest Manifest
	Graph    Graph
	Receipt  json.RawMessage
}
type ExtractOptions struct {
	GuardExecutable                          string
	Executable, Worktree, Directory, Dataset string
	DenyReads                                []string
}
type fact struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Name      string         `json:"name"`
	File      string         `json:"file"`
	Line      int            `json:"line"`
	Props     map[string]any `json:"props"`
	Relations []struct {
		Kind   string `json:"kind"`
		Target string `json:"target"`
		ID     string `json:"target_id"`
	} `json:"relations"`
}

// Extract uses only a fresh, private shadow. Enola receives no source repository
// configuration and cannot write the project or access its files after capture.
func Extract(ctx context.Context, o ExtractOptions) (out Extraction, err error) {
	dataset, e := uuid.Parse(o.Dataset)
	if e != nil || !filepath.IsAbs(o.Directory) || !filepath.IsAbs(o.Executable) {
		return out, errors.New("explicit code extraction paths/dataset required")
	}
	root, _, e := SourceRoot(ctx, o.Worktree)
	if e != nil {
		return out, e
	}
	o.Directory, e = privateDirectory(root, o.Directory)
	if e != nil {
		return out, e
	}
	if _, e = os.Stat(o.Directory); !os.IsNotExist(e) {
		return out, errors.New("code extraction directory must be new")
	}
	if e = os.MkdirAll(o.Directory, 0700); e != nil {
		return out, e
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(o.Directory)
		}
	}()
	shadow := filepath.Join(o.Directory, "shadow")
	home := filepath.Join(o.Directory, "home")
	if e = os.Mkdir(home, 0700); e != nil {
		return out, e
	}
	m, e := Capture(ctx, o.Worktree, shadow)
	if e != nil {
		return out, e
	}
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	profile := "(version 1)(allow default)(deny network*)(deny file-write*)(allow file-write* (subpath " + quote(o.Directory) + "))(allow file-write* (literal \"/dev/null\"))(deny file-read* (subpath " + quote(m.Worktree) + "))"
	for _, p := range o.DenyReads {
		if !filepath.IsAbs(p) {
			return out, errors.New("absolute denied data path required")
		}
		canonical, e := filepath.EvalSymlinks(p)
		if e != nil {
			parent, e := filepath.EvalSymlinks(filepath.Dir(p))
			if e != nil {
				return out, e
			}
			canonical = filepath.Join(parent, filepath.Base(p))
		}
		p = canonical
		profile += "(deny file-read* (subpath " + quote(p) + "))"
	}
	run := func(args ...string) ([]byte, error) {
		limited, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		executable := "/usr/bin/sandbox-exec"
		arguments := append([]string{"-p", profile, o.Executable}, args...)
		var controlRead, controlWrite *os.File
		if o.GuardExecutable != "" {
			if !filepath.IsAbs(o.GuardExecutable) {
				return nil, errors.New("absolute extractor guard required")
			}
			var e error
			controlRead, controlWrite, e = os.Pipe()
			if e != nil {
				return nil, e
			}
			defer controlRead.Close()
			defer controlWrite.Close()
			arguments = append([]string{"--proposal-guard", executable}, arguments...)
			executable = o.GuardExecutable
		}
		cmd := exec.CommandContext(limited, executable, arguments...)
		if controlRead != nil {
			cmd.ExtraFiles = []*os.File{controlRead}
		}
		cmd.Dir = shadow
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "TMPDIR=" + o.Directory, "ENOLA_NO_UPDATE_CHECK=1", "ENOLA_NO_PROMPTS=1"}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.WaitDelay = 2 * time.Second
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return nil
			}
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		stdout := &cappedOutput{limit: 1024 * 1024}
		stderr := &cappedOutput{limit: 1024 * 1024}
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		e := cmd.Start()
		if controlRead != nil {
			_ = controlRead.Close()
		}
		if e == nil {
			if controlWrite != nil {
				if e = limited.Err(); e == nil {
					_, e = controlWrite.Write([]byte{'S'})
				}
				if e != nil {
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				}
			}
			waitErr := cmd.Wait()
			if e == nil {
				e = waitErr
			}
		}
		if cmd.Process != nil {
			kill := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			if kill != nil && kill != syscall.ESRCH {
				return nil, kill
			}
			until := time.Now().Add(2 * time.Second)
			for syscall.Kill(-cmd.Process.Pid, 0) != syscall.ESRCH {
				if time.Now().After(until) {
					return nil, errors.New("extractor process group cleanup failed")
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		if e != nil {
			return nil, fmt.Errorf("Enola process failed: %w", e)
		}
		return stdout.Bytes(), nil
	}
	v, e := run("--version", "--json")
	if e != nil {
		return out, e
	}
	var version struct {
		Version          string `json:"version"`
		ExtractorVersion string `json:"extractor_version"`
	}
	if json.Unmarshal(v, &version) != nil || version.Version != "0.4.30" || version.ExtractorVersion != "v351" {
		return out, errors.New("unsupported Enola version")
	}
	if _, e = run("--generate", shadow); e != nil {
		return out, e
	}
	r, e := readBounded(filepath.Join(shadow, ".enola/receipt.json"), 1024*1024)
	if e != nil {
		return out, e
	}
	var receipt struct {
		Version   string            `json:"enola_version"`
		Extractor string            `json:"extractor_version"`
		Hashes    map[string]string `json:"output_hashes"`
		Quality   struct {
			ParseErrors int `json:"parse_errors"`
		} `json:"quality"`
	}
	if json.Unmarshal(r, &receipt) != nil || receipt.Version != "0.4.30" || receipt.Extractor != "v351" || receipt.Quality.ParseErrors != 0 {
		return out, errors.New("Enola incomplete or invalid receipt")
	}
	raw, e := readBounded(filepath.Join(shadow, ".enola/facts.jsonl"), MaxBytes)
	if e != nil {
		return out, e
	}
	h := sha256.Sum256(raw)
	if receipt.Hashes["facts.jsonl"] != "sha256:"+hex.EncodeToString(h[:]) {
		return out, errors.New("Enola facts hash mismatch")
	}
	facts := []fact{}
	scan := bufio.NewScanner(strings.NewReader(string(raw)))
	scan.Buffer(make([]byte, 4096), 1024*1024)
	for scan.Scan() {
		if len(facts) >= 20000 {
			return out, errors.New("code fact limit exceeded")
		}
		var f fact
		if json.Unmarshal(scan.Bytes(), &f) != nil {
			return out, errors.New("invalid code fact")
		}
		facts = append(facts, f)
	}
	if e = scan.Err(); e != nil {
		return out, e
	}
	graph, e := normalize(dataset, m, facts)
	if e != nil {
		return out, e
	}
	current, e := Capture(ctx, o.Worktree, "")
	if e != nil {
		return out, e
	}
	if current.Digest != m.Digest {
		return out, errors.New("code source changed during extraction")
	}
	return Extraction{Manifest: m, Graph: graph, Receipt: r}, nil
}
func readBounded(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("code output exceeds limit")
	}
	return b, e
}

func normalize(dataset uuid.UUID, m Manifest, facts []fact) (Graph, error) {
	g := Graph{Dataset: dataset.String(), Snapshot: uuid.NewSHA1(dataset, []byte(m.Digest)).String(), Manifest: m, Extractor: "enola-0.4.30-v351", Nodes: []Node{}, Edges: []Edge{}}
	id := func(s string) string { return uuid.NewSHA1(dataset, []byte(s)).String() }
	files := map[string]File{}
	nodes := map[string]Node{}
	factIDs := map[string]string{}
	names := map[string][]string{}
	modules := map[string]fact{}
	for _, f := range m.Files {
		files[f.Path] = f
		n := Node{ID: id("file:" + f.Path), Name: f.Path, Kind: "file", File: f.Path, Line: 1, FileSHA: f.SHA256}
		switch filepath.Ext(f.Path) {
		case ".py":
			n.Language = "python"
		case ".ts", ".tsx":
			n.Language = "typescript"
		}
		nodes[n.ID] = n
	}
	for _, f := range facts {
		rootModule := f.Kind == "module" && f.Name == "." && f.File == "." && f.Line == 0
		if f.ID == "" || len(f.ID) > 128 || len(f.Name) > 1024 || controls(f.Name) || (!safePath(f.File) && !rootModule) {
			return g, errors.New("invalid code fact identity/path")
		}
		if previous := factIDs[f.ID]; previous != "" {
			old, ok := modules[f.ID]
			if !ok || !sameSharedModule(old, f) {
				return g, errors.New("conflicting duplicate code fact")
			}
			n := nodes[previous]
			language, _ := f.Props["language"].(string)
			if n.Language != language {
				n.Language = ""
			}
			nodes[previous] = n
			continue
		}
		if f.Kind == "file_ref" {
			if _, ok := files[f.File]; !ok {
				return g, errors.New("file fact absent from snapshot")
			}
			factIDs[f.ID] = id("file:" + f.File)
			fileNode := nodes[factIDs[f.ID]]
			fileNode.ExtractorID = f.ID
			nodes[fileNode.ID] = fileNode
			continue
		}
		if f.Kind != "symbol" && f.Kind != "module" && f.Kind != "dependency" {
			return g, fmt.Errorf("unsupported code fact kind: %s", f.Kind)
		}
		file, ok := files[f.File]
		if !ok && f.Kind != "module" {
			return g, errors.New("code fact absent from source snapshot")
		}
		if !ok {
			found := rootModule && len(files) > 0
			for p := range files {
				if strings.HasPrefix(p, f.File+"/") {
					found = true
					break
				}
			}
			if !found {
				return g, errors.New("module absent from snapshot")
			}
		}
		if f.Line < 0 || (ok && f.Line > file.Lines) {
			return g, errors.New("code fact line outside source")
		}
		language, _ := f.Props["language"].(string)
		symbol, _ := f.Props["symbol_kind"].(string)
		if language != "typescript" && language != "python" {
			return g, errors.New("unsupported fact language")
		}
		n := Node{ExtractorID: f.ID, ID: id("fact:" + f.ID), Name: f.Name, Kind: f.Kind, File: f.File, Line: f.Line, Language: language, SymbolKind: symbol, FileSHA: file.SHA256}
		nodes[n.ID] = n
		factIDs[f.ID] = n.ID
		if f.Kind == "module" {
			modules[f.ID] = f
		}
		names[f.Name] = append(names[f.Name], n.ID)
	}
	edges := map[string]Edge{}
	add := func(a, b, k string) { e := Edge{a, b, k}; edges[a+":"+b+":"+k] = e }
	for _, n := range nodes {
		if n.Kind != "file" && n.Kind != "module" {
			add(id("file:"+n.File), n.ID, "contains")
		}
	}
	for _, f := range facts {
		for _, r := range f.Relations {
			if r.Kind != "calls" && r.Kind != "imports" && r.Kind != "declares" {
				return g, errors.New("unsupported code relation")
			}
			target := factIDs[r.ID]
			if target == "" && len(names[r.Target]) == 1 {
				target = names[r.Target][0]
			}
			if target == "" && r.Kind == "imports" {
				path := strings.TrimSuffix(r.Target, ".js")
				for _, p := range []string{r.Target, path + ".ts", path + ".tsx", r.Target + ".py", r.Target + "/index.ts", r.Target + "/__init__.py"} {
					if _, ok := files[p]; ok {
						target = id("file:" + p)
						break
					}
				}
			}
			if target == "" {
				if r.Target == "" || len(r.Target) > 1024 || controls(r.Target) {
					return g, errors.New("invalid unresolved target")
				}
				target = id("external:" + r.Target)
				nodes[target] = Node{ID: target, Name: r.Target, Kind: "external", Unresolved: true}
			}
			add(factIDs[f.ID], target, r.Kind)
		}
	}
	if len(nodes) > 20000 || len(edges) > 60000 {
		return g, errors.New("code graph exceeds node/edge budget")
	}
	for _, n := range nodes {
		g.Nodes = append(g.Nodes, n)
	}
	for _, e := range edges {
		g.Edges = append(g.Edges, e)
	}
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.Slice(g.Edges, func(i, j int) bool {
		a, b := g.Edges[i], g.Edges[j]
		return a.Source+a.Target+a.Kind < b.Source+b.Target+b.Kind
	})
	return g, nil
}

func sameSharedModule(a, b fact) bool {
	if a.Kind != "module" || b.Kind != "module" || a.Name != b.Name || a.File != b.File || a.Line != b.Line || !reflect.DeepEqual(a.Relations, b.Relations) {
		return false
	}
	copyProps := func(f fact) (map[string]any, bool) {
		language, _ := f.Props["language"].(string)
		if language != "typescript" && language != "python" {
			return nil, false
		}
		out := map[string]any{}
		for k, v := range f.Props {
			if k != "language" {
				out[k] = v
			}
		}
		return out, true
	}
	x, ok := copyProps(a)
	if !ok {
		return false
	}
	y, ok := copyProps(b)
	return ok && reflect.DeepEqual(x, y)
}
