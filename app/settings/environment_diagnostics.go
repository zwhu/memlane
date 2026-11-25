package settings

import (
	"fmt"

	"github.com/zwhu/memlane/internal/bootstrap"
)

func (a *Controller) environmentDiagnostic() (string, string) {
	report, prompt, err := bootstrap.Diagnostics(a.environmentDiagnosticInput())
	if err != nil {
		return "", err.Error()
	}
	return report, prompt
}

func (a *Controller) environmentDiagnosticInput() bootstrap.DiagnosticInput {
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
	if len(s.OnboardingLog) > 0 && (a.initialSetup || input.InstallerLog == "") {
		input.InstallerLog = s.onboardingLogText()
	}
	if s.RuntimeStage != "" {
		input.Stage = s.RuntimeStage
		input.Actions = append(input.Actions, "保留首次内容库及后台所有权，等待真实服务回执")
	}
	return input
}

func (a *Controller) exportEnvironmentDiagnostic() {
	s := &a.environment
	if s.Exporting || a.host.ChooseDiagnosticPath == nil {
		return
	}
	path, err := a.host.ChooseDiagnosticPath()
	if err != nil {
		s.onboardingInstallFailureDigest = ""
		s.OnboardingRuntimeFailed = false
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
		a.dispatch(func() {
			s.Exporting = false
			if err != nil {
				s.onboardingInstallFailureDigest = ""
				s.OnboardingRuntimeFailed = false
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
