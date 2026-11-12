//go:build memlane_graph_e2e && (!darwin || !cgo)

package main

import "errors"

func captureSystemWindow(uintptr, string) error {
	return errors.New("native window capture requires macOS cgo development build")
}
