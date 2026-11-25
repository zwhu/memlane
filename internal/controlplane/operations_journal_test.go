package controlplane

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUIOperationConnectionAction(t *testing.T) {
	for kind, want := range map[string]string{
		"connection_prepare": "prepare", "connection_activate": "activate",
		"connection_repair_trae_hooks": "repair-trae-hooks", "connection_allow_capture": "allow_capture",
		"connection_defer_capture": "defer_capture", "connection_review": "review",
		"save": "", "next": "", "handoffs": "", "validations": "", "delete": "", "": "", "connection_other": "",
	} {
		if got := (UIOperation{Kind: kind}).ConnectionAction(); got != want {
			t.Errorf("ConnectionAction(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestAtomicUIOperationsJournal(t *testing.T) {
	gui, cli := NewUIOperationID(), NewCLIOperationID()
	if !ValidOperationID(gui) || !ValidOperationID(cli) || !strings.HasPrefix(gui, "ui-") || !strings.HasPrefix(cli, "ui-cli-") || gui == cli {
		t.Fatalf("unexpected operation IDs %q, %q", gui, cli)
	}
	for _, id := range []string{"../x", "cli-123", "ui-", "ui-a\n", "ui-" + strings.Repeat("a", 62)} {
		if ValidOperationID(id) {
			t.Errorf("accepted malformed ID %q", id)
		}
	}
	parent := t.TempDir()
	root := OperationsDir(filepath.Join(parent, "generation.json"), filepath.Join(parent, "management.token"))
	if root != filepath.Join(parent, "generation.json.ui-operations") ||
		OperationsDir("", filepath.Join(parent, "management.token")) != filepath.Join(parent, "generation-ui-operations") ||
		OperationsDir("", "") != "" {
		t.Fatal("unexpected operation journal location")
	}
	loaded, err := LoadUIOperations(root)
	if err != nil || loaded != nil {
		t.Fatalf("missing journal: %v, %v", loaded, err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("read-only load created files: %v, %v", entries, err)
	}

	guiOp := UIOperation{Operation: gui, Kind: "save", Profile: "shared", Revision: 3}
	cliOp := UIOperation{Operation: cli, Kind: "connection_repair_trae_hooks", Profile: "shared"}
	if !guiOp.Valid() || guiOp.IsConnection() || !cliOp.Valid() || !cliOp.IsConnection() {
		t.Fatal("operation validation or classification failed")
	}
	if got, err := RecordUIOperation(root, guiOp); err != nil || !reflect.DeepEqual(got, []UIOperation{guiOp}) {
		t.Fatalf("record GUI operation: %v, %v", got, err)
	}
	for path, mode := range map[string]os.FileMode{
		root: 0o700, root + ".lock": 0o600, filepath.Join(root, gui+".json"): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Errorf("%s mode = %o, want %o", filepath.Base(path), info.Mode().Perm(), mode)
		}
	}

	tmp := filepath.Join(root, ".tmp-leftover")
	if err := os.WriteFile(tmp, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	duplicateProfile := UIOperation{Operation: NewCLIOperationID(), Kind: "delete", Profile: guiOp.Profile}
	if _, err := RecordUIOperation(root, duplicateProfile); err == nil {
		t.Fatal("accepted duplicate generation profile")
	}
	duplicateID := cliOp
	duplicateID.Operation = gui
	if _, err := RecordUIOperation(root, duplicateID); err == nil {
		t.Fatal("accepted duplicate operation ID")
	}
	if got, err := RecordUIOperation(root, cliOp); err != nil || !reflect.DeepEqual(got, []UIOperation{guiOp, cliOp}) {
		t.Fatalf("record CLI operation: %v, %v", got, err)
	}
	loaded, err = LoadUIOperations(root)
	if err != nil || len(loaded) != 2 {
		t.Fatalf("load two operations: %v, %v", loaded, err)
	}
	if err := FinishUIOperation(root, gui); err != nil {
		t.Fatal(err)
	}
	if err := FinishUIOperation(root, gui); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadUIOperations(root)
	if err != nil || !reflect.DeepEqual(loaded, []UIOperation{cliOp}) {
		t.Fatalf("load remaining operation: %v, %v", loaded, err)
	}
	if err := FinishUIOperation(root, cli); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(tmp) {
		t.Fatalf("journal should only contain leftover temp: %v, %v", entries, err)
	}

	alias := filepath.Join(parent, "journal-link")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUIOperations(alias + "/"); err == nil {
		t.Error("loaded directory symlink")
	}
	if _, err := RecordUIOperation(alias, guiOp); err == nil {
		t.Error("recorded through directory symlink")
	}
	if err := FinishUIOperation(alias, gui); err == nil {
		t.Error("finished through directory symlink")
	}
	if err := os.Symlink(tmp, filepath.Join(root, gui+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUIOperations(root); err == nil {
		t.Error("loaded operation symlink")
	}
}

func TestJournalRejectsInvalidMetadataAndBusyLock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "journal")
	op := UIOperation{Operation: "ui-test", Kind: "next", Profile: "model"}
	if _, err := RecordUIOperation(root, op); err != nil {
		t.Fatal(err)
	}
	lock, err := lockOperations(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if lock != nil {
			unlockOperations(lock)
		}
	})
	if err := FinishUIOperation(root, op.Operation); err == nil {
		t.Fatal("finished while another writer held the lock")
	}
	unlockOperations(lock)
	lock = nil
	oversize := UIOperation{Operation: "ui-oversize", Kind: "save", Profile: strings.Repeat("x", maxOperationBytes)}
	if _, err := RecordUIOperation(root, oversize); err == nil {
		t.Fatal("recorded oversized metadata")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("failed write left journal files: %v, %v", entries, err)
	}
	path := filepath.Join(root, op.Operation+".json")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUIOperations(root); err == nil {
		t.Fatal("accepted non-private metadata")
	}
	if err := os.Remove(root + ".lock"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, root+".lock"); err != nil {
		t.Fatal(err)
	}
	if err := FinishUIOperation(root, op.Operation); err == nil {
		t.Fatal("accepted symlink lock")
	}
}
