package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func (a *App) saveMemory() {
	if a.saving || !a.editing || a.libraryContext == nil || strings.TrimSpace(a.draft) == "" {
		return
	}
	a.rememberDraft()
	key := a.selectedID
	kind := "edit"
	id := a.selectedID
	expected := a.drafts[key].Revision
	if a.detailMode == "new" {
		key = a.newDraftKey()
		kind = "save"
		expected = 0
		id = uuid.NewString()
	}
	dataset := a.datasetForLabel(a.draftScope)
	if dataset == "" {
		a.message = "请选择已登记的项目或通用知识范围。"
		return
	}
	mutation := records.Mutation{OperationID: uuid.NewString(), Kind: kind, Memory: id, Dataset: dataset, ExpectedRevision: expected, Title: a.draftTitle, Text: a.draft}
	if kind == "save" {
		mutation.Source = "手动新增"
	}
	if item := a.current(); kind == "edit" && item != nil && item.Experience != nil {
		conditions := a.draftConditions
		mutation.Conditions = &conditions
	}
	if prior, ok := a.pendingMutations[key]; ok {
		mutation.Memory = prior.Memory
		if prior.Text == mutation.Text && prior.Title == mutation.Title && prior.Dataset == mutation.Dataset && sameConditions(prior.Conditions, mutation.Conditions) {
			mutation = prior
		}
	}
	a.pendingMutations[key] = mutation
	a.persistManualMutation(key, mutation)
}

func sameConditions(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func (a *App) restoreMemory(revision int64) {
	item := a.current()
	if item == nil || a.saving {
		return
	}
	key := fmt.Sprintf("restore:%s:%d:%d", item.ID, revision, a.restoreExpected)
	mutation, ok := a.pendingMutations[key]
	if !ok {
		mutation = records.Mutation{OperationID: uuid.NewString(), Kind: "restore", Memory: item.ID, Dataset: item.Dataset, ExpectedRevision: a.restoreExpected, RestoreRevision: revision}
		a.pendingMutations[key] = mutation
	}
	a.applyMutation(key, mutation)
}
func (a *App) applyMutation(key string, mutation records.Mutation) {
	a.saving = true
	a.message = "正在保存…"
	a.conflictCurrent = nil
	page, scope, mode := a.page, a.scope, a.detailMode
	life, client, epoch := a.libraryContext, a.library, a.libraryEpoch
	deliver := a.dispatch
	go func() {
		ctx, cancel := context.WithTimeout(life, 10*time.Second)
		defer cancel()
		v, err := client.Apply(ctx, mutation)
		var latest records.Version
		if errors.Is(err, records.ErrConflict) {
			latest, _ = client.Get(ctx, mutation.Memory)
		}
		if life.Err() != nil {
			return
		}
		deliver(func() {
			if a.libraryEpoch != epoch || life.Err() != nil {
				return
			}
			a.saving = false
			if err != nil {
				if page != a.page || scope != a.scope || mode != a.detailMode {
					return
				}
				if errors.Is(err, records.ErrConflict) {
					a.message = "内容已有新版本，保留你的草稿；请核对最新内容。"
					if latest.Memory != "" {
						item := displayVersion(latest)
						a.conflictCurrent = &item
					}
				} else {
					a.message = "保存失败，草稿已保留。恢复服务后可以再次保存。"
					if a.noteMaintenanceStorageError("manual", key, mutation.OperationID, err) {
						a.message = "保存未完成；原草稿和保存请求保留，请查看存储提示。"
					}
				}
				return
			}
			a.clearMaintenanceStorageError("manual", key)
			delete(a.pendingMutations, key)
			delete(a.drafts, key)
			if page == a.page && scope == a.scope && mode == a.detailMode {
				a.editing = false
				a.detailMode = "view"
				a.selectedID = v.Memory
				item := displayVersion(v)
				a.selectedItem = &item
				a.history = nil
				a.detailScroll.Y = 0
				a.message = "已保存；索引状态单独更新。"
				// A submitted search owns its ranked snapshot. Saving or restoring
				// a detail must not clear it or turn an unrelated record into a hit.
				if a.page != "搜索" || a.searchQuery == "" {
					a.nextCursor = ""
					a.items = nil
				}
			} else {
				a.routeStates[page+":"+scope] = PaneState{ID: v.Memory, Mode: "view"}
			}
			a.refresh(false)
		})
	}()
}
func (a *App) continueFromLatest() {
	if a.conflictCurrent == nil {
		return
	}
	item := *a.conflictCurrent
	a.rememberDraft()
	key := a.selectedID
	if a.detailMode == "new" {
		key = a.newDraftKey()
	}
	d := a.drafts[key]
	d.Revision = item.Revision
	d.Scope = a.scopeLabel(item.Dataset)
	delete(a.pendingMutations, key)
	delete(a.drafts, key)
	a.drafts[item.ID] = d
	a.selectedID = item.ID
	a.selectedItem = &item
	a.detailMode = "edit"
	a.editing = true
	a.draftScope = d.Scope
	a.confirmManualLatest(item.ID, item.Dataset)
	a.conflictCurrent = nil
	a.message = fmt.Sprintf("草稿已保留。下一次保存以 v%d 为基准，请先核对内容。", item.Revision)
}
func (a *App) retryIndex() {
	item := a.current()
	if item == nil || a.libraryContext == nil || a.retryingIndex {
		return
	}
	id, revision := item.ID, item.Revision
	life, client, epoch := a.libraryContext, a.library, a.libraryEpoch
	deliver := a.dispatch
	a.retryingIndex = true
	go func() {
		ctx, cancel := context.WithTimeout(life, 5*time.Second)
		defer cancel()
		err := client.RetryIndex(ctx, id, revision)
		if life.Err() != nil {
			return
		}
		deliver(func() {
			if a.libraryEpoch != epoch || life.Err() != nil {
				return
			}
			a.retryingIndex = false
			if a.selectedID == id {
				if err != nil {
					a.message = "索引重试失败，正文仍已保存；请检查服务。"
				} else {
					a.message = "已提交索引重试，正文和历史保留。"
				}
			}
			a.refresh(false)
		})
	}()
}
func (a *App) searchMemory() {
	if a.libraryContext == nil {
		return
	}
	query := strings.TrimSpace(a.query)
	if query == "" {
		a.searchSequence++
		a.searchQuery, a.searchError = "", ""
		a.searchHits = nil
		a.searching = false
		a.navigate("搜索", "")
		a.refresh(false)
		return
	}
	scope := a.scope
	all := scope == "" && a.page != "通用知识"
	a.navigate("搜索", scope)
	a.selectedID = ""
	a.selectedItem = nil
	a.items = nil
	a.searchHits = nil
	a.readingKnowledgeTotal, a.readingRawTotal = nil, nil
	a.readingCountsRoute = ""
	a.listTotal = 0
	a.nextCursor = ""
	a.searching = true
	a.searchError = ""
	a.searchQuery = query
	a.searchSequence++
	sequence := a.searchSequence
	life, client, epoch := a.libraryContext, a.library, a.libraryEpoch
	deliver := a.dispatch
	go func() {
		ctx, cancel := context.WithTimeout(life, 45*time.Second)
		defer cancel()
		hits, err := client.Search(ctx, scope, query, all)
		if life.Err() != nil {
			return
		}
		deliver(func() {
			if a.libraryEpoch != epoch || life.Err() != nil || sequence != a.searchSequence || a.page != "搜索" || a.scope != scope {
				return
			}
			a.searching = false
			if err != nil {
				a.searchError = "搜索失败；索引服务暂不可用，请重试。"
				return
			}
			versions := []records.Version{}
			for _, hit := range hits {
				versions = append(versions, hit.Version)
			}
			a.applySearchResults(versions)
		})
	}()
}

func (a *App) applySearchResults(versions []records.Version) {
	a.searchHits = displayVersions(versions)
	a.applySearchKind()
}

// Filter the submitted search snapshot without issuing a different query or
// replacing ranked hits with an ordinary database list on tab changes.
func (a *App) applySearchKind() {
	knowledge, raw := 0, 0
	a.items = nil
	for _, item := range a.searchHits {
		isRaw := item.Origin == "raw"
		if isRaw {
			raw++
		} else {
			knowledge++
		}
		if isRaw == (a.recordKind == "raw") && a.matchesReadingFilters(item) {
			a.items = append(a.items, item)
		}
	}
	a.listTotal = len(a.items)
	a.readingCountsRoute = a.page + ":" + a.scope
	a.readingKnowledgeTotal, a.readingRawTotal = &knowledge, &raw
}
