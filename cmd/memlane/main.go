// memlane is the lightweight MCP/CLI memory bridge bundled with the App.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/records"
)

func run() int {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: memlane mcp|call --root /absolute/service/root --connection UUID --generation N [--workspace /bound/repo] [--tool memory_save]")
		return 2
	}
	mode := os.Args[1]
	flags := flag.NewFlagSet(mode, flag.ContinueOnError)
	var c agentbridge.Config
	var tool string
	flags.StringVar(&c.Root, "root", "", "absolute service data root")
	flags.StringVar(&c.ConnectionID, "connection", "", "registered connection ID")
	flags.Int64Var(&c.Generation, "generation", 0, "configuration generation")
	flags.StringVar(&c.Workspace, "workspace", "", "explicit trusted workspace; process cwd is never used")
	flags.StringVar(&tool, "tool", "", "CLI memory tool")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		return 2
	}
	if err := c.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if mode == "mcp" {
		if err := c.MCP(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	if mode != "call" || tool == "" {
		fmt.Fprintln(os.Stderr, "mcp or call with --tool required")
		return 2
	}
	var args records.AgentArguments
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 1024*1024))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&args)
	if err == nil {
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			err = fmt.Errorf("one JSON object required")
		}
	}
	if err != nil {
		json.NewEncoder(os.Stdout).Encode(agentbridge.Result{Protocol: 1, Error: &agentbridge.Failure{Code: "invalid_request", Message: err.Error()}})
		return 2
	}
	ctx, stop := context.WithTimeout(ctx, 120*time.Second)
	defer stop()
	out := c.Call(ctx, tool, args)
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		return 1
	}
	if !out.OK {
		return 1
	}
	return 0
}
func main() { os.Exit(run()) }
