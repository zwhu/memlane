//go:build !e2e

package main

import "testing"

func TestProductionCannotEnableSyntheticStartupByEnvironment(t *testing.T) {
	t.Setenv("MEMLANE_VISUAL_FIXTURE", "1")
	t.Setenv("MEMLANE_MVP_VISUAL_STATE", "conversation-knowledge")
	t.Setenv("MEMLANE_SETTINGS_VISUAL_STATE", "agents")
	if isolatedVisualFixtureEnabled() {
		t.Fatal("production enabled synthetic startup")
	}
}
