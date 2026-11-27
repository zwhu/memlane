package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/zwhu/memlane"
	"github.com/zwhu/memlane/core/hooks"
	"github.com/zwhu/memlane/kit/agents"
	"github.com/zwhu/memlane/kit/daemon"
)

func runDev(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return reportError(stderr, errors.New("usage: memlane dev <start|status|open|stop|recover|sync-hooks> [flags]"))
	}
	if args[0] == "sync-hooks" {
		return runDevSyncHooks(args[1:], stdout, stderr)
	}
	flags := flag.NewFlagSet("dev "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "runtime configuration")
	noUI := flags.Bool("no-ui", false, "start gateway without the desktop app")
	jsonOut := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
		return 0
	} else if err != nil || flags.NArg() != 0 {
		return 2
	}
	cfg, err := readRuntime(*path)
	if err != nil {
		return reportError(stderr, err)
	}
	if err := cfg.validateStore(); err != nil {
		return reportError(stderr, err)
	}
	ownerConfig, err := daemonOwnerConfig(cfg, nil)
	if err != nil {
		return reportError(stderr, err)
	}
	switch args[0] {
	case "start":
		err = startDev(ctx, cfg, ownerConfig.LockConfig, *noUI)
		if err == nil {
			return printResult(stdout, stderr, *jsonOut, "ready", map[string]string{"status": "ready"})
		}
	case "status":
		var inspection daemon.OwnerInspection
		inspection, err = ownerConfig.LockConfig.InspectOwnerReadOnly(cfg.StoreRoot)
		if err == nil {
			var gatewayOK, engineOK bool
			var healthStatus string
			var healthOwnerPID int
			if inspection.Locked {
				gatewayOK, engineOK, healthStatus, healthOwnerPID = gatewayHealth(ctx, cfg)
				if inspection.HasReceipt && inspection.Record.PID > 0 && healthOwnerPID != inspection.Record.PID {
					gatewayOK = false
					engineOK = false
					healthStatus = ""
				}
			}
			ready := inspection.Locked && gatewayOK
			status := "stopped"
			if ready {
				if healthStatus != "" {
					status = healthStatus
				} else {
					status = "ready"
				}
			} else if inspection.Locked {
				status = "unavailable"
			} else if inspection.HasReceipt {
				status = "stale"
			}
			return printResult(stdout, stderr, *jsonOut, status, map[string]any{
				"status":       status,
				"healthy":      ready,
				"gateway_ok":   gatewayOK,
				"engine_ok":    engineOK,
				"owner_locked": inspection.Locked,
			})
		}
	case "open":
		err = openDev(cfg)
		if err == nil {
			return printResult(stdout, stderr, *jsonOut, "opened", map[string]string{"status": "opened"})
		}
	case "stop":
		err = stopOwner(ctx, cfg)
		if err == nil {
			return printResult(stdout, stderr, *jsonOut, "stopped", map[string]string{"status": "stopped"})
		}
	case "recover":
		var recovery daemon.RecoverResult
		recovery, err = daemon.RecoverStaleOwner(ctx, daemon.RecoverOptions{LockConfig: ownerConfig.LockConfig, StoreRoot: cfg.StoreRoot, ConfigPath: cfg.Path, CheckURLs: []string{cfg.GatewayURL}})
		if err == nil {
			return printResult(stdout, stderr, *jsonOut, recovery.Status, recovery)
		}
	default:
		err = fmt.Errorf("unknown dev command %q", args[0])
	}
	return reportError(stderr, err)
}

func stopOwner(ctx context.Context, cfg runtimeConfig) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	ownerConfig, err := daemonOwnerConfig(cfg, nil)
	if err != nil {
		return err
	}
	return ownerConfig.LockConfig.StopVerifiedOwner(ctx, cfg.StoreRoot, cfg.Path, 38*time.Second, 3*time.Second)
}

func printResult(stdout, stderr io.Writer, jsonOut bool, label string, result any) int {
	if jsonOut {
		return reportError(stderr, json.NewEncoder(stdout).Encode(result))
	}
	_, err := fmt.Fprintln(stdout, label)
	return reportError(stderr, err)
}

func gatewayHealth(ctx context.Context, cfg runtimeConfig) (gatewayOK, engineOK bool, status string, ownerPID int) {
	token, err := hooks.ReadPrivateKey(cfg.ManagementTokenFile)
	if err != nil {
		return false, false, "", 0
	}
	addr, err := loopbackAddress(cfg.GatewayURL)
	if err != nil {
		return false, false, "", 0
	}
	state, err := daemon.LoopbackGetJSON(ctx, "http://"+addr, "/api/health", map[string]string{"Authorization": "Bearer " + token}, time.Second)
	if err != nil {
		return false, false, "", 0
	}
	gatewayOK = state["gateway_ok"] == true || state["ok"] == true
	if rawEngineOK, exists := state["engine_ok"]; exists {
		engineOK = rawEngineOK == true
	} else {
		engineOK = state["ok"] == true
	}
	status, _ = state["status"].(string)
	if rawPID, ok := state["owner_pid"].(float64); ok && rawPID > 0 {
		ownerPID = int(rawPID)
	}
	if gatewayOK && ownerPID == 0 {
		if mon, monErr := daemon.LoopbackGetJSON(ctx, "http://"+addr, "/v1/runtime/monitor", map[string]string{"Authorization": "Bearer " + token}, time.Second); monErr == nil {
			if rawPID, ok := mon["owner_pid"].(float64); ok && rawPID > 0 {
				ownerPID = int(rawPID)
			}
		}
	}
	return gatewayOK, engineOK, status, ownerPID
}

func gatewayReadyForOwner(ctx context.Context, cfg runtimeConfig, expectedOwnerPID int) bool {
	gatewayOK, _, _, ownerPID := gatewayHealth(ctx, cfg)
	if !gatewayOK {
		return false
	}
	if expectedOwnerPID > 0 && ownerPID != expectedOwnerPID {
		return false
	}
	return true
}

func gatewayReady(ctx context.Context, cfg runtimeConfig) bool {
	return gatewayReadyForOwner(ctx, cfg, 0)
}

func startDev(ctx context.Context, cfg runtimeConfig, lockConfig daemon.LockConfig, noUI bool) error {
	inspection, err := lockConfig.InspectOwnerReadOnly(cfg.StoreRoot)
	if err != nil {
		return err
	}
	if inspection.Locked {
		owner, err := lockConfig.VerifyOwnerRecord(ctx, cfg.StoreRoot, cfg.Path)
		if err != nil {
			return err
		}
		if !gatewayReadyForOwner(ctx, cfg, owner.PID) {
			return errors.New("store owner is running but gateway is unhealthy")
		}
	} else {
		if inspection.HasReceipt {
			return errors.New("store has a stale owner receipt; run memlane dev recover")
		}
		cmd, err := launchDetached(cfg, "daemon", "--config", cfg.Path)
		if err != nil {
			return err
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		wait, cancel := context.WithTimeout(ctx, 100*time.Second)
		defer cancel()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
	ready:
		for {
			select {
			case err := <-done:
				return fmt.Errorf("daemon exited before readiness: %v", err)
			case <-wait.Done():
				signalErr := cmd.Process.Signal(syscall.SIGTERM)
				if errors.Is(signalErr, os.ErrProcessDone) {
					signalErr = nil
				}
				return errors.Join(wait.Err(), signalErr)
			case <-ticker.C:
				if owner, err := lockConfig.VerifyOwnerRecord(wait, cfg.StoreRoot, cfg.Path); err == nil && owner.PID == cmd.Process.Pid && gatewayReadyForOwner(wait, cfg, owner.PID) {
					break ready
				}
			}
		}
	}
	if !noUI {
		return openDev(cfg)
	}
	return nil
}

func openDev(cfg runtimeConfig) error {
	cmd, err := launchDetached(cfg, "app", "--config", cfg.Path)
	if err != nil {
		return err
	}
	return cmd.Process.Release()
}

func launchDetached(cfg runtimeConfig, command string, args ...string) (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executable, err = resolveDetachedExecutable(cfg.StoreRoot, executable)
	if err != nil {
		return nil, err
	}
	logPath := filepath.Join(cfg.StoreRoot, "logs", command+".log")
	log, err := daemon.OpenPrivateLog(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(executable, append([]string{command}, args...)...)
	cmd.Env = detachedEnv(os.Environ())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = log, log
	startErr := cmd.Start()
	closeErr := log.Close()
	if startErr != nil {
		return nil, errors.Join(startErr, closeErr)
	}
	if closeErr != nil {
		return nil, errors.Join(closeErr, cmd.Process.Kill(), cmd.Wait())
	}
	return cmd, nil
}

const devAppInfoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>zh_CN</string>
	<key>CFBundleDisplayName</key>
	<string>Memlane</string>
	<key>CFBundleExecutable</key>
	<string>Memlane</string>
	<key>CFBundleIconFile</key>
	<string>AppIcon.icns</string>
	<key>CFBundleIdentifier</key>
	<string>com.memlane.desktop.dev</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>Memlane</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>0.1.0</string>
	<key>CFBundleVersion</key>
	<string>1</string>
	<key>NSHighResolutionCapable</key>
	<true/>
</dict>
</plist>
`

func resolveDetachedExecutable(storeRoot, executable string) (string, error) {
	if !isEphemeralExecutable(executable) {
		return executable, nil
	}
	return stageDevExecutable(storeRoot, executable)
}

func isEphemeralExecutable(executable string) bool {
	cleaned := filepath.Clean(executable)
	parts := strings.Split(cleaned, string(filepath.Separator))
	for _, part := range parts {
		if strings.HasPrefix(part, "go-build") || strings.HasPrefix(part, "memlane-task-") {
			return true
		}
	}
	tmp := filepath.Clean(os.TempDir())
	if tmp != "" && strings.HasPrefix(cleaned, tmp+string(filepath.Separator)) {
		return true
	}
	if resolvedTmp, err := filepath.EvalSymlinks(tmp); err == nil {
		resolvedTmp = filepath.Clean(resolvedTmp)
		if resolvedExe, err := filepath.EvalSymlinks(cleaned); err == nil {
			if strings.HasPrefix(filepath.Clean(resolvedExe), resolvedTmp+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

func devStagedExecutablePath(storeRoot string) string {
	baseDir := filepath.Join(storeRoot, "cache", "dev-bin")
	if runtime.GOOS == "darwin" {
		return filepath.Join(baseDir, "Memlane.app", "Contents", "MacOS", "Memlane")
	}
	return filepath.Join(baseDir, "memlane")
}

type syncedHost struct {
	Host    string   `json:"host"`
	Changed bool     `json:"changed"`
	Applied bool     `json:"applied"`
	Files   []string `json:"files"`
}

func runDevSyncHooks(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("dev sync-hooks", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "runtime configuration")
	host := flags.String("host", "", "specific connected host to sync")
	binary := flags.String("binary", "", "absolute Memlane executable path for hook commands")
	dryRun := flags.Bool("dry-run", false, "preview managed hook changes without writing")
	apply := flags.Bool("apply", false, "apply managed hook changes")
	jsonOut := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return 0
	} else if err != nil || flags.NArg() != 0 || (*apply && *dryRun) {
		return 2
	}
	cfg, err := readRuntime(*path)
	if err != nil {
		return reportError(stderr, err)
	}
	if err := cfg.validateStore(); err != nil {
		return reportError(stderr, err)
	}
	binaryPath, err := resolveSyncHooksBinary(cfg.StoreRoot, *binary, *apply)
	if err != nil {
		return reportError(stderr, err)
	}
	targets, err := resolveSyncHookTargets(cfg, *host, binaryPath)
	if err != nil {
		return reportError(stderr, err)
	}
	previews := make([]hooks.InstallResult, len(targets))
	for i, opts := range targets {
		res, err := hooks.InstallHostConfig(opts)
		if err != nil {
			return reportError(stderr, fmt.Errorf("%s: %w", opts.Host, err))
		}
		previews[i] = res
	}
	results := make([]syncedHost, 0, len(targets))
	affected := 0
	for i, opts := range targets {
		res := previews[i]
		changed := len(res.Previews) != 0
		applied := false
		if *apply && changed {
			opts.Apply = true
			res, err = hooks.InstallHostConfig(opts)
			if err != nil {
				return reportError(stderr, fmt.Errorf("%s: %w", opts.Host, err))
			}
			applied = res.Applied
		}
		files := make([]string, 0, len(res.Previews))
		for _, p := range res.Previews {
			files = append(files, p.Path)
		}
		affected += len(files)
		results = append(results, syncedHost{
			Host:    res.Host,
			Changed: changed,
			Applied: applied,
			Files:   files,
		})
	}
	return printResult(stdout, stderr, *jsonOut,
		fmt.Sprintf("Memlane host hook files affected: %d", affected),
		map[string]any{
			"schema_version": 1,
			"applied":        *apply,
			"binary":         binaryPath,
			"hosts":          results,
		})
}

func resolveSyncHooksBinary(storeRoot, explicit string, apply bool) (string, error) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		if !filepath.IsAbs(explicit) {
			return "", errors.New("binary path must be absolute")
		}
		cleaned := filepath.Clean(explicit)
		info, err := os.Stat(cleaned)
		if err != nil {
			return "", err
		}
		if info.IsDir() || info.Mode()&0o111 == 0 {
			return "", fmt.Errorf("binary path is not an executable file: %s", cleaned)
		}
		return cleaned, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if !isEphemeralExecutable(exe) {
		return filepath.Clean(exe), nil
	}
	if !apply {
		return devStagedExecutablePath(storeRoot), nil
	}
	return stageDevExecutable(storeRoot, exe)
}

func resolveSyncHookTargets(cfg runtimeConfig, selectedHost, binaryPath string) ([]hooks.InstallOptions, error) {
	home, _ := os.UserHomeDir()
	discovery := agents.DiscoveryOptions{}
	detected := agents.DetectHosts(discovery)
	if selectedHost != "" {
		canonical := hooks.CanonicalHost(selectedHost)
		if canonical == "" {
			return nil, fmt.Errorf("unknown hook host %q", selectedHost)
		}
		agentKind := canonical
		if canonical == hooks.HostTraeIDE {
			agentKind = agents.HostTraeIDE
		}
		profileDir := agents.DefaultProfileDir(agentKind, discovery)
		for _, item := range detected {
			if hooks.CanonicalHost(item.Kind) == canonical && item.ProfileDir != "" {
				profileDir = item.ProfileDir
				break
			}
		}
		keyFile := existingHostKeyFile(cfg, canonical, agentKind, profileDir)
		if keyFile == "" {
			return nil, fmt.Errorf("host %s is not connected in store (missing agent key file)", canonical)
		}
		return []hooks.InstallOptions{{
			Host:          canonical,
			ProfileDir:    profileDir,
			BinaryPath:    binaryPath,
			GatewayURL:    cfg.GatewayURL,
			KeyFile:       keyFile,
			MCPConfigPath: hooks.DefaultHostMCPConfigPath(canonical, home),
		}}, nil
	}
	targets := make([]hooks.InstallOptions, 0, len(detected))
	for _, item := range detected {
		canonical := hooks.CanonicalHost(item.Kind)
		if canonical == "" || !item.ProfileFound {
			continue
		}
		keyFile := existingHostKeyFile(cfg, canonical, item.Kind, item.ProfileDir)
		if keyFile == "" {
			continue
		}
		targets = append(targets, hooks.InstallOptions{
			Host:          canonical,
			ProfileDir:    item.ProfileDir,
			BinaryPath:    binaryPath,
			GatewayURL:    cfg.GatewayURL,
			KeyFile:       keyFile,
			MCPConfigPath: hooks.DefaultHostMCPConfigPath(canonical, home),
		})
	}
	return targets, nil
}

func existingHostKeyFile(cfg runtimeConfig, canonicalHost, agentKind, profileDir string) string {
	if profileDir != "" {
		sum := sha256.Sum256([]byte(profileDir))
		candidate := filepath.Join(cfg.StoreRoot, "agent-keys", agentKind+"-"+hex.EncodeToString(sum[:12])+".token")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if candidate := cfg.agentKeyFile(canonicalHost); candidate != "" {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func stageDevExecutable(storeRoot, sourcePath string) (string, error) {
	baseDir := filepath.Join(storeRoot, "cache", "dev-bin")
	target := filepath.Join(baseDir, "memlane")
	if runtime.GOOS == "darwin" {
		contentsDir := filepath.Join(baseDir, "Memlane.app", "Contents")
		macOSDir := filepath.Join(contentsDir, "MacOS")
		if err := os.MkdirAll(macOSDir, 0o700); err != nil {
			return "", err
		}
		if err := ensureDevBundleFile(filepath.Join(contentsDir, "PkgInfo"), []byte("APPL????")); err != nil {
			return "", err
		}
		if err := ensureDevBundleFile(filepath.Join(contentsDir, "Info.plist"), []byte(devAppInfoPlist)); err != nil {
			return "", err
		}
		if err := ensureDevAppIcon(contentsDir); err != nil {
			return "", err
		}
		target = filepath.Join(macOSDir, "Memlane")
	} else if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return "", err
	}

	srcHash, err := fileSHA256(sourcePath)
	if err != nil {
		return "", err
	}
	if dstHash, err := fileSHA256(target); err == nil && dstHash == srcHash {
		return target, nil
	}

	src, err := os.Open(sourcePath)
	if err != nil {
		return "", err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".memlane-stage-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := io.Copy(tmp, src); err != nil {
		return "", errors.Join(err, tmp.Close())
	}
	if err := tmp.Chmod(0o700); err != nil {
		return "", errors.Join(err, tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return "", err
	}
	cleanupTmp = false
	return target, nil
}

func ensureDevBundleFile(path string, content []byte) error {
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(content) {
		return nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, content, 0o600)
}

func ensureDevAppIcon(contentsDir string) error {
	if runtime.GOOS != "darwin" || len(memlane.AppIconPNG) == 0 {
		return nil
	}
	resourcesDir := filepath.Join(contentsDir, "Resources")
	if err := os.MkdirAll(resourcesDir, 0o700); err != nil {
		return err
	}
	icnsPath := filepath.Join(resourcesDir, "AppIcon.icns")
	stampPath := filepath.Join(resourcesDir, ".AppIcon.sha256")
	sum := sha256.Sum256(memlane.AppIconPNG)
	wantStamp := hex.EncodeToString(sum[:])
	if _, err := os.Stat(icnsPath); err == nil {
		if got, readErr := os.ReadFile(stampPath); readErr == nil && strings.TrimSpace(string(got)) == wantStamp {
			return nil
		}
	}
	tmpDir, err := os.MkdirTemp(resourcesDir, ".iconset-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	srcPNG := filepath.Join(tmpDir, "source.png")
	if err := os.WriteFile(srcPNG, memlane.AppIconPNG, 0o600); err != nil {
		return err
	}
	iconsetDir := filepath.Join(tmpDir, "AppIcon.iconset")
	if err := os.MkdirAll(iconsetDir, 0o700); err != nil {
		return err
	}
	specs := []struct {
		name string
		size string
	}{
		{"icon_16x16.png", "16"},
		{"icon_16x16@2x.png", "32"},
		{"icon_32x32.png", "32"},
		{"icon_32x32@2x.png", "64"},
		{"icon_128x128.png", "128"},
		{"icon_128x128@2x.png", "256"},
		{"icon_256x256.png", "256"},
		{"icon_256x256@2x.png", "512"},
		{"icon_512x512.png", "512"},
		{"icon_512x512@2x.png", "1024"},
	}
	for _, spec := range specs {
		if out, err := exec.Command("sips", "-z", spec.size, spec.size, srcPNG, "--out", filepath.Join(iconsetDir, spec.name)).CombinedOutput(); err != nil {
			return fmt.Errorf("sips %s: %w (%s)", spec.name, err, strings.TrimSpace(string(out)))
		}
	}
	builtICNS := filepath.Join(tmpDir, "AppIcon.icns")
	if out, err := exec.Command("iconutil", "-c", "icns", iconsetDir, "-o", builtICNS).CombinedOutput(); err != nil {
		return fmt.Errorf("iconutil: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if err := os.Rename(builtICNS, icnsPath); err != nil {
		return err
	}
	if err := os.WriteFile(stampPath, []byte(wantStamp+"\n"), 0o600); err != nil {
		return err
	}
	now := time.Now()
	_ = os.Chtimes(filepath.Dir(contentsDir), now, now)
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// detachedEnv removes ephemeral scripts/task.py scratch paths so long-lived
// daemon and desktop children never inherit a TMPDIR that will be deleted on exit.
func detachedEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		key, val, ok := strings.Cut(entry, "=")
		if !ok {
			out = append(out, entry)
			continue
		}
		if key == "MEMLANE_DISK_TASK" || key == "MEMLANE_TASK_OWNER_PID" {
			continue
		}
		if key == "TMPDIR" || key == "TMP" || key == "TEMP" {
			cleaned := filepath.Clean(val)
			if strings.HasPrefix(filepath.Base(cleaned), "memlane-task-") {
				parent := filepath.Dir(cleaned)
				if key == "TMPDIR" && parent != "" && parent != "." {
					out = append(out, key+"="+parent+string(filepath.Separator))
				}
				continue
			}
		}
		out = append(out, entry)
	}
	return out
}
