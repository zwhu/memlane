//go:build darwin || linux

package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/zwhu/memlane/internal/records"
	"golang.org/x/sys/unix"
)

// These are per-source/IPC resource bounds, not the old initialization import
// count or 128 KiB selection limits. Exceeding a bound is an explicit failure.
const projectSourceBytes = 4 * 1024 * 1024
const projectSourceTotalBytes = 32 * 1024 * 1024

type projectSourceReader struct {
	root    *os.File
	sources []records.ProjectRepositorySource
	byPath  map[string]int
	total   int
}

func openProjectSourceReader(workspace string) (*projectSourceReader, error) {
	fd, err := unix.Open(workspace, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return &projectSourceReader{root: os.NewFile(uintptr(fd), workspace), byPath: map[string]int{}}, nil
}
func projectSourcePath(path string) bool {
	if path == "" || filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsAny(path, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		switch strings.ToLower(part) {
		case ".git", ".codex", ".pi", ".trae", ".agents", ".claude":
			return false
		}
		if strings.HasPrefix(strings.ToLower(part), ".env") {
			return false
		}
	}
	switch strings.ToLower(filepath.Base(path)) {
	case "auth.json", "credentials.json":
		return false
	}
	return true
}

func (r *projectSourceReader) read(ctx context.Context, path string) (records.ProjectRepositorySource, error) {
	var empty records.ProjectRepositorySource
	if filepath.IsAbs(path) {
		relative, err := filepath.Rel(r.root.Name(), path)
		if err != nil {
			return empty, errors.New("repository source is outside the authorized root")
		}
		path = filepath.ToSlash(relative)
	}
	if !projectSourcePath(path) {
		return empty, errors.New("repository source path is not permitted")
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if i, ok := r.byPath[path]; ok {
		return r.sources[i], nil
	}
	parent, err := unix.FcntlInt(r.root.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return empty, err
	}
	parts := strings.Split(path, "/")
	for _, part := range parts[:len(parts)-1] {
		next, e := unix.Openat(parent, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(parent)
		if e != nil {
			return empty, errors.New("repository source directory unavailable or symlinked")
		}
		parent = next
	}
	fd, err := unix.Openat(parent, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	unix.Close(parent)
	if err != nil {
		return empty, errors.New("repository source unavailable or symlinked")
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return empty, errors.New("repository source must be a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, projectSourceBytes+1))
	if err != nil {
		return empty, err
	}
	after, err := file.Stat()
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return empty, errors.New("repository source changed during read")
	}
	if len(raw) == 0 || !utf8.Valid(raw) || strings.IndexByte(string(raw), 0) >= 0 {
		return empty, errors.New("repository source must contain nonempty UTF-8 text")
	}
	if len(raw) > projectSourceBytes || r.total+len(raw) > projectSourceTotalBytes {
		return empty, errors.New("repository source exceeded IPC resource limit; task scope remains incomplete")
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	idSum := sha256.Sum256([]byte(path + "\x00" + digest))
	source := records.ProjectRepositorySource{ID: hex.EncodeToString(idSum[:]), Path: path, SHA256: digest, Text: string(raw)}
	r.byPath[path], r.total = len(r.sources), r.total+len(raw)
	r.sources = append(r.sources, source)
	return source, nil
}

func validateProjectProposal(proposal records.ProjectKnowledgeProposal, task records.ProjectTask, sources []records.ProjectRepositorySource) error {
	if proposal.Task != task.ID || proposal.Attempt != task.Attempt || strings.TrimSpace(proposal.Explanation) == "" || len(proposal.Explanation) > 8192 || len(proposal.Items) > 256 {
		return errors.New("project proposal task or result identity invalid")
	}
	byID := make(map[string]records.ProjectRepositorySource, len(sources))
	for _, source := range sources {
		byID[source.ID] = source
	}
	for _, item := range proposal.Items {
		if strings.TrimSpace(item.Title) == "" || len(item.Title) > 512 || strings.TrimSpace(item.Text) == "" || len(item.Text) > 64*1024 || len(item.Sources) == 0 || len(item.Sources) > 32 {
			return errors.New("project knowledge requires bounded content and host-read sources")
		}
		seen := map[string]bool{}
		for _, citation := range item.Sources {
			source, ok := byID[citation.Source]
			if !ok || seen[citation.Source] || strings.TrimSpace(citation.Quote) == "" || !strings.Contains(source.Text, citation.Quote) {
				return errors.New("project knowledge source or exact quote invalid")
			}
			seen[citation.Source] = true
		}
	}
	return nil
}
