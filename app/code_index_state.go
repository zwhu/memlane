package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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
	if control, ok := c.controlClient(); ok && p.ID != "" {
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
	generation, err := c.codeDatasetCreated(ctx, p.CodeDataset)
	if err != nil {
		return state, err
	}
	seen := map[string]bool{}
	for offset := 0; offset <= 100000; offset += 500 {
		var rows []struct {
			ID        string `json:"id"`
			Dataset   string `json:"dataset_id"`
			Kind      string `json:"kind"`
			Pipeline  string `json:"pipeline_name"`
			Operation string `json:"operation_name"`
			Status    string `json:"status"`
			Outcome   string `json:"outcome"`
			Run       string `json:"pipeline_run_id"`
			Ended     string `json:"ended_at"`
			Created   string `json:"created_at"`
			Started   string `json:"started_at"`
		}
		err := c.get(ctx, "/api/v1/activity/pipeline-runs?dataset_id="+url.QueryEscape(p.CodeDataset)+"&limit=500&offset="+strconv.Itoa(offset), &rows)
		if err != nil {
			return state, err
		}
		for _, row := range rows {
			if row.Dataset != p.CodeDataset {
				return state, fmt.Errorf("官方索引运行归属不匹配")
			}
			if row.Pipeline != "code_graph_pipeline" && row.Operation != "remember" {
				continue
			}
			before, err := codeRunPredatesGeneration(row.Created, row.Started, generation)
			if err != nil {
				return state, err
			}
			if before {
				continue
			}
			key := row.Run
			if key == "" {
				key = row.ID
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			if state.Run == "" {
				state.Run = row.Run
				state.Status = row.Status
				if state.Status == "" {
					state.Status = row.Outcome
				}
			}
			if row.Status == "DATASET_PROCESSING_STARTED" || row.Status == "DATASET_PROCESSING_INITIATED" || row.Kind == "operation" && row.Outcome == "" && row.Ended == "" {
				state.Active = true
				state.Run = row.Run
				state.Status = "running"
			}
		}
		if len(rows) < 500 {
			fresh, err := c.codeDatasetCreated(ctx, p.CodeDataset)
			if err != nil {
				return state, err
			}
			if !fresh.Equal(generation) {
				return state, fmt.Errorf("官方代码数据组已重新创建，请刷新后再操作")
			}
			return state, nil
		}
	}
	return state, fmt.Errorf("官方索引历史超出本次有界读取，运行状态未确认")
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

// The pinned API serializes dataset creation as createdAt, and activity times
// as ISO-8601 UTC (some activity rows omit the explicit UTC suffix). Neither
// wall-clock age nor local restart markers identify a dataset incarnation.
func officialTimestamp(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("官方索引时间缺失或格式不匹配；运行世代未确认")
}
func (c Client) codeDatasetCreated(ctx context.Context, id string) (time.Time, error) {
	var datasets []struct {
		ID      string `json:"id"`
		Created string `json:"createdAt"`
	}
	if err := c.get(ctx, "/api/v1/datasets", &datasets); err != nil {
		return time.Time{}, err
	}
	var generation time.Time
	found := false
	for _, dataset := range datasets {
		if dataset.ID != id {
			continue
		}
		if found {
			return time.Time{}, fmt.Errorf("官方代码数据组身份重复")
		}
		found = true
		var err error
		generation, err = officialTimestamp(dataset.Created)
		if err != nil {
			return time.Time{}, err
		}
	}
	if !found {
		return time.Time{}, fmt.Errorf("官方代码数据组不存在；未发起索引")
	}
	return generation, nil
}
func codeRunPredatesGeneration(created, started string, generation time.Time) (bool, error) {
	rowCreated, err := officialTimestamp(created)
	if err != nil {
		return false, err
	}
	if started == "" {
		return rowCreated.Before(generation), nil
	}
	runStarted, err := officialTimestamp(started)
	if err != nil {
		return false, err
	}
	// A row written at/after recreation may describe continuing work; retain it
	// even when its declared start is older. Equality belongs to the new dataset.
	return rowCreated.Before(generation) && runStarted.Before(generation), nil
}
