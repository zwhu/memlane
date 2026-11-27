//go:build unix && !darwin

package daemon

func maxFilesPerProc() uint64 {
	return 0
}
