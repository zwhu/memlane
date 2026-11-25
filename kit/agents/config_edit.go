package agents

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const configLimit = 1 << 20

// openParent walks every component without following symlinks. On macOS,
// start at the canonical system aliases for its standard temporary paths.
func openParent(path string) (*os.File, string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	path = canonicalSystemPath(path)
	parts := splitPath(path)
	if len(parts) == 0 {
		return nil, "", errors.New("file path required")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	for _, part := range parts[:len(parts)-1] {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, "", e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), filepath.Dir(path)), parts[len(parts)-1], nil
}

func openRegular(path string) (*os.File, error) {
	dir, name, err := openParent(path)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return targetAt(dir, name)
}

func SafeReadFile(path string, maxBytes int64) ([]byte, error) {
	if maxBytes < 0 || maxBytes == int64(^uint64(0)>>1) {
		return nil, errors.New("invalid file limit")
	}
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() > maxBytes {
		return nil, errors.New("file exceeds limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("file exceeds limit")
	}
	return data, nil
}

type EditPreview struct {
	Path, BeforeSHA256, AfterSHA256 string
	Before, After                   []byte
	Changed                         bool
	Summary                         string
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func previewEdit(path string, before, after []byte, summary string) EditPreview {
	a, b := digest(before), digest(after)
	return EditPreview{path, a, b, before, after, a != b, summary}
}

var editMu sync.Mutex

func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	editMu.Lock()
	defer editMu.Unlock()
	return atomicWriteFile(path, data, perm, "", 0)
}

func atomicWriteFile(path string, data []byte, perm os.FileMode, expected string, limit int64) error {
	if perm == 0 {
		perm = 0o600
	}
	dir, name, err := openParent(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := checkWriteTarget(dir, name); err != nil {
		return err
	}
	temp, err := createEditTemp(dir)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(int(dir.Fd()), temp.Name(), 0)
	defer temp.Close()
	if _, err = temp.Write(data); err != nil {
		return err
	}
	if err = temp.Chmod(perm.Perm()); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if expected != "" {
		current, err := readWriteTarget(dir, name, limit)
		if err != nil {
			return err
		}
		if digest(current) != expected {
			return errors.New("edit preview is stale")
		}
	} else if err := checkWriteTarget(dir, name); err != nil {
		return err
	}
	if err = unix.Renameat(int(dir.Fd()), temp.Name(), int(dir.Fd()), name); err != nil {
		return err
	}
	// The rename has committed. Directory sync is best effort.
	_ = dir.Sync()
	return nil
}

func ApplyEditPreview(preview EditPreview, perm os.FileMode) error {
	editMu.Lock()
	defer editMu.Unlock()
	if digest(preview.Before) != preview.BeforeSHA256 || digest(preview.After) != preview.AfterSHA256 {
		return fmt.Errorf("invalid edit preview")
	}
	limit := int64(max(configLimit, len(preview.Before)))
	if preview.BeforeSHA256 != preview.AfterSHA256 {
		return atomicWriteFile(preview.Path, preview.After, perm, preview.BeforeSHA256, limit)
	}
	current, err := SafeReadFile(preview.Path, limit)
	if err != nil {
		return err
	}
	if digest(current) != preview.BeforeSHA256 {
		return errors.New("edit preview is stale")
	}
	return nil
}

func canonicalSystemPath(path string) string {
	if runtime.GOOS == "darwin" {
		for _, alias := range []string{"/tmp", "/var"} {
			if strings.HasPrefix(path, alias+"/") {
				return "/private" + path
			}
		}
	}
	return path
}

func splitPath(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool { return r == filepath.Separator })
}

func targetAt(dir *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("regular file required")
	}
	return f, nil
}

func checkWriteTarget(dir *os.File, name string) error {
	f, err := targetAt(dir, name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}

func readWriteTarget(dir *os.File, name string, limit int64) ([]byte, error) {
	f, err := targetAt(dir, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("edit preview is stale")
	}
	return data, nil
}

func createEditTemp(dir *os.File) (*os.File, error) {
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return nil, err
	}
	name := ".agent-edit-" + hex.EncodeToString(suffix[:])
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

type spanEdit struct {
	start, end int
	text       string
}

type TOMLChange struct {
	TablePath []string
	Key       string
	Value     any
	Delete    bool
}

func SetKey(table []string, key string, value any) TOMLChange {
	return TOMLChange{TablePath: table, Key: key, Value: value}
}
func DeleteKey(table []string, key string) TOMLChange {
	return TOMLChange{TablePath: table, Key: key, Delete: true}
}
func DeleteTable(table []string) TOMLChange { return TOMLChange{TablePath: table, Delete: true} }

func tomlKey(path []string) string {
	quoted := make([]string, len(path))
	for i, key := range path {
		raw, _ := json.Marshal(key)
		quoted[i] = string(raw)
	}
	return strings.Join(quoted, ".")
}

func tomlValue(value any) (string, error) {
	switch v := value.(type) {
	case map[string]any:
		parts := []string{}
		for _, key := range sortedKeys(v) {
			encoded, err := tomlValue(v[key])
			if err != nil {
				return "", err
			}
			parts = append(parts, tomlKey([]string{key})+" = "+encoded)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	case []any:
		parts := []string{}
		for _, item := range v {
			encoded, err := tomlValue(item)
			if err != nil {
				return "", err
			}
			parts = append(parts, encoded)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		p := tomlParser{s: string(raw)}
		if _, err := p.value(0); err != nil || p.i != len(raw) {
			return "", errTOML
		}
		return string(raw), nil
	}
}

func lineTail(data string, end int) int {
	if i := strings.IndexByte(data[end:], '\n'); i >= 0 {
		return end + i + 1
	}
	return len(data)
}

func applySpans(data string, spans []spanEdit) string {
	var out strings.Builder
	offset := 0
	for _, span := range spans {
		out.WriteString(data[offset:span.start])
		out.WriteString(span.text)
		offset = span.end
	}
	out.WriteString(data[offset:])
	return out.String()
}

func deleteTOMLEntry(data string, entry tomlEntry) spanEdit {
	end := lineTail(data, entry.end)
	replacement := ""
	if comment := strings.IndexByte(data[entry.end:end], '#'); comment >= 0 {
		indent := data[entry.line:]
		indent = indent[:len(indent)-len(strings.TrimLeft(indent, " \t"))]
		replacement = indent + data[entry.end+comment:end]
	}
	return spanEdit{entry.line, end, replacement}
}

func applyTOMLChange(data string, change TOMLChange) (string, error) {
	_, entries, err := parseTOML([]byte(data))
	if err != nil {
		return "", err
	}
	if change.Key == "" && (!change.Delete || len(change.TablePath) == 0) {
		return "", errTOML
	}
	target := slices.Clone(change.TablePath)
	if change.Key != "" {
		target = append(target, change.Key)
	}
	var encoded string
	if !change.Delete {
		encoded, err = tomlValue(change.Value)
		if err != nil {
			return "", err
		}
	}
	var spans []spanEdit
	insert := 0
	foundTable := len(change.TablePath) == 0
	inTable := foundTable
	for _, entry := range entries {
		if entry.table {
			inTable = slices.Equal(entry.path, change.TablePath)
			foundTable = foundTable || inTable
		}
		if inTable {
			insert = lineTail(data, entry.end)
		}
		same := slices.Equal(entry.path, target)
		if change.Delete && (change.Key == "" && len(entry.path) >= len(target) && slices.Equal(entry.path[:len(target)], target) || change.Key != "" && same && !entry.table) {
			spans = append(spans, deleteTOMLEntry(data, entry))
		} else if !change.Delete && same && !entry.table {
			return applySpans(data, []spanEdit{{entry.value, entry.end, encoded}}), nil
		}
	}
	if change.Delete {
		return applySpans(data, spans), nil
	}
	header := ""
	if !foundTable {
		insert = len(data)
		header = "[" + tomlKey(change.TablePath) + "]\n"
	}
	if insert > 0 && data[insert-1] != '\n' {
		header = "\n" + header
	}
	return applySpans(data, []spanEdit{{insert, insert, header + tomlKey([]string{change.Key}) + " = " + encoded + "\n"}}), nil
}

func PreviewTOMLEdit(path string, edits []TOMLChange, summary string) (EditPreview, error) {
	before, err := SafeReadFile(path, configLimit)
	if errors.Is(err, os.ErrNotExist) {
		return EditPreview{}, fmt.Errorf("config required: %w", err)
	}
	if err != nil {
		return EditPreview{}, err
	}
	after := string(before)
	for _, edit := range edits {
		after, err = applyTOMLChange(after, edit)
		if err != nil {
			return EditPreview{}, err
		}
	}
	if _, err := ParseTOML([]byte(after)); err != nil {
		return EditPreview{}, err
	}
	return previewEdit(path, before, []byte(after), summary), nil
}

func makePrivateDirectory(path string) (*os.File, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path = canonicalSystemPath(path)
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Open("/", flags, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range splitPath(path) {
		next, e := unix.Openat(fd, part, flags, 0)
		if errors.Is(e, os.ErrNotExist) {
			e = unix.Mkdirat(fd, part, 0o700)
			if e == nil || errors.Is(e, os.ErrExist) {
				next, e = unix.Openat(fd, part, flags, 0)
			}
		}
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), path), nil
}
