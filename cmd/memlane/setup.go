package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/zwhu/memlane"
	"github.com/zwhu/memlane/core/hooks"
	"github.com/zwhu/memlane/core/setup"
	"github.com/zwhu/memlane/engine"
	"github.com/zwhu/memlane/internal/setupcli"
	"github.com/zwhu/memlane/kit/agents"
	"github.com/zwhu/memlane/kit/daemon"
)

func runSetup(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		args = []string{"help"}
	}
	if args[0] == "uninstall" {
		return runUninstall(ctx, args[1:], stdout, stderr)
	}
	if handled, code := setupcli.DispatchIO(ctx, args, memlane.RuntimeAssets, engine.BootstrapAssets, setup.DefaultVersion, stdout, stderr); handled {
		return code
	}
	return reportError(stderr, fmt.Errorf("unknown setup command %q", args[0]))
}

type uninstalledHost struct {
	Host    string   `json:"host"`
	Applied bool     `json:"applied"`
	Files   []string `json:"files"`
}

func runUninstall(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("setup uninstall", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "runtime configuration")
	host := flags.String("host", "", "host to uninstall")
	dryRun := flags.Bool("dry-run", false, "preview the changes")
	apply := flags.Bool("apply", false, "stop runtime and apply changes")
	jsonOut := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return 0
	} else if err != nil || flags.NArg() != 0 || *apply && *dryRun {
		return 2
	}
	cfg, err := readRuntime(*path)
	if err != nil && (!errors.Is(err, os.ErrNotExist) || *path != "" || *apply) {
		return reportError(stderr, err)
	}
	var targets []hooks.InstallOptions
	home, _ := os.UserHomeDir()
	discovery := agents.DiscoveryOptions{}
	if *host != "" {
		kind := hooks.CanonicalHost(*host)
		if kind == "" {
			return reportError(stderr, errors.New("unknown uninstall host"))
		}
		if kind == hooks.HostTraeIDE {
			kind = agents.HostTraeIDE
		}
		targets = append(targets, hooks.InstallOptions{
			Host: kind, ProfileDir: agents.DefaultProfileDir(kind, discovery),
			MCPConfigPath: hooks.DefaultHostMCPConfigPath(kind, home),
		})
	} else {
		for _, item := range agents.DetectHosts(discovery) {
			if hooks.CanonicalHost(item.Kind) != "" && item.ProfileFound {
				targets = append(targets, hooks.InstallOptions{
					Host: item.Kind, ProfileDir: item.ProfileDir,
					MCPConfigPath: hooks.DefaultHostMCPConfigPath(item.Kind, home),
				})
			}
		}
	}
	if *apply {
		for _, opts := range targets {
			if _, err := hooks.UninstallHostConfig(opts); err != nil {
				return reportError(stderr, err)
			}
		}
		if err := cfg.validateStore(); err != nil {
			return reportError(stderr, err)
		}
		s, err := daemon.InspectOwnerReadOnly(cfg.StoreRoot)
		if err != nil {
			return reportError(stderr, err)
		}
		if s.Locked {
			if err := stopOwner(ctx, cfg); err != nil {
				return reportError(stderr, err)
			}
		}
	}
	results := make([]uninstalledHost, 0, len(targets))
	count := 0
	for _, opts := range targets {
		opts.Apply = *apply
		out, err := hooks.UninstallHostConfig(opts)
		if err != nil {
			return reportError(stderr, err)
		}
		item := uninstalledHost{Host: out.Host, Applied: out.Applied, Files: make([]string, 0, len(out.Previews))}
		for _, p := range out.Previews {
			item.Files = append(item.Files, p.Path)
		}
		count += len(item.Files)
		results = append(results, item)
	}
	return printResult(stdout, stderr, *jsonOut, fmt.Sprintf("Memlane configuration files affected: %d", count),
		map[string]any{"schema_version": 1, "applied": *apply, "hosts": results})
}
