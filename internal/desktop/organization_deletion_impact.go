package desktop

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

type OrganizationDeletionImpactUI struct {
	Task, Item, Digest, Root, OrganizationEpoch string
	Epoch                                       uint64
	Query                                       records.DeletionImpactQuery
	Loading                                     bool
	Error                                       string
	Result                                      *records.DeletionImpact
}

func deletionReviewQuery(t records.OrganizationTask, e records.OrganizationEntry) records.DeletionImpactQuery {
	q := records.DeletionImpactQuery{Memory: e.Item.Target, Dataset: t.Project}
	for _, input := range t.Snapshot.Inputs {
		if input.Memory == q.Memory {
			q.ExpectedRevision = input.Revision
		}
	}
	return q
}

func (a *App) organizationDeletionImpactMatches(t records.OrganizationTask, e records.OrganizationEntry) bool {
	s := a.organization.DeletionImpact
	q := deletionReviewQuery(t, e)
	return s != nil && s.Task == t.ID && s.Item == e.ID && s.Digest == records.MergeDigest(e.Item) && s.Root == a.library.Root && s.Epoch == a.libraryEpoch && s.OrganizationEpoch == a.organization.Epoch && s.Query.Memory == q.Memory && s.Query.Dataset == q.Dataset && s.Query.ExpectedRevision == q.ExpectedRevision
}

func (a *App) organizationDeletionImpactCurrent(t records.OrganizationTask, e records.OrganizationEntry) bool {
	if !a.organizationDeletionImpactMatches(t, e) {
		return false
	}
	s := a.organization.DeletionImpact
	return !s.Loading && s.Error == "" && s.Result != nil && s.Result.Memory == s.Query.Memory && s.Result.Dataset == s.Query.Dataset && s.Result.Revision == s.Query.ExpectedRevision
}

func (a *App) refreshOrganizationDeletionImpact() {
	e := a.selectedOrganizationEntry()
	s := &a.organization
	if e == nil || e.Item.Action != "delete" || e.State != "review_pending" || s.Task == nil || s.Busy || a.libraryContext == nil || a.libraryContext.Err() != nil || a.visualFixture {
		return
	}
	if a.organizationDeletionImpactMatches(*s.Task, *e) && s.DeletionImpact.Loading {
		return
	}
	q := deletionReviewQuery(*s.Task, *e)
	seen := map[string]bool{}
	for _, scope := range a.scopes {
		if scope.ID != "" && !seen[scope.ID] {
			seen[scope.ID] = true
			q.Datasets = append(q.Datasets, scope.ID)
		}
	}
	sort.Strings(q.Datasets)
	state := &OrganizationDeletionImpactUI{Task: s.Task.ID, Item: e.ID, Digest: records.MergeDigest(e.Item), Root: a.library.Root, Epoch: a.libraryEpoch, OrganizationEpoch: s.Epoch, Query: q, Loading: true}
	s.DeletionImpact = state
	life, library, deliver := a.libraryContext, a.library, a.dispatch
	go func() {
		ctx, cancel := context.WithTimeout(life, 5*time.Second)
		defer cancel()
		result, err := library.DeletionImpact(ctx, q)
		if life.Err() != nil {
			return
		}
		deliver(func() {
			current := a.selectedOrganizationEntry()
			if a.organization.DeletionImpact != state || a.organization.Task == nil || current == nil || !a.organizationDeletionImpactMatches(*a.organization.Task, *current) || life.Err() != nil {
				return
			}
			state.Loading = false
			if err != nil {
				state.Error = "引用影响尚未核对成功，数量未知。请核对来源版本、服务与范围后重试。"
				if sourceReaderErrorCode(err) == "version_changed" {
					a.refreshOrganization(false, false)
				}
				return
			}
			if result.Memory != q.Memory || result.Dataset != q.Dataset || result.Revision != q.ExpectedRevision {
				state.Error = "返回的来源版本与本次审核不一致，请重新核对。"
				return
			}
			state.Result = &result
		})
	}()
}

func (a *App) organizationDeletionImpactPanel(c *ui.Context, t records.OrganizationTask, e records.OrganizationEntry) {
	ui.Column(c).Key("organization-delete-impact").Gap(8).Padding(12).Border(1, design.Border).Radius(6).Children(func() {
		ui.Text(c, "被引用的影响").Bold().FontSize(12)
		s := a.organization.DeletionImpact
		if !a.organizationDeletionImpactMatches(t, e) {
			ui.Text(c, "尚未核对直接引用，数量未知。").FontSize(11)
		} else if s.Loading {
			ui.Text(c, "正在核对当前知识的直接引用…").FontSize(11)
		} else if s.Error != "" {
			ui.Text(c, s.Error).FontSize(11).LineHeight(1.6)
		} else if a.organizationDeletionImpactCurrent(t, e) {
			r := s.Result
			ui.Text(c, fmt.Sprintf("本次核对：%d 条当前知识 · %d 处直接来源引用", r.DirectMemoryCount, r.ReferenceCount)).FontSize(11)
			ui.Text(c, fmt.Sprintf("覆盖 %d 个已登记范围；只统计保存的直接来源关系，不推断语义依赖或其他项目。", len(r.CoverageDatasets))).FontSize(10).TextColor(design.Muted).LineHeight(1.6)
			for _, ref := range r.References {
				ui.Text(c, fmt.Sprintf("%s · v%d · %s · 引用来源 v%d", ref.Title, ref.Revision, a.scopeLabel(ref.Dataset), ref.SourceRevision)).FontSize(11).Selectable()
				if ref.DerivedState == "stale" {
					ui.Text(c, "来源已变化 · 已排除检索").FontSize(10).TextColor(design.Muted)
				}
			}
			if r.Truncated {
				ui.Text(c, "仅展示前 50 处明细，总数包含本次核对的其余直接引用。").FontSize(10)
			}
			ui.Text(c, "删除不会改写这些知识的正文。历史和引用依据保留；依赖知识可能因来源失效而停止参与检索。").FontSize(11).LineHeight(1.6)
		}
		quietButton(c, "重新核对引用影响").Key("organization-delete-impact-refresh").Disabled(a.organization.Busy || a.organizationDeletionImpactMatches(t, e) && s.Loading).OnClick(a.refreshOrganizationDeletionImpact)
		quietButton(c, "核对来源").Key("organization-delete-source").Disabled(a.organization.Busy || a.organizationSourcePermissionDenied(e.Item.Target)).OnClick(func() { a.openOrganizationMemory(e.Item.Target) })
	})
}
