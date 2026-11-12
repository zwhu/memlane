//go:build !e2e

package main

import (
	"context"
	"github.com/egoist/mygo"
	"github.com/zwhu/memlane/internal/desktop"
)

func configureNativeReading(context.Context, *desktop.App, *mygo.Window) {}
