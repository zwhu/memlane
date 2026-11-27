package aiinstaller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Status string

const (
	Available    Status = "available"
	Missing      Status = "missing"
	Incompatible Status = "incompatible"
	Installing   Status = "installing"
	Failed       Status = "failed"
	Cancelled    Status = "cancelled"
	Ready        Status = "ready"
)

// ComponentSpec is immutable installation consent scope. Unknown sizes must be
// explicit in the UI; zero never means an unmeasured download is free.
type ComponentSpec struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Purpose        string `json:"purpose"`
	Version        string `json:"version"`
	Source         string `json:"source"`
	OfficialSource string `json:"official_source,omitempty"`
	Digest         string `json:"digest"`
	DownloadBytes  int64  `json:"download_bytes"`
	InstalledBytes int64  `json:"installed_bytes"`
	Destination    string `json:"destination"`
	SizeNote       string `json:"size_note,omitempty"`
}

type Observation struct {
	Status  Status `json:"status"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Reason  string `json:"reason,omitempty"`
	// Fingerprint binds approval to the selected external dependencies too.
	Fingerprint      string `json:"fingerprint,omitempty"`
	TransientTimeout bool   `json:"-"`
}

type Item struct {
	Requirement ComponentSpec `json:"requirement"`
	Observation Observation   `json:"observation"`
}

type Plan struct {
	Items []Item `json:"items"`
}

func (p Plan) Digest() string {
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Driver.Probe must never download, install, upgrade, initialize a content store,
// or mutate an external dependency. Install may only write the approved target.
type Driver interface {
	Probe(context.Context, ComponentSpec) (Observation, error)
	Install(context.Context, ComponentSpec) error
}

type Step struct {
	Status   Status `json:"status"`
	Attempts int    `json:"attempts"`
	Error    string `json:"error,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Log      string `json:"installer_log,omitempty"`
}

type Journal struct {
	Schema         int             `json:"schema"`
	Operation      string          `json:"operation"`
	Plan           Plan            `json:"plan"`
	ApprovedDigest string          `json:"approved_digest,omitempty"`
	DeclinedDigest string          `json:"declined_digest,omitempty"`
	Status         Status          `json:"status"`
	ActiveStep     string          `json:"active_step,omitempty"`
	Steps          map[string]Step `json:"steps"`
}

var ErrConsent = errors.New("installation plan requires confirmation")
var ErrPlanChanged = errors.New("dependencies changed; review the new installation plan")

type Installer struct {
	Root   string
	Driver Driver
	// CheckSpace optionally applies a caller-owned development resource policy.
	// The installed App does not inherit the checkout disk reserve.
	CheckSpace                  func(context.Context) error
	OnChange                    func(Journal)
	PostInstallProbeMaxAttempts int
	PostInstallProbeRetryDelay  time.Duration
	SpaceCheckInterval          time.Duration
}

func (i Installer) Detect(ctx context.Context, requirements []ComponentSpec) (Plan, error) {
	p := Plan{Items: make([]Item, 0, len(requirements))}
	seen := map[string]bool{}
	for _, r := range requirements {
		if r.ID == "" || seen[r.ID] || r.Name == "" || r.Purpose == "" || r.Version == "" || r.Source == "" || r.Digest == "" || !filepath.IsAbs(r.Destination) {
			return Plan{}, errors.New("incomplete or duplicate dependency specification")
		}
		seen[r.ID] = true
		o, err := i.Driver.Probe(ctx, r)
		if err != nil {
			return Plan{}, fmt.Errorf("detect %s: %w", r.ID, err)
		}
		if o.Status != Available && o.Status != Missing && o.Status != Incompatible {
			return Plan{}, fmt.Errorf("invalid detection status for %s", r.ID)
		}
		p.Items = append(p.Items, Item{r, o})
	}
	return p, nil
}

// Review persists an explicit choice. Merely detecting or opening the page does
// not grant permission. The exact displayed plan digest is required.
func (i Installer) Review(operation string, p Plan, displayedDigest string, approve bool) error {
	if operation == "" || displayedDigest != p.Digest() {
		return ErrPlanChanged
	}
	return i.WithLock(func() error { return i.ReviewLocked(operation, p, displayedDigest, approve) })
}

func (i Installer) ReviewLocked(operation string, p Plan, displayedDigest string, approve bool) error {
	if operation == "" || displayedDigest != p.Digest() {
		return ErrPlanChanged
	}
	j, err := i.read()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if j.Operation == "" {
		j = Journal{Schema: 1, Operation: operation, Steps: map[string]Step{}}
	}
	if j.Operation != operation {
		return ErrOperationIDMismatch
	}
	j.Plan = p
	j.ApprovedDigest, j.DeclinedDigest = "", ""
	if approve {
		j.ApprovedDigest = displayedDigest
	} else {
		j.DeclinedDigest = displayedDigest
	}
	return i.write(j)
}

// Run rechecks every component, including steps previously reported ready.
// An interrupted step is probed before it may be retried. Driver installs must
// therefore be resumable and must reject unknown/unowned destinations.
func (i Installer) Run(ctx context.Context, operation string, p Plan) error {
	return i.WithLock(func() error { return i.RunLocked(ctx, operation, p) })
}

func (i Installer) RunLocked(ctx context.Context, operation string, p Plan) error {
	j, err := i.read()
	if err != nil {
		return err
	}
	if j.Operation != operation || j.ApprovedDigest != p.Digest() || j.Plan.Digest() != p.Digest() {
		return ErrConsent
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if i.CheckSpace != nil {
		if err := i.CheckSpace(ctx); err != nil {
			return err
		}
		var cancel context.CancelCauseFunc
		ctx, cancel = monitorResources(ctx, i.CheckSpace, durationOr(i.SpaceCheckInterval, time.Second))
		defer cancel(nil)
	}
	j.Status = Installing
	if err := i.write(j); err != nil {
		return err
	}
	for _, item := range p.Items {
		r := item.Requirement
		j.ActiveStep = r.ID
		s := j.Steps[r.ID]
		fail := func(err error) error {
			if cause := context.Cause(ctx); cause != nil {
				err = cause
			}
			s.Status, s.Error, j.Status = Failed, err.Error(), Failed
			var failure *CommandFailure
			if errors.As(err, &failure) {
				code := failure.ExitCode
				s.Log = failure.Output
				if code >= 0 {
					s.ExitCode = &code
				}
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				s.Status, j.Status = Cancelled, Cancelled
			}
			j.Steps[r.ID] = s
			if writeErr := i.write(j); writeErr != nil {
				return errors.Join(err, writeErr)
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		ProgressComponent(ctx, r.ID, true)
		ProgressStage(ctx, "probing")
		o, err := i.Driver.Probe(ctx, r)
		if err != nil {
			return fail(err)
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if o.Status == Available {
			// A changed external selection is a new plan, even if compatible.
			if item.Observation.Status == Available && o != item.Observation {
				return fail(ErrPlanChanged)
			}
			s.Status, s.Error = Ready, ""
			s.ExitCode, s.Log = nil, ""
			j.Steps[r.ID] = s
			if err := i.write(j); err != nil {
				return err
			}
			ProgressStage(ctx, "ready")
			continue
		}
		// Consent to reuse an external component never authorizes installing
		// its replacement after it disappears or becomes incompatible.
		if item.Observation.Status == Available {
			return fail(ErrPlanChanged)
		}
		if o.Status != Missing && o.Status != Incompatible {
			return fail(errors.New("invalid probe result"))
		}
		if i.CheckSpace != nil {
			if err := i.CheckSpace(ctx); err != nil {
				return fail(err)
			}
		}
		s.Status, s.Error, s.Attempts = Installing, "", s.Attempts+1
		s.ExitCode, s.Log = nil, ""
		j.Steps[r.ID] = s
		if err := i.write(j); err != nil {
			return err
		}
		if err := i.Driver.Install(ctx, r); err != nil {
			return fail(err)
		}
		ProgressStage(ctx, "verifying")
		probeCtx := WithPostInstallProbe(ctx)
		maxAttempts := i.PostInstallProbeMaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 2
		}
		for attempt := 1; ; attempt++ {
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			o, err := i.Driver.Probe(probeCtx, r)
			if err != nil {
				return fail(err)
			}
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			if o.Status == Available {
				break
			}
			if !o.TransientTimeout || attempt >= maxAttempts {
				return fail(fmt.Errorf("installed component failed verification: component=%s status=%s reason=%q",
					r.ID, o.Status, RedactText(o.Reason)))
			}
			timer := time.NewTimer(durationOr(i.PostInstallProbeRetryDelay, 2*time.Second))
			select {
			case <-ctx.Done():
				timer.Stop()
				return fail(ctx.Err())
			case <-timer.C:
			}
		}
		s.Status, s.Error = Ready, ""
		j.Steps[r.ID] = s
		if err := i.write(j); err != nil {
			return err
		}
		ProgressStage(ctx, "ready")
	}
	j.Status = Ready
	j.ActiveStep = ""
	return i.write(j)
}

func monitorResources(parent context.Context, check func(context.Context) error, interval time.Duration) (context.Context, context.CancelCauseFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := check(ctx); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	return ctx, cancel
}

func (i Installer) Snapshot() (Journal, error) { return i.read() }

func (i Installer) read() (Journal, error) {
	var j Journal
	b, err := PrivateRead(filepath.Join(i.Root, "installation.json"))
	if err != nil {
		return j, err
	}
	if err = json.Unmarshal(b, &j); err != nil {
		return j, err
	}
	if j.Schema != 1 || j.Steps == nil {
		return j, errors.New("unsupported installation journal")
	}
	return j, nil
}

func (i Installer) WithLock(fn func() error) error {
	if err := PreparePrivateDir(i.Root); err != nil {
		return err
	}
	fd, err := syscall.Open(filepath.Join(i.Root, "installation.lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "installation.lock")
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("installation lock must be a private regular file")
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrInstallWorkerBusy
		}
		return err
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	return fn()
}

func (i Installer) write(j Journal) error {
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if err := AtomicPrivateFile(filepath.Join(i.Root, "installation.json"), b); err != nil {
		return err
	}
	if i.OnChange != nil {
		var snapshot Journal
		if err := json.Unmarshal(b, &snapshot); err != nil {
			return err
		}
		i.OnChange(snapshot)
	}
	return nil
}
func (p Plan) Complete() bool {
	if len(p.Items) == 0 {
		return false
	}
	for _, item := range p.Items {
		if item.Observation.Status != Available {
			return false
		}
	}
	return true
}

type postInstallProbeKey struct{}

func WithPostInstallProbe(ctx context.Context) context.Context {
	return context.WithValue(ctx, postInstallProbeKey{}, true)
}

func IsPostInstallProbe(ctx context.Context) bool {
	marked, _ := ctx.Value(postInstallProbeKey{}).(bool)
	return marked
}

type CommandFailure struct {
	ExitCode int
	Output   string
}

func (e *CommandFailure) Error() string {
	if e.ExitCode < 0 {
		return "安装命令未能启动：" + e.Output
	}
	return fmt.Sprintf("安装命令退出 %d：%s", e.ExitCode, e.Output)
}

func durationOr(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}
func PreparePrivateDir(root string) error {
	if !filepath.IsAbs(root) {
		return errors.New("absolute installation state path required")
	}
	for p := filepath.Clean(root); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("installation state path traverses symlink")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("installation state directory must be private")
	}
	return nil
}

func ClaimComponentRoot(root, marker string, identity []byte) error {
	if !filepath.IsAbs(root) {
		return errors.New("absolute managed component destination required")
	}
	root = filepath.Clean(root)
	for p := root; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("component destination traverses symlink")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	if info, err := os.Lstat(root); err == nil {
		if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return errors.New("component destination must be a private directory")
		}
		b, err := PrivateRead(filepath.Join(root, marker))
		if err != nil || string(b) != string(identity) {
			return errors.New("unowned component destination retained unchanged")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(root, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, marker), identity, 0600)
}
