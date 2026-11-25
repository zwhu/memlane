package bootstrap

import (
	"context"
	"io"

	"github.com/zwhu/memlane/kit/aiinstaller"
)

type InstallProgress = aiinstaller.InstallProgress

const DefaultProgressThrottle = aiinstaller.DefaultProgressThrottle

var (
	WithInstallProgress         = aiinstaller.WithInstallProgress
	WithInstallProgressInterval = aiinstaller.WithInstallProgressInterval
	progressComponent           = aiinstaller.ProgressComponent
	progressStage               = aiinstaller.ProgressStage
	progressDetail              = aiinstaller.ProgressDetail
	progressBatch               = aiinstaller.ProgressBatch
	progressAttempt             = aiinstaller.ProgressAttempt
	progressRuntimeSync         = aiinstaller.ProgressRuntimeSync
	progressSync                = aiinstaller.ProgressSync
)

type installProgressTracker struct{ ctx context.Context }

func progressTracker(ctx context.Context) *installProgressTracker {
	return &installProgressTracker{ctx: ctx}
}
func (p *installProgressTracker) setLength(total int64) {
	aiinstaller.ProgressSetLength(p.ctx, total)
}
func (p *installProgressTracker) finishTransfer(size int64, cached bool) {
	aiinstaller.ProgressFinishTransfer(p.ctx, size, cached)
}

type installProgressReader struct {
	io.Reader
	tracker *installProgressTracker
}

func (r installProgressReader) Read(b []byte) (int, error) {
	return aiinstaller.WrapProgressReader(r.tracker.ctx, r.Reader).Read(b)
}
