// Package recoveryfiles owns bounded private backup files, without a database driver.
package recoveryfiles

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

const MaxSnapshotBytes int64 = 64 * 1024 * 1024

type Manager struct {
	Root    string
	syncDir func(*os.Root) error // explicit IO fault fixtures; production uses fsync
}
type Snapshot struct {
	Receipt records.BackupReceipt
	Data    []byte `json:"-"`
}

func canonical(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}
func openDirectory(parent *os.Root, name string, create bool) (*os.Root, error) {
	if create {
		if err := parent.Mkdir(name, 0700); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return nil, err
			}
		} else if err = syncDirectory(parent); err != nil {
			return nil, err
		}
	}
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 || before.Mode().Perm()&0077 != 0 {
		return nil, errors.New("backup directory is not an ordinary directory")
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		root.Close()
		return nil, errors.New("backup directory identity changed")
	}
	return root, nil
}
func readRegular(root *os.Root, name string, max int64) ([]byte, error) {
	return readFile(root, name, max, false)
}
func readFile(root *os.Root, name string, max int64, persist bool) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	stat, ok := before.Sys().(*syscall.Stat_t)
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Mode().Perm()&0077 != 0 || !ok || stat.Nlink != 1 || before.Size() > max {
		return nil, errors.New("backup file is not a bounded private ordinary file")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("backup file identity changed")
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errors.New("backup file exceeds limit")
	}
	if persist {
		if err = f.Sync(); err != nil {
			return nil, err
		}
	}
	return data, nil
}
func writeFile(root *os.Root, name string, data []byte) error {
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func syncDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (m Manager) syncCatalogue(root, base *os.Root) error {
	backups, err := openDirectory(root, "backups", false)
	if err != nil {
		return err
	}
	defer backups.Close()
	current, err := openDirectory(backups, "manual", false)
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
		return errors.New("backup catalogue identity changed")
	}
	persist := m.syncDir
	if persist == nil {
		persist = syncDirectory
	}
	for _, directory := range []*os.Root{base, backups, root} {
		if err = persist(directory); err != nil {
			return err
		}
	}
	return nil
}
func vault(root *os.Root, create bool) (string, error) {
	data, err := readFile(root, ".vault-id", 64, create)
	if errors.Is(err, os.ErrNotExist) {
		base, e := manual(root, false)
		if e == nil {
			dir, e := base.Open(".")
			if e != nil {
				base.Close()
				return "", e
			}
			names, e := dir.Readdirnames(1)
			dir.Close()
			base.Close()
			if e != nil && e != io.EOF {
				return "", e
			}
			if len(names) > 0 {
				return "", errors.New("vault identity missing; existing backup files remain")
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return "", e
		}
	}
	if errors.Is(err, os.ErrNotExist) && create {
		id := uuid.NewString()
		err = writeFile(root, ".vault-id", []byte(id+"\n"))
		if err == nil {
			if err = syncDirectory(root); err != nil {
				return "", err
			}
			return id, nil
		}
		if errors.Is(err, os.ErrExist) {
			data, err = readRegular(root, ".vault-id", 64)
		}
	}
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	if !canonical(id) {
		return "", errors.New("invalid vault identity")
	}
	return id, nil
}
func (m Manager) open() (*os.Root, error) {
	if !filepath.IsAbs(m.Root) {
		return nil, errors.New("absolute backup root required")
	}
	return os.OpenRoot(m.Root)
}
func manual(root *os.Root, create bool) (*os.Root, error) {
	backups, err := openDirectory(root, "backups", create)
	if err != nil {
		return nil, err
	}
	defer backups.Close()
	return openDirectory(backups, "manual", create)
}
func metadata(dir *os.Root, id, vaultID string) (records.BackupReceipt, error) {
	var r records.BackupReceipt
	data, err := readRegular(dir, "receipt.json", 64*1024)
	if err != nil {
		return r, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&r); err != nil {
		return r, err
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return r, errors.New("invalid backup receipt suffix")
	}
	sha, err := hex.DecodeString(r.SHA256)
	if r.Format != 1 || r.ID != id || r.Vault != vaultID || r.Schema < 0 || r.Bytes < 100 || r.Bytes > MaxSnapshotBytes || err != nil || len(sha) != 32 {
		return r, errors.New("backup receipt does not match this vault")
	}
	if _, err = time.Parse(time.RFC3339Nano, r.CreatedAt); err != nil {
		return r, err
	}
	info, err := dir.Lstat("snapshot.sqlite")
	if err != nil {
		return r, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !ok || stat.Nlink != 1 || info.Size() != r.Bytes {
		return r, errors.New("backup snapshot identity or size invalid")
	}
	r.Verification = "unverified"
	r.Error = ""
	return r, nil
}
func verified(ctx context.Context, base *os.Root, id, vaultID string) (Snapshot, error) {
	var result Snapshot
	if !canonical(id) {
		return result, errors.New("canonical backup UUID required")
	}
	dir, err := openDirectory(base, id, false)
	if err != nil {
		return result, err
	}
	defer dir.Close()
	r, err := metadata(dir, id, vaultID)
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	data, err := readRegular(dir, "snapshot.sqlite", MaxSnapshotBytes)
	if err != nil {
		return result, err
	}
	sha := sha256.Sum256(data)
	if int64(len(data)) != r.Bytes || hex.EncodeToString(sha[:]) != r.SHA256 || string(data[:16]) != "SQLite format 3\x00" || int(binary.BigEndian.Uint32(data[60:64])) != r.Schema {
		return result, errors.New("backup snapshot checksum or schema mismatch")
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	r.Verification = "verified"
	return Snapshot{Receipt: r, Data: data}, nil
}
func (m Manager) Verify(ctx context.Context, id string) (Snapshot, error) {
	root, err := m.open()
	if err != nil {
		return Snapshot{}, err
	}
	defer root.Close()
	vid, err := vault(root, false)
	if err != nil {
		return Snapshot{}, err
	}
	base, err := manual(root, false)
	if err != nil {
		return Snapshot{}, err
	}
	defer base.Close()
	return verified(ctx, base, id, vid)
}
func (m Manager) List(ctx context.Context) ([]records.BackupReceipt, error) {
	out := []records.BackupReceipt{}
	root, err := m.open()
	if err != nil {
		return out, err
	}
	defer root.Close()
	vid, err := vault(root, false)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	base, err := manual(root, false)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer base.Close()
	dir, err := base.Open(".")
	if err != nil {
		return out, err
	}
	defer dir.Close()
	names, err := dir.Readdirnames(10001)
	if err != nil && err != io.EOF {
		return out, err
	}
	if len(names) > 10000 {
		return out, errors.New("backup catalogue exceeds 10000 entries")
	}
	for _, id := range names {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		if !canonical(id) {
			continue
		}
		d, err := openDirectory(base, id, false)
		if err != nil {
			out = append(out, records.BackupReceipt{ID: id, Verification: "invalid", Error: "backup directory invalid"})
			continue
		}
		r, err := metadata(d, id, vid)
		d.Close()
		if err != nil {
			out = append(out, records.BackupReceipt{ID: id, Verification: "invalid", Error: "backup receipt or snapshot invalid"})
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out, nil
}

// Create holds only a private filesystem owner lock; source uses a trusted database connection.
func (m Manager) Create(ctx context.Context, id string, source func(context.Context) (int, []byte, error)) (out records.BackupReceipt, retErr error) {
	if !canonical(id) {
		return out, errors.New("canonical backup UUID required")
	}
	if source == nil {
		return out, errors.New("backup source unavailable")
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	root, err := m.open()
	if err != nil {
		return out, err
	}
	defer root.Close()
	// O_EXCL creation never follows an existing link; reopening is independently checked.
	lock, err := root.OpenFile(".backup.lock", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if errors.Is(err, os.ErrExist) {
		info, e := root.Lstat(".backup.lock")
		if e != nil {
			return out, e
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || !ok || stat.Nlink != 1 {
			return out, errors.New("invalid backup owner lock")
		}
		lock, err = root.OpenFile(".backup.lock", os.O_RDWR, 0600)
		if err == nil {
			opened, e := lock.Stat()
			if e != nil || !os.SameFile(info, opened) {
				lock.Close()
				return out, errors.New("backup owner lock changed")
			}
		}
	}
	if err != nil {
		return out, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return out, errors.New("backup is already running")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	vid, err := vault(root, true)
	if err != nil {
		return out, err
	}
	base, err := manual(root, true)
	if err != nil {
		return out, err
	}
	defer base.Close()
	if _, err = base.Lstat(id); err == nil {
		snap, e := verified(ctx, base, id, vid)
		if e != nil {
			return snap.Receipt, e
		}
		if e = m.syncCatalogue(root, base); e != nil {
			return snap.Receipt, e
		}
		return snap.Receipt, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return out, err
	}
	schema, data, err := source(ctx)
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if schema < 0 || int64(len(data)) > MaxSnapshotBytes || len(data) < 100 || string(data[:16]) != "SQLite format 3\x00" || int(binary.BigEndian.Uint32(data[60:64])) != schema {
		return out, errors.New("invalid or oversized SQLite snapshot")
	}
	sha := sha256.Sum256(data)
	out = records.BackupReceipt{Format: 1, ID: id, Vault: vid, Schema: schema, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Bytes: int64(len(data)), SHA256: hex.EncodeToString(sha[:])}
	stageName := ".building-" + uuid.NewString()
	stage, err := openDirectory(base, stageName, true)
	if err != nil {
		return out, err
	}
	defer stage.Close()
	stageIdentity, err := stage.Stat(".")
	if err != nil {
		return out, err
	}
	published := false
	defer func() {
		if !published {
			entry, e := base.Lstat(stageName)
			if e == nil && os.SameFile(entry, stageIdentity) {
				if e = base.RemoveAll(stageName); e != nil {
					retErr = errors.Join(retErr, e)
				}
			} else if e != nil && !errors.Is(e, os.ErrNotExist) {
				retErr = errors.Join(retErr, e)
			} else if e == nil {
				retErr = errors.Join(retErr, errors.New("backup cleanup directory identity changed"))
			}
		}
	}()
	if err = writeFile(stage, "snapshot.sqlite", data); err != nil {
		return out, err
	}
	payload, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	if err = writeFile(stage, "receipt.json", payload); err != nil {
		return out, err
	}
	if err = syncDirectory(stage); err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	entry, err := base.Lstat(stageName)
	if err != nil || !os.SameFile(entry, stageIdentity) {
		return out, errors.New("backup staging identity changed")
	}
	if err = base.Rename(stageName, id); err != nil {
		return out, err
	}
	published = true
	if err = m.syncCatalogue(root, base); err != nil {
		return out, err
	}
	// Publication is committed. A late cancellation must report the actual
	// verified receipt instead of pretending the published snapshot was removed.
	snap, err := verified(context.WithoutCancel(ctx), base, id, vid)
	return snap.Receipt, err
}
