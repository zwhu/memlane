package controlplane

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
)

const maxOperationBytes = 4096

type UIOperation struct {
	Operation string `json:"operation_id"`
	Kind      string `json:"kind"`
	Profile   string `json:"profile_id"`
	Revision  int64  `json:"profile_revision,omitempty"`
}

var operationIDPattern = regexp.MustCompile(`^ui-[A-Za-z0-9_-]{1,61}$`)

func ValidOperationID(id string) bool { return operationIDPattern.MatchString(id) }

func NewUIOperationID() string { return "ui-" + rand.Text() }

func NewCLIOperationID() string { return "ui-cli-" + rand.Text() }

func (o UIOperation) Valid() bool {
	if !ValidOperationID(o.Operation) || o.Profile == "" {
		return false
	}
	switch o.Kind {
	case "save", "next", "handoffs", "validations", "delete":
		return true
	default:
		return o.IsConnection()
	}
}

func (o UIOperation) IsConnection() bool {
	switch o.Kind {
	case "connection_prepare", "connection_activate", "connection_allow_capture",
		"connection_defer_capture", "connection_repair_trae_hooks":
		return true
	default:
		return false
	}
}

func (o UIOperation) ConnectionAction() string {
	switch o.Kind {
	case "connection_prepare":
		return "prepare"
	case "connection_activate":
		return "activate"
	case "connection_repair_trae_hooks":
		return "repair-trae-hooks"
	case "connection_allow_capture":
		return "allow_capture"
	case "connection_defer_capture":
		return "defer_capture"
	case "connection_review":
		return "review"
	default:
		return ""
	}
}

func OperationsDir(generationFile, managementTokenFile string) string {
	if generationFile != "" {
		return generationFile + ".ui-operations"
	}
	if managementTokenFile != "" {
		return filepath.Join(filepath.Dir(managementTokenFile), "generation-ui-operations")
	}
	return ""
}

func LoadUIOperations(root string) ([]UIOperation, error) {
	if root == "" {
		return nil, nil
	}
	root, err := absoluteOperationsDir(root)
	if err != nil {
		return nil, err
	}
	dir, err := openOperationsDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer dir.Close()

	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	var ops []UIOperation
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".tmp-") {
			continue
		}
		op, err := readUIOperation(filepath.Join(root, name))
		if errors.Is(err, os.ErrNotExist) {
			// A concurrent writer may have finished this operation since ReadDir.
			continue
		}
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, nil
}

func readUIOperation(path string) (UIOperation, error) {
	var op UIOperation
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return op, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return op, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return op, errors.New("操作记录必须为私有普通文件")
	}
	if info.Size() > maxOperationBytes {
		return op, errors.New("操作记录超过大小限制")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxOperationBytes+1))
	if err != nil {
		return op, err
	}
	if len(raw) > maxOperationBytes {
		return op, errors.New("操作记录超过大小限制")
	}
	if json.Unmarshal(raw, &op) != nil || !op.Valid() || filepath.Base(path) != op.Operation+".json" {
		return op, errors.New("操作记录格式无效")
	}
	return op, nil
}

func RecordUIOperation(root string, op UIOperation) ([]UIOperation, error) {
	root, err := absoluteOperationsDir(root)
	if err != nil {
		return nil, err
	}
	if !op.Valid() {
		return nil, errors.New("操作记录无效")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	dir, err := openOperationsDir(root)
	if err != nil {
		return nil, err
	}
	defer dir.Close()

	lock, err := lockOperations(root)
	if err != nil {
		return nil, err
	}
	defer unlockOperations(lock)

	ops, err := LoadUIOperations(root)
	if err != nil {
		return nil, err
	}
	for _, pending := range ops {
		if pending.Operation == op.Operation {
			return nil, errors.New("操作编号已存在")
		}
		if pending.Profile == op.Profile && pending.IsConnection() == op.IsConnection() {
			return nil, errors.New("该配置已有待确认的操作")
		}
	}
	if err := writeUIOperation(root, op); err != nil {
		return nil, err
	}
	if err := dir.Sync(); err != nil {
		return nil, err
	}
	return append(ops, op), nil
}

func writeUIOperation(root string, op UIOperation) error {
	file, err := os.CreateTemp(root, ".tmp-")
	if err != nil {
		return err
	}
	tmpPath := file.Name()
	defer os.Remove(tmpPath)
	defer file.Close()

	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if err := json.NewEncoder(file).Encode(op); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() > maxOperationBytes {
		return errors.New("操作记录超过大小限制")
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, filepath.Join(root, op.Operation+".json"))
}

func FinishUIOperation(root, operationID string) error {
	root, err := absoluteOperationsDir(root)
	if err != nil {
		return err
	}
	if !ValidOperationID(operationID) {
		return errors.New("操作编号无效")
	}
	dir, err := openOperationsDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer dir.Close()

	lock, err := lockOperations(root)
	if err != nil {
		return err
	}
	defer unlockOperations(lock)

	if err := os.Remove(filepath.Join(root, operationID+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return dir.Sync()
}

func absoluteOperationsDir(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", errors.New("操作日志目录必须为绝对路径")
	}
	return filepath.Clean(root), nil
}

func openOperationsDir(root string) (*os.File, error) {
	dir, err := os.OpenFile(root, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	info, err := dir.Stat()
	if err == nil && (!info.IsDir() || info.Mode().Perm() != 0o700) {
		err = errors.New("操作日志目录权限必须为 0700")
	}
	if err != nil {
		dir.Close()
		return nil, err
	}
	return dir, nil
}

func lockOperations(root string) (*os.File, error) {
	lock, err := os.OpenFile(root+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := lock.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm() != 0o600) {
		err = errors.New("操作日志锁必须为权限 0600 的普通文件")
	}
	if err == nil {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err != nil {
		lock.Close()
		return nil, err
	}
	return lock, nil
}

func unlockOperations(lock *os.File) {
	// Explicitly release the lock before closing in case a child inherited the descriptor.
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_ = lock.Close()
}
