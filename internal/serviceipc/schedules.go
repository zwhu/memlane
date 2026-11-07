package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

type ScheduleClient struct{ Root string }

func (c ScheduleClient) Put(ctx context.Context, d records.ScheduleDraft) (records.Schedule, error) {
	var plan records.Schedule
	err := Call(ctx, c.Root, Request{Op: "schedule_put", UserConfirmed: true, ScheduleDraft: &d}, &plan)
	return plan, err
}
func (c ScheduleClient) List(ctx context.Context) ([]records.Schedule, error) {
	var plans []records.Schedule
	err := Call(ctx, c.Root, Request{Op: "schedules"}, &plans)
	return plans, err
}
func (c ScheduleClient) Runs(ctx context.Context, id string) ([]records.ScheduleRun, error) {
	var runs []records.ScheduleRun
	err := Call(ctx, c.Root, Request{Op: "schedule_runs", ScheduleID: id}, &runs)
	return runs, err
}
