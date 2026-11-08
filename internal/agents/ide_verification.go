package agents

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func BeginIDEVerification(root, id string, generation int64) (records.IDEVerification, error) {
	installationMu.Lock()
	defer installationMu.Unlock()
	i, err := LoadInstallation(root, id)
	if err != nil {
		return records.IDEVerification{}, err
	}
	if i.Connection.Agent != "trae" || i.Connection.Generation != generation {
		return records.IDEVerification{}, errors.New("IDE connection changed; apply configuration first")
	}
	if err = CheckInstallation(i); err != nil {
		return records.IDEVerification{}, err
	}
	// Repeated copy/check resumes this challenge. Cancel explicitly discards it.
	if i.IDEChallenge != nil && i.IDEReceipt == nil && time.Now().UnixMilli()-i.IDEChallenge.CreatedMS < int64(24*time.Hour/time.Millisecond) {
		return *i.IDEChallenge, nil
	}
	d := records.IDEVerification{ConnectionID: id, Generation: generation, Workspace: i.Options.Workspace, Nonce: uuid.NewString(), CreatedMS: time.Now().UnixMilli()}
	i.IDEChallenge = &d
	i.IDEReceipt = nil
	i.VerifiedGeneration = 0
	i.VerifiedAt = ""
	i.VerificationError = ""
	b, _ := json.MarshalIndent(i, "", "  ")
	return d, privateWrite(installationPath(root, id), b, 0600)
}

func FinishIDEVerification(root string, d records.IDEVerification, receipt *records.IDEVerificationReceipt) error {
	installationMu.Lock()
	defer installationMu.Unlock()
	i, err := LoadInstallation(root, d.ConnectionID)
	if err != nil {
		return err
	}
	if i.Connection.Generation != d.Generation || i.IDEChallenge == nil || *i.IDEChallenge != d {
		return errors.New("IDE verification was cancelled or changed")
	}
	if receipt == nil {
		i.IDEChallenge = nil
		i.IDEReceipt = nil
		i.VerifiedGeneration = 0
		i.VerifiedAt = ""
	} else {
		if !receipt.Complete || receipt.State != "verified" || receipt.Memory != d.MemoryID() || receipt.Revision != 1 || len(receipt.Evidence) != 4 {
			return errors.New("IDE receipt is incomplete")
		}
		if err = CheckInstallation(i); err != nil {
			return err
		}
		i.IDEReceipt = receipt
		i.VerifiedGeneration = d.Generation
		i.VerifiedAt = time.Now().UTC().Format(time.RFC3339)
		i.VerificationError = ""
	}
	b, _ := json.MarshalIndent(i, "", "  ")
	return privateWrite(installationPath(root, d.ConnectionID), b, 0600)
}

func IDEVerificationPrompt(d records.IDEVerification) string {
	params, _ := json.Marshal(map[string]any{"operation_id": d.OperationID(), "text": d.Text(), "origin": "raw", "source": map[string]string{"fragment": records.IDEVerificationSource}})
	return "这是 Memlane 合成连接验证。仅使用 Memlane memory tools，不读取文件或运行命令。先 memory_save 保存：" + string(params) + "。收到 persisted 回执后，memory_status 等待索引完成；memory_get 读取同一 memory_id 的正文与来源；最后 memory_search 查询 " + d.Marker() + "，确认返回同一当前版本。不要使用其他范围。失败如实报告，不能以文本宣称成功。App 会核对实际工具回执。"
}
