package serviceipc

import (
	"context"

	"github.com/zwhu/memlane/internal/records"
)

type ProjectTaskClient struct{ Root string }

func (c ProjectTaskClient) Watch(ctx context.Context, q records.ProjectTaskWatch) (records.ProjectTaskObservation, error) {
	var out records.ProjectTaskObservation
	err := Call(ctx, c.Root, Request{Op: "project_task_watch", ProjectTaskWatch: &q}, &out)
	return out, err
}

func (c ProjectTaskClient) History(ctx context.Context, q records.ProjectHistoryQuery) (records.ProjectHistoryPage, error) {
	var out records.ProjectHistoryPage
	err := Call(ctx, c.Root, Request{Op: "project_history", UserConfirmed: true, ProjectHistory: &q}, &out)
	return out, err
}

func (c ProjectTaskClient) RecoverIndexes(ctx context.Context, d records.IndexRebuildStart) (records.IndexRebuild, error) {
	var out records.IndexRebuild
	err := Call(ctx, c.Root, Request{Op: "rebuild_index", UserConfirmed: true, IndexRebuildStart: &d}, &out)
	return out, err
}

func (c ProjectTaskClient) Start(ctx context.Context, d records.ProjectTaskStart) (records.ProjectTask, error) {
	var out records.ProjectTask
	err := Call(ctx, c.Root, Request{Op: "project_task_start", UserConfirmed: true, ProjectTaskStart: &d}, &out)
	return out, err
}
func (c ProjectTaskClient) Cancel(ctx context.Context, id string) (records.ProjectTask, error) {
	var out records.ProjectTask
	err := Call(ctx, c.Root, Request{Op: "project_task_cancel", UserConfirmed: true, ProjectTaskID: id}, &out)
	return out, err
}

func (c ProjectTaskClient) Get(ctx context.Context, id string) (records.ProjectTask, error) {
	var out records.ProjectTask
	err := Call(ctx, c.Root, Request{Op: "project_task", ProjectTaskID: id}, &out)
	return out, err
}

func (c ProjectTaskClient) List(ctx context.Context, q records.MergeListQuery) (records.ProjectTaskPage, error) {
	var out records.ProjectTaskPage
	err := Call(ctx, c.Root, Request{Op: "project_task_list", ProjectTaskList: &q}, &out)
	return out, err
}

func (c ProjectTaskClient) Binding(ctx context.Context, project string) (records.ProjectBinding, error) {
	var out records.ProjectBinding
	err := Call(ctx, c.Root, Request{Op: "project_binding", Scope: project}, &out)
	return out, err
}

func (c ProjectTaskClient) Bind(ctx context.Context, change records.ProjectBindingChange) (records.ProjectBinding, error) {
	var out records.ProjectBinding
	err := Call(ctx, c.Root, Request{Op: "project_binding_change", UserConfirmed: true, ProjectBindingChange: &change}, &out)
	return out, err
}
