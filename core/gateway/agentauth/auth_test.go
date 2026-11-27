package agentauth

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zwhu/memlane/core/domain"
)

func TestAuthenticatorAndSecretCache(t *testing.T) {
	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "auth.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	dir := t.TempDir()
	keyFile := filepath.Join(dir, "a1.key")
	ctrlFile := filepath.Join(dir, "a2.ctrl")
	retiredFile := filepath.Join(dir, "retired.key")
	publicFile := filepath.Join(dir, "public.key")
	symlinkFile := filepath.Join(dir, "symlink.key")

	for path, content := range map[string]string{
		keyFile:     "key-secret-1\n",
		ctrlFile:    "ctrl-secret-2\n",
		retiredFile: "retired-secret\n",
		publicFile:  "public-secret\n",
	} {
		mode := os.FileMode(0o600)
		if path == publicFile {
			mode = 0o644
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(keyFile, symlinkFile); err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	for _, ag := range []domain.Agent{
		{ID: "a1", Name: "A1", KeyFile: keyFile},
		{ID: "a2", Name: "A2", ControlTokenFile: ctrlFile},
		{ID: "a-mem", Name: "AMem"},
		{ID: "a-retired", Name: "ARetired", KeyFile: retiredFile, RetiredAt: "2026-11-27T00:00:00Z"},
		{ID: "a-public", Name: "APublic", KeyFile: publicFile},
		{ID: "a-link", Name: "ALink", KeyFile: symlinkFile},
		{ID: "dup-1", Name: "Dup1"},
		{ID: "dup-2", Name: "Dup2"},
	} {
		if err := store.UpsertAgent(ctx, ag); err != nil {
			t.Fatal(err)
		}
	}

	auth := New(Config{
		Store: store,
		AgentTokens: map[string]string{
			"a-mem": "mem-secret",
			"dup-1": "shared-secret",
			"dup-2": "shared-secret",
		},
	})

	// 1. KeyFile, ControlTokenFile, and in-memory AgentTokens all authenticate.
	for _, tc := range []struct {
		token  string
		wantID string
	}{
		{"key-secret-1", "a1"},
		{"ctrl-secret-2", "a2"},
		{"mem-secret", "a-mem"},
	} {
		res, err := auth.Authenticate(ctx, tc.token)
		if err != nil || res.Status != StatusOK || res.Agent.ID != tc.wantID {
			t.Fatalf("Authenticate(%q) = (%+v, %v), want StatusOK id=%s", tc.token, res, err, tc.wantID)
		}
	}

	// 2. Retired agent -> StatusRetired.
	res, err := auth.Authenticate(ctx, "retired-secret")
	if err != nil || res.Status != StatusRetired || res.Code != "agent_retired" {
		t.Fatalf("retired auth = (%+v, %v), want StatusRetired", res, err)
	}

	// 3. Ambiguous token matching 2 active agents -> StatusAmbiguous.
	res, err = auth.Authenticate(ctx, "shared-secret")
	if err != nil || res.Status != StatusAmbiguous || res.Code != "agent_ambiguous" {
		t.Fatalf("ambiguous auth = (%+v, %v), want StatusAmbiguous", res, err)
	}

	// 4. Unregistered, 0644 public file, symlink -> StatusNotRegistered.
	for _, token := range []string{"unknown", "public-secret"} {
		res, err := auth.Authenticate(ctx, token)
		if err != nil || res.Status != StatusNotRegistered {
			t.Fatalf("rejected token %q = (%+v, %v), want StatusNotRegistered", token, res, err)
		}
	}
	if _, err := ReadPrivateSecret(symlinkFile); err == nil {
		t.Fatal("ReadPrivateSecret followed symlink")
	}

	// 5. Stat cache picks up rotated secret file content.
	if err := os.WriteFile(keyFile, []byte("rotated-secret-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = auth.Authenticate(ctx, "rotated-secret-1")
	if err != nil || res.Status != StatusOK || res.Agent.ID != "a1" {
		t.Fatalf("rotated auth = (%+v, %v), want StatusOK a1", res, err)
	}
}
