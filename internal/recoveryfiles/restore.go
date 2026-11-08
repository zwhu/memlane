package recoveryfiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

const restorePointer = ".restore-pending.json"

type priorFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Exists bool   `json:"exists"`
}
type restoreJournal struct {
	Receipt records.RestoreReceipt `json:"receipt"`
	Prior   []priorFile            `json:"prior"`
}

type RestoreManager struct {
	Root string
	// Explicit filesystem fault fixtures; never normal App acceptance.
	checkpoint func(string) error
	syncDir    func(*os.Root) error
}

type PrepareSnapshot func(context.Context, []byte, string) ([]byte, records.RestoreStats, error)

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func restoreBase(root *os.Root, create bool) (*os.Root, error) {
	backups, err := openDirectory(root, "backups", create)
	if err != nil {
		return nil, err
	}
	defer backups.Close()
	return openDirectory(backups, "restores", create)
}
func optionalFile(root *os.Root, name string) ([]byte, bool, error) {
	data, err := readRegular(root, name, MaxSnapshotBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}
func readJSON(root *os.Root, name string, value any) error {
	data, err := readRegular(root, name, 64*1024)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
func writeJSON(root *os.Root, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeFile(root, name, data)
}
func (m RestoreManager) point(stage string) error {
	if m.checkpoint != nil {
		return m.checkpoint(stage)
	}
	return nil
}

func (m RestoreManager) syncChain(root, base, dir *os.Root) error {
	backups, err := openDirectory(root, "backups", false)
	if err != nil {
		return err
	}
	defer backups.Close()
	current, err := openDirectory(backups, "restores", false)
	if err != nil {
		return err
	}
	defer current.Close()
	want, err := base.Stat(".")
	if err != nil {
		return err
	}
	have, err := current.Stat(".")
	if err != nil || !os.SameFile(want, have) {
		return errors.New("restore catalogue identity changed")
	}
	persist := m.syncDir
	if persist == nil {
		persist = syncDirectory
	}
	for _, d := range []*os.Root{dir, base, backups, root} {
		if d != nil {
			if err = persist(d); err != nil {
				return err
			}
		}
	}
	return nil
}
func lockFile(root *os.Root, name string) (*os.File, error) {
	if info, err := root.Lstat(name); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, errors.New("restore ownership file is not ordinary")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := root.OpenFile(name, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || st.Nlink != 1 || info.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, errors.New("restore ownership file is not private and exclusive")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("restore owner still active: %w", err)
	}
	return f, nil
}

// Restore requires an explicit persisted stop, and takes both ownership locks.
// No production SQLite connection is opened; arbitrary paths are not inputs.
func (m RestoreManager) Restore(ctx context.Context, backup, operation string, prepare PrepareSnapshot) (records.RestoreReceipt, error) {
	var out records.RestoreReceipt
	if !filepath.IsAbs(m.Root) || !canonical(backup) || !canonical(operation) || prepare == nil {
		return out, errors.New("absolute root, canonical restore/backup UUID and preparation required")
	}
	root, err := os.OpenRoot(m.Root)
	if err != nil {
		return out, err
	}
	defer root.Close()
	service, err := lockFile(root, ".service.lock")
	if err != nil {
		return out, err
	}
	defer service.Close()
	database, err := lockFile(root, "memlane.sqlite.owner.lock")
	if err != nil {
		return out, err
	}
	defer database.Close()
	var state struct {
		Stopped bool `json:"stopped"`
	}
	if err = readJSON(root, "service-state.json", &state); err != nil || !state.Stopped {
		return out, errors.New("restore requires an explicitly stopped service")
	}
	var pending struct {
		ID string `json:"id"`
	}
	if e := readJSON(root, restorePointer, &pending); e == nil && pending.ID != operation {
		return out, errors.New("pending restore requires its original operation ID")
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return out, e
	}
	if _, err = m.recover(ctx, root); err != nil {
		return out, err
	}
	source, err := (Manager{Root: m.Root}).Verify(ctx, backup)
	if err != nil {
		return out, err
	}
	base, err := restoreBase(root, true)
	if err != nil {
		return out, err
	}
	defer base.Close()
	if dir, e := openDirectory(base, operation, false); e == nil {
		defer dir.Close()
		if e = readJSON(dir, "receipt.json", &out); errors.Is(e, os.ErrNotExist) {
			var j restoreJournal
			if e = readJSON(dir, "journal.json", &j); e != nil {
				return out, e
			}
			out = j.Receipt
			if out.ID != operation || out.Backup != backup || out.Vault != source.Receipt.Vault || out.SourceSHA != source.Receipt.SHA256 {
				return out, errors.New("restore operation conflicts with staged journal")
			}
			if e = ctx.Err(); e != nil {
				return out, e
			}
			if e = m.syncChain(root, base, dir); e != nil {
				return out, e
			}
			if e = publishPointer(ctx, root, operation); e != nil {
				return out, e
			}
			_, e = m.recover(context.WithoutCancel(ctx), root)
			return out, e
		} else if e != nil {
			return out, e
		}
		if out.ID != operation || out.Backup != backup || out.Vault != source.Receipt.Vault || out.SourceSHA != source.Receipt.SHA256 || out.State != "restored_stopped" {
			return out, errors.New("restore operation conflicts with previous receipt")
		}
		return out, m.syncChain(root, base, dir)
	} else if !errors.Is(e, os.ErrNotExist) {
		return out, e
	}
	prepared, stats, err := prepare(ctx, source.Data, operation)
	if err != nil {
		return out, err
	}
	if len(prepared) < 100 || int64(len(prepared)) > MaxSnapshotBytes || string(prepared[:16]) != "SQLite format 3\x00" {
		return out, errors.New("prepared restore snapshot invalid")
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	stage := ".staging-" + uuid.NewString()
	dir, err := openDirectory(base, stage, true)
	if err != nil {
		return out, err
	}
	defer dir.Close()
	// Unpublished interrupted archives stay private. They are never deleted as
	// unknown files and never represented as successful restores.
	out = records.RestoreReceipt{Format: 1, ID: operation, Backup: backup, Vault: source.Receipt.Vault, SourceSHA: source.Receipt.SHA256, SHA256: digest(prepared), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Stats: stats, State: "restored_stopped"}
	journal := restoreJournal{Receipt: out}
	for _, name := range []string{"memlane.sqlite", "memlane.sqlite-wal", "memlane.sqlite-shm"} {
		data, exists, e := optionalFile(root, name)
		if e != nil {
			return out, e
		}
		prior := priorFile{Name: name, Exists: exists}
		if exists {
			prior.SHA256 = digest(data)
			if e = writeFile(dir, "previous-"+name, data); e != nil {
				return out, e
			}
		}
		journal.Prior = append(journal.Prior, prior)
	}
	if err = writeFile(dir, "prepared.sqlite", prepared); err != nil {
		return out, err
	}
	if err = writeJSON(dir, "journal.json", journal); err != nil {
		return out, err
	}
	if err = syncDirectory(dir); err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if err = base.Rename(stage, operation); err != nil {
		return out, err
	}
	if err = m.syncChain(root, base, dir); err != nil {
		return out, err
	}
	if err = m.point("archive_published"); err != nil {
		return out, err
	}
	// Publish the durable commit intent before touching the current database.
	if err = publishPointer(ctx, root, operation); err != nil {
		return out, err
	}
	if err = m.point("journal_persisted"); err != nil {
		return out, err
	}
	// The journal is now a commit intent. Cancellation cannot abandon a half
	// switched SQLite/WAL pair; the bounded local finish/recovery completes it.
	_, err = m.recover(context.WithoutCancel(ctx), root)
	return out, err
}

func publishPointer(ctx context.Context, root *os.Root, operation string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pointer := ".restore-pointer-" + uuid.NewString()
	if err := writeJSON(root, pointer, map[string]string{"id": operation}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename(pointer, restorePointer); err != nil {
		return err
	}
	return syncDirectory(root)
}

func persistRestoreStop(root *os.Root) error {
	var state records.ServiceState
	if err := readJSON(root, "service-state.json", &state); err != nil {
		return err
	}
	if state.StopGeneration < 0 || state.StopGeneration >= 9223372036854775807 {
		return errors.New("restore service stop generation invalid")
	}
	if !state.Stopped {
		state.StopGeneration++
	}
	if state.StopGeneration == 0 {
		state.StopGeneration = 1
	}
	state.Stopped = true
	state.LastStoppedMS = time.Now().UnixMilli()
	state.Restarts = nil
	name := ".restore-stop-" + uuid.NewString()
	if err := writeJSON(root, name, state); err != nil {
		return err
	}
	if err := root.Rename(name, "service-state.json"); err != nil {
		return err
	}
	return syncDirectory(root)
}

// RecoverBeforeOpen runs only while the caller holds the DB owner lock. Store
// invokes it before sql.Open, so a restored main file never reads old WAL.
func (m RestoreManager) RecoverBeforeOpen(ctx context.Context) (bool, error) {
	if !filepath.IsAbs(m.Root) {
		return false, errors.New("absolute restore root required")
	}
	root, err := os.OpenRoot(m.Root)
	if err != nil {
		return false, err
	}
	defer root.Close()
	return m.recover(ctx, root)
}

// List distinguishes pending journals from completed receipts. Corrupt records
// fail visibly instead of making an incomplete catalogue appear complete.
func (m RestoreManager) List(ctx context.Context) ([]records.RestoreReceipt, error) {
	out := []records.RestoreReceipt{}
	if !filepath.IsAbs(m.Root) {
		return nil, errors.New("absolute restore root required")
	}
	root, err := os.OpenRoot(m.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	base, err := restoreBase(root, false)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer base.Close()
	vid, err := vault(root, false)
	if err != nil {
		return nil, err
	}
	var pending struct {
		ID string `json:"id"`
	}
	if e := readJSON(root, restorePointer, &pending); e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	f, err := base.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(1001)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(names) > 1000 {
		return nil, errors.New("restore catalogue exceeds 1000 directories")
	}
	for _, name := range names {
		if !canonical(name) {
			continue
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		dir, e := openDirectory(base, name, false)
		if e != nil {
			return nil, e
		}
		var r records.RestoreReceipt
		e = readJSON(dir, "receipt.json", &r)
		if errors.Is(e, os.ErrNotExist) {
			var j restoreJournal
			e = readJSON(dir, "journal.json", &j)
			r = j.Receipt
			if e == nil {
				r.State = "recovery_required"
			}
		} else if e == nil && pending.ID == name {
			r.State = "recovery_required"
		}
		dir.Close()
		if e != nil || r.ID != name || r.Vault != vid || r.Format != 1 || (r.State != "restored_stopped" && r.State != "recovery_required") {
			return nil, errors.New("restore catalogue contains an incomplete or invalid record; files preserved")
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

func (m RestoreManager) recover(ctx context.Context, root *os.Root) (bool, error) {
	var pointer struct {
		ID string `json:"id"`
	}
	if err := readJSON(root, restorePointer, &pointer); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if !canonical(pointer.ID) {
		return false, errors.New("invalid pending restore identity")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	base, err := restoreBase(root, false)
	if err != nil {
		return false, err
	}
	defer base.Close()
	dir, err := openDirectory(base, pointer.ID, false)
	if err != nil {
		return false, err
	}
	defer dir.Close()
	var j restoreJournal
	if err = readJSON(dir, "journal.json", &j); err != nil {
		return false, err
	}
	vid, err := vault(root, false)
	if err != nil {
		return false, err
	}
	if j.Receipt.Format != 1 || j.Receipt.ID != pointer.ID || j.Receipt.Vault != vid || len(j.Prior) != 3 || !canonical(j.Receipt.Backup) {
		return false, errors.New("pending restore journal invalid")
	}
	prepared, err := readRegular(dir, "prepared.sqlite", MaxSnapshotBytes)
	if err != nil || digest(prepared) != j.Receipt.SHA256 {
		return false, errors.New("prepared restore checksum invalid")
	}
	for i, name := range []string{"memlane.sqlite", "memlane.sqlite-wal", "memlane.sqlite-shm"} {
		p := j.Prior[i]
		if p.Name != name {
			return false, errors.New("restore journal file identity invalid")
		}
		if p.Exists {
			data, e := readRegular(dir, "previous-"+name, MaxSnapshotBytes)
			if e != nil || digest(data) != p.SHA256 {
				return false, errors.New("pre-restore archive checksum invalid")
			}
		}
	}
	main, exists, err := optionalFile(root, "memlane.sqlite")
	if err != nil {
		return false, err
	}
	isPrepared := exists && digest(main) == j.Receipt.SHA256
	if !isPrepared && (exists != j.Prior[0].Exists || exists && digest(main) != j.Prior[0].SHA256) {
		return false, errors.New("current database changed outside pending restore; files preserved")
	}
	// Before switching, sidecars must match the archived bytes exactly. After
	// switching they may already have been removed by a previous finish.
	for _, p := range j.Prior[1:] {
		data, present, e := optionalFile(root, p.Name)
		if e != nil {
			return false, e
		}
		if present && (!p.Exists || digest(data) != p.SHA256) || !isPrepared && present != p.Exists {
			return false, errors.New("current SQLite sidecar changed outside restore; files preserved")
		}
	}
	if err = m.syncChain(root, base, dir); err != nil {
		return false, err
	}
	if !isPrepared {
		install := ".restore-install-" + uuid.NewString() + ".sqlite"
		if err = writeFile(root, install, prepared); err != nil {
			return false, err
		}
		if err = root.Rename(install, "memlane.sqlite"); err != nil {
			return false, err
		}
	}
	if err = m.point("main_replaced"); err != nil {
		return false, err
	}
	for _, p := range j.Prior[1:] {
		if err = root.Remove(p.Name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	if err = syncDirectory(root); err != nil {
		return false, err
	}
	if err = m.point("sidecars_removed"); err != nil {
		return false, err
	}
	var receipt records.RestoreReceipt
	if err = readJSON(dir, "receipt.json", &receipt); errors.Is(err, os.ErrNotExist) {
		if err = writeJSON(dir, "receipt.json", j.Receipt); err != nil {
			return false, err
		}
	} else if err != nil || receipt != j.Receipt {
		return false, errors.New("restore receipt conflicts with journal")
	}
	if err = m.syncChain(root, base, dir); err != nil {
		return false, err
	}
	if err = m.point("receipt_persisted"); err != nil {
		return false, err
	}
	// Stop is part of the durable recovery transaction. Never clear the intent
	// before this state is synced: a previous explicit Resume may have cleared
	// stopped before a startup recovery, and no new owner may run afterward.
	if err = persistRestoreStop(root); err != nil {
		return false, err
	}
	if err = m.point("stop_state_persisted"); err != nil {
		return false, err
	}
	if err = root.Remove(restorePointer); err != nil {
		return false, err
	}
	if err = syncDirectory(root); err != nil {
		return false, err
	}
	return true, nil
}
