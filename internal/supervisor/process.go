package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ServiceState describes the lifecycle state of a managed process or supervisor.
type ServiceState string

const (
	StatePending  ServiceState = "pending"
	StateStarting ServiceState = "starting"
	StateReady    ServiceState = "ready"
	StateDegraded ServiceState = "degraded"
	StateStopped  ServiceState = "stopped"
	StateExited   ServiceState = "exited"
)

// ServiceSpec defines one managed background process supervised in its own process group.
type ServiceSpec struct {
	Role           string
	Command        string
	Args           []string
	Dir            string
	Env            []string
	LogPath        string
	LogLimitBytes  int64
	BindURL        string
	HealthURL      string
	HealthHeaders  map[string]string
	ReadyCheck     func(context.Context) (bool, error)
	HealthCheck    func(context.Context) error
	AllowCleanExit bool
	ReadyTimeout   time.Duration
	StopGrace      time.Duration
	KillWait       time.Duration
}

// HTTPStatusError reports a non-200 HTTP response from a loopback probe.
type HTTPStatusError struct {
	StatusCode int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("loopback endpoint returned HTTP %d", e.StatusCode)
}

// ExitedBeforeReadyError reports a managed process that exited before readiness succeeded.
type ExitedBeforeReadyError struct {
	Status ServiceStatus
}

func (e *ExitedBeforeReadyError) Error() string {
	msg := fmt.Sprintf("owned service %s exited before readiness", e.Status.Role)
	if e.Status.LastError != "" {
		msg += ": " + e.Status.LastError
	} else if e.Status.Signal != "" {
		msg += ": signal " + e.Status.Signal
	} else if e.Status.ExitCode != nil {
		msg += fmt.Sprintf(": exit status %d", *e.Status.ExitCode)
	}
	return msg
}

// ServiceStatus captures an immutable snapshot of a managed process.
type ServiceStatus struct {
	Role      string       `json:"role"`
	PID       int          `json:"pid"`
	State     ServiceState `json:"state"`
	StartedAt time.Time    `json:"started_at"`
	ExitedAt  *time.Time   `json:"exited_at,omitempty"`
	ExitCode  *int         `json:"exit_code,omitempty"`
	Signal    string       `json:"signal,omitempty"`
	TailLines []string     `json:"tail_lines,omitempty"`
	LastError string       `json:"last_error,omitempty"`
}

func parseLoopbackURL(raw string, requireRootPath bool) (origin string, port string, target string, err error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", "", err
	}
	portNum, portErr := strconv.Atoi(parsed.Port())
	if portErr != nil || portNum < 1 || portNum > 65535 ||
		parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", "", errors.New("service URL must be an explicit loopback HTTP endpoint")
	}
	if requireRootPath && parsed.Path != "" && parsed.Path != "/" {
		return "", "", "", errors.New("service origin must not carry a path")
	}
	origin = "http://127.0.0.1:" + parsed.Port()
	path := parsed.Path
	if path == "" {
		path = "/"
	}
	return origin, parsed.Port(), origin + path, nil
}

// ValidateLoopbackURL verifies raw is an http://127.0.0.1:<port> origin with no query, userinfo, or sub-path.
func ValidateLoopbackURL(raw string) (string, error) {
	origin, _, _, err := parseLoopbackURL(raw, true)
	if err != nil {
		return "", err
	}
	return origin, nil
}

// CheckPortFree verifies nobody is currently listening on the validated loopback port.
func CheckPortFree(rawURL string) error {
	_, port, _, err := parseLoopbackURL(rawURL, true)
	if err != nil {
		return err
	}
	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var sockErr error
			if err := c.Control(func(fd uintptr) {
				sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
			}); err != nil {
				return err
			}
			return sockErr
		},
	}
	ln, err := lc.Listen(context.Background(), "tcp4", "127.0.0.1:"+port)
	if err != nil {
		return err
	}
	return ln.Close()
}

// ManagedProcess owns one child started in a detached session/process group.
type ManagedProcess struct {
	spec ServiceSpec
	cmd  *exec.Cmd
	pid  int
	done chan struct{}

	mu       sync.Mutex
	stopping bool
	exited   bool
	status   ServiceStatus
}

// StartManagedProcess launches spec inside its own macOS session/process group
// with private 0600 log output and a reaper goroutine that never auto-restarts.
func StartManagedProcess(spec ServiceSpec, onUnexpectedExit func(ServiceStatus)) (*ManagedProcess, error) {
	if strings.TrimSpace(spec.Role) == "" {
		return nil, errors.New("service role is required")
	}
	if strings.TrimSpace(spec.Command) == "" {
		return nil, errors.New("service command is required")
	}
	if spec.LogPath == "" {
		return nil, errors.New("service log path is required")
	}
	if err := TrimServiceLog(spec.LogPath, spec.LogLimitBytes); err != nil {
		return nil, err
	}
	logFile, err := OpenPrivateLog(spec.LogPath)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(spec.Command, spec.Args...)
	if spec.Dir != "" {
		cmd.Dir = spec.Dir
	}
	if spec.Env != nil {
		cmd.Env = slices.Clone(spec.Env)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	startedAt := time.Now().UTC()
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	_ = logFile.Close()

	specCopy := spec
	specCopy.Args = slices.Clone(spec.Args)
	specCopy.Env = slices.Clone(spec.Env)
	proc := &ManagedProcess{
		spec: specCopy,
		cmd:  cmd,
		pid:  cmd.Process.Pid,
		done: make(chan struct{}),
		status: ServiceStatus{
			Role:      spec.Role,
			PID:       cmd.Process.Pid,
			State:     StateStarting,
			StartedAt: startedAt,
		},
	}

	go func() {
		waitErr := cmd.Wait()
		exitedAt := time.Now().UTC()

		var exitCodePtr *int
		var signalName string
		if ps := cmd.ProcessState; ps != nil {
			code := ps.ExitCode()
			exitCodePtr = &code
			if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				signalName = ws.Signal().String()
			}
		}
		tail, _ := ReadLogTail(proc.spec.LogPath, 50, 64*1024)

		proc.mu.Lock()
		stoppedByUs := proc.stopping
		cleanAllowed := proc.spec.AllowCleanExit && exitCodePtr != nil && *exitCodePtr == 0 && signalName == ""
		proc.exited = true
		if stoppedByUs || cleanAllowed {
			proc.status.State = StateStopped
		} else {
			proc.status.State = StateExited
			if waitErr != nil {
				proc.status.LastError = waitErr.Error()
			}
		}
		proc.status.ExitedAt = &exitedAt
		proc.status.ExitCode = exitCodePtr
		proc.status.Signal = signalName
		proc.status.TailLines = tail
		snapshot := cloneServiceStatus(proc.status)
		proc.mu.Unlock()

		close(proc.done)
		if !stoppedByUs && !cleanAllowed && onUnexpectedExit != nil {
			onUnexpectedExit(snapshot)
		}
	}()

	return proc, nil
}

// ProcessGroupExists reports whether any process remains in process group pid.
func ProcessGroupExists(pid int) bool {
	if pid <= 1 {
		return false
	}
	err := syscall.Kill(-pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.ESRCH) {
		return false
	}
	if errors.Is(err, syscall.EPERM) {
		out, psErr := exec.Command("/bin/ps", "-axo", "pgid=").Output()
		if psErr != nil {
			return true
		}
		target := strconv.Itoa(pid)
		for _, row := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(row) == target {
				return true
			}
		}
		return false
	}
	return false
}

func isChannelClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// StopGroup terminates the entire child process group using SIGTERM then SIGKILL.
func (p *ManagedProcess) StopGroup(grace, killWait time.Duration) error {
	if p == nil {
		return nil
	}
	if grace <= 0 {
		grace = 15 * time.Second
	}
	if killWait <= 0 {
		killWait = 5 * time.Second
	}

	p.mu.Lock()
	p.stopping = true
	if p.exited {
		p.status.State = StateStopped
	}
	p.mu.Unlock()

	if isChannelClosed(p.done) && !ProcessGroupExists(p.pid) {
		return nil
	}

	phases := []struct {
		sig  syscall.Signal
		wait time.Duration
	}{
		{syscall.SIGTERM, grace},
		{syscall.SIGKILL, killWait},
	}

	for _, phase := range phases {
		if isChannelClosed(p.done) && !ProcessGroupExists(p.pid) {
			return nil
		}
		if err := syscall.Kill(-p.pid, phase.sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			if errors.Is(err, syscall.EPERM) {
				if ProcessGroupExists(p.pid) {
					return err
				}
			} else {
				return err
			}
		}
		deadline := time.Now().Add(phase.wait)
		for {
			if isChannelClosed(p.done) && !ProcessGroupExists(p.pid) {
				return nil
			}
			if !time.Now().Before(deadline) {
				break
			}
			select {
			case <-p.done:
				if !ProcessGroupExists(p.pid) {
					return nil
				}
				time.Sleep(25 * time.Millisecond)
			case <-time.After(25 * time.Millisecond):
			}
		}
	}

	if isChannelClosed(p.done) && !ProcessGroupExists(p.pid) {
		return nil
	}
	return fmt.Errorf("owned process group exit unconfirmed: %d", p.pid)
}

// PID returns the OS process identifier of the direct child.
func (p *ManagedProcess) PID() int {
	return p.pid
}

// Spec returns a defensive copy of the process specification.
func (p *ManagedProcess) Spec() ServiceSpec {
	return p.spec
}

// Done closes once cmd.Wait completes and status fields are populated.
func (p *ManagedProcess) Done() <-chan struct{} {
	return p.done
}

// Exited reports whether cmd.Wait has already completed.
func (p *ManagedProcess) Exited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited
}

// SetHealthState updates health-derived state only while the process is still running.
func (p *ManagedProcess) SetHealthState(state ServiceState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exited || p.stopping {
		return
	}
	p.status.State = state
	if state == StateReady {
		p.status.LastError = ""
	}
}

// SetLastError records the most recent diagnostic message if still running.
func (p *ManagedProcess) SetLastError(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exited || p.stopping {
		return
	}
	p.status.LastError = msg
}

// Status returns a deep-copied snapshot of the process status.
func (p *ManagedProcess) Status() ServiceStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneServiceStatus(p.status)
}

func cloneServiceStatus(in ServiceStatus) ServiceStatus {
	out := in
	if in.ExitedAt != nil {
		t := *in.ExitedAt
		out.ExitedAt = &t
	}
	if in.ExitCode != nil {
		c := *in.ExitCode
		out.ExitCode = &c
	}
	out.TailLines = slices.Clone(in.TailLines)
	return out
}

func newLoopbackProbeClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = time.Second
	}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// NewLoopbackClient returns an HTTP client configured for bounded, no-redirect loopback requests.
func NewLoopbackClient(timeout time.Duration) *http.Client {
	return newLoopbackProbeClient(timeout)
}

// LoopbackGetJSON sends a GET request to a validated 127.0.0.1 HTTP origin + endpoint
// and decodes a bounded (<= 2 MiB) JSON object response without following redirects.
func LoopbackGetJSON(ctx context.Context, origin, endpoint string, headers map[string]string, timeout time.Duration) (map[string]any, error) {
	validOrigin, err := ValidateLoopbackURL(origin)
	if err != nil {
		return nil, err
	}
	target := strings.TrimRight(validOrigin, "/") + endpoint
	if _, _, _, err := parseLoopbackURL(target, false); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := newLoopbackProbeClient(timeout)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode}
	}
	const maxBodyBytes = 2 << 20
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBodyBytes {
		return nil, errors.New("loopback JSON response exceeds size limit")
	}
	var result map[string]any
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// WaitReady polls check until it returns (true, nil), a fatal error, context expiry,
// or unexpected exit of proc. If timeout <= 0, wait honors ctx's own deadline directly.
func WaitReady(ctx context.Context, proc *ManagedProcess, check func(context.Context) (bool, error), timeout, pollInterval time.Duration, allowCleanExit bool) error {
	if check == nil {
		return errors.New("readiness check is required")
	}
	if pollInterval <= 0 {
		pollInterval = 50 * time.Millisecond
	}
	waitCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	var procDone <-chan struct{}
	if proc != nil {
		procDone = proc.Done()
	}

	checkExited := func() (bool, error) {
		if procDone == nil {
			return false, nil
		}
		select {
		case <-procDone:
			st := proc.Status()
			if allowCleanExit && st.ExitCode != nil && *st.ExitCode == 0 && st.Signal == "" {
				procDone = nil
				return false, nil
			}
			return true, &ExitedBeforeReadyError{Status: st}
		default:
			return false, nil
		}
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		if exited, err := checkExited(); exited {
			return err
		}
		if waitCtx.Err() != nil {
			return waitCtx.Err()
		}
		ready, err := check(waitCtx)
		if err != nil {
			if exited, exitErr := checkExited(); exited {
				return exitErr
			}
			if waitCtx.Err() != nil {
				return waitCtx.Err()
			}
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-procDone:
			st := proc.Status()
			if allowCleanExit && st.ExitCode != nil && *st.ExitCode == 0 && st.Signal == "" {
				procDone = nil
				continue
			}
			return &ExitedBeforeReadyError{Status: st}
		case <-waitCtx.Done():
			return waitCtx.Err()
		case <-ticker.C:
		}
	}
}

// WaitHTTPReady polls a validated loopback HTTP endpoint until HTTP 200 or proc exits.
func WaitHTTPReady(ctx context.Context, proc *ManagedProcess, rawURL string, headers map[string]string, timeout time.Duration) error {
	_, _, targetURL, err := parseLoopbackURL(rawURL, false)
	if err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := newLoopbackProbeClient(time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	probeOnce := func() bool {
		req, reqErr := http.NewRequestWithContext(probeCtx, http.MethodGet, targetURL, nil)
		if reqErr != nil {
			return false
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, doErr := client.Do(req)
		if doErr != nil {
			return false
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}

	for {
		if proc != nil {
			select {
			case <-proc.Done():
				return fmt.Errorf("owned service %s exited before readiness", proc.spec.Role)
			default:
			}
		}
		if probeOnce() {
			return nil
		}
		if proc != nil {
			select {
			case <-proc.Done():
				return fmt.Errorf("owned service %s exited before readiness", proc.spec.Role)
			case <-probeCtx.Done():
				return fmt.Errorf("service readiness timed out: %s (%w)", targetURL, probeCtx.Err())
			case <-ticker.C:
			}
		} else {
			select {
			case <-probeCtx.Done():
				return fmt.Errorf("service readiness timed out: %s (%w)", targetURL, probeCtx.Err())
			case <-ticker.C:
			}
		}
	}
}
