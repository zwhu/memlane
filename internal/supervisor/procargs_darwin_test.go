//go:build darwin

package supervisor

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestProcessArgsHandlesSpaces(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "Application Support", "Memlane")
	executable := filepath.Join(root, "runtime-0123456789abcdef", "scripts", "dev.py")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	// Hardlink (falling back to copy across filesystems) the running test binary
	// so both the kernel exec path and argv[0] contain spaces without triggering
	// a fresh Mach-O cold-execution scan on macOS.
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(testBinary, executable); err != nil {
		binary, readErr := os.ReadFile(testBinary)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if writeErr := os.WriteFile(executable, binary, 0o700); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestProcessArgsChild$", "--",
		"--config", filepath.Join(root, "runtime.json"), "", "with spaces")
	cmd.Env = append(os.Environ(), "MEMLANE_PROCARGS_TEST_CHILD=1")
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	if ready, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || ready != "ready\n" {
		t.Fatalf("child readiness = %q, %v", ready, err)
	}
	got, err := ProcessArgs(cmd.Process.Pid)
	if err != nil || !slices.Equal(got, cmd.Args) {
		t.Fatalf("ProcessArgs = %q, %v; want %q", got, err, cmd.Args)
	}
}

func TestProcessArgsChild(t *testing.T) {
	if os.Getenv("MEMLANE_PROCARGS_TEST_CHILD") != "1" {
		return
	}
	fmt.Println("ready")
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func TestParseProcArgs2(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		argc    uint32
		payload string
		want    []string
	}{
		{"spaces and empty argument", 3, "/program path\x00\x00\x00program path\x00\x00参数 space\x00ENV=ignored\x00", []string{"program path", "", "参数 space"}},
		{"zero argc", 0, "/program\x00\x00", nil},
		{"negative argc", ^uint32(0), "/program\x00\x00", nil},
		{"large argc", 4097, "/program\x00\x00", nil},
		{"missing exec terminator", 1, "/program", nil},
		{"missing argv", 1, "/program\x00\x00", nil},
		{"short argv", 2, "/program\x00\x00program\x00", nil},
		{"unterminated argument", 1, "/program\x00\x00program", nil},
		{"invalid UTF-8", 1, "/program\x00\x00\xff\x00", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := binary.LittleEndian.AppendUint32(nil, tc.argc)
			got, err := parseProcArgs2(append(raw, tc.payload...))
			if (err != nil) != (tc.want == nil) || !slices.Equal(got, tc.want) {
				t.Fatalf("parseProcArgs2 = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if _, err := parseProcArgs2([]byte{1, 0, 0}); err == nil {
		t.Fatal("accepted truncated argc")
	}
}
