package main

import (
	"context"
	"time"

	"github.com/zwhu/memlane/kit/daemon"
)

type quitCoordinator = daemon.QuitCoordinator

const defaultShutdownTimeout = daemon.DefaultShutdownTimeout

func newQuitCoordinator(
	keepAlive func() bool,
	shutdown func(context.Context) error,
	requestQuit func(),
	timeout time.Duration,
	logf func(string, ...any),
) *quitCoordinator {
	return daemon.NewQuitCoordinator(daemon.QuitCoordinatorConfig{
		KeepAlive:           keepAlive,
		Shutdown:            shutdown,
		RequestQuit:         requestQuit,
		ShutdownTimeout:     timeout,
		Logf:                logf,
		ShutdownErrorFormat: "停止本地后台服务失败，本地服务可能仍在运行，所有权锁可能仍被持有：%v",
		PanicErrorFormat:    "本地后台服务停服发生 panic: %v",
	})
}
