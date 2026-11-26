package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/zwhu/memlane"
	"github.com/zwhu/memlane/internal/bootstrap"
)

const usage = `Usage: memlane [command]

  app       Open the desktop app
  daemon    Run the local Go gateway
  hook      Forward a host hook event
  mcp       Bridge stdio MCP requests to the local gateway
  setup     Manage installation and host connections
  dev       Start, inspect, open, stop, recover or sync hooks for a local runtime
`

func main() { os.Exit(runMain(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func runMain(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-psn_") {
		return runApp(nil, stderr)
	}
	switch args[0] {
	case "--check-first-store-resources":
		if len(args) != 2 {
			return reportError(stderr, fmt.Errorf("usage: --check-first-store-resources /absolute/path/to/python3"))
		}
		resCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return reportError(stderr, bootstrap.CheckFirstStoreResources(resCtx, args[1], memlane.RuntimeAssets))
	case "app":
		return runApp(args[1:], stderr)
	case "daemon":
		return runDaemon(ctx, args[1:], stdout, stderr)
	case "hook":
		return runHook(ctx, args[1:], stdin, stdout, stderr)
	case "mcp":
		return runMCP(ctx, args[1:], stdin, stdout, stderr)
	case "dev":
		return runDev(ctx, args[1:], stdout, stderr)
	case "setup":
		return runSetup(ctx, args[1:], stdout, stderr)
	case "doctor", "install", "connect", "verify", "repair", "stop", "__install-worker", "version", "--version", "--cli":
		return runSetup(ctx, args, stdout, stderr)
	case "help", "--help", "-h":
		_, err := io.WriteString(stdout, usage)
		return reportError(stderr, err)
	default:
		return reportError(stderr, fmt.Errorf("unknown command %q", args[0]))
	}
}

func reportError(stderr io.Writer, err error) int {
	if err == nil {
		return 0
	}
	if _, writeErr := fmt.Fprintln(stderr, err); writeErr != nil {
		return 1
	}
	return 1
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
