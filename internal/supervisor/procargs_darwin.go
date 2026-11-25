//go:build darwin

package supervisor

import "github.com/zwhu/memlane/kit/daemon"

func ProcessArgs(pid int) ([]string, error) {
	return daemon.ProcessArgs(pid)
}

func parseProcArgs2(raw []byte) ([]string, error) {
	return daemon.ParseProcArgs2(raw)
}
