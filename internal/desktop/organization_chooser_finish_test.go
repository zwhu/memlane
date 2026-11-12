package desktop

import (
	"context"
	"errors"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"testing"
)

func TestOrganizationChooserCancelRequiresDurableDraftAndKeepsIdentity(t *testing.T) {
	for _, outcome := range []string{"saved", "failed", "retired"} {
		t.Run(outcome, func(t *testing.T) {
			a := compactOrganizationFixture()
			a.organization.PreviewID = "same-preview"
			updates := make(chan func(), 4)
			a.dispatch = func(fn func()) { updates <- fn }
			a.libraryContext = context.Background()
			var persisted records.OrganizationPreview
			w := &materialPreviewWriter{life: context.Background(), identity: "library", save: func(_ context.Context, q records.OrganizationPreviewWrite) (records.OrganizationPreviewState, error) {
				persisted = q.Preview
				if outcome == "failed" {
					return records.OrganizationPreviewState{}, errors.New("synthetic failure")
				}
				return records.OrganizationPreviewState{Identity: "library", Generation: q.Generation + 1}, nil
			}}
			a.materialPersistence = &materialPersistence{Loaded: true, Writer: w}
			tt := ui.NewTester(a.View, 1200, 820)
			if e := tt.Click("取消"); e != nil {
				t.Fatal(e)
			}
			if a.page != "整理记录" || !a.organization.Busy {
				t.Fatal("cancel navigated before durable acknowledgement")
			}
			if outcome == "retired" {
				a.libraryEpoch++
			}
			awaitProcessingDispatch(t, updates)
			if outcome == "saved" {
				if a.page != "整理记录" || a.organization.New {
					t.Fatal("design J01: cancel must return to organization records")
				}
			} else if a.page != "整理记录" {
				t.Fatal("failed or retired draft changed navigation")
			}
			if persisted.ID != "same-preview" || !persisted.New || len(persisted.Inputs) != 2 || persisted.Inputs[0].Memory != "manual" || persisted.Inputs[0].Revision != 5 {
				t.Fatal("cancel changed durable draft identity/materials")
			}
			if len(a.organization.Chosen) != 2 || a.organization.Order[0] != "manual" || a.organization.Start != nil {
				t.Fatal("cancel discarded or started draft")
			}
		})
	}
}
func TestOrganizationFrozenChooserCannotCancelAroundOriginalRequest(t *testing.T) {
	a := compactOrganizationFixture()
	original := &records.OrganizationStart{OperationID: "same-operation", Project: "project"}
	a.organization.Start = original
	tt := ui.NewTester(a.View, 800, 620)
	if tt.HasText("取消") {
		t.Fatal("unknown original request exposed generic cancel")
	}
	a.returnFromOrganizationChooser()
	if a.page != "整理记录" || a.organization.Start != original || len(a.organization.Chosen) != 2 {
		t.Fatal("unknown request bypassed original receipt/abandon semantics")
	}
}
func TestOrganizationChooserPrimaryAndCompactExecutor(t *testing.T) {
	a := compactOrganizationFixture()
	tt := ui.NewTester(a.View, 800, 620)
	if tt.HasText("读取当前处理方式") || tt.HasText("前往记忆处理") {
		t.Fatal("secondary actions still occupy ready field")
	}
	if e := tt.Click("更多"); e != nil {
		t.Fatal(e)
	}
	if !tt.HasText("读取当前处理方式") {
		t.Fatal("read action became unreachable")
	}
	tt.Click("更多")
	rect, ok := tt.Find("开始整理 · 会消耗 Token")
	if !ok {
		t.Fatal("primary action missing")
	}
	pixel := tt.Image().RGBAAt(int(rect.X+8), int(rect.Y+8))
	if pixel.R > 100 || pixel.G > 100 || pixel.B > 100 {
		t.Fatalf("primary action lacks dark fill: %+v", pixel)
	}
	a.organization.ProcessingLoaded = false
	tt.Frame()
	if e := tt.Click("开始整理 · 会消耗 Token"); e != nil {
		t.Fatal(e)
	}
	if a.organization.Start != nil {
		t.Fatal("disabled start activated")
	}
}
