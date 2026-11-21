package simpledesktop

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type SessionDriftItem struct {
	SessionID          string `json:"session_id"`
	BoundProjectID     string `json:"bound_project_id"`
	BoundProjectName   string `json:"bound_project_name"`
	BoundDatasetID     string `json:"bound_dataset_id"`
	TargetProjectID    string `json:"target_project_id"`
	TargetProjectName  string `json:"target_project_name"`
	TargetDatasetID    string `json:"target_dataset_id"`
	TargetReadCount    int    `json:"target_read_count"`
	TargetWriteCount   int    `json:"target_write_count"`
	TargetCommandCount int    `json:"target_command_count"`
	BoundTotalCount    int    `json:"bound_total_count"`
	UpdatedAt          string `json:"updated_at"`
	Reason             string `json:"reason"`
}

type sessionDriftResponse struct {
	StartedAt string             `json:"started_at"`
	Items     []SessionDriftItem `json:"items"`
}

type projectDriftState struct {
	Items   []SessionDriftItem
	Loaded  bool
	Loading bool
	Busy    bool
	Error   string
	Next    time.Time
	Request uint64
}

func (c ProjectControlClient) projectSessionDrifts(ctx context.Context, projectID string) ([]SessionDriftItem, error) {
	var out sessionDriftResponse
	if err := c.request(ctx, "GET", "/v1/projects/"+url.PathEscape(projectID)+"/session-drift", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c ProjectControlClient) dismissSessionDrift(ctx context.Context, projectID, sessionID, targetProjectID string) ([]SessionDriftItem, error) {
	var out sessionDriftResponse
	input := map[string]any{
		"session_id":        sessionID,
		"target_project_id": targetProjectID,
	}
	if err := c.request(ctx, "POST", "/v1/projects/"+url.PathEscape(projectID)+"/session-drift/dismiss", input, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (a *App) sessionDriftIdentityKey(p Project) string {
	identity, _ := readingIdentity(Client{Config: a.Client.Config})
	return identity + "/" + p.ID + "/" + p.Dataset
}

func (a *App) sessionDriftStateFor(p Project) *projectDriftState {
	if p.ID == "" {
		return nil
	}
	if a.projectUI.SessionDrifts == nil {
		a.projectUI.SessionDrifts = map[string]*projectDriftState{}
	}
	key := a.sessionDriftIdentityKey(p)
	st := a.projectUI.SessionDrifts[key]
	if st == nil {
		st = &projectDriftState{}
		a.projectUI.SessionDrifts[key] = st
	}
	return st
}

func (a *App) ensureProjectSessionDrifts(p Project, s *pane) *projectDriftState {
	st := a.sessionDriftStateFor(p)
	if st == nil || s == nil || s.Loading || !s.Loaded {
		return st
	}
	client, ok := a.accessClient()
	if !ok || a.Update == nil || st.Loading || st.Busy || time.Now().Before(st.Next) {
		return st
	}
	st.Loading = true
	st.Request++
	reqID, epoch := st.Request, a.epoch
	st.Next = time.Now().Add(30 * time.Second)
	identityKey := a.sessionDriftIdentityKey(p)
	projectID := p.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		items, err := client.projectSessionDrifts(ctx, projectID)
		a.Update(func() {
			if epoch != a.epoch {
				return
			}
			cur := a.projectUI.SessionDrifts[identityKey]
			if cur == nil || cur.Request != reqID {
				return
			}
			cur.Loading = false
			if err != nil {
				cur.Error = err.Error()
				return
			}
			cur.Loaded = true
			cur.Error = ""
			cur.Items = items
		})
	}()
	return st
}

func (a *App) dismissProjectSessionDrift(p Project, item SessionDriftItem) {
	st := a.sessionDriftStateFor(p)
	client, ok := a.accessClient()
	if st == nil || !ok || a.Update == nil || st.Busy {
		return
	}
	st.Busy = true
	st.Error = ""
	st.Request++
	reqID, epoch := st.Request, a.epoch
	identityKey := a.sessionDriftIdentityKey(p)
	projectID := p.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		items, err := client.dismissSessionDrift(ctx, projectID, item.SessionID, item.TargetProjectID)
		a.Update(func() {
			if epoch != a.epoch {
				return
			}
			cur := a.projectUI.SessionDrifts[identityKey]
			if cur == nil || cur.Request != reqID {
				return
			}
			cur.Busy = false
			if err != nil {
				cur.Error = err.Error()
				return
			}
			cur.Loaded = true
			cur.Error = ""
			cur.Next = time.Now().Add(30 * time.Second)
			cur.Items = items
		})
	}()
}

func formatSessionDriftSummary(item SessionDriftItem) string {
	parts := make([]string, 0, 3)
	if item.TargetWriteCount > 0 {
		parts = append(parts, fmt.Sprintf("修改 %d 次", item.TargetWriteCount))
	}
	if item.TargetReadCount > 0 {
		parts = append(parts, fmt.Sprintf("读取 %d 次", item.TargetReadCount))
	}
	if item.TargetCommandCount > 0 {
		parts = append(parts, fmt.Sprintf("命令 %d 次", item.TargetCommandCount))
	}
	summary := "主要操作了该仓库"
	if len(parts) > 0 {
		summary = "在该项目" + strings.Join(parts, " · ")
	}
	if item.BoundTotalCount == 0 {
		summary += "，当前项目无操作记录"
	} else {
		summary += fmt.Sprintf("（当前项目 %d 次）", item.BoundTotalCount)
	}
	return summary
}

func (a *App) locateSessionMemories(s *pane, sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	catalog := s.Knowledge.Catalog
	if len(catalog) == 0 {
		catalog = s.Items
	}
	var target *Item
	for n := range catalog {
		if catalog[n].Source == sessionID {
			item := catalog[n]
			target = &item
			break
		}
	}
	if target == nil {
		return
	}
	needReorder := false
	if len(s.Knowledge.Catalog) > 0 {
		inFiltered := false
		for _, row := range a.filteredKnowledgeCatalog(s) {
			if row.key() == target.key() {
				inFiltered = true
				break
			}
		}
		if !inFiltered {
			s.Knowledge.CategoryFilter = ""
			s.Knowledge.UnreadOnly = false
			s.Knowledge.RecallFilter = nil
			needReorder = true
		}
		filtered := a.filteredKnowledgeCatalog(s)
		targetIdx := -1
		for idx, row := range filtered {
			if row.key() == target.key() {
				targetIdx = idx
				break
			}
		}
		if needReorder || targetIdx >= len(s.Items) {
			limit := max(knowledgeBatch, len(s.Items))
			if targetIdx >= 0 {
				limit = max(limit, targetIdx+1)
			}
			a.reorderKnowledgeRange(s, limit, false)
		}
	}
	s.RevealItem = target.key()
	a.selectItem(s, *target)
}

func (a *App) projectSessionDriftBanner(c *ui.Context, s *pane) {
	p, ok := a.current()
	if !ok {
		return
	}
	st := a.ensureProjectSessionDrifts(p, s)
	if st == nil || len(st.Items) == 0 {
		return
	}
	top := st.Items[0]
	catalog := s.Knowledge.Catalog
	if len(catalog) == 0 {
		catalog = s.Items
	}
	matchCount := 0
	for _, item := range catalog {
		if item.Source == top.SessionID {
			matchCount++
		}
	}
	heading := fmt.Sprintf("疑似跨项目会话 · 主要操作了「%s」", top.TargetProjectName)
	if len(st.Items) > 1 {
		heading += fmt.Sprintf("（共 %d 个会话）", len(st.Items))
	}
	ui.Column(c.Key("session-drift-banner-"+p.ID)).FillWidth().Margin(0, sp4, sp2, sp4).Padding(sp2, sp3).Gap(6).
		Radius(radius).Background(tok.Warning.Alpha(.08)).Border(1, tok.Warning.Alpha(.28)).Shrink(0).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, messageAlertIcon).FontSize(13).TextColor(tok.Warning).Shrink(0)
			ui.Text(c, heading).Font("system-ui").FontSize(fsFootnote).FontWeight(600).TextColor(tok.Text1).MinWidth(0).MaxLines(2)
		})
		homeText(c, formatSessionDriftSummary(top)+"；记忆仍保存在当前项目，不会自动移动。", homeMeta).MaxLines(3)
		if st.Error != "" {
			homeText(c, st.Error, homeMeta).TextColor(tok.Warning).MaxLines(2)
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp3).Children(func() {
			if matchCount > 0 {
				homeLink(c, fmt.Sprintf("定位相关记忆 (%d)", matchCount), func() { a.locateSessionMemories(s, top.SessionID) })
			}
			homeLink(c, "留在当前项目", func() { a.dismissProjectSessionDrift(p, top) }).Disabled(st.Busy)
		})
	})
}

func (a *App) itemSessionDriftNote(c *ui.Context, i Item) {
	if i.Source == "" {
		return
	}
	p, ok := a.current()
	if !ok {
		return
	}
	st := a.sessionDriftStateFor(p)
	if st == nil || len(st.Items) == 0 {
		return
	}
	for _, item := range st.Items {
		if item.SessionID == i.Source {
			homeText(c, fmt.Sprintf("提示：该会话主要操作路径指向「%s」，当前仍保存在「%s」", item.TargetProjectName, item.BoundProjectName), homeMeta).
				TextColor(tok.Warning).MaxLines(2)
			return
		}
	}
}
