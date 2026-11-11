package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/zwhu/memlane/internal/records"
	"golang.org/x/sys/unix"
)

const graphIntentLimit = 65536

type graphSubmissionIntent struct {
	Format   int                              `json:"format"`
	Identity string                           `json:"identity"`
	Project  string                           `json:"project"`
	Request  *records.CogneeSubmissionRequest `json:"request"`
}

// Each library epoch/project has a private locked journal. It contains only IDs,
// revisions and processing ledger identity, never material bodies or credentials.
func graphIntent(ctx context.Context, root, identity, project, mode string, request *records.CogneeSubmissionRequest) (*records.CogneeSubmissionRequest, error) {
	if !filepath.IsAbs(root) || !manualUUID(identity) || !manualUUID(project) {
		return nil, errors.New("graph intent identity unavailable")
	}
	directory := filepath.Join(root, "graph-submissions")
	if err := os.Mkdir(directory, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(fd), directory)
	if err = privateManualFD(dir, true); err != nil {
		dir.Close()
		return nil, err
	}
	name := identity + "-" + project
	lockFD, err := unix.Openat(fd, name+".lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		dir.Close()
		return nil, err
	}
	lock := os.NewFile(uintptr(lockFD), name+".lock")
	disk := &manualDraftDisk{dir: dir, lock: lock, name: name + ".json"}
	defer disk.close()
	if err = privateManualFD(lock, false); err != nil {
		return nil, err
	}
	if err = unix.Flock(lockFD, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, err
	}
	e := graphSubmissionIntent{Format: 1, Identity: identity, Project: project}
	fileFD, err := unix.Openat(fd, disk.name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err == nil {
		file := os.NewFile(uintptr(fileFD), disk.name)
		if err = privateManualFD(file, false); err != nil {
			file.Close()
			return nil, err
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, graphIntentLimit+1))
		file.Close()
		if readErr != nil || len(raw) > graphIntentLimit {
			return nil, errors.New("graph intent exceeds bounds")
		}
		if err = manualUniqueJSON(raw); err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&e); err != nil {
			return nil, err
		}
		var tail any
		if decoder.Decode(&tail) != io.EOF {
			return nil, errors.New("graph intent trailing data")
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return nil, err
	}
	if e.Format != 1 || e.Identity != identity || e.Project != project {
		return nil, errors.New("graph intent epoch mismatch")
	}
	valid := func(q *records.CogneeSubmissionRequest) bool {
		if q == nil {
			return true
		}
		if !manualUUID(q.Operation) || q.Project != project || !manualUUID(q.Dataset) || q.Processing.Revision == "" || q.Processing.Generation < 1 || len(q.Sources) == 0 || len(q.Sources) > 16 {
			return false
		}
		seen := map[string]bool{}
		for _, v := range q.Sources {
			if !manualUUID(v.Memory) || v.Revision < 1 || seen[v.Memory] {
				return false
			}
			seen[v.Memory] = true
		}
		return true
	}
	if !valid(e.Request) || !valid(request) {
		return nil, errors.New("graph intent invalid request")
	}
	switch mode {
	case "read":
		return e.Request, nil
	case "save":
		if request == nil {
			return nil, errors.New("graph intent request missing")
		}
		if e.Request != nil && records.MergeDigest(e.Request) != records.MergeDigest(request) {
			return nil, errors.New("prior graph operation unresolved")
		}
		e.Request = request
	case "clear":
		if e.Request != nil && (request == nil || e.Request.Operation != request.Operation) {
			return nil, errors.New("graph operation changed")
		}
		e.Request = nil
	default:
		return nil, errors.New("graph intent mode invalid")
	}
	raw, err := json.Marshal(e)
	if err != nil || len(raw) > graphIntentLimit {
		return nil, errors.New("graph intent exceeds bounds")
	}
	if err = disk.write(ctx, raw); err != nil {
		return nil, err
	}
	return e.Request, nil
}
