//go:build !unix

package daemon

// EnsureFileDescriptorLimit is a no-op on non-Unix platforms.
func EnsureFileDescriptorLimit(uint64) (uint64, error) {
	return 0, nil
}
