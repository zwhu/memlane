package supervisor

import (
	"context"
	"net/http"
	"syscall"
	"time"

	"github.com/zwhu/memlane/kit/daemon"
)

type ServiceState = daemon.ServiceState

const (
	StatePending  = daemon.StatePending
	StateStarting = daemon.StateStarting
	StateReady    = daemon.StateReady
	StateDegraded = daemon.StateDegraded
	StateStopped  = daemon.StateStopped
	StateExited   = daemon.StateExited
)

type ServiceSpec = daemon.ServiceSpec
type HTTPStatusError = daemon.HTTPStatusError
type ExitedBeforeReadyError = daemon.ExitedBeforeReadyError
type ServiceStatus = daemon.ServiceStatus
type ManagedProcess = daemon.ManagedProcess

func ValidateLoopbackURL(raw string) (string, error) {
	return daemon.ValidateLoopbackURL(raw)
}

func CheckPortFree(rawURL string) error {
	return daemon.CheckPortFree(rawURL)
}

func StartManagedProcess(spec ServiceSpec, onUnexpectedExit func(ServiceStatus)) (*ManagedProcess, error) {
	if spec.LogRedactor == nil {
		spec.LogRedactor = serviceLogRedactor
	}
	return daemon.StartManagedProcess(spec, onUnexpectedExit)
}

func ProcessGroupExists(pid int) bool {
	return daemon.ProcessGroupExists(pid)
}

func trackDescendantsFromTable(roots []int, known map[int]string, table map[int]ProcessInfo) bool {
	return daemon.TrackDescendantsFromTable(roots, known, table)
}

func StopTrackedDescendants(ctx context.Context, known map[int]string, grace, killWait time.Duration) error {
	return daemon.StopTrackedDescendants(ctx, known, grace, killWait)
}

func stopTrackedDescendantsWith(ctx context.Context, known map[int]string, grace, killWait time.Duration, tableFn func(context.Context) (map[int]ProcessInfo, error), signalFn func(int, syscall.Signal) error) error {
	return daemon.StopTrackedDescendantsWith(ctx, known, grace, killWait, tableFn, signalFn)
}

func NewLoopbackClient(timeout time.Duration) *http.Client {
	return daemon.NewLoopbackClient(timeout)
}

func LoopbackGetJSON(ctx context.Context, origin, endpoint string, headers map[string]string, timeout time.Duration) (map[string]any, error) {
	return daemon.LoopbackGetJSON(ctx, origin, endpoint, headers, timeout)
}

func WaitReady(ctx context.Context, proc *ManagedProcess, check func(context.Context) (bool, error), timeout, pollInterval time.Duration, allowCleanExit bool) error {
	return daemon.WaitReady(ctx, proc, check, timeout, pollInterval, allowCleanExit)
}

func WaitHTTPReady(ctx context.Context, proc *ManagedProcess, rawURL string, headers map[string]string, timeout time.Duration) error {
	return daemon.WaitHTTPReady(ctx, proc, rawURL, headers, timeout)
}
