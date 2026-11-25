package settings

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (s *EnvironmentSetupState) installProgress() bootstrap.InstallProgress {
	if s.Busy {
		return s.Progress
	}
	return onboardingWorkerProgress(s.ExternalWorker)
}

func onboardingWorkerProgress(w bootstrap.InstallWorkerState) bootstrap.InstallProgress {
	return bootstrap.InstallProgress{
		Component: w.Component, Stage: w.Stage, Mirror: w.Mirror, Detail: w.Detail, BytesDone: w.BytesDone,
		BytesTotal: w.BytesTotal, Indeterminate: w.Indeterminate, BytesPerSec: w.BytesPerSec, ElapsedMs: w.ElapsedMs,
		FallbackAttempt: w.FallbackAttempt, FallbackTotal: w.FallbackTotal,
	}
}

// Render only the worker's receipt fields. The worker's raw output can include
// process diagnostics outside the installation scope.
func onboardingWorkerLogLines(worker, previous bootstrap.InstallWorkerState) []onboardingLogLine {
	if worker.State == "" {
		return nil
	}
	component := worker.Component
	if component == "" {
		component = "install"
		if worker.Stage == "starting_runtime" || (worker.State == "completed" && worker.StartRuntime) {
			component = "service"
		}
	}
	if worker.State != previous.State {
		switch worker.State {
		case "failed", "needs_review":
			return makeOnboardingLogLines("["+component+"] 命令行安装失败 · "+worker.SafeMessage, true)
		case "completed":
			message := "命令行安装已完成"
			if worker.StartRuntime {
				message = "命令行安装已完成 · 服务启动成功"
			}
			return makeOnboardingLogLines("["+component+"] "+message, false)
		case "cancelled":
			return makeOnboardingLogLines("["+component+"] 命令行安装已停止", false)
		}
	}
	quarter := func(w bootstrap.InstallWorkerState) int {
		return int(onboardingPercent(w.BytesDone, w.BytesTotal)) / 25
	}
	if worker.Component == previous.Component && worker.Stage == previous.Stage && worker.Detail == previous.Detail &&
		worker.FallbackAttempt == previous.FallbackAttempt && quarter(worker) <= quarter(previous) {
		return nil
	}
	message := "[" + component + "] " + onboardingInstallStage(worker.Stage)
	if metrics := environmentProgressMetrics(onboardingWorkerProgress(worker)); len(metrics) > 0 {
		message += " · " + strings.Join(metrics, " · ")
	}
	return makeOnboardingLogLines(message, false)
}

func environmentProgressMetrics(p bootstrap.InstallProgress) []string {
	var metrics []string
	const mib = 1 << 20
	if p.BytesTotal > 0 && !p.Indeterminate {
		metrics = append(metrics, fmt.Sprintf("%.1f / %.1f MiB", float64(p.BytesDone)/mib, float64(p.BytesTotal)/mib))
	} else if p.BytesDone > 0 {
		metrics = append(metrics, fmt.Sprintf("已下载 %.1f MiB", float64(p.BytesDone)/mib))
	}
	if p.BytesPerSec > 0 {
		metrics = append(metrics, fmt.Sprintf("%.1f MiB/s", p.BytesPerSec/mib))
	}
	if p.Mirror != "" {
		metrics = append(metrics, "来源："+bootstrap.RedactInstallerText(p.Mirror))
	}
	if p.FallbackAttempt > 1 {
		metrics = append(metrics, fmt.Sprintf("已自动切换备用镜像（第 %d/%d 个）", p.FallbackAttempt, p.FallbackTotal))
	}
	elapsed := max(p.Elapsed, time.Duration(p.ElapsedMs)*time.Millisecond)
	if elapsed > 0 {
		seconds := int(elapsed / time.Second)
		if seconds >= 60 {
			metrics = append(metrics, fmt.Sprintf("已用时 %dm %02ds", seconds/60, seconds%60))
		} else {
			metrics = append(metrics, fmt.Sprintf("已用时 %ds", seconds))
		}
	}
	return metrics
}

func onboardingInstallStage(stage string) string {
	switch stage {
	case "downloading":
		return "正在下载"
	case "extracting":
		return "正在解包"
	case "verifying":
		return "正在校验 SHA-256"
	case "probing":
		return "正在检测"
	case "syncing":
		return "正在安装运行依赖"
	case "starting_runtime":
		return "正在启动本地记忆服务"
	case "ready":
		return "已就绪"
	default:
		return "正在安装"
	}
}

func onboardingSourceLabel(source string) string {
	if source == bootstrap.DownloadSourceOfficial {
		return "官方源"
	}
	return "国内镜像"
}

func onboardingPercent(done, total int64) float32 {
	if total <= 0 {
		return 0
	}
	return float32(min(1, max(0, float64(done)/float64(total))) * 100)
}

// The progress callback is invoked in emission order on bootstrap's worker.
// UI work receives sanitized snapshots only, and byte ticks are sampled for logs.
func (a *Controller) trackEnvironmentProgress(ctx context.Context, generation uint64, initialSetup bool, plan bootstrap.Plan) context.Context {
	var last bootstrap.InstallProgress
	lastQuarter := -1
	return bootstrap.WithInstallProgress(ctx, func(p bootstrap.InstallProgress) {
		var lines []onboardingLogLine
		if initialSetup {
			changed := p.Component != last.Component || p.Stage != last.Stage || p.Mirror != last.Mirror || p.Detail != last.Detail ||
				p.FallbackAttempt != last.FallbackAttempt
			quarter := -1
			if !p.Indeterminate && p.BytesTotal > 0 {
				quarter = int(onboardingPercent(p.BytesDone, p.BytesTotal)) / 25
			}
			if changed || quarter > lastQuarter {
				stage := onboardingInstallStage(p.Stage)
				if p.Stage == "ready" {
					for _, item := range plan.Items {
						if item.Requirement.ID == p.Component && item.Observation.Status != bootstrap.Available {
							stage = "组件校验通过 · 安装位置：" + item.Requirement.Destination
							break
						}
					}
				}
				detail := strings.Join(environmentProgressMetrics(p), " · ")
				if detail != "" {
					stage += " · " + detail
				}
				lines = makeOnboardingLogLines("["+p.Component+"] "+stage, false)
			}
			last, lastQuarter = p, quarter
		}
		p.Detail = bootstrap.RedactInstallerText(p.Detail)
		p.Mirror = bootstrap.RedactInstallerText(p.Mirror)
		a.dispatch(func() {
			s := &a.environment
			if s.Generation == generation && s.Cancel != nil {
				s.Progress = p
				s.appendOnboardingLog(lines)
			}
		})
	})
}

func onboardingInstallBar(tok *kitui.Tokens, c *ui.Context, height, width float32, label string) {
	ui.Row(c).FillWidth().Height(height).Radius(height / 2).Background(tok.Fill2).
		Label(label).Children(func() {
		ui.Box(c).WidthPercent(width).Height(height).Radius(height / 2).Background(tok.Accent)
	})
}

func (a *Controller) environmentProgressBanner(c *ui.Context) {
	tok := a.tokens()
	s := &a.environment
	p := s.installProgress()
	starting := s.RuntimeStage != "" || p.Stage == "starting_runtime"
	title := s.RuntimeStage
	if starting {
		title = "正在启动本地记忆服务"
	} else {
		title = onboardingInstallStage(p.Stage)
		for _, component := range onboardingEnvironmentComponents {
			if slices.Contains(component.requirements, p.Component) {
				title += " · " + component.title
				break
			}
		}
	}
	width := s.onboardingTotalInstallProgress(p)
	trackLabel := fmt.Sprintf("组件准备进度 %.0f%%", width)
	if starting {
		width, trackLabel = 100, "组件已就绪，正在启动服务"
	}
	ui.Row(c.Key("environment-progress")).FillWidth().Padding(sp4).Gap(sp3).AlignItems(ui.Center).
		Radius(8).Background(tok.Fill1).Children(func() {
		onboardingStageBlocks(tok, c, !a.windowIsHidden() && !a.windowIsInactive())
		ui.Column(c).Grow(1).MinWidth(0).Gap(sp2).Children(func() {
			kitui.Text(c, tok, title, kitui.RoleBody).FontWeight(600)
			onboardingInstallBar(tok, c.Key("environment-progress-track"), 6, width, trackLabel)
			metrics := environmentProgressMetrics(p)
			if starting {
				metrics = []string{"校验组件 · 启动本机回环服务 · 等待健康检查"}
			} else if p.BytesPerSec > 0 && !p.Indeterminate && p.BytesDone < p.BytesTotal {
				remaining := int(math.Ceil(float64(p.BytesTotal-p.BytesDone) / p.BytesPerSec))
				metrics = append(metrics, fmt.Sprintf("当前下载剩余约 %d 秒", remaining))
			}
			if len(metrics) > 0 {
				ui.Text(c, strings.Join(metrics, " · ")).Font("SF Mono").FontSize(fsCaption).TextColor(tok.Text2).LineHeight(1.45)
			}
			if s.ExternalWorkerActive && !s.Busy {
				kitui.Badge(c, tok, "命令行安装进行中（只读进度）", "")
			}
		})
		if s.Cancel != nil {
			kitui.SmallButton(c, tok, "停止", nil, s.Cancel)
		}
	})
}

func (s *EnvironmentSetupState) onboardingTotalInstallProgress(p bootstrap.InstallProgress) float32 {
	var completed, total int64
	for _, item := range s.Plan.Items {
		if item.Observation.Status == bootstrap.Available {
			continue
		}
		// A zero-byte install still contributes after its actual ready receipt.
		size := max(1, item.Requirement.DownloadBytes)
		total += size
		if environmentRowStatus(item, s.Journal, true) == bootstrap.Ready {
			completed += size
		} else if item.Requirement.ID == p.Component && !p.Indeterminate {
			completed += int64(float64(size) * float64(onboardingPercent(p.BytesDone, p.BytesTotal)) / 100)
		}
	}
	return onboardingPercent(completed, total)
}
