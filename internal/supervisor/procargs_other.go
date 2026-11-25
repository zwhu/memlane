//go:build !darwin

package supervisor

import "github.com/zwhu/memlane/kit/daemon"

func ProcessArgs(pid int) ([]string, error) {
	return daemon.ProcessArgs(pid)
}
