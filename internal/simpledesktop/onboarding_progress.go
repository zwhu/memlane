package simpledesktop

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
)

func (s *environmentSetupState) installProgress() bootstrap.InstallProgress {
	if s.Busy {
		return s.Progress
	}
	w := s.ExternalWorker
	return bootstrap.InstallProgress{
		Component: w.Component, Stage: w.Stage, Mirror: w.Mirror, BytesDone: w.BytesDone,
		BytesTotal: w.BytesTotal, Indeterminate: w.Indeterminate, BytesPerSec: w.BytesPerSec, ElapsedMs: w.ElapsedMs,
		FallbackAttempt: w.FallbackAttempt, FallbackTotal: w.FallbackTotal,
	}
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

func (a *App) environmentProgressBanner(c *ui.Context) {
	s := &a.environment
	p := s.installProgress()
	title := s.RuntimeStage
	if title == "" {
		switch p.Stage {
		case "downloading":
			title = "正在下载"
		case "extracting":
			title = "正在解包"
		case "verifying":
			title = "正在校验"
		case "probing":
			title = "正在检测"
		case "starting_runtime":
			title = "正在启动记忆服务"
		case "ready":
			title = "组件已就绪"
		default:
			title = "正在准备本地记忆环境"
		}
	}
	switch p.Component {
	case "uv", "python", "runtime":
		title += " · 隔离运行环境"
	case "cognee-source":
		title += " · 本地记忆引擎"
	case "embedding":
		title += " · 本地语义搜索模型"
	case "agent-plugin":
		title += " · Agent 接入组件"
	}
	progressLabel, width := "进度未定", float32(28)
	if !p.Indeterminate && p.BytesTotal > 0 {
		width = float32(min(1, max(0, float64(p.BytesDone)/float64(p.BytesTotal))) * 100)
		progressLabel = fmt.Sprintf("进度 %.0f%%", width)
	}
	ui.Column(c.Key("environment-progress")).FillWidth().Padding(sp4).Gap(sp2).
		Radius(radius).Background(tok.Fill1).Children(func() {
		ui.Row(c).FillWidth().Gap(sp2).AlignItems(ui.Center).Children(func() {
			homeText(c, title, homeBody).FontWeight(600).Grow(1)
			if s.ExternalWorkerActive && !s.Busy {
				v4Badge(c, "命令行安装进行中（只读进度）", "")
			}
			if s.Busy && s.Cancel != nil && s.RuntimeStage == "" {
				v4SmallButton(c, "停止安装", nil, s.Cancel)
			}
		})
		ui.Row(c.Key("environment-progress-track")).FillWidth().Height(6).Radius(3).Background(tok.Fill2).
			Label(progressLabel).Children(func() {
			ui.Box(c.Key("environment-progress-fill")).WidthPercent(width).Height(6).Radius(3).Background(tok.Accent)
		})
		if metrics := environmentProgressMetrics(p); len(metrics) > 0 {
			homeText(c, strings.Join(metrics, " · "), homeMeta).LineHeight(1.45)
		}
	})
}
