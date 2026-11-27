package supervisor

import (
	"context"
	"time"

	"github.com/zwhu/memlane/kit/daemon"
)

// Config configures a Supervisor guarding one store root and its loopback services.
type Config struct {
	StoreRoot               string
	ConfigPath              string
	PhaseOnReady            string
	ServiceURL              string
	CompletionURL           string
	ControlURL              string
	ResourcesDir            string
	ResourcesHash           string
	RuntimeConfig           string
	GenerationMode          string
	GenerationReference     *GenerationReference
	LogDir                  string
	LogLimitBytes           int64
	MonitorInterval         time.Duration
	DescendantTrackInterval time.Duration
	ReadyPollInterval       time.Duration
	HealthFailureMax        int
	HealthTimeout           time.Duration
	ExternalOwner           bool
	KeepOnTimeout           bool
	OnCrash                 func(ServiceStatus)
}

type SupervisorSnapshot = daemon.SupervisorSnapshot

// Supervisor adapts the daemon supervisor to the application's ownership schema.
type Supervisor daemon.Supervisor

func New(cfg Config) *Supervisor {
	return (*Supervisor)(daemon.New(daemon.Config{
		StoreRoot:    cfg.StoreRoot,
		ConfigPath:   cfg.ConfigPath,
		PhaseOnReady: cfg.PhaseOnReady,
		Endpoints:    []string{cfg.ServiceURL, cfg.CompletionURL, cfg.ControlURL},
		OwnerMetadata: OwnerMetadata{
			ResourcesDir:        cfg.ResourcesDir,
			ResourcesHash:       cfg.ResourcesHash,
			RuntimeConfig:       cfg.RuntimeConfig,
			GenerationMode:      cfg.GenerationMode,
			GenerationReference: cfg.GenerationReference,
		}.Encode(),
		LockConfig:              ownerLocks,
		LogRedactor:             serviceLogRedactor,
		LogDir:                  cfg.LogDir,
		LogLimitBytes:           cfg.LogLimitBytes,
		MonitorInterval:         cfg.MonitorInterval,
		DescendantTrackInterval: cfg.DescendantTrackInterval,
		ReadyPollInterval:       cfg.ReadyPollInterval,
		HealthFailureMax:        cfg.HealthFailureMax,
		HealthTimeout:           cfg.HealthTimeout,
		ExternalOwner:           cfg.ExternalOwner,
		KeepOnTimeout:           cfg.KeepOnTimeout,
		OnCrash:                 cfg.OnCrash,
	}))
}

func (s *Supervisor) ExternalOwner() bool {
	return (*daemon.Supervisor)(s).ExternalOwner()
}

func (s *Supervisor) MarkStopping() {
	(*daemon.Supervisor)(s).MarkStopping()
}

func (s *Supervisor) Exited() <-chan ServiceStatus {
	return (*daemon.Supervisor)(s).Exited()
}

func (s *Supervisor) Start(ctx context.Context, specs []ServiceSpec) error {
	return (*daemon.Supervisor)(s).Start(ctx, specs)
}

func (s *Supervisor) WaitReady(ctx context.Context, check func(context.Context) (bool, error)) error {
	return (*daemon.Supervisor)(s).WaitReady(ctx, check)
}

func (s *Supervisor) Stop(ctx context.Context) error {
	return (*daemon.Supervisor)(s).Stop(ctx)
}

func (s *Supervisor) Snapshot() SupervisorSnapshot {
	return (*daemon.Supervisor)(s).Snapshot()
}
