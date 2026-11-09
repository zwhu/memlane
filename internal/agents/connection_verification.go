package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zwhu/memlane/internal/records"
)

// VerifyConnection is the production read/write/index handshake used before a
// connected Agent can become a processing dependency. The persisted receipt is
// written only after the actual tool transcript satisfies every check.
func VerifyConnection(ctx context.Context, discovery Discovery, installation Installation, options RunOptions) (Execution, error) {
	connection := installation.Connection
	marker := "memlaneConnectionCheck" + strings.ReplaceAll(connection.ID, "-", "") + fmt.Sprint(connection.Generation)
	params, _ := json.Marshal(map[string]any{"operation_id": "connection-check-" + fmt.Sprint(connection.Generation), "text": "Memlane synthetic connection verification " + marker, "origin": "raw", "source": map[string]any{"fragment": "Memlane connection verification; synthetic only"}})
	options.Prompt = "这是 Memlane 合成连接验证。仅使用 Memlane memory tools，不读取文件或运行命令。先 memory_save 保存：" + string(params) + "。取得 persisted 回执后，用 memory_status 等待索引完成，再 memory_search 查询 " + marker + "。失败明确报告，不能宣称成功。"
	execution, err := Run(ctx, discovery, installation, options)
	if err == nil {
		var memoryID string
		var searchable, indexed bool
		for _, call := range execution.Calls {
			if !call.Response.OK {
				continue
			}
			switch call.Tool {
			case "memory_save":
				var receipt records.SaveReceipt
				if json.Unmarshal(call.Response.Result, &receipt) == nil && receipt.Persisted && strings.Contains(receipt.Record.Text, marker) {
					memoryID = receipt.Record.Memory
				}
			case "memory_status":
				var version records.Version
				if json.Unmarshal(call.Response.Result, &version) == nil && version.Memory == memoryID && version.IndexState == "indexed" {
					indexed = true
				}
			case "memory_search":
				var results struct {
					Hits []records.SearchHit `json:"hits"`
				}
				if json.Unmarshal(call.Response.Result, &results) == nil {
					for _, hit := range results.Hits {
						if hit.Version.Memory == memoryID && strings.Contains(hit.Version.Text, marker) {
							searchable = true
						}
					}
				}
			}
		}
		if memoryID == "" || !indexed || !searchable {
			err = errors.New("真实 Agent 未完成保存、索引状态和检索回执，连接尚不可用")
		}
	}
	failure := ""
	if err != nil {
		failure = err.Error()
	}
	persistErr := SaveVerification(installation.Options.ClientRoot, connection.ID, connection.Generation, failure)
	if err == nil {
		err = persistErr
	}
	return execution, err
}
