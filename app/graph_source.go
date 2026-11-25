package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zwhu/memlane/app/shell"
)

func (a *App) graphSource() {
	p, ok := a.current()
	if !ok || a.graphController.Selection == "" || a.Update == nil {
		return
	}
	var node map[string]any
	if json.Unmarshal([]byte(a.graphController.Selection), &node) != nil {
		return
	}
	id, _ := node["document_id"].(string)
	if id == "" {
		id, _ = node["data_id"].(string)
	}
	if id == "" {
		id, _ = node["id"].(string)
	}
	dataset := p.Dataset
	if a.graphController.Mode == "代码结构" {
		dataset = p.CodeDataset
	}
	adapter := appShellAdapter{a}
	fence := shell.CaptureFence(adapter)
	client, update := a.Client, a.Update
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		i, err := client.documentByID(ctx, dataset, id)
		update(func() {
			if !fence.Valid(adapter) {
				return
			}
			if err != nil {
				a.graphController.Error = "该节点没有可读取的官方文档出处；节点面板保留真实来源字段。"
				return
			}
			a.navigate("知识", a.project)
			a.selectItem(a.state(), i)
		})
	}()
}
