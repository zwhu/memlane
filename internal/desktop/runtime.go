package desktop

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/zwhu/memlane/internal/components"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/platform"
	"github.com/zwhu/memlane/internal/serviceipc"
)

// RuntimeConfig is distributable configuration, containing a public key only.
// Tests pass its path explicitly; no default memory or Agent config is read.
type RuntimeConfig struct {
	Root                 string `json:"root"`
	ComponentSource      string `json:"component_source"`
	ComponentPublicKey   string `json:"component_public_key"`
	WorkerID             string `json:"worker_id"`
	ModelID              string `json:"model_id"`
	ServicePlist         string `json:"service_plist"`
	AllowLocalTestSource bool   `json:"allow_local_test_source,omitempty"`
	UpdateManifestURL    string `json:"update_manifest_url,omitempty"`
	UpdateCAPEM          string `json:"update_ca_pem,omitempty"`
}

type RuntimeStatus struct {
	Phase                   string
	Message                 string
	Downloaded, Total       int64
	EngineBytes, ModelBytes int64
	Installed               bool
	Service                 platform.ServiceState
}

type RuntimeController struct {
	Config RuntimeConfig
	mu     sync.Mutex
	busy   bool
	cancel context.CancelFunc
}

func LoadRuntimeConfig(path string) (RuntimeConfig, error) {
	var cfg RuntimeConfig
	if !filepath.IsAbs(path) {
		return cfg, errors.New("runtime config path must be explicit and absolute")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if len(data) > 65536 {
		return cfg, errors.New("runtime config too large")
	}
	if err = json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if !filepath.IsAbs(cfg.Root) || cfg.ComponentSource == "" || cfg.WorkerID == "" || cfg.ModelID == "" || cfg.ServicePlist == "" {
		return cfg, errors.New("incomplete runtime configuration")
	}
	return cfg, nil
}

func (r *RuntimeController) Cancel() {
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *RuntimeController) Prepare(ctx context.Context, notify func(RuntimeStatus)) error {
	r.mu.Lock()
	if r.busy {
		r.mu.Unlock()
		return errors.New("component preparation already running")
	}
	ctx, cancel := context.WithCancel(ctx)
	r.busy = true
	r.cancel = cancel
	r.mu.Unlock()
	defer func() { cancel(); r.mu.Lock(); r.busy = false; r.cancel = nil; r.mu.Unlock() }()
	if notify == nil {
		notify = func(RuntimeStatus) {}
	}
	notify(RuntimeStatus{Phase: "checking", Message: "正在检查本地组件…"})
	key, err := base64.StdEncoding.DecodeString(r.Config.ComponentPublicKey)
	if err != nil {
		return err
	}
	manager, err := components.New(filepath.Join(r.Config.Root, "runtime"), ed25519.PublicKey(key), 1, r.Config.AllowLocalTestSource)
	if err != nil {
		return err
	}
	feed, err := manager.Feed(ctx, r.Config.ComponentSource)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var networkError net.Error
		if !errors.As(err, &networkError) {
			return err
		}
		feed, err = manager.CachedFeed(r.Config.ComponentSource)
		if err != nil {
			return errors.New("本地组件尚未准备，需要联网下载；请重试")
		}
	}
	selected := []components.Descriptor{}
	for _, id := range []string{r.Config.WorkerID, r.Config.ModelID} {
		found := false
		for _, d := range feed.Components {
			if d.ID == id {
				selected = append(selected, d)
				found = true
				break
			}
		}
		if !found {
			return errors.New("component feed is missing a required compatible component")
		}
	}
	var total, completed int64
	for _, d := range selected {
		total += d.Bytes
	}
	report := notify
	notify = func(status RuntimeStatus) {
		status.EngineBytes, status.ModelBytes = selected[0].Bytes, selected[1].Bytes
		report(status)
	}
	installed := []components.Installed{}
	for _, d := range selected {
		item, e := manager.Prepare(ctx, d, func(p components.Progress) {
			notify(RuntimeStatus{Phase: p.Phase, Message: "正在准备本地引擎与模型", Downloaded: completed + p.Downloaded, Total: total})
		})
		if e != nil {
			return e
		}
		installed = append(installed, item)
		completed += d.Bytes
	}
	worker, model := installed[0], installed[1]
	if worker.Metadata.Model != model.Metadata.ID {
		return errors.New("worker and model component mismatch")
	}
	service := serviceipc.Config{Root: filepath.Join(r.Config.Root, "data"), Worker: engine.Config{
		Executable: filepath.Join(worker.Directory, worker.Metadata.Executable),
		Library:    filepath.Join(worker.Directory, worker.Metadata.Library), ModelDir: model.Directory,
		DataDir: filepath.Join(r.Config.Root, "data/index"), DenyNetwork: r.Config.AllowLocalTestSource}}
	// Service settings contain paths and protocol data; original knowledge is
	// held by the service's own database, never by this controller.
	config, err := json.Marshal(service)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(r.Config.Root, ".service-config-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err = temporary.Write(config); err != nil {
		temporary.Close()
		return err
	}
	if err = temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	if err = os.Rename(temporary.Name(), filepath.Join(r.Config.Root, "service.json")); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	stopped, err := serviceipc.IsStopped(service.Root)
	if err != nil {
		return err
	}
	if stopped {
		notify(RuntimeStatus{Phase: "stopped", Message: "组件已准备，记忆服务已主动停止", Installed: true, Downloaded: total, Total: total})
		return nil
	}
	state, err := platform.ServiceStatus(r.Config.ServicePlist)
	if err != nil {
		return err
	}
	if state == platform.NotRegistered || state == platform.NotFound {
		exists, e := platform.BundledServiceExists(r.Config.ServicePlist)
		if e != nil {
			return e
		}
		if !exists {
			return errors.New("后台服务组件缺失，请重新安装客户端")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = platform.RegisterService(r.Config.ServicePlist); err != nil {
			return err
		}
		state, err = platform.ServiceStatus(r.Config.ServicePlist)
		if err != nil {
			return err
		}
	}
	if state != platform.Enabled {
		message, phase := "组件已准备，后台服务需要系统批准", "authorization"
		if state == platform.NotFound {
			message, phase = "后台服务组件缺失，请重新安装客户端", "failed"
		}
		if state == platform.NotRegistered {
			message, phase = "后台服务尚未注册，请重试", "failed"
		}
		notify(RuntimeStatus{Phase: phase, Message: message, Installed: true, Service: state, Downloaded: total, Total: total})
		return nil
	}
	deadline := time.NewTimer(65 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		var health struct {
			State   string `json:"state"`
			Version string `json:"service_version"`
		}
		probe, stop := context.WithTimeout(ctx, time.Second)
		err = serviceipc.Call(probe, service.Root, serviceipc.Request{Op: "health"}, &health)
		stop()
		if err == nil && health.Version != "0.0.1" {
			return errors.New("incompatible business service version")
		}
		if err == nil && health.State == "ready" {
			notify(RuntimeStatus{Phase: "ready", Message: "本地记忆服务已就绪", Installed: true, Service: state, Downloaded: total, Total: total})
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("组件已准备，服务启动超时，请查看诊断并重试")
		case <-ticker.C:
		}
	}
}

type RuntimeSettings struct {
	KeepService bool `json:"keep_service"`
}

func LoadRuntimeSettings(root string) (RuntimeSettings, error) {
	settings := RuntimeSettings{KeepService: true}
	data, err := os.ReadFile(filepath.Join(root, "ui-settings.json"))
	if os.IsNotExist(err) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	err = json.Unmarshal(data, &settings)
	return settings, err
}

func SaveRuntimeSettings(root string, settings RuntimeSettings) error {
	if !filepath.IsAbs(root) {
		return errors.New("absolute settings root required")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "ui-settings.json"), data, 0600)
}

func (r *RuntimeController) StopService(ctx context.Context) error {
	r.Cancel()
	for {
		r.mu.Lock()
		busy := r.busy
		r.mu.Unlock()
		if !busy {
			break
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	root := filepath.Join(r.Config.Root, "data")
	var health struct {
		State string `json:"state"`
	}
	probe, stop := context.WithTimeout(ctx, time.Second)
	err := serviceipc.Call(probe, root, serviceipc.Request{Op: "health"}, &health)
	stop()
	if err == nil {
		if err = serviceipc.Call(ctx, root, serviceipc.Request{Op: "stop"}, nil); err != nil {
			return err
		}
	}
	state, err := platform.ServiceStatus(r.Config.ServicePlist)
	if err != nil {
		return err
	}
	if state == platform.Enabled || state == platform.RequiresApproval {
		if err = platform.UnregisterService(r.Config.ServicePlist); err != nil {
			return err
		}
	}
	// The service owns this lock until its worker is gone and its database has
	// closed. Wait for that lifecycle boundary before allowing the UI to quit.
	if err = os.MkdirAll(root, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(root, ".service.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			return serviceipc.MarkStopped(root)
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *RuntimeController) ResumeService(ctx context.Context, notify func(RuntimeStatus)) error {
	root := filepath.Join(r.Config.Root, "data")
	if err := serviceipc.Resume(root); err != nil {
		return err
	}
	return r.Prepare(ctx, notify)
}

func (r *RuntimeController) QuiesceForUpdate(ctx context.Context) (bool, error) {
	root := filepath.Join(r.Config.Root, "data")
	stopped, err := serviceipc.IsStopped(root)
	if err != nil {
		return false, err
	}
	var health struct {
		State string `json:"state"`
	}
	probe, stop := context.WithTimeout(ctx, time.Second)
	err = serviceipc.Call(probe, root, serviceipc.Request{Op: "health"}, &health)
	stop()
	wasRunning := err == nil && !stopped
	if wasRunning {
		if err = serviceipc.Call(ctx, root, serviceipc.Request{Op: "quiesce"}, nil); err != nil {
			return false, err
		}
	}
	state, err := platform.ServiceStatus(r.Config.ServicePlist)
	if err != nil {
		return false, err
	}
	if state == platform.Enabled || state == platform.RequiresApproval {
		if err = platform.UnregisterService(r.Config.ServicePlist); err != nil {
			return false, err
		}
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return false, err
	}
	lock, err := os.OpenFile(filepath.Join(root, ".service.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return false, err
	}
	defer lock.Close()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			return wasRunning, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return false, err
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case <-timer.C:
		}
	}
}
