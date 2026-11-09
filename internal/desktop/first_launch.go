package desktop

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/components"
	"github.com/zwhu/memlane/internal/platform"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type EnvironmentItem struct {
	Name     string `json:"name"`
	State    string `json:"state"`
	Evidence string `json:"evidence"`
	Bytes    int64  `json:"bytes,omitempty"`
}
type FirstLaunchState struct {
	Stage         string             `json:"stage"`
	Step          string             `json:"step,omitempty"`
	Completed     bool               `json:"completed"`
	Skipped       bool               `json:"skipped"`
	ReducedMotion bool               `json:"reduced_motion"`
	Attempt       int                `json:"attempt"`
	Message       string             `json:"message"`
	Items         []EnvironmentItem  `json:"items"`
	Agents        []agents.Discovery `json:"agents,omitempty"`
}
type FirstLaunchUI struct {
	State      FirstLaunchState
	Controller *RuntimeController
	Context    context.Context
	Dispatch   func(func())
	Open       bool
	Sequence   int64
	Cancel     context.CancelFunc
	WasReady   bool
	Error      string
	ChangedAt  time.Time
	Details    map[string]bool
}

func (a *App) ConfigureFirstLaunch(ctx context.Context, controller *RuntimeController, dispatch func(func())) bool {
	s := &a.firstLaunch
	s.Controller, s.Context, s.Dispatch = controller, ctx, dispatch
	a.configureOfficialPreparation(controller)
	raw, err := os.ReadFile(filepath.Join(controller.Config.Root, "first-launch.json"))
	if err == nil {
		if len(raw) > 65536 || json.Unmarshal(raw, &s.State) != nil {
			s.Error = "首次使用状态无法读取，请重新检查。"
		}
	} else if !os.IsNotExist(err) {
		s.Error = "首次使用状态无法读取：" + err.Error()
	}
	if s.State.Step == "" {
		s.State.Step = "environment"
	}
	if s.State.Completed {
		return false
	}
	if s.State.Skipped {
		if s.State.Step != "environment" && s.State.Stage == "ready" && a.runtimeStatus.Phase == "" {
			a.runtimeStatus = RuntimeStatus{Phase: "checking_availability", Message: "正在确认本地记忆服务…", Installed: true}
		}
		return true
	}
	s.Open = true
	if s.State.Step != "environment" && s.State.Stage == "ready" {
		// A persisted later step proves the previous environment transition, but
		// each new process still waits for a live availability observation before
		// allowing progress or completion.
		if a.runtimeStatus.Phase == "" {
			a.runtimeStatus = RuntimeStatus{Phase: "checking_availability", Message: "正在确认本地记忆服务…", Installed: true}
		}
		return true
	}
	s.State.Step = "environment"
	if s.State.Stage == "preparing" || s.State.Stage == "verifying" || s.State.Stage == "cancelling" {
		s.State.Stage = "interrupted"
		s.State.Message = "上次准备已中断。重新检查后可继续，不自动重试收费操作。"
		a.persistFirstLaunch()
	}
	a.discoverFirstLaunch()
	return true
}
func (a *App) persistFirstLaunch() {
	s := &a.firstLaunch
	if s.Controller == nil {
		return
	}
	if err := os.MkdirAll(s.Controller.Config.Root, 0700); err != nil {
		s.Error = "首启状态未保存。"
		return
	}
	if err := serviceipc.WriteJSON(filepath.Join(s.Controller.Config.Root, "first-launch.json"), s.State); err != nil {
		s.Error = "首启状态未保存。"
	}
}

func (r *RuntimeController) DiscoverEnvironment(ctx context.Context) ([]EnvironmentItem, error) {
	items := []EnvironmentItem{}
	state, err := platform.ServiceStatus(r.Config.ServicePlist)
	if err != nil {
		return items, err
	}
	items = append(items, EnvironmentItem{Name: "后台服务", State: string(state), Evidence: r.Config.ServicePlist})
	exists, err := platform.BundledServiceExists(r.Config.ServicePlist)
	if err != nil {
		return items, err
	}
	helperState := "missing"
	if exists {
		helperState = "installed"
	}
	items = append(items, EnvironmentItem{Name: "helper / LaunchAgent", State: helperState, Evidence: "来自当前 App 包的 LaunchAgent，安装与系统授权分别核对"})
	key, err := base64.StdEncoding.DecodeString(r.Config.ComponentPublicKey)
	if err != nil {
		return items, err
	}
	manager, err := components.New(filepath.Join(r.Config.Root, "runtime"), ed25519.PublicKey(key), 1, r.Config.AllowLocalTestSource)
	if err != nil {
		return items, err
	}
	feed, err := manager.Feed(ctx, r.Config.ComponentSource)
	if err != nil {
		var networkError net.Error
		if !errors.As(err, &networkError) {
			return items, err
		}
		feed, err = manager.CachedFeed(r.Config.ComponentSource)
		if err != nil {
			return items, errors.New("组件清单不可用；需要可访问且签名有效的组件来源")
		}
	}
	for _, id := range []string{r.Config.WorkerID, r.Config.ModelID} {
		var found *components.Descriptor
		for i := range feed.Components {
			if feed.Components[i].ID == id {
				found = &feed.Components[i]
				break
			}
		}
		if found == nil {
			return items, fmt.Errorf("组件清单缺少 %s", id)
		}
		item := EnvironmentItem{Name: id, State: "needs_preparation", Evidence: "固定版本 " + found.Version + " · SHA256 " + found.SHA256, Bytes: found.Bytes}
		if installed, err := manager.Inspect(ctx, *found); err == nil {
			item.State = "verified_reusable"
			item.Evidence = "文件校验通过 · " + installed.Directory
			item.Bytes = 0
		} else if _, existing := os.Stat(filepath.Join(manager.Root, "components", found.ID, found.Version)); existing == nil {
			item.State = "validation_failed"
			item.Evidence += " · 已有组件未通过校验，需要修复或重新准备"
		}
		items = append(items, item)
	}
	if stopped, err := serviceipc.IsStopped(filepath.Join(r.Config.Root, "data")); err != nil {
		return items, err
	} else if stopped {
		items = append(items, EnvironmentItem{Name: "记忆服务", State: "stopped", Evidence: "用户明确停止，准备后仍需明确启动"})
	} else {
		probe, stop := context.WithTimeout(ctx, time.Second)
		var health struct {
			State string `json:"state"`
		}
		err := serviceipc.Call(probe, filepath.Join(r.Config.Root, "data"), serviceipc.Request{Op: "health"}, &health)
		stop()
		serviceState := "unavailable"
		if err == nil {
			serviceState = health.State
		}
		items = append(items, EnvironmentItem{Name: "记忆服务", State: serviceState, Evidence: "实际私有 IPC / worker 健康回执"})
	}
	if r.Official == nil {
		items = append(items, EnvironmentItem{Name: "官方 Cognee / Codex", State: "not_verified", Evidence: "官方服务组件来源尚未配置。已有本机兼容服务可在项目官方接入入口独立授权；Agent 安装或登录不等于模型条件已具备"})
	} else {
		model := EnvironmentItem{Name: "提炼 Agent", State: "not_selected", Evidence: "在首次任务或设置中选择并验证；环境初始化无需模型 API Key"}
		if settings, e := r.Official.Settings(); e == nil {
			if settings.ModelBridge != nil {
				model.State = "configured"
				model.Evidence = "pi · " + settings.ModelBridge.Provider + " / " + settings.ModelBridge.Model + " · 已配置，实际推理需单独确认并验证"
			} else if settings.Environment["LLM_API_KEY"] != "" && settings.Environment["LLM_MODEL"] != "" {
				model.Name, model.State = "提炼模型", "configured"
				model.Evidence = settings.Environment["LLM_MODEL"] + " · 已有模型配置，实际推理需单独确认并验证"
			}
		}
		items = append(items, model)
		manager, feed, e := r.Official.Manager(ctx)
		if e != nil {
			items = append(items, EnvironmentItem{Name: "官方服务组件", State: "not_verified", Evidence: "真实签名组件来源不可用：" + e.Error()})
		} else {
			for _, id := range []string{"cognee-service", "cognee-bge-zh", "cognee-codex-plugin"} {
				found := false
				for _, d := range feed.Components {
					if d.ID != id {
						continue
					}
					found = true
					item := EnvironmentItem{Name: id, State: "needs_preparation", Evidence: "固定版本 " + d.Version + " · SHA256 " + d.SHA256, Bytes: d.Bytes}
					if installed, e := manager.Inspect(ctx, d); e == nil {
						item.State = "verified_reusable"
						item.Evidence = "真实文件校验通过 · " + installed.Directory
						item.Bytes = 0
					}
					items = append(items, item)
				}
				if !found {
					items = append(items, EnvironmentItem{Name: id, State: "not_verified", Evidence: "真实组件清单缺少此依赖"})
				}
			}
		}
	}
	return items, nil
}

func (a *App) discoverFirstLaunch() {
	s := &a.firstLaunch
	if s.Controller == nil || s.Context == nil {
		return
	}
	if s.Cancel != nil {
		s.Cancel()
	}
	s.Sequence++
	sequence := s.Sequence
	s.State.Step = "environment"
	s.State.Stage, s.State.Message, s.State.Skipped = "discovering", "正在检查本机环境…", false
	s.ChangedAt = time.Now()
	a.persistFirstLaunch()
	ctx, cancel := context.WithTimeout(s.Context, 40*time.Second)
	s.Cancel = cancel
	controller, dispatch := s.Controller, s.Dispatch
	go func() {
		defer cancel()
		items, err := controller.DiscoverEnvironment(ctx)
		home, _ := os.UserHomeDir()
		selected := map[string]string{}
		if raw, e := os.ReadFile(filepath.Join(controller.Config.Root, "agent-paths.json")); e == nil && len(raw) < 65536 {
			_ = json.Unmarshal(raw, &selected)
		}
		discovered := agents.Discover(ctx, home, selected)
		for _, agent := range discovered {
			state := "not_found"
			if agent.Path != "" {
				state = "installed"
			}
			if agent.Error != "" {
				state = "unknown"
			}
			if agent.CapabilityError != "" {
				state = "not_adapted"
			}
			evidence := agent.Path + " · " + agent.Version
			if agent.Path == "" && agent.Error == "" {
				evidence = "已知安装路径内未发现可执行程序；可手动指定现有安装。用户路径范围：" + home
			}
			if agent.Error != "" {
				evidence = agent.Error
			}
			if agent.CapabilityError != "" {
				evidence += " · " + agent.CapabilityError
			}
			items = append(items, EnvironmentItem{Name: agent.Name, State: state, Evidence: evidence})
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		reduced := false
		if result, e := exec.CommandContext(ctx, "/usr/bin/defaults", "read", "com.apple.universalaccess", "reduceMotion").Output(); e == nil {
			reduced = strings.TrimSpace(string(result)) == "1"
		}
		if s.Context.Err() != nil {
			return
		}
		dispatch(func() {
			if s.Sequence != sequence || !s.Open {
				return
			}
			s.Cancel = nil
			s.State.Items = items
			s.State.Agents = discovered
			s.State.ReducedMotion = s.State.ReducedMotion || reduced
			s.State.Stage = "results"
			s.State.Message = "本机检测已完成，请核对实际结果。"
			s.WasReady = false
			for _, item := range items {
				if item.Name == "记忆服务" && item.State == "ready" {
					s.WasReady = true
				}
			}
			if err != nil {
				s.State.Stage = "failed"
				s.State.Message = "环境发现失败：" + err.Error()
			}
			s.ChangedAt = time.Now()
			a.persistFirstLaunch()
		})
	}()
}
func (a *App) prepareFirstLaunch() {
	s := &a.firstLaunch
	if s.State.Stage != "confirmation" && s.State.Stage != "failed" && s.State.Stage != "interrupted" && s.State.Stage != "cancelled" {
		return
	}
	s.State.Stage, s.State.Message = "preparing", "正在准备已确认的本地组件与服务…"
	s.State.Attempt++
	s.ChangedAt = time.Now()
	a.persistFirstLaunch()
	stopped, err := serviceipc.IsStopped(filepath.Join(s.Controller.Config.Root, "data"))
	if err != nil {
		s.State.Stage = "failed"
		s.State.Message = "服务停止状态无法核对，请重试：" + err.Error()
		a.persistFirstLaunch()
		return
	}
	if (stopped || a.runtimeStatus.Phase == "stopped") && a.resumeRuntime != nil {
		a.resumeRuntime()
	} else if a.prepareRuntime != nil {
		a.prepareRuntime()
	}
}
func (a *App) firstLaunchRuntime(status RuntimeStatus) {
	s := &a.firstLaunch
	if s.Controller == nil || !s.Open {
		return
	}
	// Component progress may report a failed phase before the controller returns
	// its concrete error. Retain that final error after entering the failed page.
	if s.State.Stage == "failed" && status.Phase == "failed" {
		s.State.Message = status.Message
		a.persistFirstLaunch()
		return
	}
	if s.State.Stage == "cancelling" {
		if status.Phase == "stopped" || status.Phase == "cancelled" {
			s.State.Stage = "cancelled"
			s.State.Message = "本轮准备已取消；可以重新检查或稍后继续。"
			a.persistFirstLaunch()
		}
		return
	}
	if s.State.Stage != "preparing" && s.State.Stage != "verifying" {
		return
	}
	previousStage := s.State.Stage
	s.State.Message = status.Message
	switch status.Phase {
	case "ready":
		s.State.Stage = "ready"
		s.State.Message = "本机记忆服务与 Rust 已验证可用。"
		if s.Controller.Official != nil {
			s.State.Message += " Cognee 服务与本地依赖已统一准备；提炼 Agent 在首次任务或设置中选择并验证。"
		} else {
			s.State.Message += " 官方采集服务来源尚未配置。"
		}
		for i := range s.State.Items {
			item := &s.State.Items[i]
			switch item.Name {
			case s.Controller.Config.WorkerID, s.Controller.Config.ModelID, "cognee-service", "cognee-bge-zh", "cognee-codex-plugin":
				item.State = "prepared_verified"
				item.Bytes = 0
				item.Evidence += " · 实际准备与校验已完成"
			case "helper / LaunchAgent":
				item.State = "prepared_verified"
				item.Evidence = "实际 helper / worker 服务健康回执已通过"
			case "后台服务":
				item.State = string(status.Service)
			case "记忆服务":
				item.State = "ready"
			}
		}
	case "failed", "authorization", "stopped":
		s.State.Stage = "failed"
	case "cancelled":
		s.State.Stage = "cancelled"
	case "checking", "downloading", "installed", "installing":
		s.State.Stage = "preparing"
	default:
		if status.Installed {
			s.State.Stage = "verifying"
		}
	}
	if previousStage != s.State.Stage {
		s.ChangedAt = time.Now()
	}
	a.persistFirstLaunch()
}
func (a *App) cancelFirstLaunch(skip bool) {
	s := &a.firstLaunch
	s.Sequence++
	if s.Cancel != nil {
		s.Cancel()
		s.Cancel = nil
	}
	stage := s.State.Stage
	if stage == "preparing" || stage == "verifying" {
		s.State.Stage = "cancelling"
		s.State.Message = "正在取消本轮准备…"
		if a.cancelRuntime != nil {
			a.cancelRuntime()
		}
		if !s.WasReady && a.stopRuntime != nil {
			a.stopRuntime()
		} else {
			s.State.Stage = "cancelled"
		}
	}
	if stage == "discovering" {
		s.State.Stage = "cancelled"
		s.State.Message = "本机检查已取消。"
	}
	if skip {
		s.State.Skipped = true
		s.Open = false
		a.navigate("总览", "")
	}
	a.persistFirstLaunch()
}
func (a *App) openFirstLaunch() {
	s := &a.firstLaunch
	if s.Controller == nil {
		return
	}
	s.Open = true
	s.State.Skipped = false
	if s.State.Step != "" && s.State.Step != "environment" && s.State.Stage == "ready" {
		a.persistFirstLaunch()
		if s.State.Step == "processing" {
			a.refreshProcessing()
		} else {
			a.refreshAgents(true)
		}
		return
	}
	a.discoverFirstLaunch()
}

func (a *App) firstLaunchStep() string {
	step := a.firstLaunch.State.Step
	if step == "" {
		return "environment"
	}
	return step
}

func (a *App) advanceFirstLaunchToAgents() {
	s := &a.firstLaunch
	if !s.Open || s.State.Stage != "ready" || a.runtimeStatus.Phase != "ready" {
		s.State.Step = "environment"
		s.State.Stage = "failed"
		s.State.Message = "记忆服务已不可用，请重新验证。"
		a.persistFirstLaunch()
		return
	}
	s.State.Step = "agents"
	s.State.Skipped = false
	s.State.Message = "环境已就绪；Agent 可以现在连接，也可以稍后设置。"
	a.persistFirstLaunch()
	a.refreshAgents(true)
}

func (a *App) advanceFirstLaunchToProcessing() {
	s := &a.firstLaunch
	if !s.Open || s.State.Stage != "ready" || a.runtimeStatus.Phase != "ready" {
		return
	}
	s.State.Step = "processing"
	s.State.Skipped = false
	s.State.Message = "选择并验证一种记忆处理方式，再明确设为当前使用。"
	a.persistFirstLaunch()
	a.refreshProcessing()
}

func (a *App) openFirstLaunchAgentSettings() {
	s := &a.firstLaunch
	if !s.Open || a.firstLaunchStep() != "agents" {
		return
	}
	s.Open = false
	a.persistFirstLaunch()
	a.navigate("Agent", "")
	a.refreshAgents(true)
}

func (a *App) canCompleteFirstLaunch() bool {
	s, current := &a.firstLaunch, a.processing.Configuration.Current
	return s.Open && a.firstLaunchStep() == "processing" && s.State.Stage == "ready" &&
		a.runtimeStatus.Phase == "ready" && a.serviceState == "ready" && !a.processing.Busy && !a.processing.Polling &&
		current.Active && current.Revision != nil
}

func (a *App) completeFirstLaunch() {
	if !a.canCompleteFirstLaunch() {
		return
	}
	s := &a.firstLaunch
	s.State.Completed = true
	s.State.Skipped = false
	s.State.Message = "初始配置已完成。"
	s.Open = false
	a.persistFirstLaunch()
	a.navigate("总览", "")
}

func (a *App) ReconcileFirstLaunchAvailability() {
	s := &a.firstLaunch
	runtimeUnavailable := map[string]bool{"authorization": true, "cancelled": true, "failed": true, "stopped": true, "unavailable": true}[a.runtimeStatus.Phase]
	serviceUnavailable := a.serviceState == "unavailable"
	if s.Open && s.State.Stage == "ready" && (runtimeUnavailable || serviceUnavailable) {
		s.State.Stage = "failed"
		s.State.Message = "记忆服务当前不可用，请重新检查并验证。"
		a.persistFirstLaunch()
	}
}

func environmentStateLabel(state string) string {
	if state == "configured" {
		return "已配置，尚未验证"
	}
	labels := map[string]string{"validation_failed": "已有组件校验失败", "prepared_verified": "准备与实际验证通过", "verified_reusable": "已校验，可复用", "needs_preparation": "需要准备", "installed": "已安装，未验证可用", "enabled": "系统已允许", "requires_approval": "需要系统批准", "not_registered": "尚未注册", "not_found": "未找到", "missing": "缺失", "ready": "实际服务就绪", "stopped": "已明确停止", "unavailable": "暂不可用", "not_verified": "尚未验证", "unknown": "未知，检测失败", "not_adapted": "已发现，版本待适配"}
	if label := labels[state]; label != "" {
		return label
	}
	return state
}

// Only interpolation uses elapsed time. Target geometry is determined by the
// actual stage, never by a timer that declares preparation successful.
func (a *App) firstLaunchBlocks(c *ui.Context) {
	s := &a.firstLaunch
	a.preparationBlocks(c, "first-launch-blocks", s.State.Stage, s.State.ReducedMotion, s.ChangedAt)
}
func (a *App) preparationBlocks(c *ui.Context, key, stage string, reduced bool, changed time.Time) {
	a.preparationBlocksSized(c, key, stage, reduced, changed, 340, 300)
}
func (a *App) preparationBlocksSized(c *ui.Context, key, stage string, reduced bool, changed time.Time, width, height float32) {
	elapsed := time.Since(changed).Seconds()
	transition := math.Min(1, elapsed/0.7)
	if reduced {
		transition = 1
	}
	active := (stage == "discovering" || stage == "preparing" || stage == "verifying") && !reduced
	if active || transition < 1 {
		c.AnimationFrame()
	}
	ui.Column(c).Key(key).Size(width, height).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		bulb := [][2]float64{}
		for y := -8; y <= 8; y++ {
			for x := -8; x <= 8; x++ {
				filament := y >= -3 && y <= 5 && (y <= 2 && math.Abs(math.Abs(float64(x))-float64(2-y)*.42) < .6 || y > 2 && x == 0)
				if x*x+y*y <= 64 && !filament {
					bulb = append(bulb, [2]float64{float64(x) * 10, float64(y)*10 - 28})
				}
			}
		}
		for y := 9; y <= 11; y++ {
			for x := -2; x <= 2; x++ {
				bulb = append(bulb, [2]float64{float64(x) * 10, float64(y)*10 - 28})
			}
		}
		for _, y := range []float64{102, 116} {
			for x := -2; x <= 2; x++ {
				bulb = append(bulb, [2]float64{float64(x) * 10, y})
			}
		}
		for degrees := -180; degrees <= 0; degrees += 30 {
			angle := float64(degrees) * math.Pi / 180
			for _, radius := range []float64{105, 117} {
				bulb = append(bulb, [2]float64{math.Cos(angle) * radius, math.Sin(angle)*radius - 28})
			}
		}
		for i, target := range bulb {
			x, y := float64(i%15-7)*13, float64(i/15-7)*13
			if stage == "discovering" {
				angle := float64(i) * 2.399963
				radius := math.Sqrt(float64(i)/float64(len(bulb))) * 125
				x = math.Cos(angle) * radius
				y = math.Sin(angle) * radius
				if active {
					x += math.Sin(elapsed*.5+float64(i)) * 3
					y += math.Cos(elapsed*.6+float64(i)) * 3
				}
			}
			if stage == "preparing" || stage == "verifying" {
				angle := float64(i) * 2.399963
				radius := math.Sqrt(float64(i)/float64(len(bulb))) * 125
				x = math.Cos(angle)*radius + (x-math.Cos(angle)*radius)*transition
				y = math.Sin(angle)*radius + (y-math.Sin(angle)*radius)*transition
			}
			if stage == "ready" {
				x += (target[0] - x) * transition
				y += (target[1] - y) * transition
			}
			color := ui.Hex("#5c9fe8")
			if active {
				color = color.Alpha(float32(.78 + .12*math.Sin(elapsed*2+float64(i)/30)))
			}
			if stage == "failed" || stage == "cancelled" {
				color = design.Muted
			}
			scale := min(r.W/340, r.H/300)
			p.Fill(ui.Rect{X: r.X + r.W/2 + (float32(x)-3.5)*scale, Y: r.Y + r.H/2 + (float32(y)-3.5)*scale, W: 7 * scale, H: 7 * scale}, color, scale)
		}
	})
}
