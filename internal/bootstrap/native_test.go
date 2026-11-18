package bootstrap

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPinnedNativeSupportIsPrivateResumableAndTamperEvident(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "engine", "first-install-components.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pins struct {
		Native NativeSupport `json:"native_openssl_arm64"`
	}
	if err = json.Unmarshal(b, &pins); err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join("..", "..", ".local/first-install-20261118/openssl-sequoia-arm64-3.5.9.tar.gz")
	archive, err := os.ReadFile(cached)
	if os.IsNotExist(err) {
		t.Skip("fixed native artifact not cached")
	}
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, "Application Support", "runtime")
	if err = os.MkdirAll(filepath.Join(root, "venv/bin"), 0700); err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python unavailable")
	}
	if err = os.Symlink(python, filepath.Join(root, "venv/bin/python")); err != nil {
		t.Fatal(err)
	}
	identity, _ := json.Marshal(pins.Native)
	if err = claimComponentRoot(filepath.Join(root, "native"), ".memlane-native.json", identity); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "native/artifact.tar.gz"), archive, 0600); err != nil {
		t.Fatal(err)
	}
	if err = pins.Native.verify(root); err == nil {
		t.Fatal("incomplete native support accepted")
	}
	if err = pins.Native.install(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err = pins.Native.install(context.Background(), root); err != nil {
		t.Fatal("resume", err)
	}
	output, err := exec.Command(nativeWrapper(root), "-I", "-c", "import os;print(os.environ['DYLD_LIBRARY_PATH'])").CombinedOutput()
	if err != nil || string(output) != filepath.Join(root, "native/lib")+"\n" {
		t.Fatal(string(output), err)
	}
	target := filepath.Join(root, "native/lib/libssl.3.dylib")
	if err = os.WriteFile(target, []byte("foreign change"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = pins.Native.verify(root); err == nil {
		t.Fatal("modified library accepted")
	}
	if err = pins.Native.install(context.Background(), root); err == nil {
		t.Fatal("modified library overwritten")
	}
	if content, _ := os.ReadFile(target); string(content) != "foreign change" {
		t.Fatal("foreign content lost")
	}
}
