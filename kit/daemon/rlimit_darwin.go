//go:build darwin

package daemon

import "syscall"

func maxFilesPerProc() uint64 {
	n, err := syscall.SysctlUint32("kern.maxfilesperproc")
	if err != nil || n == 0 {
		return 0
	}
	return uint64(n)
}
