//go:build darwin || linux

package agents

import (
	"bufio"
	"bytes"
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
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"golang.org/x/sys/unix"
)

const codexHistoryLogBytes = 16 * 1024 * 1024

var ErrCodexHistoryEmpty = errors.New("bound Codex has no session directory")
var ErrCodexHistoryChanged = errors.New("selected history changed; select its current version")

type codexHistoryMeta struct {
	Roots   []string        `json:"runtime_workspace_roots"`
	ID      string          `json:"id"`
	Cwd     string          `json:"cwd"`
	Version string          `json:"cli_version"`
	Source  json.RawMessage `json:"source"`
}
type codexHistoryRow struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

type CodexProjectHistory struct {
	root                       *os.File
	project, workspace, common string
	workspaceInfo, commonInfo  os.FileInfo
	directories                map[string]os.FileInfo
}

func historyRepository(ctx context.Context, cwd string) (string, error) {
	if !filepath.IsAbs(cwd) {
		return "", errors.New("absolute session workspace required")
	}
	canonical, err := filepath.EvalSymlinks(cwd)
	if err != nil || canonical != cwd {
		return "", errors.New("canonical session workspace required")
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/git", "-C", cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	b, err := cmd.Output()
	if err != nil {
		return "", errors.New("session repository attribution unavailable")
	}
	common := strings.TrimSpace(string(b))
	canonical, err = filepath.EvalSymlinks(common)
	if err != nil || canonical != common {
		return "", errors.New("canonical repository identity required")
	}
	return common, nil
}

// The daemon fixes directory/project/workspace from a verified installation and
// project grant. Callers never supply filesystem paths to a session read tool.
func OpenCodexProjectHistory(ctx context.Context, directory, project, workspace string) (*CodexProjectHistory, error) {
	if id, err := uuid.Parse(project); err != nil || id.String() != project {
		return nil, errors.New("canonical project required")
	}
	if !filepath.IsAbs(directory) {
		return nil, errors.New("absolute bound Codex directory required")
	}
	info, e := os.Stat(directory)
	if e != nil {
		return nil, e
	}
	if !info.IsDir() {
		return nil, errors.New("bound Codex configuration directory unavailable")
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	if canonical != directory {
		return nil, errors.New("canonical Codex history directory required")
	}
	fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	r := &CodexProjectHistory{root: os.NewFile(uintptr(fd), directory), project: project, workspace: workspace, directories: map[string]os.FileInfo{}}
	fail := func(e error) (*CodexProjectHistory, error) { r.Close(); return nil, e }
	for _, name := range []string{"sessions", "archived_sessions"} {
		if _, err = r.catalogDirectory(name); err != nil {
			return fail(err)
		}
	}
	if len(r.directories) == 0 {
		return fail(ErrCodexHistoryEmpty)
	}
	r.workspaceInfo, err = os.Stat(workspace)
	if err != nil {
		return fail(err)
	}
	r.common, err = historyRepository(ctx, workspace)
	if err != nil {
		return fail(err)
	}
	r.commonInfo, err = os.Stat(r.common)
	if err != nil {
		return fail(err)
	}
	return r, nil
}
func (r *CodexProjectHistory) Close() error { return r.root.Close() }

// Codex 0.167 archives into one flat directory, keeping the original bytes and
// filename. Pin each catalog's identity, including a newly created archive dir.
func (r *CodexProjectHistory) catalogDirectory(name string) (bool, error) {
	f, err := historyOpen(r.root, name, true)
	if errors.Is(err, os.ErrNotExist) && r.directories[name] == nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if previous := r.directories[name]; previous != nil && !os.SameFile(previous, info) {
		return false, errors.New("Codex history catalog directory changed")
	}
	r.directories[name] = info
	return true, nil
}

func (r *CodexProjectHistory) check(ctx context.Context, cwd string) error {
	current, err := os.Stat(r.workspace)
	if err != nil || !os.SameFile(current, r.workspaceInfo) {
		return errors.New("authorized repository changed")
	}
	common, err := historyRepository(ctx, cwd)
	if err != nil || common != r.common {
		return errors.New("session belongs to another repository")
	}
	current, err = os.Stat(common)
	if err != nil || !os.SameFile(current, r.commonInfo) {
		return errors.New("session repository identity changed")
	}
	current, err = os.Lstat(r.root.Name())
	opened, e := r.root.Stat()
	if err != nil || e != nil || !os.SameFile(current, opened) {
		return errors.New("Codex history directory changed")
	}
	for name := range r.directories {
		if _, err = r.catalogDirectory(name); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Open each component relative to the already-open directory. Symlinks and
// nonregular final files are rejected, including when replaced during lookup.
func historyOpen(root *os.File, path string, directory bool) (*os.File, error) {
	parts := strings.Split(path, "/")
	fd, err := unix.FcntlInt(root.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for i, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\x00") {
			unix.Close(fd)
			return nil, errors.New("invalid history path")
		}
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if i < len(parts)-1 || directory {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !directory && !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("regular history file required")
	}
	return f, nil
}

func historyDatePart(name string, depth int) bool {
	length, min, max := 2, 1, 31
	if depth == 0 {
		length, min, max = 4, 1000, 9999
	}
	if depth == 1 {
		max = 12
	}
	n, err := strconv.Atoi(name)
	return err == nil && len(name) == length && n >= min && n <= max
}
func historyFileID(name string) (string, bool) {
	if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") || len(name) < len("rollout-")+36+6 {
		return "", false
	}
	value := name[len(name)-42 : len(name)-6]
	id, err := uuid.Parse(value)
	return value, err == nil && id.String() == value
}

func (r *CodexProjectHistory) paths(ctx context.Context) ([]string, error) {
	var paths []string
	var walk func(string, int) error
	entries := 0
	walk = func(path string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var f *os.File
		var err error
		f, err = historyOpen(r.root, path, true)
		if err != nil {
			return err
		}
		defer f.Close()
		all, err := f.ReadDir(-1)
		if err != nil {
			return err
		}
		entries += len(all)
		if entries > 100000 {
			return errors.New("history directory exceeds catalog budget; nothing truncated")
		}
		for _, entry := range all {
			name := entry.Name()
			relative := name
			if path != "" {
				relative = path + "/" + name
			}
			if depth < 3 {
				if !historyDatePart(name, depth) {
					continue
				}
				if !entry.IsDir() {
					return errors.New("regular dated history directories required")
				}
				if err = walk(relative, depth+1); err != nil {
					return err
				}
			} else if _, ok := historyFileID(name); ok {
				paths = append(paths, relative)
				if len(paths) > 10000 {
					return errors.New("history catalog exceeds 10000 logs; nothing truncated")
				}
			}
		}
		return nil
	}
	for _, catalog := range []struct {
		name  string
		depth int
	}{{"sessions", 0}, {"archived_sessions", 3}} {
		present, err := r.catalogDirectory(catalog.name)
		if err != nil {
			return nil, err
		}
		if present {
			if err = walk(catalog.name, catalog.depth); err != nil {
				return nil, err
			}
			if _, err = r.catalogDirectory(catalog.name); err != nil {
				return nil, err
			}
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func historyMeta(f *os.File) (codexHistoryMeta, string, error) {
	var row codexHistoryRow
	var meta codexHistoryMeta
	line, err := bufio.NewReader(io.LimitReader(f, 65537)).ReadBytes('\n')
	if err != nil || len(line) > 65536 || json.Unmarshal(line, &row) != nil || row.Type != "session_meta" || json.Unmarshal(row.Payload, &meta) != nil {
		return meta, "", errors.New("session metadata unavailable")
	}
	if _, err = time.Parse(time.RFC3339Nano, row.Timestamp); err != nil {
		return meta, "", errors.New("session timestamp unavailable")
	}
	return meta, row.Timestamp, nil
}

func (r *CodexProjectHistory) read(ctx context.Context, path, id string) (records.ProjectRepositorySource, string, error) {
	var out records.ProjectRepositorySource
	f, err := historyOpen(r.root, path, false)
	if err != nil {
		return out, "unavailable", err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return out, "unavailable", err
	}
	meta, created, err := historyMeta(f)
	if err != nil {
		return out, "unattributed", err
	}
	if meta.ID != id {
		return out, "unattributed", errors.New("filename/session identity mismatch")
	}
	if err = r.check(ctx, meta.Cwd); err != nil {
		return out, "outside", err
	}
	for _, root := range meta.Roots {
		if err = r.check(ctx, root); err != nil {
			return out, "outside", errors.New("historical session spans another project")
		}
	}
	var source string
	if json.Unmarshal(meta.Source, &source) != nil || source != "cli" && source != "exec" && source != "vscode" && source != "appServer" || meta.Version != "0.167.0-alpha.3.2" {
		return out, "unsupported", errors.New("session source or CLI version not adapted")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return out, "unavailable", err
	}
	raw, err := io.ReadAll(io.LimitReader(f, codexHistoryLogBytes+1))
	if err != nil {
		return out, "unavailable", err
	}
	after, err := f.Stat()
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || len(raw) != int(before.Size()) {
		return out, "changed", ErrCodexHistoryChanged
	}
	if len(raw) > codexHistoryLogBytes {
		return out, "too_large", errors.New("session log exceeds 16 MiB; nothing truncated")
	}
	text, err := r.transcript(ctx, raw, meta)
	if err != nil {
		return out, "unavailable", err
	}
	digest := sha256.Sum256(raw)
	bodyDigest := sha256.Sum256([]byte(text))
	bodySHA := hex.EncodeToString(bodyDigest[:])
	descriptor := "history/codex/" + id + ".txt"
	sourceID := sha256.Sum256([]byte(descriptor + "\x00" + bodySHA))
	out = records.ProjectRepositorySource{ID: hex.EncodeToString(sourceID[:]), Path: descriptor, SHA256: bodySHA, Text: text,
		History: &records.ProjectHistoryOriginal{Agent: "codex", Session: "codex_" + id, Workspace: meta.Cwd, CLIVersion: meta.Version, CapturedAt: created, Digest: hex.EncodeToString(digest[:])}}
	return out, "available", nil
}

func (r *CodexProjectHistory) transcript(ctx context.Context, raw []byte, meta codexHistoryMeta) (string, error) {
	if len(raw) == 0 || raw[len(raw)-1] != '\n' || !utf8.Valid(raw) {
		return "", errors.New("complete UTF-8 session log required")
	}
	var body strings.Builder
	fmt.Fprintf(&body, "Codex 历史会话 · codex_%s\n项目工作区：%s\nCLI：%s\n\n", meta.ID, meta.Cwd, meta.Version)
	metadata, completed, answer, started := 0, false, false, false
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var row codexHistoryRow
		if json.Unmarshal(line, &row) != nil {
			return "", errors.New("malformed session entry")
		}
		if _, err := time.Parse(time.RFC3339Nano, row.Timestamp); err != nil {
			return "", errors.New("message timestamp unavailable")
		}
		if row.Type == "session_meta" {
			metadata++
			continue
		}
		var p struct {
			Type, Role, Name, Phase, Input, Arguments, Cwd string
			CallID                                         string `json:"call_id"`
			Output                                         json.RawMessage
			Content                                        []struct{ Type, Text string }
			Roots                                          []string `json:"runtime_workspace_roots"`
		}
		if json.Unmarshal(row.Payload, &p) != nil {
			return "", errors.New("invalid session payload")
		}
		if row.Type == "turn_context" && p.Cwd != "" {
			if err := r.check(ctx, p.Cwd); err != nil {
				return "", errors.New("session changes project scope")
			}
			for _, root := range p.Roots {
				if err := r.check(ctx, root); err != nil {
					return "", errors.New("historical turn spans another project")
				}
			}
		}
		if row.Type == "event_msg" {
			switch p.Type {
			case "task_started":
				started = true
				completed, answer = false, false
			case "task_complete":
				completed = true
			case "turn_aborted":
				completed = false
			}
			continue
		}
		if row.Type != "response_item" {
			continue
		}
		switch p.Type {
		case "message":
			if p.Role != "user" && p.Role != "assistant" {
				continue
			}
			label := "用户"
			if p.Role == "assistant" {
				label = "助手"
			}
			for _, content := range p.Content {
				if content.Type != "input_text" && content.Type != "output_text" {
					return "", errors.New("non-text history messages are not adapted")
				}
				if p.Role == "user" && strings.HasPrefix(strings.TrimSpace(content.Text), "<environment_context>") {
					continue
				}
				fmt.Fprintf(&body, "%s · %s\n%s\n\n", row.Timestamp, label, content.Text)
			}
			if p.Role == "assistant" && p.Phase == "final_answer" {
				answer = true
			}
		case "function_call", "custom_tool_call":
			input := p.Arguments
			if p.Type == "custom_tool_call" {
				input = p.Input
			}
			fmt.Fprintf(&body, "%s · 工具请求 · %s · %s\n%s\n\n", row.Timestamp, p.Name, p.CallID, input)
		case "function_call_output", "custom_tool_call_output":
			fmt.Fprintf(&body, "%s · 工具返回 · %s\n", row.Timestamp, p.CallID)
			var text string
			if json.Unmarshal(p.Output, &text) == nil {
				body.WriteString(text)
			} else {
				var blocks []struct{ Type, Text string }
				if json.Unmarshal(p.Output, &blocks) != nil {
					return "", errors.New("unsupported history tool output")
				}
				for _, block := range blocks {
					if block.Type != "text" && block.Type != "input_text" && block.Type != "output_text" {
						return "", errors.New("non-text history tool output not adapted")
					}
					body.WriteString(block.Text + "\n")
				}
			}
			body.WriteString("\n\n")
		}
		if body.Len() > projectSourceBytes {
			return "", errors.New("history transcript exceeds source budget; nothing truncated")
		}
	}
	if metadata != 1 || !completed || !answer || !started {
		return "", errors.New("latest session task is not completed")
	}
	return body.String(), nil
}

func (r *CodexProjectHistory) Read(ctx context.Context, selection records.ProjectSessionSelection) (records.ProjectRepositorySource, error) {
	var empty records.ProjectRepositorySource
	parsed, err := uuid.Parse(strings.TrimPrefix(selection.ID, "codex_"))
	if err != nil || selection.ID != "codex_"+parsed.String() {
		return empty, errors.New("canonical Codex session selection required")
	}
	paths, err := r.paths(ctx)
	if err != nil {
		return empty, err
	}
	found := ""
	for _, path := range paths {
		if id, _ := historyFileID(filepath.Base(path)); id == parsed.String() {
			if found != "" {
				return empty, errors.New("duplicate history session identity")
			}
			found = path
		}
	}
	if found == "" {
		return empty, errors.New("selected session is unavailable")
	}
	source, _, err := r.read(ctx, found, parsed.String())
	if err != nil {
		return empty, err
	}
	if source.History.Digest != selection.Digest {
		return empty, ErrCodexHistoryChanged
	}
	return source, nil
}

// Only metadata is examined for other repositories; their bodies are never read.
func (r *CodexProjectHistory) List(ctx context.Context, after string) (records.ProjectHistoryPage, error) {
	out := records.ProjectHistoryPage{Items: []records.ProjectHistoryChoice{}}
	paths, err := r.paths(ctx)
	if err != nil {
		return out, err
	}
	all := []records.ProjectHistoryChoice{}
	seen := map[string]bool{}
	for _, path := range paths {
		id, _ := historyFileID(filepath.Base(path))
		f, e := historyOpen(r.root, path, false)
		if e != nil {
			return out, e
		}
		meta, created, e := historyMeta(f)
		f.Close()
		if e != nil || meta.ID != id {
			out.Unattributed++
			continue
		}
		common, e := historyRepository(ctx, meta.Cwd)
		if e != nil {
			out.Unattributed++
			continue
		}
		if common != r.common {
			continue
		}
		if seen[id] {
			return out, errors.New("duplicate current-project session identity")
		}
		seen[id] = true
		if e = r.check(ctx, meta.Cwd); e != nil {
			return out, e
		}
		source, state, _ := r.read(ctx, path, id)
		choice := records.ProjectHistoryChoice{ProjectSessionSelection: records.ProjectSessionSelection{ID: "codex_" + id}, CreatedAt: created, State: state}
		if state == "available" {
			choice.Digest = source.History.Digest
		}
		all = append(all, choice)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	out.Total = len(all)
	index := 0
	if after != "" {
		index = sort.Search(len(all), func(i int) bool { return all[i].ID >= after })
		if index == len(all) || all[index].ID != after {
			return out, errors.New("unknown history cursor")
		}
		index++
	}
	end := min(index+20, len(all))
	out.Items = append(out.Items, all[index:end]...)
	if end < len(all) {
		out.Next = all[end-1].ID
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	return out, nil
}
