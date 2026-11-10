//go:build !e2e || !darwin

package main

import (
	"github.com/egoist/mygo"
	"github.com/zwhu/memlane/internal/desktop"
)

func configureNativeProbe(*mygo.Window, *desktop.App) {}
