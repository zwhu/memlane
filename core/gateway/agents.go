package gateway

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/kit/agents"
)

type agentView struct {
	ID            string             `json:"id"`
	Kind          string             `json:"kind"`
	Name          string             `json:"name"`
	PrincipalID   string             `json:"principal_id"`
	DefaultAccess domain.AccessLevel `json:"default_access"`
	Archived      bool               `json:"archived"`
	RetiredAt     string             `json:"retired_at,omitempty"`
}

func publicAgent(a domain.Agent) agentView {
	return agentView{a.ID, a.Kind, a.Name, a.PrincipalID, a.DefaultAccess, a.Archived, a.RetiredAt}
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	registered, err := s.cfg.Store.ListAgents(r.Context(), true)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	out := make([]agentView, 0, len(registered))
	for _, a := range registered {
		out = append(out, publicAgent(a))
	}
	writeJSON(w, 200, map[string]any{"detected_hosts": agents.DetectHosts(s.cfg.DiscoveryOptions), "registered_agents": out})
}

func (s *Server) connectAgent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID               string `json:"id"`
		AgentID          string `json:"agent_id"`
		Kind             string `json:"kind"`
		Name             string `json:"name"`
		PrincipalID      string `json:"principal_id"`
		KeyFile          string `json:"key_file"`
		ControlTokenFile string `json:"control_token_file"`
		HostProfile      string `json:"host_profile"`
	}
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	if in.ID == "" {
		in.ID = in.AgentID
	}
	if in.ID == "" {
		in.ID = rand.Text()
	}
	if strings.ContainsAny(in.ID, "/\\") || strings.TrimSpace(in.ID) != in.ID {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	if in.Name == "" {
		in.Name = in.Kind
		if in.Name == "" {
			in.Name = in.ID
		}
	}
	a := domain.Agent{ID: in.ID, Kind: in.Kind, Name: in.Name, PrincipalID: in.PrincipalID, KeyFile: in.KeyFile,
		ControlTokenFile: in.ControlTokenFile, HostProfile: in.HostProfile, DefaultAccess: domain.AccessNone}
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if err := s.cfg.Store.ConnectAgent(r.Context(), a); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 201, publicAgent(a))
}

func (s *Server) retireAgent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	a, err := s.cfg.Store.GetAgent(ctx, r.PathValue("id"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if strings.TrimSpace(a.RetiredAt) == "" {
		a.RetiredAt = domain.NowUTCNS()
		if err := s.cfg.Store.UpsertAgent(ctx, a); err != nil {
			writeDomainError(w, err)
			return
		}
	}
	writeJSON(w, 200, publicAgent(a))
}

type conflictFinding struct {
	Kind    string                `json:"host_kind"`
	Surface string                `json:"surface"`
	Name    string                `json:"entry_name"`
	Path    string                `json:"config_path"`
	Action  agents.ConflictAction `json:"action"`
	Blocked bool                  `json:"blocked"`
}

func appendFindings(out []conflictFinding, source []agents.ConflictFinding) []conflictFinding {
	for _, f := range source {
		out = append(out, conflictFinding{f.HostKind, f.Surface, f.EntryName, f.ConfigPath, f.Action, f.Blocked})
	}
	return out
}

func (s *Server) agentConflicts(w http.ResponseWriter, r *http.Request) {
	in := struct {
		Kind string `json:"kind"`
	}{Kind: r.URL.Query().Get("kind")}
	if r.Method == http.MethodPost && r.ContentLength != 0 && !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	opts := s.cfg.DiscoveryOptions
	if in.Kind != "" {
		opts.Kinds = agents.NormalizeKinds([]string{in.Kind})
		if len(opts.Kinds) == 0 {
			writeDomainError(w, domain.ErrInvalidArgument)
			return
		}
	}
	managed, thirdParty := []conflictFinding{}, []conflictFinding{}
	total, blocked := 0, false
	for _, host := range agents.DetectHosts(opts) {
		report, err := agents.ScanHostConflicts(host, *s.cfg.ConflictPolicy)
		if err != nil {
			writeError(w, 500, "conflict_scan_failed", "host configuration could not be scanned")
			return
		}
		managed = appendFindings(managed, report.ManagedLegacy)
		thirdParty = appendFindings(thirdParty, report.ThirdPartyConflicts)
		total += report.UnrelatedCount
		blocked = blocked || report.Blocked
	}
	writeJSON(w, 200, map[string]any{"managed_legacy": managed, "third_party_conflicts": thirdParty, "unrelated_count": total, "blocked": blocked})
}

type accessAgent struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Kind          string             `json:"kind"`
	DefaultAccess domain.AccessLevel `json:"default_access,omitempty"`
}

func (s *Server) activeAgents(ctx context.Context) ([]accessAgent, error) {
	registered, err := s.cfg.Store.ListAgents(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]accessAgent, 0, len(registered))
	for _, a := range registered {
		if strings.TrimSpace(a.RetiredAt) == "" {
			out = append(out, accessAgent{ID: a.ID, Name: a.Name, Kind: a.Kind, DefaultAccess: a.DefaultAccess})
		}
	}
	return out, nil
}

type connectionCaptureState struct {
	ProjectID   string  `json:"project_id,omitempty"`
	State       string  `json:"state"`
	Permission  string  `json:"permission,omitempty"`
	ReviewSHA   string  `json:"review_sha,omitempty"`
	Reason      string  `json:"reason,omitempty"`
	SavedCount  int     `json:"saved_count,omitempty"`
	LastSavedAt float64 `json:"last_saved_at,omitempty"`
	CheckedAt   float64 `json:"checked_at,omitempty"`
}

type connectionView struct {
	ID                  string                  `json:"instance_id"`
	Kind                string                  `json:"kind"`
	Status              string                  `json:"status"`
	Detected            bool                    `json:"detected"`
	Registered          bool                    `json:"agent_registered"`
	Version             string                  `json:"host_version,omitempty"`
	Profile             string                  `json:"profile,omitempty"`
	NewSessions         int                     `json:"new_sessions"`
	NeedsHookRepair     bool                    `json:"needs_hook_repair"`
	NeedsHookTrust      bool                    `json:"needs_hook_trust,omitempty"`
	UntrustedHookCount  int                     `json:"untrusted_hook_count,omitempty"`
	ModifiedHookCount   int                     `json:"modified_hook_count,omitempty"`
	DisabledHookCount   int                     `json:"disabled_hook_count,omitempty"`
	UntrustedHookEvents []string                `json:"untrusted_hook_events,omitempty"`
	LastReceiptAt       float64                 `json:"last_receipt_at,omitempty"`
	PluginVersion       string                  `json:"plugin_version,omitempty"`
	EnvironmentVerified bool                    `json:"environment_verified,omitempty"`
	Capture             *connectionCaptureState `json:"capture,omitempty"`
}

type managedHookStatus struct {
	installed     bool
	needsRepair   bool
	pluginVersion string
	configPath    string
	configModAt   float64
}

const (
	managedHookConfigLimit      = 1 << 20
	hookTrustCacheTTL           = 60 * time.Second
	hookTrustCacheMaxItems      = 128
	hookConfigStaleReceiptGrace = 5 * time.Second
	minRealisticUnixTimestamp   = 1577836800 // 2020-01-01T00:00:00Z
)

type hookTrustCacheKey struct {
	hostKind   string
	executable string
	profileDir string
	stamp      string
}

type hookTrustCacheEntry struct {
	report   agents.HostHookTrustReport
	cachedAt time.Time
}

var (
	hookTrustCacheMu sync.RWMutex
	hookTrustCache   = map[hookTrustCacheKey]hookTrustCacheEntry{}
)

func fileModUnix(path string) float64 {
	info, err := os.Stat(path)
	if err != nil || info.ModTime().IsZero() {
		return 0
	}
	return float64(info.ModTime().UnixMilli()) / 1000
}

func hookTrustFilesStamp(executable string, hookPaths, tomlPaths []string) string {
	var b strings.Builder
	writeStat := func(label, path string) {
		b.WriteString(label)
		b.WriteByte('=')
		b.WriteString(path)
		b.WriteByte(':')
		if info, err := os.Stat(path); err == nil {
			fmt.Fprintf(&b, "%d:%d;", info.ModTime().UnixNano(), info.Size())
		} else {
			b.WriteString("missing;")
		}
	}
	if executable != "" {
		writeStat("exe", executable)
	}
	for _, p := range hookPaths {
		writeStat("h", p)
	}
	for _, p := range tomlPaths {
		writeStat("t", p)
	}
	return b.String()
}

func probeHostHookTrustCached(ctx context.Context, hostKind, executable, profileDir string, force bool) agents.HostHookTrustReport {
	hookPaths, tomlPaths := agents.HookTrustConfigFiles(hostKind, profileDir)
	key := hookTrustCacheKey{
		hostKind:   hostKind,
		executable: executable,
		profileDir: profileDir,
		stamp:      hookTrustFilesStamp(executable, hookPaths, tomlPaths),
	}
	now := time.Now()
	if !force {
		hookTrustCacheMu.RLock()
		cached, ok := hookTrustCache[key]
		hookTrustCacheMu.RUnlock()
		if ok && now.Sub(cached.cachedAt) < hookTrustCacheTTL {
			return cached.report
		}
	}
	report := agents.ProbeHostHookTrust(ctx, hostKind, executable, profileDir, hookPaths)
	hookTrustCacheMu.Lock()
	if len(hookTrustCache) >= hookTrustCacheMaxItems {
		for k, v := range hookTrustCache {
			if now.Sub(v.cachedAt) >= hookTrustCacheTTL {
				delete(hookTrustCache, k)
			}
		}
		if len(hookTrustCache) >= hookTrustCacheMaxItems {
			clear(hookTrustCache)
		}
	}
	hookTrustCache[key] = hookTrustCacheEntry{report: report, cachedAt: now}
	hookTrustCacheMu.Unlock()
	return report
}

func isReceiptStaleAfterHookConfig(kind string, hookStatus managedHookStatus, stats captureActivityStats, trustReport agents.HostHookTrustReport) bool {
	switch kind {
	case agents.HostCodex, agents.HostTraeX, agents.HostTraeIDE:
	default:
		return false
	}
	if !hookStatus.installed || stats.newSessions <= 0 || stats.lastSavedAt < minRealisticUnixTimestamp || hookStatus.configModAt < minRealisticUnixTimestamp {
		return false
	}
	if trustReport.Source == "app_server" && trustReport.Evaluated && !trustReport.NeedsTrust() && trustReport.TotalManaged > 0 {
		return false
	}
	return hookStatus.configModAt-stats.lastSavedAt > hookConfigStaleReceiptGrace.Seconds()
}

func isSupportedTraeIDEHookEvent(event string) bool {
	switch event {
	case "SessionStart", "UserPromptSubmit", "PostToolUse", "Stop":
		return true
	default:
		return false
	}
}

func isLegacyTraeIDECommand(command string) bool {
	return strings.Contains(command, "trae_ide_hook.py")
}

func isLegacyManagedHookCommand(command string) bool {
	for _, name := range []string{"memlane-cognee", "memlane_cognee", "memlane-memory"} {
		if strings.Contains(command, "/"+name+"/") {
			return true
		}
	}
	return false
}

func isManagedHookEntry(entry any, hostKind string) (managed bool, needsRepair bool) {
	obj, ok := entry.(map[string]any)
	if !ok {
		return false, false
	}
	if obj["_memlane_managed"] == "memlane-hook" {
		managed = true
	}
	if cmd, ok := obj["command"].(string); ok {
		if strings.Contains(cmd, "memlane-hook") ||
			(strings.Contains(cmd, " hook ") && strings.Contains(cmd, "--gateway-url ") && strings.Contains(cmd, "--key-file ")) {
			managed = true
		}
		if hostKind == agents.HostTraeIDE && isLegacyTraeIDECommand(cmd) {
			managed = true
			needsRepair = true
		}
		if hostKind == agents.HostTraeX && isLegacyManagedHookCommand(cmd) {
			managed = true
			needsRepair = true
		}
	}
	if nested, ok := obj["hooks"].([]any); ok {
		for _, child := range nested {
			m, nr := isManagedHookEntry(child, hostKind)
			managed = managed || m
			needsRepair = needsRepair || nr
		}
	}
	return managed, needsRepair
}

func scanHookEventGroups(groups map[string]any, hostKind string) (installed bool, needsRepair bool) {
	for event, raw := range groups {
		rows, ok := raw.([]any)
		if !ok {
			continue
		}
		eventManaged := false
		for _, row := range rows {
			m, nr := isManagedHookEntry(row, hostKind)
			if m {
				eventManaged = true
				installed = true
			}
			if nr {
				needsRepair = true
			}
		}
		if eventManaged && hostKind == agents.HostTraeIDE && !isSupportedTraeIDEHookEvent(event) {
			needsRepair = true
		}
	}
	return installed, needsRepair
}

func scanHooksFile(path, kind string) (installed bool, needsRepair bool) {
	raw, err := agents.SafeReadFile(path, managedHookConfigLimit)
	if err != nil {
		return false, false
	}
	doc, err := agents.ParseJSONC(raw)
	if err != nil || doc == nil {
		return false, false
	}
	if kind == agents.HostAntigravity {
		if memlaneGroup, ok := doc["memlane"].(map[string]any); ok {
			if inst, _ := scanHookEventGroups(memlaneGroup, kind); inst {
				return true, false
			}
		}
	}
	if wrapper, ok := doc["hooks"].(map[string]any); ok {
		inst, repair := scanHookEventGroups(wrapper, kind)
		if inst {
			if kind == agents.HostTraeIDE {
				if v, hasVersion := doc["version"]; !hasVersion || (v != float64(1) && v != json.Number("1")) {
					repair = true
				}
			}
			return true, repair
		}
		return false, false
	}
	inst, repair := scanHookEventGroups(doc, kind)
	if inst {
		if kind == agents.HostTraeIDE {
			repair = true
		}
		return true, repair
	}
	return false, false
}

func inspectManagedHookStatus(kind, profileDir string) managedHookStatus {
	if strings.TrimSpace(profileDir) == "" {
		return managedHookStatus{}
	}
	if kind == agents.HostPi {
		bindingPath := filepath.Join(profileDir, "extensions", "memlane-cognee", "host-binding.json")
		raw, err := agents.SafeReadFile(bindingPath, managedHookConfigLimit)
		if err != nil {
			return managedHookStatus{}
		}
		var doc map[string]any
		if json.Unmarshal(raw, &doc) != nil || doc == nil {
			return managedHookStatus{}
		}
		if doc["managed_by"] == "memlane" {
			return managedHookStatus{installed: true, pluginVersion: "memlane-pi", configPath: bindingPath, configModAt: fileModUnix(bindingPath)}
		}
		if py, ok := doc["python"].(string); ok && strings.TrimSpace(py) != "" {
			return managedHookStatus{installed: true, pluginVersion: "memlane-pi", configPath: bindingPath, configModAt: fileModUnix(bindingPath)}
		}
		return managedHookStatus{}
	}

	if kind == agents.HostTraeX {
		// Current traecli loads hooks from <profile>/cli/hooks.json and ignores deprecated <profile>/hooks.json.
		cliHooksPath := filepath.Join(profileDir, "cli", "hooks.json")
		activeInstalled, activeRepair := scanHooksFile(cliHooksPath, kind)
		deprecatedInstalled, _ := scanHooksFile(filepath.Join(profileDir, "hooks.json"), kind)
		var result managedHookStatus
		if activeInstalled {
			result.installed = true
			result.pluginVersion = "memlane-hook"
			result.configPath = cliHooksPath
			result.configModAt = fileModUnix(cliHooksPath)
			if activeRepair {
				result.needsRepair = true
			}
		}
		if deprecatedInstalled {
			result.needsRepair = true
		}
		return result
	}

	hooksPath := filepath.Join(profileDir, "hooks.json")
	inst, repair := scanHooksFile(hooksPath, kind)
	if !inst {
		return managedHookStatus{}
	}
	return managedHookStatus{installed: true, needsRepair: repair, pluginVersion: "memlane-hook", configPath: hooksPath, configModAt: fileModUnix(hooksPath)}
}

type captureActivityStats struct {
	newSessions int
	savedCount  int
	lastSavedAt float64
}

func isCaptureManagedHostKind(kind string) bool {
	switch kind {
	case agents.HostCodex, agents.HostPi, agents.HostTraeX, agents.HostTraeIDE, agents.HostAntigravity:
		return true
	default:
		return false
	}
}

func parseUnixTimestamp(raw string) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999",
		"2006-01-02 15:04:05.999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return float64(t.UnixMilli()) / 1000
		}
	}
	if sec, err := strconv.ParseFloat(raw, 64); err == nil && sec > 0 && !math.IsNaN(sec) && !math.IsInf(sec, 0) {
		return sec
	}
	return 0
}

func (s *Server) loadCaptureActivity(ctx context.Context) (map[string]captureActivityStats, map[string]captureActivityStats, error) {
	type activityRow struct {
		key         string
		newSessions int
		savedCount  int
		maxUpdated  string
	}
	scanRow := func(row rowScanner) (activityRow, error) {
		var r activityRow
		err := row.Scan(&r.key, &r.newSessions, &r.savedCount, &r.maxUpdated)
		return r, err
	}
	inboxRows, err := readRows(ctx, s.cfg.Store.DB(),
		`SELECT agent_id, COUNT(DISTINCT session_id), COUNT(*), COALESCE(MAX(updated_at), '')
		 FROM capture_inbox WHERE status='succeeded' GROUP BY agent_id`, nil, scanRow)
	if err != nil {
		return nil, nil, err
	}
	receiptRows, err := readRows(ctx, s.cfg.Store.DB(),
		`SELECT user_id, COUNT(DISTINCT session_id), COUNT(*), COALESCE(MAX(updated_at), '')
		 FROM capture_receipts WHERE status='saved' GROUP BY user_id`, nil, scanRow)
	if err != nil {
		return nil, nil, err
	}
	toMap := func(rows []activityRow) map[string]captureActivityStats {
		out := make(map[string]captureActivityStats, len(rows))
		for _, r := range rows {
			out[r.key] = captureActivityStats{
				newSessions: r.newSessions,
				savedCount:  r.savedCount,
				lastSavedAt: parseUnixTimestamp(r.maxUpdated),
			}
		}
		return out
	}
	return toMap(inboxRows), toMap(receiptRows), nil
}

func mergeCaptureStats(a domain.Agent, inboxByAgent, receiptByUser map[string]captureActivityStats) captureActivityStats {
	stats := inboxByAgent[a.ID]
	merge := func(candidate captureActivityStats) {
		stats.newSessions = max(stats.newSessions, candidate.newSessions)
		stats.savedCount = max(stats.savedCount, candidate.savedCount)
		stats.lastSavedAt = max(stats.lastSavedAt, candidate.lastSavedAt)
	}
	if a.PrincipalID != "" {
		if r, ok := receiptByUser[a.PrincipalID]; ok {
			merge(r)
		}
	}
	if r, ok := receiptByUser[a.ID]; ok {
		merge(r)
	}
	return stats
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	allAgents, err := s.cfg.Store.ListAgents(ctx, false)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	registered := make([]domain.Agent, 0, len(allAgents))
	for _, a := range allAgents {
		if strings.TrimSpace(a.RetiredAt) == "" {
			registered = append(registered, a)
		}
	}
	defaults, err := s.cfg.Store.GetRegistryDefaults(ctx)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	inboxByAgent, receiptByUser, err := s.loadCaptureActivity(ctx)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	hosts := agents.DetectHosts(s.cfg.DiscoveryOptions)
	hostByKind := make(map[string]agents.DetectedHost, len(hosts))
	for _, host := range hosts {
		hostByKind[host.Kind] = host
	}
	checkedAt := float64(time.Now().UnixMilli()) / 1000
	force := r.URL.Query().Get("force") == "1"
	out := make([]connectionView, 0, len(hosts)+len(registered))
	seen := map[string]bool{}
	for _, a := range registered {
		row := connectionView{
			ID:         a.ID,
			Kind:       a.Kind,
			Status:     "registered",
			Registered: true,
			Profile:    strings.TrimSpace(a.HostProfile),
		}
		var detectedHost agents.DetectedHost
		if host, ok := hostByKind[a.Kind]; ok {
			detectedHost = host
			row.Detected = host.Detected
			row.Version = host.Version
			if row.Profile == "" {
				row.Profile = host.ProfileDir
			}
		}
		stats := mergeCaptureStats(a, inboxByAgent, receiptByUser)
		row.NewSessions = stats.newSessions
		if isCaptureManagedHostKind(a.Kind) && row.Detected {
			hookStatus := inspectManagedHookStatus(a.Kind, row.Profile)
			row.NeedsHookRepair = hookStatus.needsRepair
			var trustReport agents.HostHookTrustReport
			if hookStatus.installed && (a.Kind == agents.HostCodex || a.Kind == agents.HostTraeX) {
				trustReport = probeHostHookTrustCached(ctx, a.Kind, detectedHost.Executable, row.Profile, force)
			}
			if hookStatus.installed || stats.newSessions > 0 {
				row.Status = "configured_pending_new_session"
				switch {
				case trustReport.NeedsTrust():
					row.NeedsHookTrust = true
					row.UntrustedHookCount = trustReport.UntrustedCount
					row.ModifiedHookCount = trustReport.ModifiedCount
					row.DisabledHookCount = trustReport.DisabledCount
					row.UntrustedHookEvents = trustReport.UntrustedEvents
					row.PluginVersion = hookStatus.pluginVersion
					row.LastReceiptAt = stats.lastSavedAt
					row.Capture = &connectionCaptureState{
						State:       "needs_hook_trust",
						Permission:  "observed",
						Reason:      "host_hooks_untrusted",
						SavedCount:  stats.savedCount,
						LastSavedAt: stats.lastSavedAt,
						CheckedAt:   checkedAt,
					}
				case stats.newSessions > 0 && isReceiptStaleAfterHookConfig(a.Kind, hookStatus, stats, trustReport):
					row.PluginVersion = hookStatus.pluginVersion
					row.LastReceiptAt = stats.lastSavedAt
					row.Capture = &connectionCaptureState{
						State:       "waiting_session",
						Permission:  "observed",
						Reason:      "hook_config_updated_after_receipt",
						SavedCount:  stats.savedCount,
						LastSavedAt: stats.lastSavedAt,
						CheckedAt:   checkedAt,
					}
				case stats.newSessions > 0:
					row.Capture = &connectionCaptureState{
						State:       "healthy",
						Permission:  "observed",
						SavedCount:  stats.savedCount,
						LastSavedAt: stats.lastSavedAt,
						CheckedAt:   checkedAt,
					}
					row.LastReceiptAt = stats.lastSavedAt
					if hookStatus.installed {
						row.EnvironmentVerified = true
						row.PluginVersion = hookStatus.pluginVersion
					}
				default:
					row.Capture = &connectionCaptureState{
						State:      "waiting_session",
						Permission: "observed",
						CheckedAt:  checkedAt,
					}
				}
			}
		}
		seen[a.Kind] = true
		out = append(out, row)
	}
	for _, host := range hosts {
		if !seen[host.Kind] {
			profile := ""
			if host.Detected || host.ProfileFound {
				profile = host.ProfileDir
			}
			out = append(out, connectionView{
				ID:       host.Kind,
				Kind:     host.Kind,
				Status:   "not_connected",
				Detected: host.Detected,
				Version:  host.Version,
				Profile:  profile,
			})
		}
	}
	writeJSON(w, 200, map[string]any{"revision": defaults.Revision, "connections": out})
}
