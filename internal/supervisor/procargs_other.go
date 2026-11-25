//go:build !darwin

package supervisor

import "errors"

// ProcessArgs is unsupported outside macOS.
func ProcessArgs(pid int) ([]string, error) {
	return nil, errors.New("process args inspection unsupported on this platform")
}
