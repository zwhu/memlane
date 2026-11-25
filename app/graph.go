package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
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
	if a.Config.ProjectControl != nil {
		a.syncSettingsTransport()
		a.settingsController.SetCodeIndexEnabled(p, enabled)
		return
	}
	if enabled && p.CodeDataset == "" {
		a.graphController.CodeBusy = true
		a.graphController.CodeStatus = "正在准备本项目的官方代码数据组…"
		client := a.Client
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			dataset, err := client.ensureCodeDataset(ctx, p)
			a.Update(func() {
				a.graphController.CodeBusy = false
				if err != nil {
					a.graphController.CodeStatus = "代码数据组准备失败：" + err.Error()
					return
				}
				index := -1
				for idx, current := range a.Config.Projects {
					if current.ID == p.ID && current.Path == p.Path && current.Dataset == p.Dataset {
						index = idx
						break
					}
				}
				if index < 0 {
					a.graphController.CodeStatus = "项目关联已改变；已保留官方数据组，未开始索引"
					return
				}
				before := a.Config.Projects[index]
				p.CodeDataset = dataset
				p.CodeEnabled = true
				a.Config.Projects[index] = p
				if err := a.saveProjects(); err != nil {
					a.Config.Projects[index] = before
					a.graphController.CodeStatus = "代码关联保存失败；未开始索引：" + err.Error()
					return
				}
				a.indexProjectCode(p)
			})
		}()
		return
	}
	a.Config.Projects[a.project].CodeEnabled = enabled
	if err := a.saveProjects(); err != nil {
		a.Config.Projects[a.project].CodeEnabled = !enabled
		a.graphController.Error = err.Error()
		return
	}
	if enabled {
		a.indexCode()
	} else {
		a.graphController.CodeStatus = "已停止发起新扫描；已有官方任务未请求取消"
	}
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
	name := p.ID + "-code"
	b, err := c.request(ctx, "POST", "/api/v1/datasets", map[string]string{"name": name})
	if err != nil {
		return "", err
	}
	var dataset struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if json.Unmarshal(b, &dataset) != nil || dataset.ID == "" || dataset.Name != name || dataset.ID == p.Dataset {
		return "", fmt.Errorf("官方代码数据组身份不匹配")
	}
	return dataset.ID, nil
}
func (c Client) indexProject(ctx context.Context, p Project) (codeIndexResult, error) {
	state, err := c.codeActivity(ctx, p)
	if err != nil {
		return codeIndexResult{}, err
	}
	if state.Active {
		return codeIndexResult{}, errOfficialCodeActive
	}
	c.HTTP = &http.Client{Timeout: 30 * time.Minute, Transport: sharedHTTPTransport, CheckRedirect: noRedirectFunc}
	b, err := c.request(ctx, "POST", "/api/v1/remember", url.Values{"raw_data": {p.Path}, "datasetId": {p.CodeDataset}, "content_type": {"code"}, "run_in_background": {"false"}, "index_vectors": {"false"}, "self_improvement": {"false"}})
	var result codeIndexResult
	if err != nil {
		return result, err
	}
	if json.Unmarshal(b, &result) != nil || result.Dataset != p.CodeDataset || result.Status == "" {
		return result, fmt.Errorf("官方索引回执身份或结构不匹配")
	}
	if result.Status != "completed" && result.Status != "errored" {
		return result, fmt.Errorf("官方索引终态未知：%s", result.Status)
	}
	return result, nil
}
