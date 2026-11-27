package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/zwhu/memlane/app/settings"
)

var errOfficialCodeActive = errors.New("official code index already active")

type codeActivityState struct {
	Active      bool
	Status, Run string
}

func (c Client) codeActivity(ctx context.Context, p Project) (codeActivityState, error) {
	state := codeActivityState{}
	control, ok := c.controlClient()
	if !ok {
		return state, fmt.Errorf("本地网关尚未配置")
	}
	if p.ID == "" {
		return state, fmt.Errorf("项目身份缺失，无法读取代码索引状态")
	}
	res, err := control.projectCodeActivityV2(ctx, p.ID)
	if err == nil {
		if p.CodeDataset != "" && res.CodeDatasetID != "" && res.CodeDatasetID != p.CodeDataset {
			return state, fmt.Errorf("官方索引运行归属不匹配")
		}
		return codeActivityState{
			Active: res.Active,
			Status: res.Status,
			Run:    res.RunID,
		}, nil
	}
	if settings.IsControlAuthDenied(err) {
		return state, err
	}
	if settings.IsControlErrorCode(err, http.StatusConflict, "code_dataset_not_configured") ||
		settings.IsControlErrorCode(err, http.StatusNotFound, "code_dataset_not_found") {
		return state, err
	}
	return state, err
}
func (a *App) restoreCodeState() {
	projects := append([]Project(nil), a.Config.Projects...)
	client := a.Client
	go func() {
		for _, p := range projects {
			if !p.CodeEnabled || p.CodeDataset == "" {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			state, err := client.codeActivity(ctx, p)
			cancel()
			a.Update(func() {
				if a.graphController.CodeBusy {
					return
				}
				if err != nil {
					a.graphController.CodeUnknown = true
					a.graphController.CodeStatus = "官方代码索引状态读取失败；未发起新扫描：" + err.Error()
					return
				}
				a.graphController.CodeRemoteActive = state.Active
				a.graphController.CodeUnknown = false
				a.graphController.CodeStatus = "官方代码索引状态：" + state.Status
				if state.Run != "" {
					a.graphController.CodeStatus += " · " + state.Run
				}
				if state.Status == "" {
					a.graphController.CodeStatus = "官方尚未提供代码索引运行记录；未自动重发"
				}
			})
		}
	}()
}
