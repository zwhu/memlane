package settings

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// The control registry owns managed code associations; the owner prepares the
// official group, and indexing starts only after every subject's ACL is verified.
func (a *Controller) SetCodeIndexEnabled(p Project, enabled bool) {
	client, ok := a.accessClient()
	if !ok || !a.canDispatch() || !a.access.Loaded || a.access.Busy || a.codeBusy || (a.host.ProjectCodeBusy != nil && a.host.ProjectCodeBusy()) {
		a.reportCodeStatus("", "请先刷新项目规则，待当前操作完成后再设置代码图谱")
		return
	}
	var identifier string
	for _, record := range a.access.Directory.Projects {
		if record.Dataset == p.Dataset {
			identifier = record.ID
			break
		}
	}
	if identifier == "" {
		a.reportCodeStatus("", "项目尚未登记，未开始代码索引")
		return
	}
	revision := a.access.Directory.Revision
	a.access.Busy, a.codeBusy = true, true
	a.reportCodeStatus("正在核对代码组及项目权限…", "")
	official := a.client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 125*time.Second)
		defer cancel()
		dataset := p.CodeDataset
		var err error
		if enabled && dataset == "" {
			dataset, err = official.ensureCodeDataset(ctx, p)
		}
		var directory AccessDirectory
		if err == nil {
			err = client.request(ctx, "PUT", "/v1/projects/"+url.PathEscape(identifier)+"/code",
				map[string]any{"expected_revision": revision, "dataset_id": dataset, "enabled": enabled}, &directory)
		}
		a.dispatch(func() {
			a.access.Busy, a.codeBusy = false, false
			if err != nil {
				a.reportCodeStatus("", fmt.Sprintf("代码设置未确认，未开始索引：%v", err))
				return
			}
			a.ApplyAccessDirectory(directory)
			for _, record := range directory.Projects {
				if record.ID != identifier {
					continue
				}
				if !record.PermissionsReady(directory.Agents...) {
					a.reportCodeStatus("", "代码组权限尚未核对，未开始索引；请在项目设置核对原操作")
					return
				}
				p.CodeDataset, p.CodeEnabled = record.CodeDataset, record.CodeEnabled
				if enabled {
					if a.host.IndexProjectCode != nil {
						a.host.IndexProjectCode(p)
					}
				} else {
					a.reportCodeStatus("已停止发起新扫描；已有官方任务未请求取消", "")
				}
				return
			}
			a.reportCodeStatus("", "项目关联已改变；未开始索引")
		})
	}()
}
