package official

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type UpgradeSnapshotSpec struct {
	ClientRoot, Destination string
	// Only individually selected host configuration files, not Agent homes or
	// chat history. Names are safe leaf names inside the private snapshot.
	AgentFiles        map[string]string
	RetainedResources []string
}

type UpgradeSnapshotFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type UpgradeSnapshotReceipt struct {
	Format            int                   `json:"format"`
	ClientRoot        string                `json:"client_root"`
	CreatedMS         int64                 `json:"created_ms"`
	Directories       []string              `json:"directories"`
	Files             []UpgradeSnapshotFile `json:"files"`
	AgentFiles        map[string]string     `json:"agent_files"`
	RetainedResources []string              `json:"retained_resources"`
	Verification      string                `json:"verification"`
}

var upgradePaths = []string{"data", "agent-adapters", "client-drafts", "project-preference-intents", "agent-paths.json", "agent-connection-intent.json", "browsing.json", "first-launch.json", "service.json", "runtime/feed-cache.json", "official/agents", "official/cognee", "official/cognee-config.json", "official/cognee-service-state.json", "official/runtime/feed-cache.json"}

func upgradeRelative(path string) bool {
	return path != "." && path != "" && !filepath.IsAbs(path) && filepath.Clean(path) == path && path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}

func upgradeConfinedPath(root, relative string) error {
	path := root
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("upgrade_source_path_invalid")
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		path = filepath.Join(path, part)
		info, err = os.Lstat(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("upgrade_source_path_invalid")
		}
	}
	return nil
}

func upgradeSyncDirectory(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func lockUpgradeWriters(root string) ([]*os.File, error) {
	var locks []*os.File
	for _, relative := range []string{"data/.service.lock", "data/memlane.sqlite.owner.lock", "official/cognee/.service-owner"} {
		file, err := os.OpenFile(filepath.Join(root, relative), os.O_RDWR|syscall.O_NOFOLLOW, 0)
		if err != nil {
			for _, f := range locks {
				f.Close()
			}
			return nil, errors.New("upgrade_writer_identity_unconfirmed")
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			file.Close()
			for _, f := range locks {
				f.Close()
			}
			return nil, errors.New("upgrade_writer_identity_unconfirmed")
		}
		if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			file.Close()
			for _, f := range locks {
				f.Close()
			}
			if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
				return nil, errors.New("upgrade_writers_active")
			}
			return nil, errors.New("upgrade_writer_identity_unconfirmed")
		}
		locks = append(locks, file)
	}
	return locks, nil
}

type upgradeOutput struct{ data bytes.Buffer }

func (b *upgradeOutput) Len() int       { return b.data.Len() }
func (b *upgradeOutput) String() string { return b.data.String() }

func (b *upgradeOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1024*1024 {
		return 0, errors.New("upgrade_process_observation_unbounded")
	}
	return b.data.Write(p)
}

func upgradeOpenFileOutput(raw string, ownPID int) error {
	pid := 0
	haveDescriptor, ownNamedFile := false, false
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			value, err := strconv.Atoi(line[1:])
			if err != nil || value < 1 {
				return errors.New("upgrade_process_observation_unconfirmed")
			}
			pid = value
			haveDescriptor = false
			if pid != ownPID {
				return errors.New("upgrade_storage_files_open")
			}
		case 'f':
			// lsof emits the descriptor separator even when only p/n were
			// requested. It is metadata, never a second process identity.
			if pid == 0 || len(line) < 2 || len(line) > 64 {
				return errors.New("upgrade_process_observation_unconfirmed")
			}
			haveDescriptor = true
		case 'n':
			if pid == 0 || !haveDescriptor || len(line) < 2 {
				return errors.New("upgrade_process_observation_unconfirmed")
			}
			ownNamedFile = true
		default:
			return errors.New("upgrade_process_observation_unconfirmed")
		}
	}
	// Each inspected storage root has one of our acquired writer locks open.
	// Missing that sentinel is an observation failure, not proof of no readers.
	if !ownNamedFile {
		return errors.New("upgrade_process_observation_unconfirmed")
	}
	return nil
}

func checkUpgradeStorageClosed(ctx context.Context, root string) error {
	for _, relative := range []string{"data", "official/cognee"} {
		command := exec.CommandContext(ctx, "/usr/sbin/lsof", "-n", "-P", "+D", filepath.Join(root, relative), "-Fpn")
		var output, diagnostic upgradeOutput
		command.Stdout, command.Stderr = &output, &diagnostic
		err := command.Run()
		var exit *exec.ExitError
		if diagnostic.Len() != 0 || (err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1)) {
			return errors.New("upgrade_process_observation_unconfirmed")
		}
		if err = upgradeOpenFileOutput(output.String(), os.Getpid()); err != nil {
			return err
		}
	}
	return nil
}

func upgradeInventory(ctx context.Context, root string) (map[string]string, []string, error) {
	files := map[string]string{}
	var dirs []string
	for _, relative := range upgradePaths {
		if err := upgradeConfinedPath(root, relative); err != nil {
			return nil, nil, err
		}
		base := filepath.Join(root, relative)
		if _, err := os.Lstat(base); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, nil, err
		}
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			name, err := filepath.Rel(root, path)
			if err != nil || !upgradeRelative(name) || entry.Type()&os.ModeSymlink != 0 {
				return errors.New("upgrade_source_path_invalid")
			}
			if entry.IsDir() {
				dirs = append(dirs, filepath.Join("client", name))
				return nil
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				return errors.New("upgrade_source_file_invalid")
			}
			files[filepath.Join("client", name)] = path
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	if _, ok := files["client/data/memlane.sqlite"]; !ok {
		return nil, nil, errors.New("upgrade_client_database_required")
	}
	if _, ok := files["client/official/cognee-config.json"]; !ok {
		return nil, nil, errors.New("upgrade_official_configuration_required")
	}
	sort.Strings(dirs)
	return files, dirs, nil
}

type upgradeReader struct {
	ctx    context.Context
	source io.Reader
}

func (r upgradeReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}

func upgradeDigest(ctx context.Context, path string, output io.Writer) (UpgradeSnapshotFile, error) {
	var result UpgradeSnapshotFile
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return result, errors.New("upgrade_source_file_invalid")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return result, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return result, errors.New("upgrade_source_changed")
	}
	hash := sha256.New()
	writer := io.Writer(hash)
	if output != nil {
		writer = io.MultiWriter(hash, output)
	}
	result.Bytes, err = io.Copy(writer, upgradeReader{ctx, file})
	if err != nil {
		return result, err
	}
	after, err := file.Stat()
	if err != nil || result.Bytes != before.Size() || !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		return result, errors.New("upgrade_source_changed")
	}
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

// CreateUpgradeSnapshot copies only stopped, exclusively owned application
// data/configuration. Components/models remain referenced at their retained
// paths. It never stops processes or changes source permissions/configuration.
// A byte-verified snapshot is not a claim that a downgrade/restore was tested.
func CreateUpgradeSnapshot(ctx context.Context, spec UpgradeSnapshotSpec) (UpgradeSnapshotReceipt, error) {
	var receipt UpgradeSnapshotReceipt
	if !filepath.IsAbs(spec.ClientRoot) || !filepath.IsAbs(spec.Destination) {
		return receipt, errors.New("upgrade_absolute_roots_required")
	}
	relative, err := filepath.Rel(spec.ClientRoot, spec.Destination)
	if err != nil || relative == "." || (!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "..") {
		return receipt, errors.New("upgrade_destination_must_be_outside_client")
	}
	if _, err = os.Lstat(spec.Destination); !os.IsNotExist(err) {
		return receipt, errors.New("upgrade_destination_already_exists")
	}
	parent, err := os.Lstat(filepath.Dir(spec.Destination))
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 || parent.Mode()&os.ModeSymlink != 0 {
		return receipt, errors.New("upgrade_private_destination_parent_required")
	}
	for _, path := range []string{"data/.service.lock", "data/memlane.sqlite.owner.lock", "official/cognee/.service-owner"} {
		if err = upgradeConfinedPath(spec.ClientRoot, path); err != nil {
			return receipt, err
		}
	}
	locks, err := lockUpgradeWriters(spec.ClientRoot)
	if err != nil {
		return receipt, err
	}
	defer func() {
		for _, file := range locks {
			file.Close()
		}
	}()
	if err = checkUpgradeStorageClosed(ctx, spec.ClientRoot); err != nil {
		return receipt, err
	}
	files, dirs, err := upgradeInventory(ctx, spec.ClientRoot)
	if err != nil {
		return receipt, err
	}
	for name, path := range spec.AgentFiles {
		if filepath.Base(name) != name || !upgradeRelative(name) || !filepath.IsAbs(path) {
			return receipt, errors.New("upgrade_agent_file_invalid")
		}
		files[filepath.Join("agents", name)] = path
	}
	if err = os.Mkdir(spec.Destination, 0700); err != nil {
		return receipt, err
	}
	// No manifest is published on failure; leave the private partial snapshot
	// for inspection rather than overwriting or deleting uncertain material.
	payload := filepath.Join(spec.Destination, "payload")
	if err = os.Mkdir(payload, 0700); err != nil {
		return receipt, err
	}
	for _, name := range dirs {
		if err = os.MkdirAll(filepath.Join(payload, name), 0700); err != nil {
			return receipt, err
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	receipt = UpgradeSnapshotReceipt{Format: 1, ClientRoot: spec.ClientRoot, CreatedMS: time.Now().UnixMilli(), Directories: dirs, AgentFiles: spec.AgentFiles, RetainedResources: spec.RetainedResources, Verification: "byte_consistent_stopped_snapshot"}
	for _, name := range names {
		target := filepath.Join(payload, name)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return receipt, err
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return receipt, err
		}
		entry, err := upgradeDigest(ctx, files[name], file)
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return receipt, err
		}
		entry.Path = name
		receipt.Files = append(receipt.Files, entry)
	}
	current, currentDirs, err := upgradeInventory(ctx, spec.ClientRoot)
	if err != nil {
		return receipt, err
	}
	for name, path := range spec.AgentFiles {
		current[filepath.Join("agents", name)] = path
	}
	if len(current) != len(files) || strings.Join(currentDirs, "\n") != strings.Join(dirs, "\n") {
		return receipt, errors.New("upgrade_source_changed")
	}
	for _, entry := range receipt.Files {
		path, exists := current[entry.Path]
		if !exists {
			return receipt, errors.New("upgrade_source_changed")
		}
		now, err := upgradeDigest(ctx, path, nil)
		if err != nil || now.SHA256 != entry.SHA256 || now.Bytes != entry.Bytes {
			return receipt, errors.New("upgrade_source_changed")
		}
	}
	if err = checkUpgradeStorageClosed(ctx, spec.ClientRoot); err != nil {
		return receipt, err
	}
	var payloadDirs []string
	if err = filepath.WalkDir(payload, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			payloadDirs = append(payloadDirs, path)
		}
		return nil
	}); err != nil {
		return receipt, err
	}
	for i := len(payloadDirs) - 1; i >= 0; i-- {
		if err = upgradeSyncDirectory(payloadDirs[i]); err != nil {
			return receipt, err
		}
	}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return receipt, err
	}
	manifest := filepath.Join(spec.Destination, "manifest.json")
	file, err := os.OpenFile(manifest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return receipt, err
	}
	_, err = file.Write(append(raw, '\n'))
	if err == nil {
		err = file.Sync()
	}
	file.Close()
	if err != nil {
		return receipt, err
	}
	directory, err := os.Open(spec.Destination)
	if err != nil {
		return receipt, err
	}
	defer directory.Close()
	if err = directory.Sync(); err != nil {
		return receipt, err
	}
	if err = upgradeSyncDirectory(filepath.Dir(spec.Destination)); err != nil {
		return receipt, err
	}
	return receipt, nil
}

func VerifyUpgradeSnapshot(ctx context.Context, destination string) (UpgradeSnapshotReceipt, error) {
	var receipt UpgradeSnapshotReceipt
	if !filepath.IsAbs(destination) {
		return receipt, errors.New("upgrade_absolute_roots_required")
	}
	rootInfo, err := os.Lstat(destination)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode().Perm()&0077 != 0 || rootInfo.Mode()&os.ModeSymlink != 0 {
		return receipt, errors.New("upgrade_manifest_invalid")
	}
	manifest := filepath.Join(destination, "manifest.json")
	info, err := os.Lstat(manifest)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16*1024*1024 {
		return receipt, errors.New("upgrade_manifest_invalid")
	}
	raw, err := os.ReadFile(manifest)
	if err != nil {
		return receipt, err
	}
	if json.Unmarshal(raw, &receipt) != nil || receipt.Format != 1 || len(receipt.Files) == 0 || !filepath.IsAbs(receipt.ClientRoot) || receipt.Verification != "byte_consistent_stopped_snapshot" {
		return receipt, errors.New("upgrade_manifest_invalid")
	}
	seen := map[string]bool{}
	for _, entry := range receipt.Files {
		if !upgradeRelative(entry.Path) || seen[entry.Path] {
			return receipt, errors.New("upgrade_manifest_invalid")
		}
		seen[entry.Path] = true
		if err = upgradeConfinedPath(filepath.Join(destination, "payload"), entry.Path); err != nil {
			return receipt, err
		}
		now, err := upgradeDigest(ctx, filepath.Join(destination, "payload", entry.Path), nil)
		if err != nil || now.SHA256 != entry.SHA256 || now.Bytes != entry.Bytes {
			return receipt, errors.New("upgrade_snapshot_changed")
		}
	}
	if !seen["client/data/memlane.sqlite"] {
		return receipt, errors.New("upgrade_manifest_invalid")
	}
	err = filepath.WalkDir(filepath.Join(destination, "payload"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("upgrade_snapshot_changed")
		}
		if !entry.IsDir() {
			name, err := filepath.Rel(filepath.Join(destination, "payload"), path)
			if err != nil || !seen[name] {
				return errors.New("upgrade_snapshot_changed")
			}
		}
		return nil
	})
	return receipt, err
}
