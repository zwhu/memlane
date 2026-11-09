// This unbundled verifier exercises the actual C runner on a new synthetic
// repository. Binding/connection receipts are fixtures; it proves neither the
// daemon task owner, production connection verification, UI, saves nor indexes.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func main() { os.Exit(run()) }
func run() int {
	root := flag.String("root", "", "new absolute synthetic evidence directory")
	executable := flag.String("executable", "", "actual Codex absolute executable")
	authHome := flag.String("auth-home", "", "explicit Codex current configuration/login directory")
	guard := flag.String("guard", "", "packaged task guard executable")
	mode := flag.String("mode", "inspect", "inspect, normal or cancel")
	flag.Parse()
	if !filepath.IsAbs(*root) || !filepath.IsAbs(*executable) || !filepath.IsAbs(*authHome) || (*mode != "inspect" && *mode != "normal" && *mode != "cancel") {
		return 2
	}
	if err := os.Mkdir(*root, 0700); err != nil {
		return 2
	}
	workspace := filepath.Join(*root, "project")
	if err := os.Mkdir(workspace, 0700); err != nil {
		return 2
	}
	if _, err := exec.Command("/usr/bin/git", "init", workspace).CombinedOutput(); err != nil {
		return 2
	}
	material := "# TicketBox 合成项目\n\n本项目使用 pnpm 管理依赖；依赖变化时提交 pnpm-lock.yaml。\n项目知识只能归属于 TicketBox，不推断其他仓库规则。\nnormalize_ticket 只接受 str，去除边缘空白、连续空白转短横线、英文字母转小写；未测试不声称测试通过。\n"
	os.WriteFile(filepath.Join(workspace, "README.md"), []byte(material), 0600)
	os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("本仓库是本轮合成验证材料。整理时核对README与代码，禁止执行项目脚本、修改文件或编造测试结果。"), 0600)
	os.WriteFile(filepath.Join(workspace, "ticket.py"), []byte("def normalize_ticket(value):\n    if not isinstance(value, str):\n        raise TypeError('str required')\n    return '-'.join(value.strip().split()).lower()\n"), 0600)
	config := filepath.Join(*root, "installation-receipt-fixture.toml")
	block := "# this is a synthetic installation receipt fixture\n"
	os.WriteFile(config, []byte(block), 0600)
	i := agents.Installation{Connection: records.Connection{ID: uuid.NewString(), Agent: "codex", Generation: 1}, AgentVersion: "codex-cli 0.167.0-alpha.3.2", Options: agents.InstallOptions{AgentPath: *executable}, ConfigPath: config, Block: block, VerifiedGeneration: 1, VerifiedAt: "explicit synthetic receipt fixture"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	started := time.Now()
	plan, err := agents.ResolveCodexProject(ctx, i, workspace, *authHome, *guard)
	report := map[string]any{"status": "failed", "mode": *mode, "synthetic_only": true, "actual_synthetic_git_repository": true, "installation_verification_fixture": true, "actual_connection_verified": false, "actual_daemon_task": false, "actual_app": false, "actual_save": false, "actual_index": false, "model_turn_started": false, "credentials_copied": false, "fact_quality": "requires_itemwise_review"}
	if err == nil {
		report["actual_config_read"] = true
		report["executor"] = plan.Executor
		if *mode == "inspect" {
			report["status"] = "passed"
		} else {
			task := records.ProjectTask{ID: uuid.NewString(), Attempt: 1, Snapshot: records.ProjectTaskSnapshot{Workspace: workspace, Executor: plan.Executor}}
			triggered := false
			var out agents.CodexProjectExecution
			out, err = agents.RunCodexProject(ctx, plan, task, nil, func(event string) {
				if *mode == "cancel" && event == "model_update" {
					triggered = true
					cancel()
				}
			})
			report["execution"], report["model_turn_started"], report["active_cancel_triggered"] = out, out.Turn != "", triggered
			if out.RejectedSourcePath != "" {
				report["synthetic_rejected_source_path"] = out.RejectedSourcePath
			}
			if err == nil && out.Proposal != nil && out.Terminal && out.Reaped && out.GroupGone {
				report["status"] = "passed"
			}
			if *mode == "cancel" && triggered && errors.Is(err, context.Canceled) && out.InterruptSent && out.InterruptAcknowledged && out.Reaped && out.GroupGone && out.Proposal == nil {
				report["status"] = "passed"
			}
		}
	}
	if err != nil {
		report["error"] = err.Error()
	}
	report["seconds"] = time.Since(started).Seconds()
	b, _ := json.MarshalIndent(report, "", "  ")
	os.WriteFile(filepath.Join(*root, "report.json"), append(b, '\n'), 0600)
	json.NewEncoder(os.Stdout).Encode(map[string]any{"status": report["status"], "report": filepath.Join(*root, "report.json"), "error": report["error"]})
	if report["status"] != "passed" {
		return 1
	}
	return 0
}
