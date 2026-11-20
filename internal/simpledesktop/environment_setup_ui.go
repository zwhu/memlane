package simpledesktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/engine"
	"github.com/zwhu/memlane/internal/bootstrap"
)

type environmentSetupState struct {
	PathDialog                                     bool
	PathDraft                                      environmentPathDraft
	CopiedPath                                     string
	Loaded, Loading, Busy, Review, Details, Manual bool
	Plan                                           bootstrap.Plan
	Journal                                        bootstrap.Journal
	Environment                                    *bootstrap.Environment
	Error, Message                                 string
	Python, UV, Source, Archive, ModelCache        string
	Plugin                                         string
	SelectionLoaded                                bool
	Generation                                     uint64
	Cancel                                         context.CancelFunc
	InitialStep                                    int
	Exporting                                      bool
	AgentOpen                                      bool
	DiagnosticAgents                               []bootstrap.DiagnosticAgent
	RuntimeStarted                                 bool
	RuntimeStage                                   string
	RuntimePreparation                             bootstrap.FirstRuntime
}

func (a *App) environmentOptions() (bootstrap.EnvironmentOptions, error) {
	s := &a.environment
	options := bootstrap.EnvironmentOptions{Root: os.Getenv("MEMLANE_BOOTSTRAP_ROOT"), Python: s.Python, UV: s.UV, Source: s.Source, Archive: s.Archive, ModelCache: s.ModelCache, Plugin: s.Plugin, ProtectedStore: a.Config.OfficialData}
	if !s.SelectionLoaded {
		options.Python, options.Source, options.Archive = a.Config.Python, a.Config.OfficialSource, a.Config.OfficialArchive
		if a.Config.ProjectControl != nil {
			options.Plugin = a.Config.ProjectControl.PluginDir
		}
		if a.Config.ServiceEnvironmentFile != "" {
			// Decode only model selection metadata; never expose the environment or
			// credentials to diagnostics, clipboard or component subprocesses.
			b, err := os.ReadFile(a.Config.ServiceEnvironmentFile)
			if err != nil {
				return options, err
			}
			var fields struct {
				Model  string `json:"EMBEDDING_MODEL"`
				HFHome string `json:"HF_HOME"`
			}
			if err := json.Unmarshal(b, &fields); err != nil {
				return options, err
			}
			if fields.Model != "" && fields.Model != "BAAI/bge-small-zh-v1.5" {
				return options, errors.New("已有向量模型与首次准备模型不同，请保留原模型配置并核对兼容性")
			}
			options.ModelCache = fields.HFHome
		}
		selection, err := bootstrap.LoadSelections(options.Root, a.Config.ConfigFile)
		if err == nil {
			options.Python, options.UV, options.Source, options.Archive, options.ModelCache = selection.Python, selection.UV, selection.Source, selection.Archive, selection.ModelCache
			if selection.Plugin != "" {
				options.Plugin = selection.Plugin
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return options, err
		}
		s.Python, s.UV, s.Source, s.Archive, s.ModelCache = options.Python, options.UV, options.Source, options.Archive, options.ModelCache
		s.Plugin = options.Plugin
		s.SelectionLoaded = true
	}
	if options.Root != "" && a.Config.OfficialData != "" {
		root, _ := filepath.Abs(options.Root)
		store, _ := filepath.Abs(a.Config.OfficialData)
		for p := root; ; p = filepath.Dir(p) {
			if p == store {
				return options, errors.New("组件位置不能位于日常记忆库内")
			}
			if p == filepath.Dir(p) {
				break
			}
		}
	}
	return options, nil
}

func (a *App) refreshEnvironment() {
	a.detectEnvironment(false)
}

func (a *App) detectEnvironment(saveSelection bool) {
	s := &a.environment
	if s.Busy || s.Loading || a.Update == nil {
		return
	}
	options, err := a.environmentOptions()
	if err != nil {
		s.Error = bootstrap.RedactInstallerText(err.Error())
		s.Loaded = true
		return
	}
	s.Loading, s.Review, s.Error = true, false, ""
	s.Generation++
	generation := s.Generation
	configBinding := a.Config.ConfigFile
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		environment, err := bootstrap.NewEnvironment(ctx, engine.BootstrapAssets, options)
		if err == nil && saveSelection {
			err = bootstrap.SaveSelections(options, configBinding)
		}
		var plan bootstrap.Plan
		var journal bootstrap.Journal
		if err == nil {
			plan, err = environment.Detect(ctx)
			journal, _ = environment.Installer().Snapshot()
		}
		a.Update(func() {
			if s.Generation != generation {
				return
			}
			s.Loading, s.Loaded = false, true
			s.Environment, s.Plan, s.Journal = environment, plan, journal
			if err != nil {
				s.Error = bootstrap.RedactInstallerText(err.Error())
			} else if !plan.Complete() {
				for _, item := range plan.Items {
					if item.Observation.Status == bootstrap.Available {
						continue
					}
					if step, ok := journal.Steps[item.Requirement.ID]; ok && step.Status == bootstrap.Failed {
						s.Error = bootstrap.RedactInstallerText(step.Error)
						break
					}
				}
			}
		})
	}()
}

func (a *App) runEnvironment(approve bool) {
	s := &a.environment
	if s.Busy || s.Loading || s.Environment == nil {
		return
	}
	environment, plan := s.Environment, s.Plan
	resume := !approve && s.Journal.ApprovedDigest != "" && s.Journal.Operation == "first-install"
	if resume {
		plan = s.Journal.Plan
	}
	s.Busy, s.Review, s.Error, s.Message = true, false, "", ""
	s.RuntimeStage = ""
	s.Generation++
	generation := s.Generation
	ctx, cancel := context.WithCancel(context.Background())
	s.Cancel = cancel
	go func() {
		defer cancel()
		installer := environment.Installer()
		installer.OnChange = func(j bootstrap.Journal) {
			a.Update(func() {
				if s.Generation == generation {
					s.Journal = j
				}
			})
		}
		var err error
		if approve {
			err = installer.Review("first-install", plan, plan.Digest(), true)
		} else if !resume {
			err = bootstrap.ErrConsent
		}
		if err == nil {
			err = installer.Run(ctx, "first-install", plan)
		}
		var detected bootstrap.Plan
		if err == nil {
			detected, err = environment.Detect(context.Background())
		}
		a.Update(func() {
			if s.Generation != generation {
				return
			}
			s.Busy, s.Cancel = false, nil
			if err != nil {
				s.Error = bootstrap.RedactInstallerText(err.Error())
				if errors.Is(err, context.Canceled) {
					s.Message = "已停止，已完成的准备会保留。"
				}
			} else {
				s.Plan = detected
				s.Message = "环境组件已准备完成"
			}
		})
	}()
}

func (a *App) declineEnvironment() {
	s := &a.environment
	if s.Busy {
		return
	}
	s.Review = false
	if s.Environment == nil {
		return
	}
	s.Busy = true
	environment, plan := s.Environment, s.Plan
	go func() {
		err := environment.Installer().Review("first-install", plan, plan.Digest(), false)
		a.Update(func() {
			s.Busy = false
			if err != nil {
				s.Error = bootstrap.RedactInstallerText(err.Error())
			} else {
				s.Message = "暂不安装，已有环境和准备进度会保留。"
			}
		})
	}()
}

func (a *App) environmentDiagnostic() (string, string) {
	report, prompt, err := bootstrap.Diagnostics(a.environmentDiagnosticInput())
	if err != nil {
		return "", err.Error()
	}
	return report, prompt
}

func (a *App) environmentDiagnosticInput() bootstrap.DiagnosticInput {
	s := &a.environment
	input := bootstrap.DiagnosticInput{Operation: "first-install", Stage: "环境准备", Platform: "macOS", Error: s.Error, Actions: []string{"检测已有依赖", "保留已有环境及安装进度"}}
	plan := s.Plan
	input.Plan = &plan
	if s.Journal.Operation != "" {
		j := s.Journal
		input.Journal = &j
		if j.ActiveStep != "" {
			input.Stage = j.ActiveStep
			if step, ok := j.Steps[j.ActiveStep]; ok {
				input.ExitCode, input.InstallerLog = step.ExitCode, step.Log
			}
		}
	}
	if s.RuntimeStage != "" {
		input.Stage = s.RuntimeStage
		input.Actions = append(input.Actions, "保留首次内容库及后台所有权，等待真实服务回执")
	}
	return input
}

func (a *App) exportEnvironmentDiagnostic() {
	s := &a.environment
	if s.Exporting || a.ChooseDiagnosticPath == nil {
		return
	}
	path, err := a.ChooseDiagnosticPath()
	if err != nil {
		s.Error = bootstrap.RedactInstallerText(err.Error())
		return
	}
	if path == "" {
		return
	}
	input := a.environmentDiagnosticInput()
	s.Exporting = true
	go func() {
		err := bootstrap.ExportDiagnostic(path, input)
		a.Update(func() {
			s.Exporting = false
			if err != nil {
				s.Error = bootstrap.RedactInstallerText(err.Error())
			} else {
				s.Message = "诊断包已导出"
			}
		})
	}()
}

func environmentSize(n int64) string {
	if n < 0 {
		return "尚未确定"
	}
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}
func environmentLabel(status bootstrap.Status) string {
	switch status {
	case bootstrap.Available, bootstrap.Ready:
		return "可用"
	case bootstrap.Installing:
		return "正在安装"
	case bootstrap.Failed:
		return "失败"
	case bootstrap.Cancelled:
		return "已停止"
	case "unconfirmed":
		return "准备结果待核对"
	case bootstrap.Incompatible:
		return "不兼容"
	default:
		return "需要安装"
	}
}

func environmentRowStatus(item bootstrap.Item, j bootstrap.Journal, busy bool) bootstrap.Status {
	if item.Observation.Status == bootstrap.Available {
		return bootstrap.Available
	}
	for _, old := range j.Plan.Items {
		if old.Requirement == item.Requirement {
			if step, ok := j.Steps[item.Requirement.ID]; ok {
				if step.Status == bootstrap.Installing && !busy {
					return "unconfirmed"
				}
				return step.Status
			}
		}
	}
	return item.Observation.Status
}

func (a *App) environmentSetup(c *ui.Context, initial bool) {
	if initial {
		a.initialEnvironment(c)
		a.environmentReviewModal(c)
		a.initialEnvironmentDetails(c)
		return
	}
	s := &a.environment
	if !s.Loaded && !s.Loading && a.Update != nil {
		a.refreshEnvironment()
	}
	settingsSection(c, "运行环境", func() {
		if s.Plan.Complete() && !s.Busy {
			homeText(c, "已就绪", homeMeta).TextColor(ui.Hex("#4d7a66"))
		}
	}, func() {
		textButton(c, "重新检测", nil).Disabled(s.Loading || s.Busy).OnClick(a.refreshEnvironment)
		label := "查看详情"
		if s.Details {
			label = "收起详情"
		}
		textButton(c, label, nil).OnClick(func() { s.Details = !s.Details })
	}, func() {
		if s.Loading {
			homeText(c, "检测中…", homeMeta).Margin(10, 0)
		}
		for index, item := range s.Plan.Items {
			if index > 0 {
				settingsListDivider(c)
			}
			row := item
			status := environmentRowStatus(row, s.Journal, s.Busy)
			ui.Column(c.Key("environment-" + row.Requirement.ID)).FillWidth().Gap(2).Children(func() {
				settingsListRow(c, func() {
					dependencyMark(c, row.Requirement.ID)
					ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Gap(12).Children(func() {
						homeText(c, dependencyDisplayName(row.Requirement), homeBody).FontSize(14).FontWeight(500).TextColor(homeInk).Width(104).Shrink(0)
						homeText(c, dependencyDisplayVersion(row, s.Environment), homeMeta).TextColor(ui.Hex("#596675")).Grow(1).MinWidth(0).SingleLine().Tooltip(row.Requirement.Version)
					})
					color := homeMuted
					if status == bootstrap.Available || status == bootstrap.Ready {
						color = ui.Hex("#4d7a66")
					}
					homeText(c, environmentLabel(status), homeMeta).TextColor(color)
				})
				if s.Details && row.Observation.Reason != "" && status != bootstrap.Available && status != bootstrap.Ready {
					homeText(c, bootstrap.RedactInstallerText(row.Observation.Reason), homeMeta).LineHeight(1.5).Margin(0, 0, 8, 36)
				}
			})
		}
		if s.Error != "" {
			homeText(c, "准备未完成", homeMeta).Margin(8, 0)
			if s.Details {
				homeText(c, s.Error, homeMeta).LineHeight(1.6)
			}
		}
		if s.Message != "" {
			homeText(c, s.Message, homeMeta).LineHeight(1.6)
		}
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			if s.Busy && s.RuntimeStage != "" {
				homeText(c, s.RuntimeStage+"…", homeMeta)
			} else if s.Busy {
				commonButton(c, "停止安装", false, func() {
					if s.Cancel != nil {
						s.Cancel()
					}
				})
			} else if s.Plan.Complete() {
				if initial {
					commonButton(c, "继续", true, a.continueEnvironment)
				}
			} else if len(s.Plan.Items) > 0 {
				commonButton(c, "安装缺少的依赖", true, func() { s.Review = true }).Disabled(s.Loading)
			}
			if s.Error != "" && !s.Busy {
				textButton(c, "重试", nil).OnClick(func() {
					if s.Journal.ApprovedDigest != "" {
						a.runEnvironment(false)
					} else {
						a.refreshEnvironment()
					}
				})
				textButton(c, "用 Agent 排查", nil).OnClick(func() {
					_, prompt := a.environmentDiagnostic()
					c.WriteClipboard(prompt)
					s.Message = "已复制排查提示词"
					s.AgentOpen = true
					s.DiagnosticAgents = bootstrap.ExistingDiagnosticAgents(nil, nil)
				})
			}
		})
		a.environmentDiagnosticAgents(c)
		if s.Details {
			a.environmentLocations(c)
			ui.Row(c.Key("environment-detail-actions")).FillWidth().AlignItems(ui.Center).Gap(20).Padding(12, 0, 0, 0).Children(func() {
				textButton(c, "复制诊断材料", nil).OnClick(func() {
					report, _ := a.environmentDiagnostic()
					c.WriteClipboard(report)
					s.Message = "脱敏诊断材料已复制"
				})
				textButton(c, "导出诊断包", nil).Disabled(s.Exporting || a.ChooseDiagnosticPath == nil).OnClick(a.exportEnvironmentDiagnostic)
				ui.Spacer(c)
				textButton(c, "指定已有路径", a.openEnvironmentPaths).Disabled(s.Busy || s.Loading)
			})
		}
	})
	a.environmentReviewModal(c)
	a.environmentPathsDialog(c)
}

func (a *App) initialEnvironmentPage(c *ui.Context) {
	a.initialSetupContent(c)
}

func (a *App) continueEnvironment() {
	a.beginFirstRuntime(2)
}

func (a *App) environmentDiagnosticAgents(c *ui.Context) {
	s := &a.environment
	if s.AgentOpen {
		ui.Column(c).FillWidth().Gap(8).Margin(10, 0).Children(func() {
			homeText(c, "在已有 Agent 的新会话中粘贴提示词", homeMeta)
			ui.Row(c).Gap(12).Children(func() {
				for _, entry := range s.DiagnosticAgents {
					agent := entry
					label := "复制给 " + agent.Name
					if agent.Application != "" && a.OpenExistingAgent != nil {
						label = "打开 " + agent.Name
					}
					textButton(c, label, nil).OnClick(func() {
						_, prompt := a.environmentDiagnostic()
						c.WriteClipboard(prompt)
						s.Message = "已复制，请在 " + agent.Name + " 新会话中粘贴"
						if agent.Application != "" && a.OpenExistingAgent != nil {
							if err := a.OpenExistingAgent(agent.Application); err != nil {
								s.Message = "提示词已复制，请手动打开 " + agent.Name
							}
						}
					})
				}
				textButton(c, "复制完整提示词", nil).OnClick(func() {
					_, prompt := a.environmentDiagnostic()
					c.WriteClipboard(prompt)
					s.Message = "已复制排查提示词"
				})
			})
		})
	}
}
