package records

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestOrganizationValidationKeepsStrictGateAndBoundedDetail(t *testing.T) {
	id := uuid.NewString()
	s := OrganizationSnapshot{Schema: OrganizationReviewSchema, Task: uuid.NewString(), Project: uuid.NewString(), Attempt: 1, Inputs: []OrganizationInput{{MergeInput: MergeInput{Memory: id, Revision: 1, Title: "合成依据", Text: "项目使用 pnpm 管理依赖。", CapturedAt: "2026-11-10T00:00:00Z"}, Origin: "knowledge", Roots: []MergeTarget{{Memory: id, Revision: 1}}}}}
	if err := s.Seal(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		reason string
		change func(*OrganizationItem)
	}{
		{"item_order", func(i *OrganizationItem) { i.Index = 2 }},
		{"citation", func(i *OrganizationItem) { i.Sources[0].Quote = "private-unmatched-material" }},
		{"reason", func(i *OrganizationItem) { i.Reason = " " }},
		{"summary_fields", func(i *OrganizationItem) { i.Target = id }},
		{"tag_value", func(i *OrganizationItem) {
			i.Action = "add_tags"
			i.Target = id
			i.Title = ""
			i.Text = ""
			i.Tags = []string{"pnpm", "pnpm"}
		}},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			i := OrganizationItem{Index: 1, Action: "summary", Title: "合成摘要", Text: "使用 pnpm。", Reason: "具体合成依据", Tags: []string{}, Sources: []MergeSource{{Memory: id, Revision: 1, Quote: s.Inputs[0].Text}}}
			tc.change(&i)
			r := OrganizationResult{Schema: s.Schema, Task: s.Task, Attempt: s.Attempt, Digest: s.Digest, ID: s.ProposalID(), Items: []OrganizationItem{i}, Explanation: "synthetic"}
			raw, _ := json.Marshal(r)
			_, err := s.Decode(raw)
			if err == nil {
				t.Fatal("invalid candidate passed")
			}
			var access *AccessError
			if !errors.As(err, &access) || access.Code != "invalid_proposal" {
				t.Fatal("changed public failure code", err)
			}
			detail, ok := err.(interface {
				ValidationReason() string
				ValidationItem() int
			})
			if !ok || detail.ValidationReason() != tc.reason || detail.ValidationItem() != 1 || err.Error() != "invalid_proposal" {
				t.Fatal("missing or unbounded diagnostic", err)
			}
		})
	}
}
