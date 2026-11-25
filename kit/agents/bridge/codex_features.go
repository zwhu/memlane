package bridge

import (
	"container/list"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

var disabledFeatures = []string{
	"shell_tool", "unified_exec", "apps", "plugins", "hooks", "memories", "multi_agent", "multi_agent_v2",
	"browser_use", "browser_use_external", "computer_use", "image_generation", "skill_search",
	"skill_mcp_dependency_install", "workspace_dependencies", "code_mode_host", "view_image", "remote_plugin",
}

func traeFeatureFallback(name string) bool { return name != "view_image" && name != "remote_plugin" }

type featureKey struct {
	executable, kind string
	dev, ino         uint64
	size, modified   int64
}
type featureEntry struct {
	key                 featureKey
	supported, excluded map[string]bool
}

var featureCache = struct {
	sync.Mutex
	order   list.List
	entries map[featureKey]*list.Element
	probes  map[featureKey]chan struct{}
}{entries: map[featureKey]*list.Element{}, probes: map[featureKey]chan struct{}{}}

func featureIdentity(opts ExecOptions) (featureKey, error) {
	info, err := os.Stat(opts.Executable)
	if err != nil {
		return featureKey{}, err
	}
	stat := info.Sys().(*syscall.Stat_t)
	return featureKey{opts.Executable, opts.BridgeKind, uint64(stat.Dev), uint64(stat.Ino), info.Size(), info.ModTime().UnixNano()}, nil
}

func cacheFeatureEntry(entry *featureEntry) {
	if elem := featureCache.entries[entry.key]; elem != nil {
		featureCache.order.Remove(elem)
	}
	featureCache.entries[entry.key] = featureCache.order.PushFront(entry)
	if featureCache.order.Len() > 64 {
		oldest := featureCache.order.Back()
		delete(featureCache.entries, oldest.Value.(*featureEntry).key)
		featureCache.order.Remove(oldest)
	}
}

func getFeatures(ctx context.Context, opts ExecOptions, key featureKey) (map[string]bool, error) {
	for {
		featureCache.Lock()
		if elem := featureCache.entries[key]; elem != nil {
			featureCache.order.MoveToFront(elem)
			supported := elem.Value.(*featureEntry).supported
			featureCache.Unlock()
			return supported, nil
		}
		if done := featureCache.probes[key]; done != nil {
			featureCache.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		done := make(chan struct{})
		featureCache.probes[key] = done
		featureCache.Unlock()
		supported, err := probeFeatures(ctx, opts)
		featureCache.Lock()
		if err == nil && ctx.Err() == nil {
			cacheFeatureEntry(&featureEntry{key, supported, map[string]bool{}})
		}
		delete(featureCache.probes, key)
		close(done)
		featureCache.Unlock()
		return supported, err
	}
}

func selectedFeatures(key featureKey, supported, excluded map[string]bool) []string {
	featureCache.Lock()
	defer featureCache.Unlock()
	if elem := featureCache.entries[key]; elem != nil {
		for name := range elem.Value.(*featureEntry).excluded {
			excluded[name] = true
		}
	}
	var selected []string
	for _, name := range disabledFeatures {
		if excluded[name] {
			continue
		}
		if supported != nil && supported[name] || supported == nil && (key.kind != "traex" || traeFeatureFallback(name)) {
			selected = append(selected, name)
		}
	}
	return selected
}

func excludeFeature(key featureKey, name string) {
	featureCache.Lock()
	defer featureCache.Unlock()
	if elem := featureCache.entries[key]; elem != nil {
		elem.Value.(*featureEntry).excluded[name] = true
	}
}

func codexEnvironment(w workspace, opts ExecOptions) []string {
	if opts.BridgeKind == "traex" {
		return w.environment(opts, "TRAE_HOME="+w.private, "TRAECLI_HOME="+filepath.Join(w.private, "cli"))
	}
	return w.environment(opts, "CODEX_HOME="+w.private)
}

var featureName = regexp.MustCompile(`^[a-z0-9_]+$`)

func probeFeatures(ctx context.Context, opts ExecOptions) (supported map[string]bool, err error) {
	w, e := createWorkspace(opts, "probe")
	if e != nil {
		return nil, fault("codex_process_unavailable", 503)
	}
	remove := true
	defer func() {
		if remove {
			_ = os.RemoveAll(w.root)
		}
	}()
	if opts.BridgeKind == "traex" {
		if e := os.Mkdir(filepath.Join(w.private, "cli"), 0o700); e != nil {
			return nil, fault("codex_process_unavailable", 503)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.Command(opts.Executable, "features", "list")
	cmd.Dir, cmd.Env = w.task, codexEnvironment(w, opts)
	p, e := StartIsolatedProcess(ctx, cmd, NewActivityClock(5*time.Second))
	if e != nil {
		return nil, nil
	}
	defer func() {
		t := transport{kind: "codex", process: p}
		remove = t.cleanup(&err)
	}()
	result := map[string]bool{}
	received := 0
	for {
		line, e := ReadLineWithIdle(ctx, p.Stdout, p.Clock, 16<<10)
		if e == io.EOF {
			break
		}
		received += len(line)
		if e != nil || received > 256<<10 {
			return nil, nil
		}
		fields := strings.Fields(string(line))
		if len(fields) >= 3 && featureName.MatchString(fields[0]) && !strings.EqualFold(strings.Join(fields[1:len(fields)-1], " "), "removed") {
			last := fields[len(fields)-1]
			if last == "true" || last == "false" {
				result[fields[0]] = true
			}
		}
	}
	select {
	case <-ctx.Done():
		return nil, nil
	case <-p.done:
		if p.waitErr != nil || len(result) == 0 {
			return nil, nil
		}
	}
	return result, nil
}

var unknownFeaturePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bunknown (?:configuration )?field\s*[:=]?\s*['"\x60]features\.([a-z0-9_]+)['"\x60]`),
	regexp.MustCompile(`(?i)\b(?:unknown|invalid) feature\s*[:=]?\s*['"\x60]?(?:features\.)?([a-z0-9_]+)(?:[^a-z0-9_-]|$)`),
	regexp.MustCompile(`(?i)\bunknown (?:configuration )?field\s*[:=]?\s*['"\x60]([a-z0-9_]+)['"\x60]`),
}

func unknownStartupFeature(stderr []byte, candidates []string) string {
	for _, line := range strings.Split(string(stderr), "\n") {
		for i, pattern := range unknownFeaturePatterns {
			lower := strings.ToLower(line)
			if i == 2 && !strings.Contains(lower, "features") && !strings.Contains(lower, "config.toml") && !strings.Contains(lower, "traecli.toml") {
				continue
			}
			for _, match := range pattern.FindAllStringSubmatch(line, -1) {
				for _, candidate := range candidates {
					if strings.EqualFold(candidate, match[1]) {
						return candidate
					}
				}
			}
		}
	}
	return ""
}
