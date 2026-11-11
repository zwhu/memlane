package desktop

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type ProjectPreferenceUI struct {
	Panels  map[string]*ProjectPreferencePanel
	Panel   *ProjectPreferencePanel
	Notices map[string]*PreferenceNotice
}
type PreferenceNotice struct {
	Epoch        uint64
	Sequence     int64
	Navigation   string
	Busy, Loaded bool
	Error        string
	Page         records.ProjectPreferenceDecisionsPage
	Cancel       context.CancelFunc
	Checked      time.Time
}
type PreferenceDecisionDraft struct {
	ProjectQuote, PreferenceQuote, Conditions, Reason string
	Rule                                              bool
}
type ProjectPreferencePanel struct {
	Confirming                                                         bool
	Wanted                                                             records.UsageTarget
	Project                                                            records.UsageTarget
	Root, IntentRoot, Identity, Search, Submitted, Cursor, Next, Error string
	Preference                                                         records.UsageTarget
	Items                                                              []records.Version
	Datasets                                                           []string
	Comparison                                                         *records.ProjectPreferenceComparison
	Receipt                                                            *records.ProjectPreferenceDecision
	Pending                                                            *records.ProjectPreferenceConfirmation
	Drafts                                                             map[string]*PreferenceDecisionDraft
	Draft                                                              *PreferenceDecisionDraft
	Open, Loading, Busy, KnownRejected, ReturnFocus, EditingPreference bool
	Sequence                                                           uint64
	Scroll                                                             ui.ScrollState
	Cancel                                                             context.CancelFunc
}

func preferenceTarget(v records.Version) records.UsageTarget {
	return records.UsageTarget{Memory: v.Memory, Dataset: v.Dataset, Revision: v.Revision}
}
func preferenceItemTarget(v MemoryItem) records.UsageTarget {
	return records.UsageTarget{Memory: v.ID, Dataset: v.Dataset, Revision: v.Revision}
}
func preferenceKey(ref records.UsageTarget) string {
	return fmt.Sprintf("%s:%s:%d", ref.Dataset, ref.Memory, ref.Revision)
}
func (a *App) preferencePanelCurrent(s *ProjectPreferencePanel, epoch uint64) bool {
	return s != nil && a.libraryEpoch == epoch && a.library.Root == s.Root && a.projectPreference.Panels[preferenceKey(s.Project)] == s
}
func (a *App) projectPreferencePanelOpen() bool {
	s := a.projectPreference.Panel
	item := a.current()
	return s != nil && s.Open && a.page == "项目" && a.scope == s.Project.Dataset && item != nil && preferenceItemTarget(*item) == s.Project && !a.editing && !a.reader.Open
}
func (a *App) openProjectPreference(item MemoryItem) {
	if item.Origin != "knowledge" || item.State != "active" || a.page != "项目" || a.scope != item.Dataset || a.editing {
		return
	}
	ref := preferenceItemTarget(item)
	if a.projectPreference.Panels == nil {
		a.projectPreference.Panels = map[string]*ProjectPreferencePanel{}
	}
	s := a.projectPreference.Panels[preferenceKey(ref)]
	if s == nil || s.Project != ref || s.Root != a.library.Root {
		root := a.guidanceRoot
		if root == "" {
			root = a.library.Root
		}
		s = &ProjectPreferencePanel{Project: ref, Root: a.library.Root, IntentRoot: root, Drafts: map[string]*PreferenceDecisionDraft{}}
		a.projectPreference.Panels[preferenceKey(ref)] = s
	}
	a.projectPreference.Panel = s
	s.Open = true
	s.ReturnFocus = false
	if s.Pending != nil {
		a.loadPreferenceComparison(s, s.Pending.Query.PreferenceRecord)
	} else if s.Comparison != nil {
		a.loadPreferenceComparison(s, preferenceTarget(s.Comparison.PreferenceRecord))
	} else {
		a.loadPreferenceChoices(s)
	}
}
func (a *App) closeProjectPreference() {
	if s := a.projectPreference.Panel; s != nil {
		s.Open = false
		s.ReturnFocus = true
	}
}
func (a *App) retireProjectPreference() {
	for _, s := range a.projectPreference.Panels {
		if s.Cancel != nil {
			s.Cancel()
		}
	}
	for _, n := range a.projectPreference.Notices {
		if n.Cancel != nil {
			n.Cancel()
		}
	}
	a.projectPreference = ProjectPreferenceUI{}
}

func preferenceReadDatasets(scopes []records.Scope, project string) []string {
	seen := map[string]bool{project: true}
	for _, s := range scopes {
		if s.Kind == "preference" {
			seen[s.ID] = true
		}
	}
	out := []string{}
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
func (a *App) loadPreferenceChoices(s *ProjectPreferencePanel) {
	if s == nil || s.Loading || s.Busy || s.Pending != nil || a.libraryContext == nil || a.dispatch == nil {
		return
	}
	s.Loading = true
	s.Sequence++
	sequence := s.Sequence
	life, client, dispatch, epoch := a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	project, search, cursor, root := s.Project, s.Submitted, s.Cursor, s.IntentRoot
	ctx, cancel := context.WithTimeout(life, 12*time.Second)
	s.Cancel = cancel
	go func() {
		defer cancel()
		scopes, err := client.Scopes(ctx)
		datasets := preferenceReadDatasets(scopes, project.Dataset)
		var page records.Page
		var discovery records.ProjectPreferenceDecisionsPage
		var pending *records.ProjectPreferenceConfirmation
		if err == nil {
			discovery, err = client.ProjectPreferenceDecisions(ctx, records.ProjectPreferenceDecisionsQuery{Project: project.Dataset, ProjectRecord: project, Datasets: datasets})
		}
		if err == nil && (discovery.Project.ID != project.Dataset || discovery.ProjectRecord != project || !manualUUID(discovery.LibraryIdentity)) {
			err = errors.New("comparison discovery mismatch")
		}
		prefIDs := []string{}
		for _, scope := range scopes {
			if scope.Kind == "preference" {
				prefIDs = append(prefIDs, scope.ID)
			}
		}
		if err == nil && len(prefIDs) > 0 {
			page, err = client.List(ctx, records.ListQuery{ScopeKind: "preference", Datasets: prefIDs, Kind: "knowledge", Text: search, Cursor: cursor, Limit: 30, Sort: "updated"})
		}
		if err == nil {
			pending, err = preferenceIntent(ctx, root, discovery.LibraryIdentity, project, "read", nil)
		}
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if !a.preferencePanelCurrent(s, epoch) || s.Sequence != sequence {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = preferenceFailureMessage(err)
				return
			}
			s.Identity = discovery.LibraryIdentity
			s.Datasets = discovery.CoverageDatasets
			s.Items = nil
			for _, v := range page.Items {
				if v.Origin == "knowledge" && v.ScopeKind == "preference" && v.State == "active" && v.Revision > 0 {
					s.Items = append(s.Items, v)
				}
			}
			s.Next = page.NextCursor
			s.Error = ""
			if pending != nil {
				s.Pending = pending
				s.Draft = &PreferenceDecisionDraft{ProjectQuote: pending.ProjectQuote, PreferenceQuote: pending.PreferenceQuote, Conditions: pending.Conditions, Reason: pending.Reason, Rule: pending.ConfirmProjectRule}
				a.loadPreferenceComparison(s, pending.Query.PreferenceRecord)
			} else if s.Wanted.Memory != "" {
				wanted := s.Wanted
				s.Wanted = records.UsageTarget{}
				a.loadPreferenceComparison(s, wanted)
			}
		})
	}()
}
func (a *App) loadPreferenceComparison(s *ProjectPreferencePanel, preference records.UsageTarget) {
	if s == nil || s.Loading || s.Busy || a.libraryContext == nil {
		return
	}
	s.Loading = true
	s.Sequence++
	sequence := s.Sequence
	q := records.ProjectPreferenceQuery{LibraryIdentity: s.Identity, Project: s.Project.Dataset, ProjectRecord: s.Project, PreferenceRecord: preference, Datasets: []string{s.Project.Dataset, preference.Dataset}}
	if s.Pending != nil {
		q = s.Pending.Query
	}
	s.Comparison = nil
	s.Receipt = nil
	life, client, dispatch, epoch := a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	ctx, cancel := context.WithTimeout(life, 10*time.Second)
	s.Cancel = cancel
	go func() {
		defer cancel()
		comparison, err := client.ProjectPreferenceComparison(ctx, q)
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if !a.preferencePanelCurrent(s, epoch) || s.Sequence != sequence {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = preferenceFailureMessage(err)
				s.KnownRejected = s.Pending != nil && preferenceKnownRejection(err)
				s.Comparison = nil
				s.Receipt = nil
				return
			}
			if !validPreferenceComparison(comparison, q) {
				s.Error = "对照回执与所选知识、版本或范围不匹配。"
				return
			}
			s.Preference = q.PreferenceRecord
			s.Identity = comparison.LibraryIdentity
			s.Comparison = &comparison
			s.KnownRejected = false
			s.Receipt = nil
			s.Error = ""
			if s.Pending == nil {
				key := preferenceKey(preference)
				if s.Drafts == nil {
					s.Drafts = map[string]*PreferenceDecisionDraft{}
				}
				if s.Drafts[key] == nil {
					s.Drafts[key] = &PreferenceDecisionDraft{}
				}
				s.Draft = s.Drafts[key]
			}
			if d := comparison.Confirmation; d != nil && validPreferenceDecision(*d, comparison, s.Pending) {
				s.Receipt = d
			}
		})
	}()
}
func validPreferenceComparison(c records.ProjectPreferenceComparison, q records.ProjectPreferenceQuery) bool {
	return manualUUID(c.LibraryIdentity) && (q.LibraryIdentity == "" || c.LibraryIdentity == q.LibraryIdentity) && c.Project.ID == q.Project && c.Project.Kind == "project" && preferenceTarget(c.ProjectRecord) == q.ProjectRecord && preferenceTarget(c.PreferenceRecord) == q.PreferenceRecord && c.ProjectRecord.Origin == "knowledge" && c.PreferenceRecord.Origin == "knowledge" && c.ProjectRecord.State == "active" && c.PreferenceRecord.State == "active" && c.ProjectRecord.ScopeKind == "project" && c.PreferenceRecord.ScopeKind == "preference"
}
func validPreferenceDecision(d records.ProjectPreferenceDecision, c records.ProjectPreferenceComparison, q *records.ProjectPreferenceConfirmation) bool {
	if d.Source != "user" || d.State != "confirmed" || d.Decision != "use_project" || d.LibraryIdentity != c.LibraryIdentity || d.Project != c.Project.ID || d.ProjectRecord != preferenceTarget(c.ProjectRecord) || d.PreferenceRecord != preferenceTarget(c.PreferenceRecord) || d.ConfirmedMS <= 0 {
		return false
	}
	return q == nil || d.ID == q.OperationID && d.ProjectQuote == q.ProjectQuote && d.PreferenceQuote == q.PreferenceQuote && d.Conditions == q.Conditions && d.Reason == q.Reason
}
func validPreferenceDraft(s *ProjectPreferencePanel) bool {
	if s == nil || s.Comparison == nil || s.Draft == nil || !s.Draft.Rule {
		return false
	}
	d := s.Draft
	for i, v := range []string{d.ProjectQuote, d.PreferenceQuote, d.Conditions, d.Reason} {
		if !utf8.ValidString(v) || len(v) > 4096 || i < 3 && strings.TrimSpace(v) == "" {
			return false
		}
	}
	return strings.Contains(s.Comparison.ProjectRecord.Text, d.ProjectQuote) && strings.Contains(s.Comparison.PreferenceRecord.Text, d.PreferenceQuote)
}
func (a *App) confirmPreferenceDecision(s *ProjectPreferencePanel) {
	if s == nil || s.Loading || s.Busy || s.KnownRejected || a.libraryContext == nil {
		return
	}
	if s.Pending == nil {
		if !validPreferenceDraft(s) {
			s.Error = "请填写双方原文中的确切引文、本次适用说明，并明确勾选项目约定。"
			return
		}
		c, d := s.Comparison, s.Draft
		q := records.ProjectPreferenceConfirmation{OperationID: uuid.NewString(), LibraryIdentity: c.LibraryIdentity, Query: records.ProjectPreferenceQuery{LibraryIdentity: c.LibraryIdentity, Project: c.Project.ID, ProjectRecord: preferenceTarget(c.ProjectRecord), PreferenceRecord: preferenceTarget(c.PreferenceRecord), Datasets: []string{c.ProjectRecord.Dataset, c.PreferenceRecord.Dataset}}, ProjectQuote: d.ProjectQuote, PreferenceQuote: d.PreferenceQuote, Conditions: d.Conditions, Reason: d.Reason, ConfirmProjectRule: d.Rule, Decision: "use_project"}
		s.Pending = &q
	}
	q := *s.Pending
	s.Busy = true
	s.Error = ""
	life, client, dispatch, epoch := a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	root := s.IntentRoot
	ctx, cancel := context.WithTimeout(life, 12*time.Second)
	s.Cancel = cancel
	go func() {
		defer cancel()
		_, err := preferenceIntent(ctx, root, q.LibraryIdentity, q.Query.ProjectRecord, "save", &q)
		saved := err == nil
		var receipt records.ProjectPreferenceDecision
		if err == nil {
			receipt, err = client.ConfirmProjectPreference(ctx, q)
		}
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if !a.preferencePanelCurrent(s, epoch) {
				return
			}
			s.Busy = false
			if err != nil {
				s.KnownRejected = saved && preferenceKnownRejection(err)
				if !saved {
					s.Error = "原确认请求尚未完成本地持久保存，尚未发送；编辑和原操作标识保留。"
				} else {
					s.Error = preferenceFailureMessage(err) + "原请求保留，请使用相同请求重试。"
				}
				return
			}
			if s.Comparison == nil || !validPreferenceDecision(receipt, *s.Comparison, &q) {
				s.Error = "确认回执尚未核验，原操作标识保留；请重试原请求。"
				return
			}
			s.Receipt = &receipt
			s.Error = ""
			if a.projectPreference.Notices != nil {
				delete(a.projectPreference.Notices, preferenceKey(s.Project))
			}
		})
	}()
}
func (a *App) resetPreferenceDecision(s *ProjectPreferencePanel) {
	if s == nil || s.Busy || s.Pending != nil && s.Receipt == nil && !s.KnownRejected {
		return
	}
	if s.Pending == nil {
		s.Confirming = false
		if s.Draft != nil {
			s.Draft.Rule = false
		}
		for _, d := range s.Drafts {
			d.Rule = false
		}
		s.Wanted = records.UsageTarget{}
		s.Comparison = nil
		s.Receipt = nil
		s.Preference = records.UsageTarget{}
		s.Draft = nil
		a.loadPreferenceChoices(s)
		return
	}
	s.Busy = true
	q := *s.Pending
	life, dispatch, epoch := a.libraryContext, a.dispatch, a.libraryEpoch
	root := s.IntentRoot
	go func() {
		ctx, cancel := context.WithTimeout(life, 5*time.Second)
		defer cancel()
		_, err := preferenceIntent(ctx, root, q.LibraryIdentity, q.Query.ProjectRecord, "retire", &q)
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if !a.preferencePanelCurrent(s, epoch) {
				return
			}
			s.Busy = false
			if err != nil {
				s.Error = "原请求仍保留，未新建确认；请检查本地保存后重试。"
				return
			}
			if s.Draft != nil {
				s.Draft.Rule = false
			}
			for _, d := range s.Drafts {
				d.Rule = false
			}
			s.Pending = nil
			s.Confirming = false
			s.Wanted = records.UsageTarget{}
			s.Comparison = nil
			s.Receipt = nil
			s.Preference = records.UsageTarget{}
			s.Draft = nil
			s.KnownRejected = false
			s.Error = ""
			a.loadPreferenceChoices(s)
		})
	}()
}
func preferenceKnownRejection(err error) bool {
	var failure *serviceipc.ServiceError
	if !errors.As(err, &failure) {
		return false
	}
	switch failure.Code {
	case "version_changed", "record_unavailable", "source_stale", "scope_denied", "library_changed", "confirmation_required", "invalid_request", "conflict":
		return true
	}
	return false
}
func preferenceFailureMessage(err error) string {
	var failure *serviceipc.ServiceError
	if errors.As(err, &failure) {
		switch failure.Code {
		case "version_changed", "record_unavailable", "source_stale":
			return "所选知识版本或来源状态已变化，请保留原请求并重新核对当前记录。"
		case "scope_denied":
			return "当前读取范围不足，未确认该关系；请重新核对可读范围。"
		case "library_changed":
			return "记忆库身份已变化，原库请求不会用于当前库。"
		case "comparison_unavailable":
			return "当前服务尚不支持该独立确认账本，请更新服务后再核对。"
		}
	}
	return "确认或读取回执尚未取得；不能据此认定已确认或没有差异。"
}

func (a *App) projectPreferenceKnowledgeNotice(c *ui.Context, item MemoryItem) {
	s := a.projectPreference.Panel
	if s != nil && s.EditingPreference && s.Preference.Memory == item.ID && a.page == "通用知识" {
		a.projectPreferenceEditorNotice(c)
	}
	if a.page != "项目" || a.scope != item.Dataset || item.Origin != "knowledge" || item.State != "active" || a.editing {
		return
	}
	ref, key := preferenceItemTarget(item), preferenceKey(preferenceItemTarget(item))
	if a.projectPreference.Notices == nil {
		a.projectPreference.Notices = map[string]*PreferenceNotice{}
	}
	n := a.projectPreference.Notices[key]
	if n == nil || n.Epoch != a.libraryEpoch {
		n = &PreferenceNotice{Epoch: a.libraryEpoch}
		a.projectPreference.Notices[key] = n
	}
	if !a.visualFixture && !n.Busy && (n.Navigation != a.usageNavigation || n.Sequence != a.loadSequence || !n.Loaded && n.Error == "" || time.Since(n.Checked) > 15*time.Second) {
		a.loadPreferenceNotice(n, ref)
	}
	ui.Column(c).Key("project-preference-entry").Gap(6).Children(func() {
		button := quietButton(c, "对照个人偏好…").Key("project-preference-open").AlignSelf(ui.Start).OnClick(func() { a.openProjectPreference(item) })
		if s != nil && s.ReturnFocus && s.Project == ref {
			button.Focus()
			if button.Focused() {
				s.ReturnFocus = false
			}
		}
		if n.Busy {
			ui.Text(c, "正在核验当前可读范围内的用户确认记录…").FontSize(10)
		} else if n.Error != "" {
			ui.Text(c, n.Error).FontSize(11)
			quietButton(c, "重新核验确认记录").OnClick(func() { a.loadPreferenceNotice(n, ref) })
		} else if n.Loaded {
			for _, d := range n.Page.Decisions {
				d := d
				quietButton(c, "用户确认：本次按项目约定参考").Key("project-preference-confirmation-" + d.ID).Tooltip("打开该次确认，重新核验双方版本与适用说明").AlignSelf(ui.Start).OnClick(func() {
					if p := a.projectPreference.Panels[key]; p != nil {
						p.Comparison = nil
						p.Receipt = nil
						p.Wanted = d.PreferenceRecord
					}
					a.openProjectPreference(item)
					if p := a.projectPreference.Panel; p != nil {
						p.Wanted = d.PreferenceRecord
					}
				})
			}
			if len(n.Page.Decisions) > 0 {
				ui.Text(c, fmt.Sprintf("限本次核验的 %d 个可读范围；打开时重新核对双方当前版本。", len(n.Page.CoverageDatasets))).FontSize(10).TextColor(design.Muted)
			}
			if n.Page.Truncated {
				ui.Text(c, "当前只显示前 50 条用户确认，请进入对照选择具体偏好核验。").FontSize(10)
			}
		}
	})
}
func (a *App) loadPreferenceNotice(n *PreferenceNotice, project records.UsageTarget) {
	if n.Busy || a.libraryContext == nil || a.dispatch == nil {
		return
	}
	n.Busy = true
	n.Error = ""
	n.Navigation = a.usageNavigation
	n.Sequence = a.loadSequence
	n.Checked = time.Now()
	life, client, dispatch, epoch := a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	sequence, navigation := n.Sequence, n.Navigation
	ctx, cancel := context.WithTimeout(life, 8*time.Second)
	n.Cancel = cancel
	go func() {
		defer cancel()
		scopes, err := client.Scopes(ctx)
		var page records.ProjectPreferenceDecisionsPage
		if err == nil {
			page, err = client.ProjectPreferenceDecisions(ctx, records.ProjectPreferenceDecisionsQuery{Project: project.Dataset, ProjectRecord: project, Datasets: preferenceReadDatasets(scopes, project.Dataset)})
		}
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if a.libraryEpoch != epoch || a.projectPreference.Notices[preferenceKey(project)] != n || n.Sequence != sequence || n.Navigation != navigation {
				return
			}
			n.Busy = false
			n.Checked = time.Now()
			if err != nil {
				n.Loaded = false
				n.Error = "用户确认记录尚未核验；不能据此判断是否存在适用差异。"
				return
			}
			if page.Project.ID != project.Dataset || page.ProjectRecord != project || !manualUUID(page.LibraryIdentity) {
				n.Error = "当前确认记录范围未匹配，请重新核验。"
				n.Loaded = false
				return
			}
			filtered := page.Decisions[:0]
			for _, d := range page.Decisions {
				if d.Source == "user" && d.State == "confirmed" && d.Decision == "use_project" && d.ProjectRecord == project && d.LibraryIdentity == page.LibraryIdentity {
					filtered = append(filtered, d)
				}
			}
			page.Decisions = filtered
			n.Page = page
			n.Loaded = true
		})
	}()
}
func (a *App) projectPreferenceEditorNotice(c *ui.Context) {
	s := a.projectPreference.Panel
	if s == nil || !s.EditingPreference || a.selectedID != s.Preference.Memory || a.page != "通用知识" {
		return
	}
	quietButton(c, "返回项目对照（保留偏好草稿）").Key("project-preference-edit-return").AlignSelf(ui.Start).OnClick(func() {
		a.navigate("项目", s.Project.Dataset)
		s.EditingPreference = false
		s.Open = true
		if item := a.current(); item != nil && preferenceItemTarget(*item) == s.Project {
			a.loadPreferenceComparison(s, s.Preference)
		}
	})
}
func (a *App) editComparedPreference(s *ProjectPreferencePanel) {
	if s == nil || s.Comparison == nil || s.Busy || s.Pending != nil && s.Receipt == nil {
		return
	}
	v := s.Comparison.PreferenceRecord
	item := displayVersion(v)
	s.Open = false
	s.EditingPreference = true
	a.navigate("通用知识", "")
	a.commonCategory = "preference"
	a.recordKind = "knowledge"
	a.selectedID = v.Memory
	a.selectedItem = &item
	a.detailMode = "view"
	a.editMemory()
}
func (a *App) projectPreferencePanel(c *ui.Context) {
	s := a.projectPreference.Panel
	if s == nil {
		return
	}
	ui.Column(c).Key("project-preference-panel").Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c).Key("project-preference-body").Grow(1).MinHeight(0).TrackScroll(&s.Scroll).Padding(24).Gap(16).Children(func() {
			ui.Text(c, "适用范围").FontSize(11).TextColor(design.Muted)
			ui.Text(c, "项目约定与个人偏好").FontSize(design.Heading).Bold()
			ui.Text(c, "项目明确约定优先。双方内容与出处分别保留，查看不会改变任何一条知识。").FontSize(11).LineHeight(1.6)
			if s.Loading {
				ui.Text(c, "正在核验当前知识、偏好与可读范围…").FontSize(11)
			}
			if s.Error != "" {
				ui.Text(c, s.Error).Key("project-preference-error").FontSize(11).LineHeight(1.6)
			}
			if s.Comparison == nil {
				if s.Pending != nil {
					ui.Text(c, "原确认请求保留 · "+s.Pending.OperationID).Selectable().FontSize(10)
					ui.Text(c, "以下为原请求的历史确认依据，当前版本尚未核验。").FontSize(11)
					ui.Text(c, "项目引文："+s.Pending.ProjectQuote).Selectable().LineHeight(1.6)
					ui.Text(c, "偏好引文："+s.Pending.PreferenceQuote).Selectable().LineHeight(1.6)
					ui.Text(c, "本次适用说明："+s.Pending.Conditions).Selectable().LineHeight(1.6)
				} else {
					ui.Text(c, "明确选择一条个人偏好").Bold().FontSize(12)
					ui.TextInput(c.Key("project-preference-search"), &s.Search).Label("查找个人偏好").Placeholder("按标题或正文查找").Disabled(s.Busy || s.Loading)
					quietButton(c, "搜索个人偏好").Disabled(s.Busy || s.Loading).OnClick(func() { s.Submitted = s.Search; s.Cursor = ""; a.loadPreferenceChoices(s) })
					if len(s.Items) == 0 && !s.Loading && s.Identity != "" {
						ui.Text(c, "当前可读个人偏好中没有匹配记录；可调整搜索，或从通用知识手动新增。").FontSize(11)
					}
					for _, v := range s.Items {
						v := v
						quietButton(c, fmt.Sprintf("选择 %s · v%d", v.Title, v.Revision)).Key("project-preference-choice-" + v.Memory).Justify(ui.Start).Disabled(s.Busy || s.Loading).OnClick(func() { a.loadPreferenceComparison(s, preferenceTarget(v)) })
					}
					ui.Row(c).Wrap().Gap(8).Children(func() {
						quietButton(c, "返回第一页").Disabled(s.Cursor == "" || s.Loading).OnClick(func() { s.Cursor = ""; a.loadPreferenceChoices(s) })
						quietButton(c, "更多个人偏好").Disabled(s.Next == "" || s.Loading).OnClick(func() { s.Cursor = s.Next; a.loadPreferenceChoices(s) })
						quietButton(c, "重新读取范围").Disabled(s.Busy || s.Loading).OnClick(func() { a.loadPreferenceChoices(s) })
					})
				}
			} else {
				comparison := s.Comparison
				width, _ := c.Size()
				project := func() {
					a.preferenceEvidenceCard(c, "当前项目知识", comparison.ProjectRecord, comparison.ProjectConditions, comparison.ProjectConditionsRecorded, s.Receipt != nil)
				}
				preference := func() {
					a.preferenceEvidenceCard(c, "个人偏好", comparison.PreferenceRecord, comparison.PreferenceConditions, comparison.PreferenceConditionsRecorded, false)
				}
				if width >= 1100 {
					ui.Row(c).Gap(14).AlignItems(ui.Start).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Children(project)
						ui.Column(c).Grow(1).MinWidth(0).Children(preference)
					})
				} else {
					project()
					preference()
				}
				quietButton(c, "编辑这条个人偏好").Disabled(s.Busy || s.Loading || s.Pending != nil && s.Receipt == nil).OnClick(func() { a.editComparedPreference(s) })
				if d := s.Receipt; d != nil {
					ui.Text(c, "用户确认：本次按项目约定参考").Key("project-preference-user-badge").Bold().FontSize(12)
					ui.Text(c, "本次适用说明："+d.Conditions).Selectable().LineHeight(1.7)
					if d.Reason != "" {
						ui.Text(c, "确认理由："+d.Reason).Selectable().LineHeight(1.6)
					}
					ui.Text(c, time.UnixMilli(d.ConfirmedMS).Local().Format("2006-01-02 15:04")+" · 独立用户确认记录").FontSize(10).TextColor(design.Muted)
				} else if d := s.Draft; d != nil && (s.Confirming || s.Pending != nil) {
					frozen := s.Busy || s.Pending != nil
					ui.Text(c, "本次确认依据").Bold().FontSize(12)
					ui.Text(c, "项目知识确切引文").FontSize(11).Bold()
					ui.TextArea(c.Key("project-preference-project-quote"), &d.ProjectQuote).Label("项目知识确切引文").Placeholder("从上方项目正文复制确切原文，最多4096字节").Height(65).Disabled(frozen)
					ui.Text(c, "个人偏好确切引文").FontSize(11).Bold()
					ui.TextArea(c.Key("project-preference-preference-quote"), &d.PreferenceQuote).Label("个人偏好确切引文").Placeholder("从上方偏好正文复制确切原文，最多4096字节").Height(65).Disabled(frozen)
					ui.Text(c, "本次适用说明").FontSize(11).Bold()
					ui.TextArea(c.Key("project-preference-conditions"), &d.Conditions).Label("本次适用说明").Placeholder("明确说明本次按项目约定参考的适用情形").Height(80).Disabled(frozen)
					ui.Text(c, "确认理由（可选）").FontSize(11).Bold()
					ui.TextInput(c.Key("project-preference-reason"), &d.Reason).Label("确认理由（可选）").Placeholder("确认理由（可选）").Disabled(frozen)
					ui.Checkbox(c.Key("project-preference-explicit-rule"), &d.Rule, "该条为本项目明确约定").Disabled(frozen)
					ui.Text(c, "本次说明单独记录；两条知识的正文、来源与原有适用条件保持各自版本。").FontSize(10).LineHeight(1.6)
				}
			}
		})
		ui.Row(c).Key("project-preference-footer").Shrink(0).Wrap().Padding(14, 24).Gap(10).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Text(c, "双方内容与出处分别保留").FontSize(10).Grow(1).MinWidth(0)
			if s.Receipt != nil || s.KnownRejected {
				quietButton(c, "重新选择并核对").Disabled(s.Busy).OnClick(func() { a.resetPreferenceDecision(s) })
			} else if s.Comparison != nil && !s.Confirming && s.Pending == nil {
				quietButton(c, "记录本次适用关系…").Key("project-preference-start-confirmation").Disabled(s.Busy || s.Loading).OnClick(func() { s.Confirming = true })
			} else if s.Comparison != nil || s.Pending != nil {
				label := "确认本次按项目约定参考"
				if s.Pending != nil {
					label = "重试原确认请求"
				}
				actionButton(c, label, nil).Key("project-preference-confirm").Disabled(s.Busy || s.Loading || s.Pending == nil && !validPreferenceDraft(s)).OnClick(func() { a.confirmPreferenceDecision(s) })
			}
		})
	})
}
func (a *App) preferenceEvidenceCard(c *ui.Context, label string, v records.Version, conditions string, recorded, confirmed bool) {
	ui.Column(c).Key("preference-evidence-"+v.Memory).Padding(14).Gap(10).Border(1, design.Border).Radius(7).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(design.Muted)
		if confirmed {
			ui.Text(c, "本项目参考 · 用户确认").FontSize(10).Bold()
		}
		ui.Text(c, v.Title).FontSize(14).Bold()
		ui.Text(c, fmt.Sprintf("v%d · %s · %s", v.Revision, v.ScopeName, sourceDisplayName(v.Source))).FontSize(10).TextColor(design.Muted)
		ui.Text(c, a.conflictReadableText(c, fmt.Sprintf("preference-evidence-%s-%d", v.Memory, v.Revision), v.Text)).Selectable().LineHeight(1.7)
		ui.Text(c, "该版本适用条件").Bold().FontSize(11)
		if recorded && strings.TrimSpace(conditions) == "" {
			conditions = "该版本未填写适用条件。"
		}
		if recorded {
			ui.Text(c, conditions).Selectable().LineHeight(1.6)
		} else {
			ui.Text(c, "该版本未单独记录适用条件，可结合正文核对。").FontSize(11).TextColor(design.Muted)
		}
		quietButton(c, "复制此条全文").Key("preference-copy-" + v.Memory).OnClick(func() { c.WriteClipboard(v.Text) })
	})
}
