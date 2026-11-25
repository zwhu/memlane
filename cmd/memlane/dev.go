package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/zwhu/memlane/core/hooks"
	"github.com/zwhu/memlane/kit/daemon"
)

func runDev(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return reportError(stderr, errors.New("usage: memlane dev <start|status|open|stop|recover> [flags]"))
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
			ready := inspection.Locked && gatewayReady(ctx, cfg)
			status := "stopped"
			if ready {
				status = "ready"
			} else if inspection.Locked {
				status = "unavailable"
			} else if inspection.HasReceipt {
				status = "stale"
			}
			return printResult(stdout, stderr, *jsonOut, status, map[string]any{"status": status, "healthy": ready, "owner_locked": inspection.Locked})
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

func gatewayReady(ctx context.Context, cfg runtimeConfig) bool {
	token, err := hooks.ReadPrivateKey(cfg.ManagementTokenFile)
	if err != nil {
		return false
	}
	addr, err := loopbackAddress(cfg.GatewayURL)
	if err != nil {
		return false
	}
	state, err := daemon.LoopbackGetJSON(ctx, "http://"+addr, "/api/health", map[string]string{"Authorization": "Bearer " + token}, time.Second)
	return err == nil && state["ok"] == true
}

func startDev(ctx context.Context, cfg runtimeConfig, lockConfig daemon.LockConfig, noUI bool) error {
	inspection, err := lockConfig.InspectOwnerReadOnly(cfg.StoreRoot)
	if err != nil {
		return err
	}
	if inspection.Locked {
		if _, err := lockConfig.VerifyOwnerRecord(ctx, cfg.StoreRoot, cfg.Path); err != nil {
			return err
		}
		if !gatewayReady(ctx, cfg) {
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
				if owner, err := lockConfig.VerifyOwnerRecord(wait, cfg.StoreRoot, cfg.Path); err == nil && owner.PID == cmd.Process.Pid && gatewayReady(wait, cfg) {
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
	logPath := filepath.Join(cfg.StoreRoot, "logs", command+".log")
	log, err := daemon.OpenPrivateLog(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(executable, append([]string{command}, args...)...)
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
