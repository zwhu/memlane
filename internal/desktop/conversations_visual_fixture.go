//go:build e2e

package desktop

import (
	"encoding/json"
	"github.com/zwhu/memlane/internal/records"
)

// Synthetic layout evidence only. No official, Agent, model or persistence
// receipts are manufactured by this fixture.
func (a *App) setConversationVisualFixture(state string) {
	const project = "visual-project-a"
	a.page, a.scope, a.recordKind, a.detailMode = "项目", project, "raw", "view"
	a.browsing.View = "memories"
	a.listSort = "最近更新"
	a.projects = []ProjectItem{{ID: project, Name: "item-fe"}, {ID: "visual-project-b", Name: "memlane"}}
	a.scopes = []records.Scope{{ID: project, Kind: "project", Name: "item-fe"}}
	a.openConversations()
	s := &a.conversations
	s.Binding = &records.CogneeBinding{Instance: "synthetic-instance", Dataset: "synthetic-dataset", Project: project}
	s.Loaded = true
	titles := []string{"并发下库存偶尔会扣成负数", "统一接口的错误处理", "梳理组件的测试范围", "列表筛选条件切换后保留选中项"}
	for i, title := range titles {
		row := records.SessionSummary{Session: titles[i], User: "synthetic-user", Dataset: s.Binding.Dataset, LastActivity: "2026-11-12T01:47:00+08:00"}
		s.Rows = append(s.Rows, row)
		detail := records.SessionDetail{SessionSummary: row, Label: title, QACount: 2, ToolCount: 3,
			QAs:    []records.SessionQA{{ID: "qa1", Question: title + "，帮我检查一下事务和更新条件。", Answer: "库存查询与扣减分成了两步。并发请求可能同时通过检查，随后分别执行扣减。\n\n**建议使用带条件的原子更新**\n\n```sql\nUPDATE inventory\nSET stock = stock - :quantity\nWHERE sku_id = :sku_id\n  AND stock >= :quantity;\n```\n\n- 根据受影响行数，判断扣减是否成功。\n- 补充并发用例，验证库存不会小于零。"}, {ID: "qa2", Question: "先补充并发测试，再修改扣减逻辑。", Answer: "## 验证范围\n\n| 场景 | 预期 |\n| --- | --- |\n| 两次并发扣减 | 条件校验生效 |\n| 库存不足 | 返回失败 |\n\n```ts\nconst result = await submitInventoryUpdateWithAnIntentionallyLongFunctionNameForHorizontalScrolling(sku, quantity, transaction);\n```"}},
			Traces: []records.SessionTrace{{ID: "tool1", Function: "Read src/inventory.ts", Status: "success", Params: json.RawMessage(`{"path":"src/inventory.ts"}`), Result: json.RawMessage(`"const stock = await getStock(sku);\nawait updateStock(sku, stock - quantity);"`)}, {ID: "tool2", Function: "Grep updateStock", Status: "success", Result: json.RawMessage(`"src/inventory.ts:24: updateStock(skuId, next)"`)}, {ID: "tool3", Function: "Bash pnpm test inventory", Status: "error", Params: json.RawMessage(`{"command":"pnpm test inventory"}`), Error: "并发用例未通过：预期库存不小于 0。"}},
		}
		key := conversationKey(*s.Binding, row)
		s.Views[key] = &conversationReadingState{Detail: &detail, Tools: state == "conversation-tools"}
		if i == 0 {
			s.Selected = key
			s.SelectedRow = &row
		}
	}
	if state == "conversation-empty" {
		s.Rows = nil
		s.Selected = ""
	}
	if state == "conversation-error" {
		s.Views[s.Selected].ErrorCode = "timeout"
	}
	if state == "conversation-knowledge" {
		a.recordKind = "knowledge"
		owner := MemoryItem{ID: "synthetic-official-knowledge", Dataset: project, ScopeName: "item-fe", Origin: "knowledge", State: "active", IndexState: "indexed", Source: "Codex", Category: "项目约定", Revision: 1, Title: "库存扣减使用原子更新", Text: "库存扣减使用带库存下限条件的原子更新，避免并发请求先查询、后扣减造成超卖。\n\n## 实施约定\n\n- 在更新语句中同时校验可用库存。\n- 根据受影响行数判断扣减结果。\n- 保留并发场景的回归测试。\n\n## 适用范围\n\n适用于 item-fe 项目的库存扣减流程。多商品事务与补偿逻辑需要另行核对。"}
		owner.SourceDetail = &records.AgentSource{Official: &records.CogneeOriginal{Instance: "synthetic-instance", Session: titles[0], Kind: "data"}}
		a.items = []MemoryItem{owner}
		a.selectedID = owner.ID
		a.selectedItem = &owner
		a.organizationSourcesFor, a.organizationSourcesRevision = owner.ID, owner.Revision
		a.organizationSources = []records.OrganizationSource{{Project: project, Memory: "synthetic-official-raw", Revision: 1, Quote: "库存查询与扣减分成了两步。建议使用带条件的原子更新。", Input: records.OrganizationInput{Origin: "raw", Title: "并发下库存偶尔会扣成负数", Source: "codex", Text: "库存查询与扣减分成了两步。建议使用带条件的原子更新。", SourceDetail: &records.AgentSource{Official: &records.CogneeOriginal{Instance: "synthetic-instance", Session: titles[0], Kind: "qa"}}}}}
	}
}
