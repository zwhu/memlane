//go:build !darwin

package daemon

import "errors"

// ProcessArgs is unsupported outside macOS.
func ProcessArgs(pid int) ([]string, error) {
	return nil, errors.New("process args inspection unsupported on this platform")
}
