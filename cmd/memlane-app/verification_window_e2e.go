//go:build e2e

package main

import "os"

// A deterministic startup size for the two approved Native layout baselines.
// No post-launch window automation or production preference is introduced.
func verificationWindowSize() (int, int) {
	if os.Getenv("MEMLANE_NATIVE_WINDOW_SIZE") == "800x620" {
		return 800, 620
	}
	return 1200, 820
}
