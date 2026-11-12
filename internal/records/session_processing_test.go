package records

import "testing"

func TestSessionProcessingSourceDigestTracksInputRatherThanActivity(t *testing.T) {
	detail := SessionDetail{SessionSummary: SessionSummary{Session: "s", User: "u", Dataset: "d", Status: "running"}, QACount: 1, QAs: []SessionQA{{ID: "qa", Question: "original", Answer: "answer"}}}
	before := SessionProcessingSourceDigest(detail)
	detail.Status, detail.LastActivity = "completed", "2026-11-12T00:00:00Z"
	if SessionProcessingSourceDigest(detail) != before {
		t.Fatal("activity metadata invalidated unchanged observed input")
	}
	detail.QAs[0].Answer = "changed"
	if SessionProcessingSourceDigest(detail) == before {
		t.Fatal("changed input retained the same processing observation")
	}
}
