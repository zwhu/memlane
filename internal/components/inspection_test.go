package components

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func inspectionFixture(t *testing.T) (*Manager, Descriptor, string) {
	t.Helper()
	m, err := New(t.TempDir(), make(ed25519.PublicKey, ed25519.PublicKeySize), 1, true)
	if err != nil {
		t.Fatal(err)
	}
	d := Descriptor{ID: "worker", Version: "1", Platform: runtime.GOOS + "-" + runtime.GOARCH, ProtocolMin: 1, ProtocolMax: 1, URL: "http://127.0.0.1/archive", Bytes: 1, UnpackedBytes: 1, SHA256: strings.Repeat("0", 64)}
	root := filepath.Join(m.Root, "components", d.ID, d.Version)
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	meta := Metadata{ID: d.ID, Version: d.Version, Platform: d.Platform, ProtocolMin: 1, ProtocolMax: 1, Executable: "f000", Links: []Link{{Path: "alias", Target: "f000"}}}
	for i := 0; i < 128; i++ {
		name := fmt.Sprintf("f%03d", i)
		data := []byte(strings.Repeat(name, 1024))
		sum := sha256.Sum256(data)
		if err = os.WriteFile(filepath.Join(root, name), data, 0700); err != nil {
			t.Fatal(err)
		}
		meta.Files = append(meta.Files, Asset{Path: name, Bytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
	}
	if err = os.Symlink("f000", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "component.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	d.ManifestSHA256 = hex.EncodeToString(sum[:])
	m.approved[d.ID] = d
	return m, d, root
}

func TestInspectionStillRejectsChangedAssetsLinksAndUnlistedFiles(t *testing.T) {
	for _, kind := range []string{"hash", "link", "unexpected", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			m, d, root := inspectionFixture(t)
			if _, err := m.Inspect(context.Background(), d); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "hash":
				p := filepath.Join(root, "f050")
				data, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				data[0] ^= 1
				if err = os.WriteFile(p, data, 0700); err != nil {
					t.Fatal(err)
				}
			case "link":
				if err := os.Remove(filepath.Join(root, "alias")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../outside", filepath.Join(root, "alias")); err != nil {
					t.Fatal(err)
				}
			case "unexpected":
				if err := os.WriteFile(filepath.Join(root, "unlisted"), []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				p := filepath.Join(root, "component.json")
				data, _ := os.ReadFile(p)
				var meta Metadata
				if err := json.Unmarshal(data, &meta); err != nil {
					t.Fatal(err)
				}
				meta.Files = append(meta.Files, meta.Files[0])
				data, _ = json.Marshal(meta)
				if err := os.WriteFile(p, data, 0600); err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(data)
				d.ManifestSHA256 = hex.EncodeToString(sum[:])
				m.approved[d.ID] = d
			}
			if _, err := m.Inspect(context.Background(), d); err == nil {
				t.Fatal("integrity violation accepted", kind)
			}
		})
	}
}

// Trigger cancellation at a deterministic point during a long inspection rather
// than depending on a timer or the machine's disk speed.
type inspectionCancelContext struct {
	context.Context
	cancel context.CancelFunc
	checks atomic.Int64
}

func (c *inspectionCancelContext) Err() error {
	if c.checks.Add(1) == 20 {
		c.cancel()
	}
	return c.Context.Err()
}
func TestInspectionObservesCancellationDuringAssetWork(t *testing.T) {
	m, d, _ := inspectionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controlled := &inspectionCancelContext{Context: ctx, cancel: cancel}
	_, err := m.Inspect(controlled, d)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("inspection did not observe cancellation during its asset work", err, controlled.checks.Load())
	}
}
