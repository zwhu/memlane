//go:build !e2e

package main

import "github.com/zwhu/memlane/internal/desktop"

func configureVisualFixture(*desktop.App) {}

func isolatedVisualFixtureEnabled() bool { return false }
