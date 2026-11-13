package simpledesktop

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"
)

type queuedSession struct {
	Agent   AccessAgent
	Binding ProjectBinding
	View    *pendingBufferView
}
type projectQueueState struct {
	Loading, Loaded bool
	Request         uint64
	Next            time.Time
	Sessions        []queuedSession
	Documents       []Item
	DocumentError   string
	Offset          int
}

func (c Client) pendingProjectDocuments(ctx context.Context, p Project) ([]Item, error) {
	var status struct {
		Items []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Completed *bool  `json:"completed"`
		} `json:"items"`
	}
	if err := c.get(ctx, "/api/v1/datasets/"+url.PathEscape(p.Dataset)+"/processing-status?pipeline=cognify_pipeline", &status); err != nil {
		return nil, err
	}
	if status.Items == nil {
		return nil, fmt.Errorf("整理状态缺少文档清单")
	}
	docs, err := c.overviewDocuments(ctx, p)
	if err != nil {
		return nil, err
	}
	metadata := map[string]Document{}
	for _, d := range docs {
		metadata[d.ID] = d
	}
	result := []Item{}
	for _, row := range status.Items {
		if row.Completed == nil {
			return nil, fmt.Errorf("文档整理状态不完整")
		}
		if *row.Completed {
			continue
		}
		d, ok := metadata[row.ID]
		if !ok {
			return nil, fmt.Errorf("文档列表已变化，请重新读取")
		}
		if d.knowledge() {
			continue
		}
		copy := d
		result = append(result, Item{ID: d.ID, Dataset: p.Dataset, Kind: "文档", Title: d.title(), Date: d.date(), Document: &copy})
	}
	return result, nil
}
func (a *App) projectQueue(p AccessProject) *projectQueueState {
	if a.access.Queues == nil {
		a.access.Queues = map[string]*projectQueueState{}
	}
	identity, _ := readingIdentity(Client{Config: a.Client.Config})
	key := identity + "/" + p.ID + "/" + p.Dataset
	q := a.access.Queues[key]
	if q == nil {
		q = &projectQueueState{}
		a.access.Queues[key] = q
	}
	if !q.Loading && !a.access.Busy && (!q.Loaded || time.Now().After(q.Next)) && a.Update != nil {
		a.loadProjectQueue(q, p)
	}
	return q
}
func (a *App) loadProjectQueue(q *projectQueueState, p AccessProject) {
	if q.Loading || a.access.Busy {
		return
	}
	for _, row := range q.Sessions {
		if row.View.Busy {
			return
		}
	}
	q.Loading = true
	q.Request++
	request := q.Request
	q.Next = time.Now().Add(30 * time.Second)
	identity, _ := readingIdentity(Client{Config: a.Client.Config})
	if a.access.Buffers == nil {
		a.access.Buffers = map[string]*pendingBufferView{}
	}
	sessions := []queuedSession{}
	for _, binding := range a.access.Directory.Bindings {
		if binding.ProjectID != p.ID {
			continue
		}
		for _, agent := range a.access.Directory.Agents {
			if agent.ID != binding.AgentID || agent.Installation == nil {
				continue
			}
			key := agent.ID + "/" + binding.HostSessionKey
			view := a.access.Buffers[key]
			if view == nil {
				view = &pendingBufferView{}
				a.access.Buffers[key] = view
			}
			sessions = append(sessions, queuedSession{agent, binding, view})
		}
	}
	client, python := a.Client, a.Config.Python
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		type result struct {
			Status PendingMaterials
			Error  error
		}
		results := make([]result, len(sessions))
		var documents []Item
		var documentErr error
		var workers sync.WaitGroup
		workers.Add(1)
		go func() {
			defer workers.Done()
			documents, documentErr = client.pendingProjectDocuments(ctx, Project{ID: p.ID, Dataset: p.Dataset})
		}()
		slots := make(chan struct{}, 4)
		for n, row := range sessions {
			workers.Add(1)
			go func(n int, row queuedSession) {
				defer workers.Done()
				select {
				case slots <- struct{}{}:
				case <-ctx.Done():
					results[n].Error = ctx.Err()
					return
				}
				defer func() { <-slots }()
				results[n].Status, results[n].Error = runPendingMaterials(ctx, python, *row.Agent.Installation, row.Binding, p.Dataset, "show", "")
				status := results[n].Status
				if results[n].Error == nil && (status.Signature == "" || status.Pending < 0 || status.Unknown < 0 || status.Unknown > status.Pending) {
					results[n].Error = fmt.Errorf("会话保存状态不完整")
				}
			}(n, row)
		}
		workers.Wait()
		a.Update(func() {
			if request != q.Request {
				return
			}
			q.Loading = false
			current, _ := readingIdentity(Client{Config: a.Client.Config})
			if identity != current {
				q.Sessions = nil
				q.Documents = nil
				q.Loaded = false
				return
			}
			q.Loaded = true
			q.Sessions = sessions
			for n, row := range sessions {
				if results[n].Error != nil {
					row.View.Message = "保存状态暂时无法读取"
					row.View.Loaded = false
				} else {
					row.View.Status = results[n].Status
					row.View.Loaded = true
					row.View.Message = ""
				}
			}
			q.DocumentError = ""
			if documentErr != nil {
				q.DocumentError = "整理状态暂时无法读取"
				if readingUnavailable(documentErr) {
					q.Documents = nil
				}
			} else {
				q.Documents = documents
				q.Offset = min(q.Offset, max(0, len(documents)-1))
			}
		})
	}()
}
