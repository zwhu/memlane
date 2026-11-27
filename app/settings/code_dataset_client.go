package settings

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

func (c Client) ensureCodeDataset(ctx context.Context, p Project) (string, error) {
	control, ok := c.controlClient()
	if !ok {
		return "", fmt.Errorf("本地网关尚未配置")
	}
	if strings.TrimSpace(p.ID) == "" {
		return "", fmt.Errorf("项目身份缺失，无法准备代码数据组")
	}
	var dataset struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		ProjectID string `json:"project_id"`
	}
	if err := control.request(ctx, "POST", "/v1/projects/"+url.PathEscape(p.ID)+"/code-dataset", nil, &dataset); err != nil {
		return "", err
	}
	if dataset.ID == "" || dataset.ID == p.Dataset || (dataset.ProjectID != "" && dataset.ProjectID != p.ID) {
		return "", fmt.Errorf("官方代码数据组身份不匹配")
	}
	return dataset.ID, nil
}
