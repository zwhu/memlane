package desktop

import (
	"context"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// Drafts belong to a frozen model item, never to a row index or its label.
// ConfigureLibrary retires this map with the enclosing library epoch.
type ExperienceReviewDraft struct {
	ProposalDigest string
	Value          records.ExperienceReviewEdit
	Request        *records.OrganizationReview
	OriginalOpen   bool
	RejectRequest  *records.OrganizationReview
}

func (a *App) experienceReviewDraft(e records.OrganizationEntry) *ExperienceReviewDraft {
	s := &a.organization
	if s.ExperienceDrafts == nil {
		s.ExperienceDrafts = map[string]*ExperienceReviewDraft{}
	}
	digest := records.MergeDigest(e.Item)
	d := s.ExperienceDrafts[e.ID]
	if d == nil || d.ProposalDigest != digest {
		d = &ExperienceReviewDraft{ProposalDigest: digest, Value: records.ExperienceReviewEdit{Title: e.Item.Title, Text: e.Item.Text, Conditions: e.Item.Conditions}}
		s.ExperienceDrafts[e.ID] = d
	}
	return d
}

func (a *App) experienceReviewPreview(c *ui.Context, t records.OrganizationTask, e records.OrganizationEntry) {
	ui.Text(c, e.Item.Reason).Selectable().LineHeight(1.7)
	meta(c, "保存范围", a.scopeLabel(e.Item.Destination))
	if e.State == "review_pending" {
		d := a.experienceReviewDraft(e)
		frozen := a.organization.Busy || d.Request != nil
		ui.Text(c, "先看清通用表述与适用条件").Bold()
		ui.Text(c, "标题").FontSize(11).TextColor(design.Muted)
		ui.TextInput(c.Key("experience-review-title-"+e.ID), &d.Value.Title).Label("标题").Height(34).Disabled(frozen)
		ui.Text(c, "通用表述").FontSize(11).TextColor(design.Muted)
		ui.TextArea(c.Key("experience-review-text-"+e.ID), &d.Value.Text).Label("通用表述").MinHeight(100).Height(150).LineHeight(1.7).Disabled(frozen)
		ui.Text(c, "适用条件").FontSize(11).TextColor(design.Muted)
		ui.TextArea(c.Key("experience-review-conditions-"+e.ID), &d.Value.Conditions).Label("适用条件").Height(80).LineHeight(1.7).Disabled(frozen)
		ui.Text(c, "确认后新建独立通用经验；原项目知识、来源版本和模型原候选保留。项目明确约定优先，来源关联不授予原项目读取权限。").FontSize(11).TextColor(design.Muted)
		ui.Row(c).Gap(8).Children(func() {
			label := "确认保存为通用经验"
			if d.Request != nil {
				label = "重试确认回执"
			}
			actionButton(c, label, nil).Key("experience-review-confirm-" + e.ID).Disabled(a.organization.Busy || !d.Value.Valid()).OnClick(func() { a.confirmExperienceReview(t, e) })
			quietButton(c, "拒绝此项").Key("organization-reject-" + e.ID).Disabled(a.organization.Busy).OnClick(func() { a.reviewOrganizationEntry(t, e, false) })
		})
		if d.Request != nil && !a.organization.Busy && !a.organization.Loading {
			quietButton(c, "继续编辑预览").Key("experience-review-unfreeze-" + e.ID).OnClick(func() { d.Request = nil })
		}
		ui.Collapsible(c, "模型原候选", &d.OriginalOpen, func() {
			ui.Text(c, e.Item.Title).Bold()
			ui.Text(c, e.Item.Text).Selectable().LineHeight(1.7)
			ui.Text(c, e.Item.Conditions).Selectable().LineHeight(1.7)
		})
		return
	}
	ui.Text(c, "模型原候选").FontSize(11).TextColor(design.Muted)
	ui.Text(c, e.Item.Title).Bold()
	ui.Text(c, e.Item.Text).Selectable().LineHeight(1.7)
	meta(c, "原候选适用条件", e.Item.Conditions)
	if e.Receipt != nil && e.Receipt.EditedExperience != nil {
		edited := e.Receipt.EditedExperience
		ui.Text(c, "本次保存的用户确认内容").Bold()
		ui.Text(c, edited.Title).Bold()
		ui.Text(c, edited.Text).Selectable().LineHeight(1.7)
		meta(c, "本次适用条件", edited.Conditions)
	}
}

func (a *App) confirmExperienceReview(t records.OrganizationTask, e records.OrganizationEntry) {
	if a.organization.Busy || a.libraryContext == nil || e.State != "review_pending" || e.Item.Action != "global_experience" {
		return
	}
	d := a.experienceReviewDraft(e)
	if !d.Value.Valid() {
		a.organization.Error = "请填写标题、通用表述和适用条件。"
		return
	}
	if d.Request == nil {
		edit := d.Value
		d.Request = &records.OrganizationReview{Task: t.ID, Item: e.ID, Digest: records.MergeDigest(e.Item), Receipt: uuid.NewString(), Confirm: true, Edit: &edit}
	}
	request := *d.Request
	client := a.organization.Client
	a.organizationAction(func(ctx context.Context) (records.OrganizationTask, error) {
		return client.Review(ctx, request)
	}, false)
}
