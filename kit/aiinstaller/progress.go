package aiinstaller

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"slices"
	"sync"
	"time"
)

const DefaultProgressThrottle = 200 * time.Millisecond

type InstallProgress struct {
	Component              string        `json:"component"`
	Stage                  string        `json:"stage"`
	Detail                 string        `json:"detail,omitempty"`
	Mirror                 string        `json:"mirror,omitempty"`
	FallbackAttempt        int           `json:"fallback_attempt,omitempty"`
	FallbackTotal          int           `json:"fallback_total,omitempty"`
	BytesDone              int64         `json:"bytes_done"`
	BytesTotal             int64         `json:"bytes_total"`
	NetworkBytesDownloaded int64         `json:"network_bytes_downloaded,omitempty"`
	RuntimeSyncRan         bool          `json:"runtime_sync_ran,omitempty"`
	Indeterminate          bool          `json:"indeterminate"`
	BytesPerSec            float64       `json:"bytes_per_sec,omitempty"`
	Elapsed                time.Duration `json:"-"`
	ElapsedMs              int64         `json:"elapsed_ms,omitempty"`
	UpdatedAt              time.Time     `json:"updated_at"`
}

type installProgressKey struct{}

type installProgressTracker struct {
	mu                              sync.Mutex
	fn                              func(InstallProgress)
	interval                        time.Duration
	value                           InstallProgress
	startedAt, lastSample, lastEmit time.Time
	sampleBytes                     int64
	networkBytes                    int64
	runtimeSyncRan                  bool
	batchBaseBytes, batchTotalBytes int64
}

func WithInstallProgress(ctx context.Context, fn func(InstallProgress)) context.Context {
	return WithInstallProgressInterval(ctx, DefaultProgressThrottle, fn)
}

// WithInstallProgressInterval delivers snapshots synchronously in emission order.
// An interval of zero emits every read tick.
func WithInstallProgressInterval(ctx context.Context, interval time.Duration, fn func(InstallProgress)) context.Context {
	return context.WithValue(ctx, installProgressKey{}, &installProgressTracker{fn: fn, interval: interval})
}

func progressTracker(ctx context.Context) *installProgressTracker {
	p, _ := ctx.Value(installProgressKey{}).(*installProgressTracker)
	return p
}

func (p *installProgressTracker) emit(now time.Time) {
	p.lastEmit = now
	v := p.value
	v.NetworkBytesDownloaded = p.networkBytes
	v.RuntimeSyncRan = p.runtimeSyncRan
	v.BytesDone += p.batchBaseBytes
	if p.batchTotalBytes > 0 {
		v.BytesTotal = p.batchTotalBytes
	}
	v.UpdatedAt = now.UTC()
	v.Elapsed = now.Sub(p.startedAt)
	v.ElapsedMs = v.Elapsed.Milliseconds()
	if p.fn != nil {
		p.fn(v)
	}
}

func ProgressComponent(ctx context.Context, id string, replace bool) {
	if p := progressTracker(ctx); p != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		if replace || p.value.Component == "" {
			p.value = InstallProgress{Component: id}
			p.batchBaseBytes, p.batchTotalBytes = 0, 0
			p.startedAt = time.Now()
		}
	}
}

func ProgressStage(ctx context.Context, stage string) {
	if p := progressTracker(ctx); p != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		now := time.Now()
		p.startedAt = now
		p.value.Stage = stage
		p.value.Indeterminate = stage != "ready"
		p.emit(now)
	}
}

func ProgressDetail(ctx context.Context, detail string) {
	if p := progressTracker(ctx); p != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.value.Detail = detail
	}
}

func ProgressBatch(ctx context.Context, total int64) {
	if p := progressTracker(ctx); p != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.batchBaseBytes, p.batchTotalBytes = 0, total
	}
}

func ProgressAttempt(ctx context.Context, stage, rawURL string, attempt, total int) {
	if p := progressTracker(ctx); p != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		host := ""
		if u, err := url.Parse(rawURL); err == nil {
			host = u.Host
		}
		now := time.Now()
		p.value = InstallProgress{
			Component: p.value.Component, Detail: p.value.Detail,
			Stage: stage, Mirror: host, FallbackAttempt: attempt, FallbackTotal: total,
			Indeterminate: true,
		}
		p.startedAt, p.lastSample, p.sampleBytes = now, now, 0
		p.emit(now)
	}
}

func (p *installProgressTracker) setLength(total int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.value.BytesTotal = max(0, total)
	p.value.Indeterminate = total <= 0
	now := time.Now()
	p.lastSample = now
	p.emit(now)
}

func (p *installProgressTracker) emitTransfer(now time.Time) {
	if elapsed := now.Sub(p.lastSample).Seconds(); elapsed > 0 && p.value.BytesDone > p.sampleBytes {
		rate := float64(p.value.BytesDone-p.sampleBytes) / elapsed
		if p.value.BytesPerSec == 0 {
			p.value.BytesPerSec = rate
		} else {
			p.value.BytesPerSec = 0.25*rate + 0.75*p.value.BytesPerSec
		}
		p.lastSample, p.sampleBytes = now, p.value.BytesDone
	}
	p.emit(now)
}

func (p *installProgressTracker) finishTransfer(size int64, cached bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.value.BytesDone = size
	if cached {
		p.value.BytesTotal, p.value.Indeterminate = size, false
		p.emit(time.Now())
	} else {
		p.emitTransfer(time.Now())
	}
	if p.batchTotalBytes > 0 {
		p.batchBaseBytes += size
		p.value.BytesDone = 0
	}
}

type installProgressReader struct {
	io.Reader
	tracker *installProgressTracker
}

func (r installProgressReader) Read(b []byte) (int, error) {
	n, err := r.Reader.Read(b)
	if n > 0 {
		p := r.tracker
		p.mu.Lock()
		p.value.BytesDone += int64(n)
		p.networkBytes += int64(n)
		now := time.Now()
		if now.Sub(p.lastEmit) >= p.interval {
			p.emitTransfer(now)
		}
		p.mu.Unlock()
	}
	return n, err
}

func ProgressRuntimeSync(ctx context.Context) {
	if p := progressTracker(ctx); p != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.runtimeSyncRan = true
	}
}

func ProgressSync(ctx context.Context, detail, index string, attempt, total int) func() {
	p := progressTracker(ctx)
	if p == nil {
		return func() {}
	}
	ProgressDetail(ctx, detail)
	ProgressAttempt(ctx, "syncing", index, attempt, total)
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.mu.Lock()
				p.emit(time.Now())
				p.mu.Unlock()
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

func ProgressSetLength(ctx context.Context, total int64) {
	if tracker := progressTracker(ctx); tracker != nil {
		tracker.setLength(total)
	}
}
func ProgressFinishTransfer(ctx context.Context, size int64, cached bool) {
	if tracker := progressTracker(ctx); tracker != nil {
		tracker.finishTransfer(size, cached)
	}
}
func WrapProgressReader(ctx context.Context, reader io.Reader) io.Reader {
	if tracker := progressTracker(ctx); tracker != nil {
		return installProgressReader{Reader: reader, tracker: tracker}
	}
	return reader
}

// RunWorkerComponentsLocked verifies the plan and installs the requested components.
// The caller must hold the installation lock.
func RunWorkerComponentsLocked(ctx context.Context, cfg WorkerConfig, options RunWorkerOptions, w *WorkerReceipt) error {
	pids, err := DetectOrphanProcesses(ctx, options.Processes, cfg.ComponentRoot, options.OrphanBinaries)
	if err != nil {
		return err
	}
	if len(pids) > 0 {
		return ErrOrphanInstallProcess
	}
	if err := w.writeWorkerState(func(s *WorkerState) { s.State, s.Stage = "running", "probing" }); err != nil {
		return err
	}
	full, err := options.Detect(ctx)
	if err != nil {
		return err
	}
	digest := full.Digest()
	if err := w.writeWorkerState(func(s *WorkerState) { s.PlanDigest = digest }); err != nil {
		return err
	}
	if cfg.PlanDigest != "" && digest != cfg.PlanDigest {
		return ErrPlanChanged
	}
	runPlan := full
	if len(cfg.Components) > 0 {
		runPlan.Items = nil
		for _, item := range full.Items {
			if slices.Contains(cfg.Components, item.Requirement.ID) {
				runPlan.Items = append(runPlan.Items, item)
			}
		}
		for _, id := range cfg.Components {
			if !slices.ContainsFunc(runPlan.Items, func(item Item) bool { return item.Requirement.ID == id }) {
				return fmt.Errorf("%w: requested component is absent from the plan", ErrPlanChanged)
			}
		}
	}
	progressCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	baseNetworkBytes := w.state.NetworkBytesDownloaded
	runCtx := WithInstallProgressInterval(progressCtx, 500*time.Millisecond, func(p InstallProgress) {
		err := w.writeWorkerState(func(s *WorkerState) {
			s.Component, s.Stage, s.Detail, s.Mirror = p.Component, p.Stage, p.Detail, p.Mirror
			s.BytesDone, s.BytesTotal, s.Indeterminate = p.BytesDone, p.BytesTotal, p.Indeterminate
			if totalNetworkBytes := baseNetworkBytes + p.NetworkBytesDownloaded; totalNetworkBytes > s.NetworkBytesDownloaded {
				s.NetworkBytesDownloaded = totalNetworkBytes
			}
			s.RuntimeSyncRan = s.RuntimeSyncRan || p.RuntimeSyncRan
			s.BytesPerSec, s.ElapsedMs = p.BytesPerSec, p.ElapsedMs
			s.FallbackAttempt, s.FallbackTotal = p.FallbackAttempt, p.FallbackTotal
			if p.Stage == "ready" {
				s.FallbackAttempt, s.FallbackTotal = 0, 0
			}
		})
		if err != nil {
			cancel(err)
		}
	})
	if err := options.Installer.ReviewLocked(cfg.OperationID, runPlan, runPlan.Digest(), true); err != nil {
		return err
	}
	if err := options.Installer.RunLocked(runCtx, cfg.OperationID, runPlan); err != nil {
		return err
	}
	return context.Cause(runCtx)
}

// artifactStallReader gives each successful read a fresh idle window. A timer
// callback already waiting for the lock checks the latest deadline before firing.
type artifactStallReader struct {
	body     io.Reader
	idle     time.Duration
	cancel   context.CancelCauseFunc
	mu       sync.Mutex
	timer    *time.Timer
	deadline time.Time
	stopped  bool
}

func newArtifactStallReader(body io.Reader, idle time.Duration, cancel context.CancelCauseFunc) *artifactStallReader {
	r := &artifactStallReader{body: body, idle: idle, cancel: cancel}
	r.mu.Lock()
	r.deadline = time.Now().Add(idle)
	r.timer = time.AfterFunc(idle, r.check)
	r.mu.Unlock()
	return r
}

func (r *artifactStallReader) check() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	if remaining := time.Until(r.deadline); remaining > 0 {
		r.timer.Reset(remaining)
		return
	}
	r.stopped = true
	r.cancel(ErrArtifactStalled)
}

func (r *artifactStallReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if n > 0 {
		r.mu.Lock()
		if !r.stopped {
			r.deadline = time.Now().Add(r.idle)
			if r.timer.Stop() {
				r.timer.Reset(r.idle)
			}
		}
		r.mu.Unlock()
	}
	return n, err
}

func (r *artifactStallReader) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	r.timer.Stop()
}
