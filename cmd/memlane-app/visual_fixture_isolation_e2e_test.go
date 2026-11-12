//go:build e2e

package main

import (
	"os"
	"testing"
)

func TestSyntheticReadingAndSettingsSkipRealStartup(t *testing.T) {
	prior := os.Args
	t.Cleanup(func() { os.Args = prior })
	for _, tc := range []struct {
		name, enable, reading, settings string
		arg, want                       bool
	}{
		{"disabled", "", "conversation-knowledge", "", false, false},
		{"reading", "1", "conversation-knowledge", "", false, true},
		{"settings", "1", "", "agents", false, true},
		{"other_existing_fixture", "1", "existing-scene", "", false, false},
		{"explicit_cli_fixture", "", "conversation-tools", "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = []string{"synthetic-app"}
			if tc.arg {
				os.Args = append(os.Args, "--p1-visual-fixture")
			}
			t.Setenv("MEMLANE_VISUAL_FIXTURE", tc.enable)
			t.Setenv("MEMLANE_MVP_VISUAL_STATE", tc.reading)
			t.Setenv("MEMLANE_SETTINGS_VISUAL_STATE", tc.settings)
			if got := isolatedVisualFixtureEnabled(); got != tc.want {
				t.Fatalf("isolation=%v want=%v", got, tc.want)
			}
		})
	}
}
