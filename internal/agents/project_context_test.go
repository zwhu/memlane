package agents

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestProjectContextCursorCoverageAndSentReceipt(t *testing.T) {
	one, two := "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"
	revoked := false
	calls := 0
	r := newProjectContextReader(func(ctx context.Context, cursor string) (records.ProjectKnowledgeContextPage, error) {
		calls++
		if revoked {
			return records.ProjectKnowledgeContextPage{}, errors.New("scope revoked fixture")
		}
		if cursor == "" {
			return records.ProjectKnowledgeContextPage{Items: []records.ProjectKnowledgeContextItem{{Memory: one, Revision: 2, Title: "v2", Text: "本项目已用pnpm"}}, Next: one, Total: 2}, nil
		}
		return records.ProjectKnowledgeContextPage{Items: []records.ProjectKnowledgeContextItem{{Memory: two, Revision: 1, Text: "保留当前知识"}}, Total: 2}, nil
	})
	if _, err := r.page(context.Background(), uuid.NewString()); err == nil || calls != 0 {
		t.Fatal("unissued cursor reached host")
	}
	if _, err := r.page(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	r.markSent("")
	if r.requireComplete() == nil {
		t.Fatal("first page claimed complete context")
	}
	if _, err := r.page(context.Background(), one); err != nil {
		t.Fatal(err)
	}
	if r.requireComplete() == nil {
		t.Fatal("unsent final page allowed citations")
	}
	r.markSent(one)
	if err := r.requireComplete(); err != nil {
		t.Fatal(err)
	}
	proof := r.proof()
	if proof.Pages != 2 || proof.SentPages != 2 || proof.Items != 2 || !proof.Complete || len(proof.Digest) != 64 {
		t.Fatal(proof)
	}
	if _, err := r.page(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	r.markSent("")
	if r.proof().Pages != 2 || r.proof().SentPages != 2 {
		t.Fatal("cached page manufactured extra reads")
	}
	revoked = true
	if _, err := r.page(context.Background(), ""); err == nil {
		t.Fatal("cached context bypassed host revocation")
	}
}

func TestProjectContextRejectsChangesMissingItemsAndBudgetOverflow(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	for _, sample := range []struct {
		name string
		page records.ProjectKnowledgeContextPage
	}{
		{"missing", records.ProjectKnowledgeContextPage{Items: []records.ProjectKnowledgeContextItem{{Memory: id, Revision: 1}}, Total: 2}},
		{"bad-revision", records.ProjectKnowledgeContextPage{Items: []records.ProjectKnowledgeContextItem{{Memory: id, Revision: 0}}, Total: 1}},
		{"bad-next", records.ProjectKnowledgeContextPage{Items: []records.ProjectKnowledgeContextItem{{Memory: id, Revision: 1}}, Next: uuid.NewString(), Total: 2}},
		{"oversized", records.ProjectKnowledgeContextPage{Items: []records.ProjectKnowledgeContextItem{{Memory: id, Revision: 1, Text: strings.Repeat("x", 2*1024*1024)}}, Total: 1}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			r := newProjectContextReader(func(context.Context, string) (records.ProjectKnowledgeContextPage, error) { return sample.page, nil })
			if _, err := r.page(context.Background(), ""); err == nil {
				t.Fatal("invalid context accepted")
			}
		})
	}
	changed := false
	r := newProjectContextReader(func(context.Context, string) (records.ProjectKnowledgeContextPage, error) {
		rev := int64(1)
		if changed {
			rev = 2
		}
		return records.ProjectKnowledgeContextPage{Items: []records.ProjectKnowledgeContextItem{{Memory: id, Revision: rev, Text: "current"}}, Total: 1}, nil
	})
	if _, err := r.page(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	changed = true
	if _, err := r.page(context.Background(), ""); err == nil {
		t.Fatal("cached changed version returned")
	}
}
