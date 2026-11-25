//go:build darwin

package supervisor

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unicode/utf8"

	"github.com/ebitengine/purego"
)

var procArgsSysctl = sync.OnceValues(func() (func(*int32, *byte, *uintptr) error, error) {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		return nil, err
	}
	symbol, err := purego.Dlsym(lib, "sysctl")
	if err != nil {
		_ = purego.Dlclose(lib)
		return nil, err
	}
	var sysctl func(*int32, uint32, *byte, *uintptr, *byte, uintptr) int32
	purego.RegisterFunc(&sysctl, symbol)
	errnoSymbol, err := purego.Dlsym(lib, "__error")
	if err != nil {
		_ = purego.Dlclose(lib)
		return nil, err
	}
	var errno func() *int32
	purego.RegisterFunc(&errno, errnoSymbol)
	return func(mib *int32, buffer *byte, size *uintptr) error {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		errnoPtr := errno()
		if sysctl(mib, 3, buffer, size, nil, 0) != 0 {
			return syscall.Errno(*errnoPtr)
		}
		return nil
	}, nil
})

// ProcessArgs reads the process's argv from the macOS kernel.
func ProcessArgs(pid int) ([]string, error) {
	if pid <= 0 || int(int32(pid)) != pid {
		return nil, errors.New("invalid process ID")
	}
	sysctl, err := procArgsSysctl()
	if err != nil {
		return nil, err
	}
	const (
		ctlKern       = 1
		kernProcArgs2 = 49
		maxSize       = (256 << 10) + 4
	)
	mib := [3]int32{ctlKern, kernProcArgs2, int32(pid)}
	for range 3 {
		var size uintptr
		if err := sysctl(&mib[0], nil, &size); err != nil {
			return nil, fmt.Errorf("KERN_PROCARGS2 size query for PID %d: %w", pid, err)
		}
		if size == 0 || size > maxSize {
			return nil, fmt.Errorf("invalid KERN_PROCARGS2 size %d", size)
		}
		// XNU rejects a buffer whose payload capacity exceeds ARG_MAX (256 KiB).
		raw := make([]byte, min(size+4096, maxSize))
		size = uintptr(len(raw))
		err := sysctl(&mib[0], &raw[0], &size)
		if err == nil {
			if size > uintptr(len(raw)) {
				return nil, errors.New("KERN_PROCARGS2 result exceeds buffer")
			}
			return parseProcArgs2(raw[:size])
		}
		if !errors.Is(err, syscall.ENOMEM) {
			return nil, fmt.Errorf("KERN_PROCARGS2 read for PID %d: %w", pid, err)
		}
		// Query the size again in case the process exec'd between calls.
	}
	return nil, fmt.Errorf("KERN_PROCARGS2 read failed for PID %d: %w", pid, syscall.ENOMEM)
}

func parseProcArgs2(raw []byte) ([]string, error) {
	if len(raw) < 4 {
		return nil, errors.New("missing KERN_PROCARGS2 argc")
	}
	argc := int32(binary.LittleEndian.Uint32(raw))
	if argc <= 0 || argc > 4096 {
		return nil, fmt.Errorf("invalid KERN_PROCARGS2 argc %d", argc)
	}
	raw = raw[4:]
	end := bytes.IndexByte(raw, 0)
	if end <= 0 {
		return nil, errors.New("missing KERN_PROCARGS2 executable path")
	}
	// Skip the executable terminator and kernel padding. Subsequent empty
	// arguments are significant, so only trim before argv[0].
	raw = bytes.TrimLeft(raw[end+1:], "\x00")
	argv := make([]string, 0, argc)
	for range argc {
		end = bytes.IndexByte(raw, 0)
		if end < 0 || !utf8.Valid(raw[:end]) {
			return nil, errors.New("missing or invalid KERN_PROCARGS2 argument")
		}
		argv = append(argv, string(raw[:end]))
		raw = raw[end+1:]
	}
	return argv, nil
}
