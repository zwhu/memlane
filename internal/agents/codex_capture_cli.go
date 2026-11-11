//go:build darwin || linux

package agents

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"time"
)

// RunCodexCaptureCLI is the installed hook entry, not a model-visible tool.
// Failures warn without blocking the user's ordinary Codex conversation.
func RunCodexCaptureCLI(args []string) int {
	flags := flag.NewFlagSet("capture-codex", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	root := flags.String("client-root", "", "installed Memlane client root")
	connection := flags.String("connection", "", "installed Codex identity")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return 2
	}
	// Memlane model/protocol work is outside daily collection even if a future
	// launcher happens to inherit a user's hook config. No original is read.
	if os.Getenv("MEMLANE_TOOL_MODE") != "" || os.Getenv("MEMLANE_USAGE_CHANNEL") != "" && os.Getenv("MEMLANE_USAGE_CHANNEL") != "daily" {
		_, _ = io.WriteString(os.Stdout, "{}\n")
		return 0
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var receipt CodexCaptureReceipt
	if err == nil {
		receipt, err = CaptureCodexHook(ctx, *root, *connection, raw, time.Now())
	}
	output := map[string]any{}
	if err != nil && err.Error() != "capture_not_authorized" {
		message := "Memlane 自动记录尚未取得本机持久化回执，请在 App 检查采集状态。"
		if receipt.LocalRetained {
			message = "Memlane 已暂存本次事件，但原文采集尚未完成，请在 App 检查采集状态。"
		}
		output["systemMessage"] = message
	}
	_ = json.NewEncoder(os.Stdout).Encode(output)
	return 0
}
