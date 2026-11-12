package official

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func upgradeSnapshotFixture(t *testing.T) (string, string, string) {
	t.Helper()
	base := t.TempDir()
	// testing.TempDir's numbered child uses 0777 subject to umask; the vault
	// parent must explicitly satisfy the application's private-directory gate.
	if err := os.Chmod(base, 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "client")
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"data/.service.lock", "data/memlane.sqlite.owner.lock", "official/cognee/.service-owner"} {
		write(path, "")
	}
	write("official/cognee-config.json", `{"root":"synthetic","environment":{"LLM_API_KEY":"synthetic-retained-credential"}}`)
	write("official/cognee/system/graph.lbug", "synthetic graph bytes; not a real SDK graph")
	write("official/cognee/cache/session", "synthetic QA and traces")
	write("official/cognee/data/document", "synthetic official knowledge")
	write("agent-adapters/installation.json", `{"generation":1}`)
	write("runtime/components/model/weights", "retained model must not be copied")
	db, err := sql.Open("sqlite", filepath.Join(root, "data/memlane.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE items(value TEXT); INSERT INTO items VALUES('synthetic knowledge');`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(base, "codex-config.toml")
	if err = os.WriteFile(agent, []byte("model = 'synthetic-retained-model'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(base, "snapshot"), agent
}

func TestUpgradeSnapshotStoppedLayersAndPayloadIntegrity(t *testing.T) {
	root, destination, agent := upgradeSnapshotFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	receipt, err := CreateUpgradeSnapshot(ctx, UpgradeSnapshotSpec{ClientRoot: root, Destination: destination, AgentFiles: map[string]string{"codex-config.toml": agent}, RetainedResources: []string{filepath.Join(root, "runtime/components/model")}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Verification != "byte_consistent_stopped_snapshot" {
		t.Fatal("snapshot overstated its evidence scope")
	}
	if _, err = VerifyUpgradeSnapshot(ctx, destination); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"client/official/cognee/system/graph.lbug", "client/official/cognee/cache/session", "client/official/cognee/data/document", "client/official/cognee-config.json", "agents/codex-config.toml"} {
		info, err := os.Stat(filepath.Join(destination, "payload", name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("missing/private snapshot layer", name, err)
		}
	}
	if _, err = os.Stat(filepath.Join(destination, "payload/client/runtime/components/model/weights")); !os.IsNotExist(err) {
		t.Fatal("snapshot duplicated an engine/model")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(destination, "payload/client/data/memlane.sqlite")+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	var value string
	err = db.QueryRow(`SELECT value FROM items`).Scan(&value)
	db.Close()
	if err != nil || value != "synthetic knowledge" {
		t.Fatal("copied client database unreadable", err)
	}
	if _, err = CreateUpgradeSnapshot(ctx, UpgradeSnapshotSpec{ClientRoot: root, Destination: destination}); err == nil {
		t.Fatal("existing snapshot overwritten")
	}
	if err = os.WriteFile(filepath.Join(destination, "payload/client/official/cognee/data/document"), []byte("altered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyUpgradeSnapshot(ctx, destination); err == nil {
		t.Fatal("changed official content passed verification")
	}
}

func TestUpgradeSnapshotRequiresExclusiveWritersAndNoPathEscape(t *testing.T) {
	root, destination, _ := upgradeSnapshotFixture(t)
	file, err := os.OpenFile(filepath.Join(root, "data/.service.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	_, err = CreateUpgradeSnapshot(context.Background(), UpgradeSnapshotSpec{ClientRoot: root, Destination: destination})
	if err == nil || err.Error() != "upgrade_writers_active" {
		t.Fatal("live writer was not rejected", err)
	}
	if _, err = os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("live-writer refusal created backup payload")
	}
	file.Close()
	if _, err = CreateUpgradeSnapshot(context.Background(), UpgradeSnapshotSpec{ClientRoot: root, Destination: filepath.Join(root, "nested-backup")}); err == nil {
		t.Fatal("recursive snapshot accepted")
	}
	outside := t.TempDir()
	if err = os.Rename(filepath.Join(root, "official"), filepath.Join(outside, "official")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(outside, "official"), filepath.Join(root, "official")); err != nil {
		t.Fatal(err)
	}
	if _, err = CreateUpgradeSnapshot(context.Background(), UpgradeSnapshotSpec{ClientRoot: root, Destination: destination}); err == nil {
		t.Fatal("source ancestor symlink escaped snapshot boundary")
	}
}

func TestUpgradeOpenFileObservationsNeverTreatUnknownAsClosed(t *testing.T) {
	own := os.Getpid()
	if err := upgradeOpenFileOutput("p"+strconv.Itoa(own)+"\nf3\nn/private/synthetic/.service-owner\n", own); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "p" + strconv.Itoa(own) + "\n", "p" + strconv.Itoa(own+1) + "\nf3\nn/private/synthetic/graph.lbug\n", "punknown\n", "n/file-without-owner\n", "unexpected observation\n"} {
		if err := upgradeOpenFileOutput(raw, own); err == nil {
			t.Fatal("unknown/external open-file observation accepted")
		}
	}
}
