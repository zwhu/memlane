package credentials

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
)

func TestCredentialTargetsRejectUnsafeAddressesAndReferences(t *testing.T) {
	valid := Reference{ID: uuid.NewString(), Revision: 1}
	target := Target{Provider: "synthetic supplier", Endpoint: "https://supplier.invalid/v1", Protocol: "openai-responses"}
	for _, endpoint := range []string{"http://supplier.invalid/v1", "https://user:password@supplier.invalid/v1", "https://supplier.invalid/v1?secret=value", "https://supplier.invalid/v1#secret", "file:///tmp/provider"} {
		changed := target
		changed.Endpoint = endpoint
		if validate(valid, changed) == nil {
			t.Fatal("unsafe address accepted")
		}
	}
	if validate(Reference{ID: "../foreign", Revision: 1}, target) == nil || validate(Reference{ID: valid.ID}, target) == nil {
		t.Fatal("invalid version identity accepted")
	}
	if validate(valid, target) != nil {
		t.Fatal("valid supplier target rejected")
	}
}

func TestNativeCredentialVaultImmutableVersionsAndCleanup(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("MEMLANE_NATIVE_VAULT_PROBE") != "1" {
		t.Skip("explicit opt-in required: writes and deletes owned synthetic Keychain entries")
	}
	root := t.TempDir()
	v, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Provider: "synthetic-only", Endpoint: "https://supplier.invalid/v1", Protocol: "openai-responses"}
	secret, other := make([]byte, 32), make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	if _, err = rand.Read(other); err != nil {
		t.Fatal(err)
	}
	defer clear(secret)
	defer clear(other)
	first := Reference{ID: uuid.NewString(), Revision: 1}
	second := Reference{ID: first.ID, Revision: 2}
	// Persist only owned identities before the first system write, allowing
	// exact cleanup after a native crash. No secret or user configuration.
	marker, _ := json.Marshal(map[string]any{"synthetic_only": true, "root": root, "references": []Reference{first, second}})
	if err = os.WriteFile(filepath.Join(root, "synthetic-vault-probe.json"), marker, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("owned synthetic credential references:", filepath.Join(root, "synthetic-vault-probe.json"))
	owned := []Reference{}
	defer func() {
		for _, ref := range owned {
			if err := v.Delete(ref); err != nil {
				t.Errorf("owned synthetic credential cleanup failed: %v", err)
			}
		}
	}()
	if err = v.Put(first, target, secret); err != nil {
		t.Fatal(err)
	}
	owned = append(owned, first)
	if err = v.Put(first, target, other); err == nil {
		t.Fatal("existing immutable credential was overwritten")
	}
	read, err := v.Get(first, target)
	if err != nil || !bytes.Equal(read, secret) {
		clear(read)
		t.Fatalf("first version was not retained: %v", err)
	}
	clear(read)
	changed := target
	changed.Endpoint = "https://other.invalid/v1"
	if read, err = v.Get(first, changed); err == nil {
		clear(read)
		t.Fatal("credential reused after supplier address change")
	}
	if err = v.Put(second, target, other); err != nil {
		t.Fatal(err)
	}
	owned = append(owned, second)
	read, err = v.Get(second, target)
	if err != nil || !bytes.Equal(read, other) {
		clear(read)
		t.Fatalf("second version unavailable: %v", err)
	}
	clear(read)
	for len(owned) > 0 {
		ref := owned[len(owned)-1]
		if err = v.Delete(ref); err != nil {
			t.Fatal(err)
		}
		owned = owned[:len(owned)-1]
		if read, err = v.Get(ref, target); err == nil {
			clear(read)
			t.Fatal("deleted synthetic credential remained readable")
		}
	}
	t.Log("actual Keychain versions preserved; address change denied; both owned synthetic entries deleted")
}
