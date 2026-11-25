package settings

import (
	"context"
	"encoding/json"
	"fmt"
)

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
