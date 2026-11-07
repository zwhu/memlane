// memlaned is the independent business service packaged with the light client.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/zwhu/memlane/internal/daemon"
)

func main() {
	path := flag.String("config", os.Getenv("MEMLANE_SERVICE_CONFIG"), "explicit service configuration file")
	resume := flag.Bool("resume", false, "explicitly resume a stopped service before starting")
	flag.Parse()
	if !filepath.IsAbs(*path) {
		fmt.Fprintln(os.Stderr, "an explicit absolute service config is required")
		os.Exit(2)
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "service config unavailable")
		os.Exit(1)
	}
	var config daemon.Config
	if len(data) > 65536 || json.Unmarshal(data, &config) != nil {
		fmt.Fprintln(os.Stderr, "invalid service config")
		os.Exit(1)
	}
	if *resume {
		if err = daemon.Resume(config.Root); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = daemon.Run(ctx, config); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
