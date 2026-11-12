package daemon

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/codegraph"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

// Exact bytes of the already selected darwin-arm64 Enola 0.4.30/v351
// component. Updating this input requires a component/provenance revalidation.
const officialCodeEnolaSHA = "060cff152dda0c317b378ca04e04023df09a1c7dd2e3cfcb0c174c668b7b9775"

type officialCodeRun struct {
	ctx   context.Context
	state records.CodeState
	job   records.CodeJob
	grant records.OfficialCodeExecutionGrant
}

func (s *Server) authorizeOfficialCodeExecution(ctx context.Context, capability, id string) (records.OfficialCodeExecutionGrant, error) {
	var empty records.OfficialCodeExecutionGrant
	o := s.sessionProcessing
	if o == nil || o.capability == "" || subtle.ConstantTimeCompare([]byte(capability), []byte(o.capability)) != 1 {
		return empty, records.MergeError("service_authorization_required")
	}
	o.mu.Lock()
	closed := o.closed
	o.mu.Unlock()
	if closed {
		return empty, records.MergeError("service_stopping")
	}
	s.codeMu.Lock()
	run := s.officialCodeRuns[id]
	stopping := s.codeStopping
	s.codeMu.Unlock()
	if stopping || run == nil || run.ctx.Err() != nil {
		return empty, records.MergeError("code_disabled")
	}
	j, err := s.store.OfficialCodeJob(ctx, id)
	if err != nil || j.State != "running" || j.Error != "" || j.ExecutionEpoch == "" || j.ExecutionEpoch != run.grant.ExecutionEpoch || j.Manifest == nil || j.Manifest.Digest != run.grant.SourceDigest || j.Official == nil || *j.Official != *run.state.Official {
		return empty, records.MergeError("code_disabled")
	}
	if err = s.store.AuthorizeOfficialCode(ctx, run.state); err != nil {
		return empty, err
	}
	s.codeMu.Lock()
	same := !s.codeStopping && s.officialCodeRuns[id] == run && run.ctx.Err() == nil
	s.codeMu.Unlock()
	if !same {
		return empty, records.MergeError("code_disabled")
	}
	out := run.grant
	out.DenyReads = append([]string(nil), out.DenyReads...)
	return out, nil
}

func officialCodeIdentity(ctx context.Context, scope records.OfficialCodeScope, token string) (string, error) {
	var wire struct {
		Instance     string   `json:"instance"`
		Commit       string   `json:"source_commit"`
		Epoch        string   `json:"code_execution_epoch"`
		Capabilities []string `json:"capabilities"`
	}
	if err := official.Request(ctx, scope.Endpoint, "/api/v1/memlane/identity", "GET", token, "", nil, &wire); err != nil {
		return "", err
	}
	capable := false
	for _, capability := range wire.Capabilities {
		capable = capable || capability == "official_code_execution_v1"
	}
	id, err := uuid.Parse(wire.Epoch)
	if err != nil || id.String() != wire.Epoch || wire.Instance != scope.Instance || wire.Commit != official.CodingMemoryServiceCommit || !capable {
		return "", records.MergeError("official_code_backend_unavailable")
	}
	return wire.Epoch, nil
}

func officialCodeBinary(path string) (string, error) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" || !filepath.IsAbs(path) {
		return "", errors.New("pinned native code component required")
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0111 == 0 || info.Size() > 256*1024*1024 {
		return "", errors.New("pinned native code component required")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, io.LimitReader(f, 256*1024*1024+1)); err != nil {
		return "", err
	}
	if hex.EncodeToString(hash.Sum(nil)) != officialCodeEnolaSHA {
		return "", errors.New("pinned native code component required")
	}
	return path, nil
}

type officialSourceMarker struct {
	Operation string `json:"operation"`
	Worktree  string `json:"worktree"`
	Digest    string `json:"digest"`
}

// Only an exact task-owned stable slot whose previous execution is confirmed
// terminal can be reclaimed. Missing/unrecognized markers preserve the data.
func (s *Server) clearOfficialSourceSlot(ctx context.Context, path string, c records.CodeState) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("official source slot unverified")
	}
	markerPath := filepath.Join(path, "owner.json")
	info, err = os.Lstat(markerPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return errors.New("official source slot unverified")
	}
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		return err
	}
	var marker officialSourceMarker
	if json.Unmarshal(raw, &marker) != nil || marker.Worktree != c.ID {
		return errors.New("official source slot unverified")
	}
	j, err := s.store.OfficialCodeJob(ctx, marker.Operation)
	if err != nil || j.WorktreeID != c.ID || j.State == "running" || j.State == "queued" || j.Manifest == nil || j.Manifest.Digest != marker.Digest || j.Official == nil || c.Official == nil || *j.Official != *c.Official {
		return errors.New("official source slot still owned or unverified")
	}
	return os.RemoveAll(path)
}

func officialCodePayload(j records.CodeJob, owner string) map[string]any {
	return map[string]any{"operation": j.ID, "instance": j.Official.Instance, "dataset": j.Official.Dataset, "user": j.Official.User, "source_digest": j.Manifest.Digest, "execution_epoch": j.ExecutionEpoch, "owner": owner}
}

func matchesOfficialCode(j records.CodeJob, r records.OfficialCodeReceipt) bool {
	if j.Official == nil || j.Manifest == nil || r.Operation != j.ID || r.Instance != j.Official.Instance || r.Dataset != j.Official.Dataset || r.User != j.Official.User || r.SourceDigest != j.Manifest.Digest || r.ExecutionEpoch != j.ExecutionEpoch {
		return false
	}
	return r.Status == "completed" || r.Status == "failed" || r.Status == "cancelled"
}

// Cancellation is sent once; failure leaves the persistent active slot. A new
// service epoch or absent task is never evidence that an old execution stopped.
func (s *Server) stopOfficialCode(ctx context.Context, j records.CodeJob, token string) (*records.OfficialCodeReceipt, error) {
	if s.sessionProcessing == nil || j.ExecutionEpoch == "" {
		return nil, errors.New("official execution unconfirmed")
	}
	data, _ := json.Marshal(officialCodePayload(j, s.sessionProcessing.capability))
	var result records.OfficialCodeReceipt
	if err := official.Request(ctx, j.Official.Endpoint, "/api/v1/memlane/code/index/cancel", "POST", token, "application/json", data, &result); err != nil {
		return nil, err
	}
	if !matchesOfficialCode(j, result) {
		return nil, errors.New("official execution unconfirmed")
	}
	return &result, nil
}

func (s *Server) refreshOfficialCode(ctx context.Context, c records.CodeState) {
	s.codeMu.Lock()
	current, err := s.store.Code(ctx, c.ID)
	if err != nil || s.codeStopping || !current.Enabled || current.Generation != c.Generation || current.Official == nil || s.codeRuns[c.ID] != nil {
		s.codeMu.Unlock()
		return
	}
	c = current
	if s.sessionProcessing == nil {
		_ = s.store.CodeFailure(ctx, c, "official_code_backend_unavailable")
		s.codeMu.Unlock()
		return
	}
	run, cancel := context.WithTimeout(ctx, 700*time.Second)
	if s.codeRuns == nil {
		s.codeRuns = map[string]context.CancelFunc{}
	}
	s.codeRuns[c.ID] = cancel
	s.codeMu.Unlock()
	defer func() { cancel(); s.codeMu.Lock(); delete(s.codeRuns, c.ID); s.codeMu.Unlock() }()
	if err = s.store.AuthorizeOfficialCode(run, c); err != nil {
		return
	}
	token, err := s.cogneeToken(c.Official.Instance, c.Official.Dataset)
	if err != nil || token == "" {
		_ = s.store.CodeFailure(ctx, c, "cognee_authorization_required")
		return
	}
	// Reconcile prior persisted execution before touching its source slot.
	jobs, err := s.store.CodeJobs(run, c.ID)
	if err != nil {
		return
	}
	for _, j := range jobs {
		if j.Official != nil && j.State == "running" {
			observe, done := context.WithTimeout(context.Background(), 10*time.Second)
			receipt, e := s.stopOfficialCode(observe, j, token)
			if e == nil {
				e = s.store.FinishOfficialCodeJob(observe, j, receipt, "interrupted")
			}
			if e != nil {
				_ = s.store.MarkOfficialCodeUnconfirmed(observe, j)
			}
			done()
			return // no automatic new execution after observation/reconciliation
		}
	}
	if c.State == "interrupted" || c.State == "cancelled" || c.State == "failed" && (c.ObservedDigest == "" || codeNeedsExplicitRetry(c.Error)) {
		return
	}
	epoch, err := officialCodeIdentity(run, *c.Official, token)
	if err != nil {
		_ = s.store.CodeFailure(ctx, c, "official_code_backend_unavailable")
		return
	}
	binary, err := officialCodeBinary(s.config.CodeExtractor)
	if err != nil {
		_ = s.store.CodeFailure(ctx, c, "code_component_required")
		return
	}
	m, err := codegraph.Capture(run, c.Worktree, "")
	if err != nil {
		_ = s.store.CodeFailure(ctx, c, "source_unavailable")
		return
	}
	if c.State != "queued" && m.Digest == c.ObservedDigest {
		return
	}
	j, err := s.store.StartOfficialCodeJob(run, uuid.NewString(), c, m)
	if err != nil || j.Replayed {
		return
	}
	// Before dispatch every failure is known not to have started SDK work.
	dispatched, stopped := false, true
	var receipt *records.OfficialCodeReceipt
	cause := "code_process_failed"
	var source codegraph.OfficialSource
	defer func() {
		finish, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if dispatched && !stopped {
			r, e := s.stopOfficialCode(finish, j, token)
			if e == nil {
				receipt = r
				stopped = true
				cause = "interrupted"
			}
		}
		if !stopped {
			_ = s.store.MarkOfficialCodeUnconfirmed(finish, j)
			return
		}
		if err := s.store.FinishOfficialCodeJob(finish, j, receipt, cause); err != nil {
			return
		}
		if source.Directory != "" {
			_ = s.clearOfficialSourceSlot(finish, source.Directory, c)
		}
	}()
	j, err = s.store.BindOfficialCodeEpoch(run, c, j.ID, epoch)
	if err != nil || j.Replayed {
		return
	}
	base := filepath.Join(s.config.Root, "official-code-sources")
	if err = os.MkdirAll(base, 0700); err != nil {
		return
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return
	}
	slot := filepath.Join(base, c.ID)
	if err = s.clearOfficialSourceSlot(run, slot, c); err != nil {
		return
	}
	source, err = codegraph.PrepareOfficialSource(run, c.Worktree, slot)
	if err != nil {
		return
	}
	if err = serviceipc.WriteJSON(filepath.Join(slot, "owner.json"), officialSourceMarker{j.ID, c.ID, m.Digest}); err != nil {
		return
	}
	if source.Manifest.Digest != m.Digest {
		cause = "source_changed"
		return
	}
	clientRoot := s.config.ClientRoot
	if clientRoot == "" {
		clientRoot = filepath.Dir(s.config.Root)
	}
	clientRoot, err = filepath.EvalSymlinks(clientRoot)
	if err != nil {
		return
	}
	root, err := filepath.EvalSymlinks(s.config.Root)
	if err != nil {
		return
	}
	denied := []string{c.Worktree, filepath.Join(root, "memlane.sqlite"), filepath.Join(root, "memlane.sqlite-wal"), filepath.Join(root, "memlane.sqlite-shm"), filepath.Join(root, "backups"), filepath.Join(clientRoot, "official", "cognee")}
	grant := records.OfficialCodeExecutionGrant{Operation: j.ID, Instance: c.Official.Instance, Dataset: c.Official.Dataset, User: c.Official.User, SourceDigest: m.Digest, ExecutionEpoch: epoch, Repository: source.Repository, Scratch: source.Directory, Binary: binary, BinarySHA256: officialCodeEnolaSHA, DenyReads: denied}
	owned := &officialCodeRun{run, c, j, grant}
	s.codeMu.Lock()
	if s.officialCodeRuns == nil {
		s.officialCodeRuns = map[string]*officialCodeRun{}
	}
	s.officialCodeRuns[j.ID] = owned
	s.codeMu.Unlock()
	defer func() { s.codeMu.Lock(); delete(s.officialCodeRuns, j.ID); s.codeMu.Unlock() }()
	if _, err = s.authorizeOfficialCodeExecution(run, s.sessionProcessing.capability, j.ID); err != nil {
		return
	}
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-run.Done():
		cause = "cancelled"
		return
	}
	body, _ := json.Marshal(officialCodePayload(j, s.sessionProcessing.capability))
	var result records.OfficialCodeReceipt
	dispatched, stopped = true, false
	err = official.Request(run, c.Official.Endpoint, "/api/v1/memlane/code/index", "POST", token, "application/json", body, &result)
	if err != nil || !matchesOfficialCode(j, result) {
		return
	}
	receipt = &result
	stopped = true
	cause = "official_pipeline_failed"
	if result.Status == "cancelled" {
		cause = "cancelled"
		return
	}
	if result.Status != "completed" {
		return
	}
	if err = source.Verify(run); err != nil {
		cause = "source_changed"
		return
	}
	if run.Err() != nil {
		cause = "cancelled"
		return
	}
	cause = ""
}
