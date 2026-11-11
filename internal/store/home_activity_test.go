package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestHomeActivitiesKeepCommittedFactsDistinctAndPageWithoutLoss(t *testing.T) {
	ctx := context.Background()
	s, c, v := usageFixture(t)
	read := usageObservation(c, v, "read")
	receipt, err := s.RecordUsage(ctx, read)
	mustMerge(t, err)
	_, err = s.RecordUsage(ctx, read)
	mustMerge(t, err)
	returned := usageObservation(c, v, "return")
	_, err = s.RecordUsage(ctx, returned)
	mustMerge(t, err)
	adopted := usageObservation(c, v, "adopt")
	adopted.Exposure = receipt.ID
	_, err = s.RecordUsage(ctx, adopted)
	mustMerge(t, err)
	verification := usageObservation(c, v, "read")
	verification.Channel = "verification"
	_, err = s.RecordUsage(ctx, verification)
	mustMerge(t, err)
	_, err = s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "edit", Memory: v.Memory, Dataset: v.Dataset, ExpectedRevision: 1, Title: "new title", Text: "updated actual knowledge"})
	mustMerge(t, err)
	seen := map[string]bool{}
	kinds := map[string]int{}
	before := ""
	for {
		page, err := s.HomeActivities(ctx, records.HomeActivityQuery{Limit: 2, Before: before})
		mustMerge(t, err)
		if page.Total != 5 {
			t.Fatalf("facts duplicated or probe appeared: %+v", page)
		}
		for _, event := range page.Items {
			if seen[event.ID] {
				t.Fatal("cursor repeated event", event)
			}
			seen[event.ID] = true
			kinds[event.Kind]++
			if event.Kind == "read" && (event.Revision != 1 || event.Title == "new title") {
				t.Fatal("read fact was rewritten by edit", event)
			}
		}
		if page.Next == "" {
			break
		}
		before = page.Next
	}
	if len(seen) != 5 || kinds["read"] != 1 || kinds["return"] != 1 || kinds["adopt"] != 1 || kinds["save"] != 1 || kinds["edit"] != 1 {
		t.Fatalf("facts lost: %v %v", seen, kinds)
	}
	first, err := s.HomeActivities(ctx, records.HomeActivityQuery{Limit: 1})
	mustMerge(t, err)
	if _, err = s.HomeActivities(ctx, records.HomeActivityQuery{Limit: 1, Before: first.Next, Kind: "read"}); err == nil {
		t.Fatal("cursor reused with different filters")
	}
	filtered, err := s.HomeActivities(ctx, records.HomeActivityQuery{Project: v.Dataset, Kind: "read"})
	mustMerge(t, err)
	if filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].Revision != 1 {
		t.Fatal(filtered)
	}
	missing, err := s.HomeActivities(ctx, records.HomeActivityQuery{Project: uuid.NewString()})
	mustMerge(t, err)
	if missing.Total != 0 || len(missing.Items) != 0 {
		t.Fatal("project filter leaked activity")
	}
}

func TestHomeReviewQueueIncludesOrganizationAndRemovesStaleOrDecided(t *testing.T) {
	s, _ := mergeDB(t)
	task, decision := reviewOrganizationFixture(t, s, "merge")
	page, err := s.HomeReviews(context.Background(), records.HomeReviewQuery{Limit: 3})
	mustMerge(t, err)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Task != task.ID || page.Items[0].ID != decision.Item || page.Items[0].Owner != "organization" {
		t.Fatal("review queue omitted persisted proposal", page)
	}
	source := task.Snapshot.Inputs[0]
	_, err = s.Apply(context.Background(), Mutation{OperationID: uuid.NewString(), Kind: "edit", Memory: source.Memory, Dataset: task.Project, ExpectedRevision: source.Revision, Text: "source changed"})
	mustMerge(t, err)
	page, err = s.HomeReviews(context.Background(), records.HomeReviewQuery{})
	mustMerge(t, err)
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatal("stale suggestion appeared as ready", page)
	}
	before := sqlRows(t, s.DB, "versions")
	if _, err = s.HomeReviews(context.Background(), records.HomeReviewQuery{}); err != nil {
		t.Fatal(err)
	}
	after := sqlRows(t, s.DB, "versions")
	if len(before) != len(after) {
		t.Fatal("reading review queue changed knowledge")
	}
}

func TestHomeActivityScopeAndObservedVersionSurviveConfirmedMove(t *testing.T) {
	s, _ := mergeDB(t)
	task, decision := reviewOrganizationFixture(t, s, "move")
	source := task.Snapshot.Inputs[0]
	_, err := s.ReviewOrganization(context.Background(), decision)
	mustMerge(t, err)
	page, err := s.HomeActivities(context.Background(), records.HomeActivityQuery{Project: task.Project})
	mustMerge(t, err)
	found := false
	for _, event := range page.Items {
		if event.Memory == source.Memory && event.Revision == 1 {
			found = true
			if event.Dataset != task.Project || event.CurrentDataset != task.Entries[0].Item.Destination {
				t.Fatal("move rewrote historical scope or lost current location", event)
			}
		}
	}
	if !found {
		t.Fatal("move removed original save fact")
	}
}

func TestHomeReviewQueueUnifiesMergeAndGeneralExperienceWithoutApplying(t *testing.T) {
	s, _ := mergeDB(t)
	merge, _, raw := mergeFixture(t, s)
	completeFixture(t, s, merge, raw)
	global, decision := globalExperienceFixture(t, s)
	before := sqlRows(t, s.DB, "versions")
	page, err := s.HomeReviews(context.Background(), records.HomeReviewQuery{Limit: 1})
	mustMerge(t, err)
	if page.Total != 2 || len(page.Items) != 1 || page.Next == "" {
		t.Fatal("incomplete combined review queue", page)
	}
	second, err := s.HomeReviews(context.Background(), records.HomeReviewQuery{Limit: 1, Before: page.Next})
	mustMerge(t, err)
	if second.Total != 2 || len(second.Items) != 1 || second.Items[0].ID == page.Items[0].ID {
		t.Fatal("review paging lost identity", second)
	}
	filtered, err := s.HomeReviews(context.Background(), records.HomeReviewQuery{Action: "global_experience"})
	mustMerge(t, err)
	if filtered.Total != 1 || filtered.Items[0].Task != global.ID {
		t.Fatal("global proposal missing", filtered)
	}
	if len(before) != len(sqlRows(t, s.DB, "versions")) {
		t.Fatal("queue read applied knowledge")
	}
	decision.Confirm = false
	_, err = s.ReviewOrganization(context.Background(), decision)
	mustMerge(t, err)
	page, err = s.HomeReviews(context.Background(), records.HomeReviewQuery{})
	mustMerge(t, err)
	if page.Total != 1 || page.Items[0].Owner != "merge" {
		t.Fatal("decided review remained pending", page)
	}
}

func TestHomeActivitiesExcludeOnlyExactSyntheticConnectionSaves(t *testing.T) {
	ctx := context.Background()
	s, c, v := usageFixture(t)
	operation := "connection-check-" + fmt.Sprint(c.Generation)
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(c.ID+"\x00"+operation)).String()
	body := "Memlane synthetic connection verification memlaneConnectionCheck" + strings.ReplaceAll(c.ID, "-", "") + fmt.Sprint(c.Generation)
	synthetic, err := s.Apply(ctx, Mutation{OperationID: "agent:" + id, Kind: "save", Memory: id, Dataset: v.Dataset, Text: body, Origin: "raw", Source: "pi", SourceDetail: &records.AgentSource{Fragment: records.ConnectionVerificationFragment}})
	mustMerge(t, err)
	// Same title/body/fragment with a normal identity remains ordinary raw data.
	ordinary, err := s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "save", Memory: uuid.NewString(), Dataset: v.Dataset, Text: body, Title: body, Origin: "raw", Source: "pi", SourceDetail: &records.AgentSource{Fragment: records.ConnectionVerificationFragment}})
	mustMerge(t, err)
	// A probe reading real knowledge cannot hide its save or daily read facts.
	verification := usageObservation(c, v, "read")
	verification.Channel = "verification"
	_, err = s.RecordUsage(ctx, verification)
	mustMerge(t, err)
	daily := usageObservation(c, v, "read")
	_, err = s.RecordUsage(ctx, daily)
	mustMerge(t, err)
	// Even if an older client reports a daily read of the synthetic identity, it
	// stays outside the daily product feed. No real knowledge is filtered by this.
	_, err = s.RecordUsage(ctx, usageObservation(c, synthetic, "read"))
	mustMerge(t, err)
	seen := map[string]bool{}
	before := ""
	for {
		page, err := s.HomeActivities(ctx, records.HomeActivityQuery{Limit: 1, Before: before})
		mustMerge(t, err)
		if page.Total != 3 {
			t.Fatalf("excluded total drifted across pages: %+v", page)
		}
		for _, event := range page.Items {
			if event.Memory == synthetic.Memory {
				t.Fatal("connection verification leaked into daily feed", event)
			}
			if seen[event.ID] {
				t.Fatal("pagination repeated fact")
			}
			seen[event.ID] = true
		}
		// A later connection generation may verify while the user paginates.
		// Its insertion must not change the daily total or displace facts.
		if before == "" {
			nextGeneration := c.Generation + 1
			nextID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(c.ID+"\x00connection-check-"+fmt.Sprint(nextGeneration))).String()
			nextBody := "Memlane synthetic connection verification memlaneConnectionCheck" + strings.ReplaceAll(c.ID, "-", "") + fmt.Sprint(nextGeneration)
			_, err = s.Apply(ctx, Mutation{OperationID: "agent:" + nextID, Kind: "save", Memory: nextID, Dataset: v.Dataset, Text: nextBody, Origin: "raw", Source: "pi", SourceDetail: &records.AgentSource{Fragment: records.ConnectionVerificationFragment}})
			mustMerge(t, err)
		}
		if page.Next == "" {
			break
		}
		before = page.Next
	}
	if len(seen) != 3 || !seen["version:"+v.Memory+":1"] || !seen["version:"+ordinary.Memory+":1"] || !seen["usage:"+daily.OperationID+":"+v.Memory+":1"] {
		t.Fatal("real knowledge or ordinary namesake was hidden", seen)
	}
	// Filtering is a read projection; synthetic original and its content type stay.
	current, err := s.Current(ctx, id)
	mustMerge(t, err)
	if current.Origin != "raw" || current.Text != body {
		t.Fatal("projection changed saved verification record")
	}
}
