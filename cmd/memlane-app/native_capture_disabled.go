//go:build !e2e || !darwin || !cgo

package main

import "github.com/egoist/mygo"

func appendNativeCaptureMenu(items []*mygo.MenuItem, _ func() *mygo.Window) []*mygo.MenuItem {
	return items
}
