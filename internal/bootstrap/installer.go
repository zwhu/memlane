package bootstrap

import (
	"context"
	"time"

	"github.com/zwhu/memlane/core/setup"
	"github.com/zwhu/memlane/kit/aiinstaller"
)

type Status = aiinstaller.Status
type Requirement = aiinstaller.ComponentSpec
type Step = aiinstaller.Step

const (
	Available    = aiinstaller.Available
	Missing      = aiinstaller.Missing
	Incompatible = aiinstaller.Incompatible
	Installing   = aiinstaller.Installing
	Failed       = aiinstaller.Failed
	Cancelled    = aiinstaller.Cancelled
	Ready        = aiinstaller.Ready
)

var (
	ErrConsent                  = aiinstaller.ErrConsent
	ErrPlanChanged              = aiinstaller.ErrPlanChanged
	postInstallProbeMaxAttempts = 2
	postInstallProbeRetryDelay  = 2 * time.Second
	spaceCheckInterval          = time.Second
)

type Observation struct {
	Status  Status `json:"status"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Reason  string `json:"reason,omitempty"`
	// Fingerprint binds approval to the selected external dependencies too.
	Fingerprint      string `json:"fingerprint,omitempty"`
	transientTimeout bool
}

type Item struct {
	Requirement Requirement `json:"requirement"`
	Observation Observation `json:"observation"`
}

type Plan struct {
	Items []Item `json:"items"`
}

// Driver.Probe must never download, install, upgrade, initialize a content store,
// or mutate an external dependency. Install may only write the approved target.
type Driver interface {
	Probe(context.Context, Requirement) (Observation, error)
	Install(context.Context, Requirement) error
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

func (p Plan) Digest() string { return p.toInstallerPlan().Digest() }
func (p Plan) Complete() bool { return p.toInstallerPlan().Complete() }

func (item Item) toInstallerItem() aiinstaller.Item {
	o := item.Observation
	return aiinstaller.Item{Requirement: item.Requirement, Observation: aiinstaller.Observation{
		Status: o.Status, Path: o.Path, Version: o.Version, Reason: o.Reason,
		Fingerprint: o.Fingerprint, TransientTimeout: o.transientTimeout,
	}}
}

func (p Plan) toInstallerPlan() aiinstaller.Plan {
	var out aiinstaller.Plan
	if p.Items != nil {
		out.Items = make([]aiinstaller.Item, len(p.Items))
		for index, item := range p.Items {
			out.Items[index] = item.toInstallerItem()
		}
	}
	return out
}

func observationFromInstaller(o aiinstaller.Observation) Observation {
	return Observation{Status: o.Status, Path: o.Path, Version: o.Version, Reason: o.Reason,
		Fingerprint: o.Fingerprint, transientTimeout: o.TransientTimeout}
}

func planFromInstaller(p aiinstaller.Plan) Plan {
	var out Plan
	if p.Items != nil {
		out.Items = make([]Item, len(p.Items))
		for index, item := range p.Items {
			out.Items[index] = Item{item.Requirement, observationFromInstaller(item.Observation)}
		}
	}
	return out
}

func journalFromInstaller(j aiinstaller.Journal) Journal {
	return Journal{Schema: j.Schema, Operation: j.Operation, Plan: planFromInstaller(j.Plan),
		ApprovedDigest: j.ApprovedDigest, DeclinedDigest: j.DeclinedDigest, Status: j.Status,
		ActiveStep: j.ActiveStep, Steps: j.Steps}
}

func (j Journal) toInstallerJournal() aiinstaller.Journal {
	return aiinstaller.Journal{Schema: j.Schema, Operation: j.Operation, Plan: j.Plan.toInstallerPlan(),
		ApprovedDigest: j.ApprovedDigest, DeclinedDigest: j.DeclinedDigest, Status: j.Status,
		ActiveStep: j.ActiveStep, Steps: j.Steps}
}

type Installer struct {
	Root       string
	Driver     Driver
	CheckSpace func(context.Context) error
	OnChange   func(Journal)
}

type installerDriver struct{ Driver }

func (d installerDriver) Probe(ctx context.Context, r Requirement) (aiinstaller.Observation, error) {
	o, err := d.Driver.Probe(ctx, r)
	return (Item{Requirement: r, Observation: o}).toInstallerItem().Observation, err
}

func (i Installer) toInstaller() aiinstaller.Installer {
	out := setup.NewInstaller(i.Root, installerDriver{i.Driver})
	out.CheckSpace = i.CheckSpace
	out.PostInstallProbeMaxAttempts = postInstallProbeMaxAttempts
	out.PostInstallProbeRetryDelay = postInstallProbeRetryDelay
	out.SpaceCheckInterval = spaceCheckInterval
	if i.OnChange != nil {
		out.OnChange = func(j aiinstaller.Journal) { i.OnChange(journalFromInstaller(j)) }
	}
	return out
}

func (i Installer) Detect(ctx context.Context, requirements []Requirement) (Plan, error) {
	p, err := i.toInstaller().Detect(ctx, requirements)
	return planFromInstaller(p), err
}
func (i Installer) Review(operation string, p Plan, digest string, approve bool) error {
	return i.toInstaller().Review(operation, p.toInstallerPlan(), digest, approve)
}
func (i Installer) reviewLocked(operation string, p Plan, digest string, approve bool) error {
	return i.toInstaller().ReviewLocked(operation, p.toInstallerPlan(), digest, approve)
}
func (i Installer) Run(ctx context.Context, operation string, p Plan) error {
	return i.toInstaller().Run(ctx, operation, p.toInstallerPlan())
}
func (i Installer) runLocked(ctx context.Context, operation string, p Plan) error {
	return i.toInstaller().RunLocked(ctx, operation, p.toInstallerPlan())
}
func (i Installer) Snapshot() (Journal, error) {
	j, err := i.toInstaller().Snapshot()
	return journalFromInstaller(j), err
}
func (i Installer) withLock(fn func() error) error { return i.toInstaller().WithLock(fn) }
func (i Installer) read() (Journal, error)         { return i.Snapshot() }
func prepareInstallationDir(root string) error     { return aiinstaller.PreparePrivateDir(root) }
