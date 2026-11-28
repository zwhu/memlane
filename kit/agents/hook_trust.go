package agents

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

const (
	HookTrustTrusted   = "trusted"
	HookTrustUntrusted = "untrusted"
	HookTrustModified  = "modified"
	HookTrustManaged   = "managed"

	defaultHookTrustProbeTimeout = 4 * time.Second
	hookTrustGracefulExitTimeout = 400 * time.Millisecond
	hookTrustScanMaxBytes        = 4 << 20
)

type HostHookTrustEntry struct {
	Key         string `json:"key,omitempty"`
	EventName   string `json:"event_name,omitempty"`
	Command     string `json:"command,omitempty"`
	SourcePath  string `json:"source_path,omitempty"`
	TrustStatus string `json:"trust_status,omitempty"`
	Enabled     bool   `json:"enabled"`
}

type HostHookTrustReport struct {
	HostKind        string               `json:"host_kind"`
	Source          string               `json:"source"`
	Evaluated       bool                 `json:"evaluated"`
	TotalManaged    int                  `json:"total_managed"`
	TrustedCount    int                  `json:"trusted_count"`
	UntrustedCount  int                  `json:"untrusted_count"`
	ModifiedCount   int                  `json:"modified_count"`
	DisabledCount   int                  `json:"disabled_count"`
	UntrustedEvents []string             `json:"untrusted_events,omitempty"`
	Entries         []HostHookTrustEntry `json:"entries,omitempty"`
}

func (r HostHookTrustReport) NeedsTrust() bool {
	return r.UntrustedCount > 0 || r.ModifiedCount > 0 || r.DisabledCount > 0
}

// HookTrustConfigFiles returns the read-only hook config files and TOML state
// files used for host hook trust probing and cache invalidation.
func HookTrustConfigFiles(hostKind, profileDir string) (hookPaths []string, tomlPaths []string) {
	profileDir = strings.TrimSpace(profileDir)
	if profileDir == "" {
		return nil, nil
	}
	switch hostKind {
	case HostCodex:
		return []string{filepath.Join(profileDir, "hooks.json")},
			[]string{filepath.Join(profileDir, "config.toml")}
	case HostTraeX:
		return []string{filepath.Join(profileDir, "cli", "hooks.json")},
			[]string{
				filepath.Join(profileDir, "traecli.toml"),
				filepath.Join(profileDir, "cli", "traecli.toml"),
			}
	default:
		return nil, nil
	}
}

// IsManagedHookCommand reports whether a hook command string belongs to a
// Memlane-managed hook entry.
func IsManagedHookCommand(command string) bool {
	if strings.Contains(command, "memlane-hook") {
		return true
	}
	lower := strings.ToLower(command)
	if strings.Contains(lower, "memlane hook") {
		return true
	}
	if strings.Contains(lower, " hook ") &&
		strings.Contains(lower, "--gateway-url ") &&
		strings.Contains(lower, "--key-file ") {
		return true
	}
	for _, marker := range []string{"/memlane-cognee/", "/memlane_cognee/", "/memlane-memory/"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// HasManagedHostHooks reports whether any hook configuration file for the
// given host contains at least one Memlane-managed hook entry.
func HasManagedHostHooks(hostKind, profileDir string, hookPaths []string) bool {
	if hostKind != HostCodex && hostKind != HostTraeX {
		return false
	}
	if len(hookPaths) == 0 {
		hookPaths, _ = HookTrustConfigFiles(hostKind, profileDir)
	}
	_, managedEntries, _ := collectManagedHookIndex(hookPaths)
	return len(managedEntries) > 0
}

// ProbeHostHookTrust performs a strictly read-only probe of host hook trust
// status for Codex and TRAE CLI. It first queries `<executable> app-server`
// via JSON-RPC (`initialize` -> `initialized` -> `hooks/list`), and falls back
// conservatively to parsing `[hooks.state]` in config.toml / traecli.toml for
// known Memlane-managed hook entries when app-server is unavailable or times out.
func ProbeHostHookTrust(ctx context.Context, hostKind, executable, profileDir string, hookPaths []string) HostHookTrustReport {
	empty := HostHookTrustReport{HostKind: hostKind, Source: "none"}
	if hostKind != HostCodex && hostKind != HostTraeX {
		return empty
	}
	if ctx == nil {
		ctx = context.Background()
	}
	defaultHooks, tomlPaths := HookTrustConfigFiles(hostKind, profileDir)
	if len(hookPaths) == 0 {
		hookPaths = defaultHooks
	}

	allowedPaths, managedEntries, managedCommands := collectManagedHookIndex(hookPaths)

	if strings.TrimSpace(executable) != "" && isHostExecutable(executable) {
		if report, ok := probeHostHookTrustAppServer(ctx, hostKind, executable, profileDir, allowedPaths, managedCommands); ok {
			return report
		}
	}

	if report, ok := probeHostHookTrustTOML(hostKind, tomlPaths, managedEntries); ok {
		return report
	}
	return empty
}

type managedHookFileEntry struct {
	sourcePath string
	event      string
	groupIdx   int
	handlerIdx int
	command    string
}

func canonicalHookPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	cleaned := filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil && resolved != "" {
		return filepath.Clean(resolved)
	}
	if parentResolved, err := filepath.EvalSymlinks(filepath.Dir(cleaned)); err == nil && parentResolved != "" {
		return filepath.Join(parentResolved, filepath.Base(cleaned))
	}
	return cleaned
}

func collectManagedHookIndex(hookPaths []string) (map[string]bool, []managedHookFileEntry, map[string]bool) {
	allowed := make(map[string]bool, len(hookPaths)*2)
	managedCommands := make(map[string]bool)
	var entries []managedHookFileEntry

	for _, rawPath := range hookPaths {
		trimmed := strings.TrimSpace(rawPath)
		if trimmed == "" {
			continue
		}
		cleaned := filepath.Clean(trimmed)
		canon := canonicalHookPath(cleaned)
		allowed[cleaned] = true
		if canon != "" {
			allowed[canon] = true
		}
		raw, err := SafeReadFile(cleaned, configLimit)
		if err != nil {
			continue
		}
		doc, err := ParseJSONC(raw)
		if err != nil || doc == nil {
			continue
		}
		groups := doc
		if wrapped, ok := doc["hooks"].(map[string]any); ok {
			groups = wrapped
		}
		for _, event := range sortedKeys(groups) {
			rows, ok := groups[event].([]any)
			if !ok {
				continue
			}
			for groupIdx, row := range rows {
				obj, ok := row.(map[string]any)
				if !ok {
					continue
				}
				groupManaged := obj["_memlane_managed"] == "memlane-hook"
				if nested, hasNested := obj["hooks"].([]any); hasNested {
					for handlerIdx, child := range nested {
						childObj, ok := child.(map[string]any)
						if !ok {
							continue
						}
						cmd, _ := childObj["command"].(string)
						cmdTrimmed := strings.TrimSpace(cmd)
						if groupManaged || childObj["_memlane_managed"] == "memlane-hook" || IsManagedHookCommand(cmdTrimmed) {
							entries = append(entries, managedHookFileEntry{
								sourcePath: cleaned,
								event:      event,
								groupIdx:   groupIdx,
								handlerIdx: handlerIdx,
								command:    cmdTrimmed,
							})
							if cmdTrimmed != "" {
								managedCommands[cmdTrimmed] = true
							}
						}
					}
					continue
				}
				cmd, _ := obj["command"].(string)
				cmdTrimmed := strings.TrimSpace(cmd)
				if groupManaged || IsManagedHookCommand(cmdTrimmed) {
					entries = append(entries, managedHookFileEntry{
						sourcePath: cleaned,
						event:      event,
						groupIdx:   groupIdx,
						handlerIdx: 0,
						command:    cmdTrimmed,
					})
					if cmdTrimmed != "" {
						managedCommands[cmdTrimmed] = true
					}
				}
			}
		}
	}
	return allowed, entries, managedCommands
}

func buildHookTrustProbeEnv(hostKind, profileDir string) []string {
	base := os.Environ()
	env := make([]string, 0, len(base)+2)
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "CODEX_HOME", "TRAE_HOME", "TRAECLI_HOME":
			continue
		default:
			env = append(env, kv)
		}
	}
	profileDir = strings.TrimSpace(profileDir)
	if profileDir != "" {
		switch hostKind {
		case HostCodex:
			env = append(env, "CODEX_HOME="+profileDir)
		case HostTraeX:
			// TRAE CLI locates <profile>/cli/hooks.json and <profile>/traecli.toml via TRAE_HOME.
			// Do not set TRAECLI_HOME to profileDir because TRAECLI_HOME overrides the cli/ subdir.
			env = append(env, "TRAE_HOME="+profileDir)
		}
	}
	return env
}

type rpcResponseEnvelope struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type rpcHookItem struct {
	Key            string `json:"key"`
	EventName      string `json:"eventName"`
	EventNameSnake string `json:"event_name"`
	Command        string `json:"command"`
	SourcePath     string `json:"sourcePath"`
	SourcePathSnk  string `json:"source_path"`
	TrustStatus    string `json:"trustStatus"`
	TrustStatusSnk string `json:"trust_status"`
	Enabled        *bool  `json:"enabled"`
}

type rpcHooksListResult struct {
	Data []struct {
		Cwd   string        `json:"cwd"`
		Hooks []rpcHookItem `json:"hooks"`
	} `json:"data"`
	Hooks []rpcHookItem `json:"hooks"`
}

func rpcIDMatches(raw json.RawMessage, want int) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return false
	}
	var asInt int
	if err := json.Unmarshal(raw, &asInt); err == nil {
		return asInt == want
	}
	var asStr string
	if err := json.Unmarshal(raw, &asStr); err == nil {
		return asStr == strconv.Itoa(want)
	}
	return false
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

func probeHostHookTrustAppServer(
	ctx context.Context,
	hostKind, executable, profileDir string,
	allowedPaths map[string]bool,
	managedCommands map[string]bool,
) (HostHookTrustReport, bool) {
	probeCtx, cancel := context.WithTimeout(ctx, defaultHookTrustProbeTimeout)
	defer cancel()

	cmd := exec.Command(executable, "app-server")
	cmd.Dir = os.TempDir()
	cmd.Env = buildHookTrustProbeEnv(hostKind, profileDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return HostHookTrustReport{}, false
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return HostHookTrustReport{}, false
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return HostHookTrustReport{}, false
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return HostHookTrustReport{}, false
	}

	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(io.Discard, stderr)
	}()

	stopWatch := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-probeCtx.Done():
			killProcessGroup(cmd)
		case <-stopWatch:
		}
	}()
	defer func() {
		// Join the RPC-phase watchdog strictly before cmd.Wait() can reap the PID.
		close(stopWatch)
		<-watchDone
		_ = stdin.Close()

		stdoutDone := make(chan struct{})
		go func() {
			defer close(stdoutDone)
			_, _ = io.Copy(io.Discard, stdout)
		}()
		pipesDone := make(chan struct{})
		go func() {
			defer close(pipesDone)
			<-stdoutDone
			<-stderrDone
		}()

		if probeCtx.Err() != nil {
			killProcessGroup(cmd)
			<-pipesDone
			_ = cmd.Wait()
			return
		}

		// Give the child a short graceful window to exit cleanly on stdin EOF
		// (so codex/traecli remove their tmp/arg0* directories and exit 0).
		graceTimer := time.NewTimer(hookTrustGracefulExitTimeout)
		defer graceTimer.Stop()
		select {
		case <-pipesDone:
		case <-probeCtx.Done():
			killProcessGroup(cmd)
			<-pipesDone
		case <-graceTimer.C:
			killProcessGroup(cmd)
			<-pipesDone
		}
		_ = cmd.Wait()
	}()

	writeRPC := func(payload string) error {
		_, writeErr := io.WriteString(stdin, payload+"\n")
		return writeErr
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), hookTrustScanMaxBytes)

	readResponse := func(wantID int) (json.RawMessage, error) {
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || line[0] != '{' {
				continue
			}
			var msg rpcResponseEnvelope
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if !rpcIDMatches(msg.ID, wantID) {
				continue
			}
			if len(msg.Error) > 0 && strings.TrimSpace(string(msg.Error)) != "null" {
				return nil, errors.New("rpc error response")
			}
			return msg.Result, nil
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}

	if err := writeRPC(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"memlane","version":"1.0"}}}`); err != nil {
		return HostHookTrustReport{}, false
	}
	if _, err := readResponse(1); err != nil {
		return HostHookTrustReport{}, false
	}
	if err := writeRPC(`{"jsonrpc":"2.0","method":"initialized","params":{}}`); err != nil {
		return HostHookTrustReport{}, false
	}
	if err := writeRPC(`{"jsonrpc":"2.0","id":2,"method":"hooks/list","params":{}}`); err != nil {
		return HostHookTrustReport{}, false
	}
	rawResult, err := readResponse(2)
	if err != nil {
		return HostHookTrustReport{}, false
	}

	var parsed rpcHooksListResult
	if err := json.Unmarshal(rawResult, &parsed); err != nil {
		return HostHookTrustReport{}, false
	}
	items := make([]rpcHookItem, 0, len(parsed.Hooks))
	for _, group := range parsed.Data {
		items = append(items, group.Hooks...)
	}
	if len(items) == 0 && len(parsed.Hooks) > 0 {
		items = append(items, parsed.Hooks...)
	}

	report := HostHookTrustReport{
		HostKind: hostKind,
		Source:   "app_server",
	}
	seenKeys := map[string]bool{}
	for _, item := range items {
		cmdStr := strings.TrimSpace(item.Command)
		sourcePath := strings.TrimSpace(item.SourcePath)
		if sourcePath == "" {
			sourcePath = strings.TrimSpace(item.SourcePathSnk)
		}
		if !matchesManagedRPCHook(cmdStr, sourcePath, allowedPaths, managedCommands) {
			continue
		}
		dedupKey := item.Key
		if dedupKey == "" {
			dedupKey = sourcePath + "|" + item.EventName + "|" + cmdStr
		}
		if seenKeys[dedupKey] {
			continue
		}
		seenKeys[dedupKey] = true

		eventName := strings.TrimSpace(item.EventName)
		if eventName == "" {
			eventName = strings.TrimSpace(item.EventNameSnake)
		}
		eventName = displayHookEventName(eventName)

		trustStatus := strings.ToLower(strings.TrimSpace(item.TrustStatus))
		if trustStatus == "" {
			trustStatus = strings.ToLower(strings.TrimSpace(item.TrustStatusSnk))
		}
		enabled := true
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		recordHookTrustEntry(&report, HostHookTrustEntry{
			Key:         item.Key,
			EventName:   eventName,
			Command:     cmdStr,
			SourcePath:  sourcePath,
			TrustStatus: trustStatus,
			Enabled:     enabled,
		})
	}
	report.Evaluated = true
	return report, true
}

func matchesManagedRPCHook(command, sourcePath string, allowedPaths, managedCommands map[string]bool) bool {
	if sourcePath != "" && len(allowedPaths) > 0 {
		cleaned := filepath.Clean(sourcePath)
		canon := canonicalHookPath(sourcePath)
		if !allowedPaths[cleaned] && !allowedPaths[canon] {
			return false
		}
	}
	return IsManagedHookCommand(command) || managedCommands[command]
}

func recordHookTrustEntry(report *HostHookTrustReport, entry HostHookTrustEntry) {
	report.TotalManaged++
	report.Entries = append(report.Entries, entry)
	switch entry.TrustStatus {
	case HookTrustUntrusted:
		report.UntrustedCount++
		appendUniqueEvent(&report.UntrustedEvents, entry.EventName)
	case HookTrustModified:
		report.ModifiedCount++
		appendUniqueEvent(&report.UntrustedEvents, entry.EventName)
	case HookTrustTrusted, HookTrustManaged:
		if !entry.Enabled {
			report.DisabledCount++
			appendUniqueEvent(&report.UntrustedEvents, entry.EventName)
		} else {
			report.TrustedCount++
		}
	default:
		if !entry.Enabled {
			report.DisabledCount++
			appendUniqueEvent(&report.UntrustedEvents, entry.EventName)
		} else if entry.TrustStatus != "" {
			report.UntrustedCount++
			appendUniqueEvent(&report.UntrustedEvents, entry.EventName)
		} else {
			report.TrustedCount++
		}
	}
}

func appendUniqueEvent(dst *[]string, event string) {
	event = strings.TrimSpace(event)
	if event == "" || slices.Contains(*dst, event) {
		return
	}
	*dst = append(*dst, event)
}

func probeHostHookTrustTOML(
	hostKind string,
	tomlPaths []string,
	managedEntries []managedHookFileEntry,
) (HostHookTrustReport, bool) {
	if len(managedEntries) == 0 {
		return HostHookTrustReport{}, false
	}
	stateByKey := map[string]map[string]any{}
	foundStateTable := false

	for _, path := range tomlPaths {
		raw, err := SafeReadFile(path, configLimit)
		if err != nil {
			continue
		}
		doc, err := ParseTOML(raw)
		if err != nil || doc == nil {
			continue
		}
		hooksTbl, ok := doc["hooks"].(map[string]any)
		if !ok {
			continue
		}
		stateTbl, ok := hooksTbl["state"].(map[string]any)
		if !ok {
			continue
		}
		foundStateTable = true
		for rawKey, val := range stateTbl {
			entryMap, ok := val.(map[string]any)
			if !ok {
				continue
			}
			stateByKey[rawKey] = entryMap
			if canonKey := canonicalizeHookStateKey(rawKey); canonKey != "" {
				stateByKey[canonKey] = entryMap
			}
		}
	}
	if !foundStateTable {
		return HostHookTrustReport{}, false
	}

	report := HostHookTrustReport{
		HostKind:  hostKind,
		Source:    "toml_fallback",
		Evaluated: true,
	}

	for _, entry := range managedEntries {
		snakeEvent := snakeHookEventName(entry.event)
		suffix := ":" + snakeEvent + ":" + strconv.Itoa(entry.groupIdx) + ":" + strconv.Itoa(entry.handlerIdx)
		rawKey := filepath.Clean(entry.sourcePath) + suffix
		canonKey := canonicalHookPath(entry.sourcePath) + suffix
		stateObj, ok := stateByKey[canonKey]
		if !ok {
			stateObj, ok = stateByKey[rawKey]
		}
		displayEvent := displayHookEventName(entry.event)
		if !ok {
			recordHookTrustEntry(&report, HostHookTrustEntry{
				Key:         canonKey,
				EventName:   displayEvent,
				Command:     entry.command,
				SourcePath:  entry.sourcePath,
				TrustStatus: HookTrustUntrusted,
				Enabled:     true,
			})
			continue
		}
		trustedHash, _ := stateObj["trusted_hash"].(string)
		enabledVal, hasEnabled := stateObj["enabled"].(bool)
		enabled := !hasEnabled || enabledVal
		trustStatus := HookTrustTrusted
		if strings.TrimSpace(trustedHash) == "" {
			trustStatus = HookTrustUntrusted
		}
		recordHookTrustEntry(&report, HostHookTrustEntry{
			Key:         canonKey,
			EventName:   displayEvent,
			Command:     entry.command,
			SourcePath:  entry.sourcePath,
			TrustStatus: trustStatus,
			Enabled:     enabled,
		})
	}
	return report, true
}

func canonicalizeHookStateKey(key string) string {
	idx3 := strings.LastIndexByte(key, ':')
	if idx3 <= 0 {
		return key
	}
	idx2 := strings.LastIndexByte(key[:idx3], ':')
	if idx2 <= 0 {
		return key
	}
	idx1 := strings.LastIndexByte(key[:idx2], ':')
	if idx1 <= 0 {
		return key
	}
	canonPath := canonicalHookPath(key[:idx1])
	return canonPath + key[idx1:]
}

func snakeHookEventName(event string) string {
	trimmed := strings.TrimSpace(event)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "_") {
		return strings.ToLower(trimmed)
	}
	var b strings.Builder
	runes := []rune(trimmed)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
					b.WriteByte('_')
				}
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func displayHookEventName(event string) string {
	switch snakeHookEventName(event) {
	case "session_start":
		return "SessionStart"
	case "user_prompt_submit":
		return "UserPromptSubmit"
	case "pre_tool_use":
		return "PreToolUse"
	case "post_tool_use":
		return "PostToolUse"
	case "post_tool_use_failure":
		return "PostToolUseFailure"
	case "pre_compact":
		return "PreCompact"
	case "session_end":
		return "SessionEnd"
	case "stop":
		return "Stop"
	case "pre_invocation":
		return "PreInvocation"
	default:
		return strings.TrimSpace(event)
	}
}
