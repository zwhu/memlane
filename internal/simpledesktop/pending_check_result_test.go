package simpledesktop

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPendingCheckMessageUsesActualOpenCodeAcknowledgementReceipt(t *testing.T) {
	// Checkpoint41 exported this actual Go->Node->official response. A matched
	// item has already left the uncertain queue, so subtracting it again is wrong.
	raw, err := os.ReadFile("testdata/opencode-pending-management.json")
	if err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		Checks struct {
			Cases map[string]struct {
				Status PendingMaterials `json:"status"`
			} `json:"real_cases"`
		} `json:"checks"`
	}
	if json.Unmarshal(raw, &evidence) != nil {
		t.Fatal("invalid checkpoint evidence")
	}
	correct := evidence.Checks.Cases["pending-operator-check-correct"].Status
	if correct.Pending != 0 || correct.Unknown != 0 || correct.Matched != 1 || !correct.Checked {
		t.Fatal("actual acknowledgement fixture missing")
	}
	message := pendingCheckMessage(AgentInstallation{HostKind: "opencode"}, correct)
	if !strings.Contains(message, "1 条记录已确认采集，0 条采集结果仍未知") || strings.Contains(message, "-1") || strings.Contains(message, "20 条") {
		t.Fatalf("wrong post-acknowledgement presentation: %s", message)
	}
	shadow := evidence.Checks.Cases["pending-operator-check-shadow"].Status
	if shadow.Unknown != 1 || shadow.Matched != 0 {
		t.Fatal("original-dataset refusal fixture missing")
	}
	message = pendingCheckMessage(AgentInstallation{HostKind: "opencode"}, shadow)
	if !strings.Contains(message, "1 条采集结果仍未知") || !strings.Contains(message, "原记录继续保留") {
		t.Fatalf("unknown disappeared from presentation: %s", message)
	}
}

func TestPendingCheckMessagePreservesReadOnlyPythonMatchSemantics(t *testing.T) {
	message := pendingCheckMessage(AgentInstallation{}, PendingMaterials{Unknown: 3, Matched: 1})
	if !strings.Contains(message, "1 条内容在服务端最近记录中匹配，2 条仍无法确认") || !strings.Contains(message, "最近 20 条") {
		t.Fatalf("read-only match changed into queue acknowledgement: %s", message)
	}
	message = pendingCheckMessage(AgentInstallation{}, PendingMaterials{Unknown: 0, Matched: 1})
	if strings.Contains(message, "-1") || !strings.Contains(message, "核对计数尚未确认") {
		t.Fatalf("invalid receipt displayed as a confirmed count: %s", message)
	}
}
