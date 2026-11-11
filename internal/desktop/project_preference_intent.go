package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/zwhu/memlane/internal/records"
	"golang.org/x/sys/unix"
)

const preferenceIntentLimit = 65536

// Retired requests remain bounded local history; an unknown request is never
// retired automatically and the same operation cannot change its payload.
type preferenceIntentEnvelope struct {
	Format        int                                              `json:"format"`
	Library       string                                           `json:"library"`
	ProjectRecord records.UsageTarget                              `json:"project_record"`
	Current       string                                           `json:"current"`
	Requests      map[string]records.ProjectPreferenceConfirmation `json:"requests"`
}

func validPreferenceRequest(q records.ProjectPreferenceConfirmation, identity string, project records.UsageTarget) bool {
	if !manualUUID(q.OperationID) || q.LibraryIdentity != identity || q.Query.LibraryIdentity != identity || q.Query.Project != project.Dataset || q.Query.ProjectRecord != project || !q.ConfirmProjectRule || q.Decision != "use_project" {
		return false
	}
	p := q.Query.PreferenceRecord
	if !manualUUID(p.Memory) || !manualUUID(p.Dataset) || p.Memory == project.Memory || p.Dataset == project.Dataset || p.Revision < 1 {
		return false
	}
	for i, value := range []string{q.ProjectQuote, q.PreferenceQuote, q.Conditions, q.Reason} {
		if !utf8.ValidString(value) || len(value) > 4096 || i < 3 && strings.TrimSpace(value) == "" {
			return false
		}
	}
	if len(q.Query.Datasets) > 256 {
		return false
	}
	scopes := map[string]bool{}
	for _, id := range q.Query.Datasets {
		if !manualUUID(id) {
			return false
		}
		scopes[id] = true
	}
	return scopes[project.Dataset] && scopes[p.Dataset]
}
func preferenceIntent(ctx context.Context, root, identity string, project records.UsageTarget, mode string, q *records.ProjectPreferenceConfirmation) (*records.ProjectPreferenceConfirmation, error) {
	if !filepath.IsAbs(root) || !manualUUID(identity) || !manualUUID(project.Memory) || !manualUUID(project.Dataset) || project.Revision < 1 {
		return nil, errors.New("comparison identity unavailable")
	}
	directory := filepath.Join(root, "project-preference-intents")
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
	name := identity + "-" + project.Memory + "-" + strconv.FormatInt(project.Revision, 10)
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
	read := func(name string) (preferenceIntentEnvelope, bool, error) {
		e := preferenceIntentEnvelope{Format: 1, Library: identity, ProjectRecord: project, Requests: map[string]records.ProjectPreferenceConfirmation{}}
		fileFD, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) {
			return e, false, nil
		}
		if err != nil {
			return e, false, err
		}
		file := os.NewFile(uintptr(fileFD), name)
		defer file.Close()
		if err = privateManualFD(file, false); err != nil {
			return e, true, err
		}
		raw, err := io.ReadAll(io.LimitReader(file, preferenceIntentLimit+1))
		if err != nil || len(raw) > preferenceIntentLimit || !utf8.Valid(raw) {
			return e, true, errors.New("comparison intent exceeds bounds")
		}
		if err = manualUniqueJSON(raw); err != nil {
			return e, true, err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&e); err != nil {
			return e, true, err
		}
		var tail any
		if decoder.Decode(&tail) != io.EOF {
			return e, true, errors.New("comparison intent extra payload")
		}
		if e.Format != 1 || e.Library != identity || e.ProjectRecord != project || e.Requests == nil || len(e.Requests) > 1 {
			return e, true, errors.New("comparison intent scope changed")
		}
		for id, request := range e.Requests {
			if id != request.OperationID || !validPreferenceRequest(request, identity, project) {
				return e, true, errors.New("comparison intent invalid payload")
			}
		}
		if e.Current != "" {
			if _, ok := e.Requests[e.Current]; !ok {
				return e, true, errors.New("comparison intent missing current request")
			}
		}
		return e, true, nil
	}
	e, _, err := read(disk.name)
	if err != nil {
		return nil, err
	}
	if e.Current == "" && len(e.Requests) != 0 {
		return nil, errors.New("comparison intent has no current owner")
	}
	archiveName := func(id string) string { return identity + "-operation-" + id + ".json" }
	archived := func(q records.ProjectPreferenceConfirmation) (bool, error) {
		prior, found, err := read(archiveName(q.OperationID))
		if err != nil || !found {
			return found, err
		}
		request, ok := prior.Requests[q.OperationID]
		if !ok || prior.Current != "" || records.MergeDigest(request) != records.MergeDigest(q) {
			return true, errors.New("comparison archived payload changed")
		}
		return true, nil
	}
	current := func() *records.ProjectPreferenceConfirmation {
		if e.Current == "" {
			return nil
		}
		copy := e.Requests[e.Current]
		return &copy
	}
	switch mode {
	case "read":
		return current(), nil
	case "save":
		if q == nil || !validPreferenceRequest(*q, identity, project) {
			return nil, errors.New("comparison request invalid")
		}
		if e.Current != "" && e.Current != q.OperationID {
			return nil, errors.New("another comparison request still owns this version")
		}
		if prior, ok := e.Requests[q.OperationID]; ok {
			if records.MergeDigest(prior) != records.MergeDigest(q) {
				return nil, errors.New("comparison request payload changed")
			}
		} else {
			if found, err := archived(*q); err != nil {
				return nil, err
			} else if found {
				return nil, errors.New("comparison request retired")
			}
			e.Requests[q.OperationID] = *q
		}
		e.Current = q.OperationID
	case "retire":
		if q == nil || !validPreferenceRequest(*q, identity, project) {
			return nil, errors.New("comparison request invalid")
		}
		found, err := archived(*q)
		if err != nil {
			return nil, err
		}
		if e.Current == "" && found {
			return nil, nil
		}
		prior, ok := e.Requests[q.OperationID]
		if e.Current != q.OperationID || !ok || records.MergeDigest(prior) != records.MergeDigest(q) {
			return nil, errors.New("comparison request changed")
		}
		if !found {
			archive := preferenceIntentEnvelope{Format: 1, Library: identity, ProjectRecord: project, Requests: map[string]records.ProjectPreferenceConfirmation{q.OperationID: *q}}
			raw, err := json.Marshal(archive)
			if err != nil || len(raw) > preferenceIntentLimit {
				return nil, errors.New("comparison intent exceeds bounds")
			}
			previous := disk.name
			disk.name = archiveName(q.OperationID)
			err = disk.write(ctx, raw)
			disk.name = previous
			if err != nil {
				return nil, err
			}
		}
		e.Current = ""
		e.Requests = map[string]records.ProjectPreferenceConfirmation{}
	default:
		return nil, errors.New("invalid comparison intent mode")
	}
	raw, err := json.Marshal(e)
	if err != nil || len(raw) > preferenceIntentLimit {
		return nil, errors.New("comparison intent exceeds bounds")
	}
	if err = disk.write(ctx, raw); err != nil {
		return nil, err
	}
	return current(), nil
}
