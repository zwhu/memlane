package main

import (
	"context"
	"flag"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/zwhu/memlane/core/hooks"
)

func runHook(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cfg := hooks.Config{ErrOut: stderr}
	if os.Getenv("MEMLANE_PROCESSING_SESSION") == "1" {
		return reportError(stderr, hooks.Run(ctx, "", "", stdin, stdout, cfg))
	}
	if len(args) < 2 {
		if _, err := io.WriteString(stderr, "usage: memlane hook <host> <event> [flags]\n"); err != nil {
			return 1
		}
		return 2
	}
	flags := flag.NewFlagSet("hook", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&cfg.GatewayURL, "gateway-url", os.Getenv("MEMLANE_GATEWAY_URL"), "loopback gateway URL")
	flags.StringVar(&cfg.KeyFile, "key-file", os.Getenv("MEMLANE_AGENT_KEY_FILE"), "private agent key file")
	if ms, err := strconv.Atoi(os.Getenv("MEMLANE_HOOK_TIMEOUT_MS")); err == nil && ms > 0 && uint64(ms) <= uint64((1<<63-1)/time.Millisecond) {
		cfg.Timeout = time.Duration(ms) * time.Millisecond
	}
	flags.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "gateway request timeout")
	flags.BoolVar(&cfg.Verbose, "verbose", false, "write redacted hook diagnostics")
	if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 {
		return 2
	}
	event := args[1]
	if event != hooks.SessionStart && (cfg.GatewayURL == "" || cfg.KeyFile == "") {
		if runtime, err := readRuntime(""); err == nil {
			cfg.GatewayURL = firstNonempty(cfg.GatewayURL, runtime.GatewayURL)
			cfg.KeyFile = firstNonempty(cfg.KeyFile, runtime.agentKeyFile(hooks.CanonicalHost(args[0])))
		}
	}
	return reportError(stderr, hooks.Run(ctx, args[0], event, stdin, stdout, cfg))
}
