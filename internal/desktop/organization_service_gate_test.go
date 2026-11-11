package desktop

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
	"testing"
)

func TestOrganizationStoppedServiceCannotFreezeOrRetryOldReadySnapshot(t *testing.T) {
	for _, state := range []string{"stopped", "checking", "failed", ""} {
		for _, frozen := range []bool{false, true} {
			t.Run(state+map[bool]string{false: "/new", true: "/retry"}[frozen], func(t *testing.T) {
				a := compactOrganizationFixture()
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				a.libraryContext = ctx
				a.dispatch = func(func()) {}
				a.serviceState = state
				a.runtimeStatus.Phase = state
				var original *records.OrganizationStart
				if frozen {
					original = &records.OrganizationStart{OperationID: "same-operation", Project: "project"}
					a.organization.Start = original
				}
				a.startOrganization()
				if a.organization.Start != original || a.organization.Busy {
					t.Fatal("known unavailable service froze or dispatched stale ready processing")
				}
				if len(a.organization.Chosen) != 2 || a.organization.Order[0] != "manual" || a.organization.Project != "project" || !organizationProcessingAvailable(a.organization.Processing) {
					t.Fatal("stop gate discarded material or original processing evidence")
				}
			})
		}
	}
}

func TestOrganizationServiceAvailabilityRejectsMixedState(t *testing.T) {
	for _, phase := range []string{"stopped", "checking", "failed", "unavailable", ""} {
		a := compactOrganizationFixture()
		a.runtimeStatus.Phase = phase
		if a.organizationServiceAvailable() {
			t.Fatal("nonready runtime accepted", phase)
		}
		a.runtimeStatus.Phase = "ready"
		a.serviceState = phase
		if a.organizationServiceAvailable() {
			t.Fatal("nonready service accepted", phase)
		}
	}
	a := compactOrganizationFixture()
	if !a.organizationServiceAvailable() {
		t.Fatal("current ready service refused")
	}
	a.libraryError = "read failed"
	if a.organizationServiceAvailable() {
		t.Fatal("failed current library accepted")
	}
}
