package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

func (a *App) toggleCode(enabled bool) {
	a.graphController.Error = ""
	p, ok := a.current()
	if !ok || p.Path == "" {
		a.graphController.Error = "代码索引需要当前项目的仓库路径"
		return
	}
	if enabled && a.codeIndexBlocked() {
		return
	}
	if a.Config.ProjectControl == nil || strings.TrimSpace(a.Config.ProjectControl.URL) == "" {
		a.graphController.Error = "本地网关尚未配置"
		return
	}
	a.syncSettingsTransport()
	a.settingsController.SetCodeIndexEnabled(p, enabled)
}
func (a *App) saveProjects() error {
	b, e := os.ReadFile(a.Config.ConfigFile)
	if e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(b, &fields); e != nil {
		return e
	}
	fields["projects"], e = json.Marshal(a.Config.Projects)
	if e != nil {
		return e
	}
	b, e = json.MarshalIndent(fields, "", "  ")
	if e != nil {
		return e
	}
	tmp := a.Config.ConfigFile + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, a.Config.ConfigFile)
}
func (a *App) indexCode() {
	p, ok := a.current()
	if ok {
		a.indexProjectCode(p)
	}
}
func (a *App) indexProjectCode(p Project) {
	if !p.CodeEnabled || p.CodeDataset == "" || a.codeIndexBlocked() {
		return
	}
	a.graphController.CodeBusy = true
	a.graphController.CodeStatus = "官方正在索引当前仓库；等待实际结果…"
	client := a.Client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		result, err := client.indexProject(ctx, p)
		a.Update(func() {
			a.graphController.CodeBusy = false
			if errors.Is(err, errOfficialCodeActive) {
				a.graphController.CodeRemoteActive = true
				a.graphController.CodeStatus = "官方已有代码索引运行中；未重复提交"
				return
			}
			if err != nil {
				a.graphController.CodeUnknown = true
				a.graphController.CodeStatus = "索引结果未确认：" + err.Error() + "；未重发请求"
				return
			}
			a.graphController.CodeStatus = fmt.Sprintf("官方代码索引：%s · 已处理 %d 个仓库 · %s", result.Status, result.Items, result.Run)
			if result.Status == "completed" {
				if current, ok := a.current(); ok && current.ID == p.ID && a.page == "图谱" {
					a.graphController.Mode = "代码结构"
					a.graphController.Host.Clear()
					a.loadGraph()
				}
			}
		})
	}()
}

type codeIndexResult struct {
	Status  string `json:"status"`
	Dataset string `json:"dataset_id"`
	Run     string `json:"pipeline_run_id"`
	Items   int    `json:"items_processed"`
}

func (c Client) ensureCodeDataset(ctx context.Context, p Project) (string, error) {
	control, ok := c.controlClient()
	if !ok {
		return "", fmt.Errorf("本地网关尚未配置")
	}
	if strings.TrimSpace(p.ID) == "" {
		return "", fmt.Errorf("项目身份缺失，无法准备代码数据组")
	}
	dataset, err := control.ensureProjectCodeDatasetV2(ctx, p.ID)
	if err != nil {
		return "", err
	}
	if dataset.ID == "" || dataset.ID == p.Dataset || (dataset.ProjectID != "" && dataset.ProjectID != p.ID) {
		return "", fmt.Errorf("官方代码数据组身份不匹配")
	}
	return dataset.ID, nil
}
func (c Client) indexProject(ctx context.Context, p Project) (codeIndexResult, error) {
	control, ok := c.controlClient()
	if !ok {
		return codeIndexResult{}, fmt.Errorf("本地网关尚未配置")
	}
	state, err := c.codeActivity(ctx, p)
	if err != nil {
		return codeIndexResult{}, err
	}
	if state.Active {
		return codeIndexResult{}, errOfficialCodeActive
	}
	result, err := control.triggerProjectCodeIndexV2(ctx, p.ID, p.Path)
	if err != nil {
		return codeIndexResult{}, err
	}
	if result.Dataset == "" || (p.CodeDataset != "" && result.Dataset != p.CodeDataset) || result.Status == "" {
		return result, fmt.Errorf("官方索引回执身份或结构不匹配")
	}
	if result.Status != "completed" && result.Status != "errored" {
		return result, fmt.Errorf("官方索引终态未知：%s", result.Status)
	}
	return result, nil
}
