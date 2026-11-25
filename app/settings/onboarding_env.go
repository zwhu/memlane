package settings

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/bootstrap"
	kitui "github.com/zwhu/memlane/kit/ui"
)

const (
	onboardingLogMaxLines = 500
	onboardingLogMaxBytes = 2048
)

type onboardingLogLine struct {
	text   string
	failed bool
}

// Call from the installation or startup worker, before handing data to the UI.
func makeOnboardingLogLines(message string, failed bool) []onboardingLogLine {
	timestamp := time.Now().Format("15:04:05")
	lines := strings.Split(strings.ToValidUTF8(bootstrap.RedactInstallerText(message), "�"), "\n")
	lines = lines[max(0, len(lines)-onboardingLogMaxLines):]
	out := make([]onboardingLogLine, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > onboardingLogMaxBytes {
			end := onboardingLogMaxBytes
			for !utf8.RuneStart(line[end]) {
				end--
			}
			line = line[:end] + "…"
		}
		out = append(out, onboardingLogLine{timestamp + "  " + line, failed})
	}
	return out
}

func (s *EnvironmentSetupState) appendOnboardingLog(lines []onboardingLogLine) {
	if len(lines) == 0 {
		return
	}
	excess := len(s.OnboardingLog) + len(lines) - onboardingLogMaxLines
	if excess > 0 {
		copy(s.OnboardingLog, s.OnboardingLog[excess:])
		s.OnboardingLog = s.OnboardingLog[:len(s.OnboardingLog)-excess]
	}
	s.OnboardingLog = append(s.OnboardingLog, lines...)
	for _, line := range lines {
		if line.failed {
			s.OnboardingLogOpen = true
			break
		}
	}
	s.onboardingLogScroll.Y = math.MaxFloat32
}

func onboardingMiB(n int64) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

func onboardingDownloadSize(plan bootstrap.Plan) (size int64, unknown bool) {
	for _, item := range plan.Items {
		if item.Observation.Status != bootstrap.Available {
			unknown = unknown || item.Requirement.DownloadBytes < 0
			size += max(0, item.Requirement.DownloadBytes)
		}
	}
	return
}

func onboardingDownloadLabel(plan bootstrap.Plan) string {
	size, unknown := onboardingDownloadSize(plan)
	if unknown {
		return onboardingMiB(size) + " + 待定"
	}
	return onboardingMiB(size)
}

func (a *Controller) onboardingEnvironmentAction() (label string, disabled bool, run func()) {
	s := &a.environment
	blocked := s.Busy || s.Loading || s.ExternalWorkerActive || s.workerInspecting ||
		s.selectedAgentsSaving || s.RestartingStaleDaemon || s.onboardingStartRequested || a.integrations.Busy
	switch {
	case s.RuntimeStage != "" || s.onboardingStartRequested || s.ExternalWorkerActive && s.ExternalWorker.Stage == "starting_runtime":
		return "正在启动…", true, nil
	case s.Busy || s.ExternalWorkerActive:
		return "正在安装…", true, nil
	case s.Loading:
		return "正在检测…", true, nil
	case s.onboardingRedetectRequired:
		return "重新检测", blocked, a.refreshEnvironment
	case s.RuntimeStarted:
		return "继续", blocked, a.continueEnvironment
	case s.Plan.Complete():
		label := "启动服务并继续"
		if s.OnboardingRuntimeFailed {
			label = "重新启动服务"
			if s.hasOnboardingPlanConsent() {
				label = "重试"
			}
		}
		return label, blocked || s.ownerInspecting, a.continueEnvironment
	case s.canRetryOnboardingInstall():
		return "重试", blocked || s.Environment == nil, func() {
			if s.canRetryOnboardingInstall() {
				a.runEnvironment(false)
			}
		}
	case s.Loaded && len(s.Plan.Items) > 0:
		displayedDigest := s.Plan.Digest()
		return "下载并安装 · " + onboardingDownloadLabel(s.Plan), blocked || s.Environment == nil, func() {
			if !s.onboardingRedetectRequired && s.Plan.Digest() == displayedDigest {
				a.runEnvironment(true)
			}
		}
	default:
		return "正在检测…", true, nil
	}
}

func (s *EnvironmentSetupState) hasOnboardingPlanConsent() bool {
	return s.Journal.ApprovedDigest != "" && s.Journal.ApprovedDigest == s.Plan.Digest()
}

func (s *EnvironmentSetupState) canRetryOnboardingInstall() bool {
	return s.Error != "" && s.Journal.Operation != "" && s.hasOnboardingPlanConsent() &&
		s.onboardingInstallFailureDigest == s.Journal.ApprovedDigest
}

func (a *Controller) onboardingEnvironmentHint() string {
	s := &a.environment
	switch {
	case s.Busy || s.ExternalWorkerActive || s.onboardingStartRequested:
		return "可以离开这个窗口，安装会继续进行"
	case s.canRetryOnboardingInstall() || s.OnboardingRuntimeFailed:
		return "已完成的组件会保留"
	case s.Loaded && len(s.Plan.Items) > 0 && !s.Plan.Complete():
		return "点击即表示同意安装上方「待下载」的组件"
	default:
		return ""
	}
}

func (a *Controller) copyOnboardingDiagnostic(c *ui.Context) {
	_, prompt := a.environmentDiagnostic()
	c.WriteClipboard(prompt)
	a.showOnboardingToast("已复制诊断，请在 Agent 的新会话中粘贴")
}

func (a *Controller) onboardingEnvironment(c *ui.Context) {
	tok := a.tokens()
	s := &a.environment
	title := "准备本地记忆环境"
	if s.canRetryOnboardingInstall() || s.OnboardingRuntimeFailed {
		title = "准备未完成，可以重试"
	}
	onboardingHeading(tok, c, onboardingEnv, title, "Memlane 在本机运行记忆引擎和语义搜索模型，记忆正文、向量与图谱都不会上传。")
	a.onboardingEnvironmentSummary(c)

	kitui.Group(c, tok, "准备内容", "", nil, func() {
		progress := s.installProgress()
		for _, component := range onboardingEnvironmentComponents {
			row := s.onboardingComponentRow(component, progress)
			kitui.Row(c, tok, row.options(tok, c))
		}
	})
	if s.Loaded && len(s.Plan.Items) > 0 && !s.Plan.Complete() && !s.Busy && !s.ExternalWorkerActive {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			kitui.Text(c, tok, "下载来源", kitui.RoleMeta).Grow(1)
			a.onboardingDownloadSource(c)
		})
	}
	if len(s.OnboardingLog) > 0 {
		a.onboardingInstallLog(c)
	}
	if s.Loaded {
		icon := kitui.IconChev
		if s.TechDetailsOpen {
			icon = kitui.IconDown
		}
		kitui.GhostButton(c, tok, "技术清单（版本、校验值与安装位置）", icon, func() { s.TechDetailsOpen = !s.TechDetailsOpen }).AlignSelf(ui.Start)
		if s.TechDetailsOpen {
			a.onboardingEnvironmentTechnical(c)
		}
	}
	if s.Message != "" {
		kitui.Text(c, tok, s.Message, kitui.RoleMeta)
	}
}

var onboardingDownloadIcon = kitui.IconSVG(`<path d="M10 3v9M6 8l4 4 4-4M4 13v3h12v-3"/>`)

func (a *Controller) onboardingEnvironmentSummary(c *ui.Context) {
	tok := a.tokens()
	s := &a.environment
	title, detail := "", ""
	icon, color := onboardingDownloadIcon, tok.Accent
	switch {
	case s.Busy || s.ExternalWorkerActive:
		a.environmentProgressBanner(c)
		return
	case s.Error != "":
		title, detail = "准备未完成", bootstrap.RedactInstallerText(s.Error)
		icon, color = kitui.IconClose, tok.Danger
	case s.RuntimeStarted:
		title, detail = "本地记忆环境已就绪", "记忆服务已在本机启动。"
		icon, color = kitui.IconCheck, tok.OK
	case s.Loading || !s.Loaded:
		title, detail = "正在检测本机已有组件…", "已有的兼容组件会直接复用，只下载缺少的部分。"
	case s.Plan.Complete():
		title, detail = "无需下载", "本机已有全部组件，并已通过校验。下一步启动本地记忆服务。"
		icon, color = kitui.IconCheck, tok.OK
	default:
		title = "需下载 " + onboardingDownloadLabel(s.Plan)
		reused := 0
		for _, item := range s.Plan.Items {
			if item.Observation.Status == bootstrap.Available {
				reused++
			}
		}
		detail = fmt.Sprintf("已复用本机 %d 项组件。新组件全部放在 Memlane 应用目录，不改系统 Python 和环境变量，可一键卸载。", reused)
	}
	ui.Row(c.Key("environment-summary")).FillWidth().Padding(sp4).Gap(sp4).
		Radius(8).Background(tok.Fill1).AlignItems(ui.Center).Children(func() {
		if s.Error == "" && (s.Loading || !s.Loaded) {
			kitui.Spinner(c, tok)
		} else {
			ui.Row(c).Size(40, 40).Shrink(0).Radius(10).Background(color.Alpha(.14)).
				AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Icon(c, icon).FontSize(20).TextColor(color)
			})
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			size := fsTitle
			if s.Loading || s.Error != "" {
				size = fsBody
			}
			ui.Text(c, title).FontSize(size).TextColor(tok.Text1).FontWeight(600).FontFeatures("tnum")
			ui.Text(c, detail).FontSize(fsFootnote).TextColor(tok.Text2).LineHeight(1.55)
			if s.Error != "" {
				ui.Row(c).FillWidth().Gap(sp2).Margin(sp2, 0, 0).Children(func() {
					if s.DownloadSource != bootstrap.DownloadSourceOfficial && !s.Plan.Complete() {
						kitui.SmallButton(c, tok, "改用官方源", nil, a.useOnboardingOfficialSource).Disabled(s.Loading || s.workerInspecting || s.selectedAgentsSaving)
					}
					kitui.SmallButton(c, tok, "复制诊断，交给 Agent 排查", kitui.IconCopy, func() { a.copyOnboardingDiagnostic(c) })
				})
			}
		})
	})
}

type onboardingEnvironmentComponent struct {
	id, title, description string
	requirements           []string
}

var onboardingEnvironmentComponents = []onboardingEnvironmentComponent{
	{"runtime", "隔离运行环境", "Python 3.12 · uv · 独立虚拟环境，不改系统 Python", []string{"uv", "python", "runtime"}},
	{"engine", "本地记忆引擎", "把对话整理成结构化记忆与关系图谱", []string{"cognee-source"}},
	{"embedding", "本地语义搜索模型", "离线理解中文与代码含义，不上传云端", []string{"embedding"}},
	{"plugin", "Agent 接入组件", "让 Agent 自动写入与召回项目记忆", []string{"agent-plugin"}},
	{"service", "启动本地记忆服务", "仅监听本机回环地址，退出窗口后可保持后台运行", nil},
}

type onboardingEnvironmentRow struct {
	onboardingEnvironmentComponent
	status bootstrap.Status
	label  string
	width  float32
}

func (s *EnvironmentSetupState) onboardingComponentRow(component onboardingEnvironmentComponent, p bootstrap.InstallProgress) onboardingEnvironmentRow {
	row := onboardingEnvironmentRow{onboardingEnvironmentComponent: component, label: "等待"}
	busy := s.Busy || s.ExternalWorkerActive
	if component.id == "service" {
		switch {
		case s.RuntimeStarted:
			row.status, row.label = bootstrap.Ready, "运行中"
		case s.RuntimeStage != "" || (s.ExternalWorkerActive && p.Stage == "starting_runtime"):
			row.status, row.label, row.width = bootstrap.Installing, "启动中", 28
		case s.OnboardingRuntimeFailed:
			row.status, row.label = bootstrap.Failed, "失败"
		}
		return row
	}

	var bytes int64
	var unknown, found, allReady, failed, active, cancelled bool
	allReady = true
	for _, item := range s.Plan.Items {
		if !slices.Contains(component.requirements, item.Requirement.ID) {
			continue
		}
		found = true
		status := environmentRowStatus(item, s.Journal, busy)
		active = active || status == bootstrap.Installing || (busy && p.Component == item.Requirement.ID && p.Stage != "ready" && status != bootstrap.Available && status != bootstrap.Ready)
		if status == bootstrap.Ready || status == bootstrap.Available {
			continue
		}
		allReady = false
		failed = failed || status == bootstrap.Failed
		cancelled = cancelled || status == bootstrap.Cancelled
		unknown = unknown || item.Requirement.DownloadBytes < 0
		bytes += max(0, item.Requirement.DownloadBytes)
	}
	switch {
	case active:
		row.status, row.label, row.width = bootstrap.Installing, onboardingInstallStage(p.Stage), 28
		if !p.Indeterminate && p.BytesTotal > 0 {
			row.width = onboardingPercent(p.BytesDone, p.BytesTotal)
			row.label = fmt.Sprintf("%.0f%%", row.width)
		}
	case failed:
		row.status, row.label = bootstrap.Failed, "失败"
	case cancelled:
		row.status, row.label = bootstrap.Cancelled, "已停止"
	case found && allReady:
		row.status, row.label = bootstrap.Ready, "已就绪"
	case found:
		row.status, row.label = bootstrap.Missing, "待安装"
		if unknown {
			row.label = "待下载 · 大小待定"
		} else if bytes > 0 {
			row.label = "待下载 " + onboardingMiB(bytes)
		}
	}
	return row
}

func (r onboardingEnvironmentRow) options(tok *kitui.Tokens, c *ui.Context) kitui.RowOptions {
	row := kitui.RowOptions{
		Key: "onboarding-component-" + r.id, Title: r.title, Desc: r.description,
		Value: r.label, ValueColor: environmentStatusColor(tok, r.status),
		Leading: func() { kitui.StatusDot(c, tok, environmentDotState(r.status)) },
	}
	if r.status == bootstrap.Installing {
		row.DescExtra = func() { onboardingInstallBar(tok, c.Key(r.id), 3, r.width, r.label) }
	}
	return row
}

func (s *EnvironmentSetupState) onboardingLogText() string {
	lines := make([]string, 0, len(s.OnboardingLog))
	for _, line := range s.OnboardingLog {
		lines = append(lines, line.text)
	}
	return strings.Join(lines, "\n")
}

func (a *Controller) onboardingInstallLog(c *ui.Context) {
	tok := a.tokens()
	s := &a.environment
	icon := kitui.IconChev
	if s.OnboardingLogOpen {
		icon = kitui.IconDown
	}
	kitui.GhostButton(c, tok, fmt.Sprintf("安装日志（%d 行）", len(s.OnboardingLog)), icon, func() {
		s.OnboardingLogOpen = !s.OnboardingLogOpen
		if s.OnboardingLogOpen {
			s.onboardingLogScroll.Y = math.MaxFloat32
		}
	}).AlignSelf(ui.Start)
	if !s.OnboardingLogOpen {
		return
	}
	ui.Scroll(c.Key("onboarding-install-log")).FillWidth().Height(min(180, float32(len(s.OnboardingLog))*17+16)).
		Radius(8).Background(tok.Fill1).TrackScroll(&s.onboardingLogScroll).Children(func() {
		ui.Column(c).FillWidth().Padding(sp2).Children(func() {
			for _, entry := range s.OnboardingLog {
				color := tok.Text2
				if entry.failed {
					color = tok.Danger
				}
				ui.Text(c, entry.text).Font("SF Mono").FontSize(fsCaption).LineHeight(1.65).TextColor(color)
			}
		})
	})
	ui.Row(c).Gap(sp2).AlignItems(ui.Center).Children(func() {
		kitui.SmallButton(c, tok, "复制日志", kitui.IconCopy, func() {
			c.WriteClipboard(s.onboardingLogText())
			a.showOnboardingToast("已复制安装日志")
		})
		ui.Text(c, "已脱敏，不含密钥或聊天内容").FontSize(fsCaption).TextColor(tok.Text3)
	})
}

func (a *Controller) onboardingEnvironmentTechnical(c *ui.Context) {
	tok := a.tokens()
	s := &a.environment
	for _, item := range s.Plan.Items {
		available := item.Observation.Status == bootstrap.Available
		version := dependencyDisplayVersion(item, s.Environment)
		if item.Requirement.ID == "embedding" {
			version = item.Requirement.Version
		}
		if item.Requirement.Digest != "" {
			version += " · SHA-256 " + item.Requirement.Digest
		}
		path := item.Observation.Path
		if path == "" {
			path = item.Requirement.Destination
		}
		label := environmentLabel(environmentRowStatus(item, s.Journal, s.Busy))
		if available {
			label = "已复用本机"
		}
		kitui.Row(c, tok, kitui.RowOptions{
			Key: "onboarding-technical-" + item.Requirement.ID, Title: dependencyDisplayName(item.Requirement),
			Desc: version + "\n" + path, DescMono: true, Value: label,
			DescExtra: func() {
				if item.Observation.Reason != "" {
					kitui.Text(c, tok, bootstrap.RedactInstallerText(item.Observation.Reason), kitui.RoleMeta)
				}
			},
		})
	}
	ui.Row(c).Gap(sp2).Children(func() {
		kitui.SmallButton(c, tok, "复制安装命令，自己在终端执行", kitui.IconCopy, func() {
			a.copyManualInstallScript(c)
			a.showOnboardingToast("已复制终端安装命令")
		}).Disabled(len(s.Plan.Items) == 0)
		kitui.SmallButton(c, tok, "指定已有组件路径…", kitui.IconFolder, func() {
			s.Details, s.Manual = true, true
		}).Disabled(s.Busy || s.Loading || s.ExternalWorkerActive)
	})
}

func (a *Controller) onboardingDownloadSource(c *ui.Context) {
	tok := a.tokens()
	s := &a.environment
	a.syncDownloadSourceIdx()
	kitui.Segmented(c, tok, "initial-download-source", "下载来源", &s.DownloadSourceIdx, []string{"国内镜像（推荐）", "官方源"}, func() {
		source := bootstrap.DownloadSourceChina
		if s.DownloadSourceIdx == 1 {
			source = bootstrap.DownloadSourceOfficial
		}
		a.switchDownloadSource(source)
	})
}

func (a *Controller) useOnboardingOfficialSource() {
	a.switchDownloadSource(bootstrap.DownloadSourceOfficial)
}
