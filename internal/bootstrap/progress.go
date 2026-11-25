package bootstrap

import (
	"context"
	"io"
	"net/url"
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

func progressComponent(ctx context.Context, id string, replace bool) {
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

func progressStage(ctx context.Context, stage string) {
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

func progressDetail(ctx context.Context, detail string) {
	if p := progressTracker(ctx); p != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.value.Detail = detail
	}
}

func progressBatch(ctx context.Context, total int64) {
	if p := progressTracker(ctx); p != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.batchBaseBytes, p.batchTotalBytes = 0, total
	}
}

func progressAttempt(ctx context.Context, stage, rawURL string, attempt, total int) {
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

func progressRuntimeSync(ctx context.Context) {
	if p := progressTracker(ctx); p != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.runtimeSyncRan = true
	}
}

func progressSync(ctx context.Context, detail, index string, attempt, total int) func() {
	p := progressTracker(ctx)
	if p == nil {
		return func() {}
	}
	progressDetail(ctx, detail)
	progressAttempt(ctx, "syncing", index, attempt, total)
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
