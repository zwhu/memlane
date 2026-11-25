package bootstrap

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zwhu/memlane/core/setup"
	"github.com/zwhu/memlane/kit/aiinstaller"
)

type EnvironmentOptions struct {
	Root           string
	Python         string
	UV             string
	Source         string
	Archive        string
	ModelCache     string
	Plugin         string
	ProtectedStore string
	DownloadSource string

	SelectedAgents []string
}

type Environment struct {
	Options          EnvironmentOptions
	CogneeSDKVersion string
	Python           ToolDriver
	UV               ToolDriver
	Runtime          RuntimeDriver
	Source           sourceLocations
	Model            ModelDriver
	Plugin           PluginDriver
	requirements     []Requirement
}

func NewEnvironment(ctx context.Context, assets fs.FS, options EnvironmentOptions) (*Environment, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("此安装入口需要 macOS")
	}
	root, err := ComponentRoot(options.Root)
	if err != nil {
		return nil, err
	}
	options.Root = root
	if err := validateComponentLocation(options); err != nil {
		return nil, err
	}
	m, err := setup.LoadManifests(assets, runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	if options.Python == "" {
		for _, p := range PythonCandidates(nil) {
			o, err := ProbePython(ctx, p)
			if err != nil {
				return nil, err
			}
			if o.Status == Available {
				options.Python = p
				break
			}
		}
	}
	if options.UV == "" {
		options.UV = discoverUV()
	}
	sourceMode := NormalizeDownloadSource(options.DownloadSource)
	options.DownloadSource = sourceMode
	e := &Environment{Options: options, CogneeSDKVersion: m.Service.SDKVersion}
	e.Python = ToolDriver{ID: "python", Version: m.Python.Version, Executable: "bin/python3.12", Artifact: Artifact(m.Python.Artifact), ReusePath: options.Python, DownloadSource: sourceMode}
	e.UV = ToolDriver{ID: "uv", Version: m.UV.Version, Executable: "uv", Artifact: Artifact(m.UV.Artifact), ReusePath: options.UV, DownloadSource: sourceMode}
	e.Runtime = RuntimeDriver{Lock: m.Lock, ExistingPython: options.Python, Python: e.basePython, UV: e.uvPath, DownloadSource: sourceMode}
	if n := m.Native; n != nil {
		e.Runtime.Native = &NativeSupport{Version: n.Version, Artifact: Artifact(n.Artifact), ArchivePrefix: n.ArchivePrefix, DownloadSource: sourceMode}
	}
	e.Source = sourceLocations{Managed: SourceDriver{Spec: m.Service.SourceSpec, DownloadSource: sourceMode}, ExistingSource: options.Source, ExistingArchive: options.Archive}
	e.Model = ModelDriver{Spec: bootstrapModelSpec(m.Model), ExistingRoot: options.ModelCache, Python: e.runtimePython, DownloadSource: sourceMode}
	e.Plugin = PluginDriver{Spec: m.Plugin.SourceSpec, Version: m.Plugin.Version, Manifest: m.Plugin.Patch,
		ExistingPath: options.Plugin, ResourceRoot: filepath.Join(options.Root, "installation"), Python: e.runtimePython, DownloadSource: sourceMode}
	e.requirements = setup.BuildRequirements(options.Root, m, e.Runtime.Digest(), sourceMode, m.Native)

	return e, nil
}

func validateComponentLocation(options EnvironmentOptions) error {
	if options.ProtectedStore != "" {
		root, err := prospectivePath(options.Root)
		if err != nil {
			return err
		}
		store, err := prospectivePath(options.ProtectedStore)
		if err != nil {
			return err
		}
		within := func(a, b string) bool {
			rel, err := filepath.Rel(a, b)
			return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
		}
		if within(root, store) || within(store, root) {
			return errors.New("组件目录必须与现有日常库分开，保留原目录不变")
		}
	}
	return nil
}

func prospectivePath(p string) (string, error) {
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	tail := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(p)
		if err == nil {
			for n := len(tail) - 1; n >= 0; n-- {
				resolved = filepath.Join(resolved, tail[n])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", err
		}
		tail = append(tail, filepath.Base(p))
		p = parent
	}
}

func (e *Environment) Requirement(id string) (Requirement, error) {
	for _, r := range e.requirements {
		if r.ID == id {
			return r, nil
		}
	}
	return Requirement{}, errors.New("unknown bootstrap component")
}
func (e *Environment) basePython(ctx context.Context) (string, error) {
	r, _ := e.Requirement("python")
	o, err := e.Python.Probe(ctx, r)
	if err != nil {
		return "", err
	}
	if o.Status != Available {
		return "", errors.New("Python 尚未准备完成")
	}
	return o.Path, nil
}
func (e *Environment) uvPath(ctx context.Context) (string, error) {
	r, _ := e.Requirement("uv")
	o, err := e.UV.Probe(ctx, r)
	if err != nil {
		return "", err
	}
	if o.Status != Available {
		return "", errors.New("安装工具尚未准备完成")
	}
	return o.Path, nil
}
func (e *Environment) runtimePython(ctx context.Context) (string, error) {
	r, _ := e.Requirement("runtime")
	o, err := e.Runtime.Probe(ctx, r)
	if err != nil {
		return "", err
	}
	if o.Status != Available {
		return "", errors.New("引擎运行库尚未准备完成")
	}
	return o.Path, nil
}

func (e *Environment) driverRouter() setup.DriverRouter {
	return setup.DriverRouter{
		Specs: e.requirements,
		Drivers: map[string]aiinstaller.Driver{
			"python":        installerDriver{&e.Python},
			"uv":            installerDriver{&e.UV},
			"runtime":       installerDriver{&e.Runtime},
			"cognee-source": installerDriver{&e.Source},
			"embedding":     installerDriver{&e.Model},
			"agent-plugin":  installerDriver{&e.Plugin},
		},
	}
}

func (e *Environment) Probe(ctx context.Context, r Requirement) (Observation, error) {
	observation, err := e.driverRouter().Probe(ctx, r)
	return observationFromInstaller(observation), err
}

func (e *Environment) Install(ctx context.Context, r Requirement) error {
	return e.driverRouter().Install(ctx, r)
}

func (e *Environment) Installer() Installer {
	return Installer{Root: filepath.Join(e.Options.Root, "installation"), Driver: e}
}
func (e *Environment) Detect(ctx context.Context) (Plan, error) {
	// uv is an installation dependency, not a permanent service prerequisite.
	runtimeRequirement, _ := e.Requirement("runtime")
	o, err := e.Runtime.Probe(ctx, runtimeRequirement)
	if err != nil {
		return Plan{}, err
	}
	reqs := []Requirement{}
	for _, r := range e.requirements {
		if r.ID == "uv" && o.Status == Available {
			continue
		}
		reqs = append(reqs, r)
	}
	return e.Installer().Detect(ctx, reqs)
}

type sourceLocations struct {
	Managed                         SourceDriver
	ExistingSource, ExistingArchive string
}

func (d sourceLocations) Probe(ctx context.Context, r Requirement) (Observation, error) {
	o, err := d.Managed.Probe(ctx, r)
	if err != nil || o.Status == Available {
		return o, err
	}
	if d.ExistingSource != "" && d.ExistingArchive != "" {
		if err := VerifySource(d.ExistingArchive, d.ExistingSource, d.Managed.Spec.SHA256, d.Managed.Spec.Packages); err == nil {
			return Observation{Status: Available, Path: d.ExistingSource, Version: d.Managed.Spec.Commit, Fingerprint: d.Managed.Spec.SHA256}, nil
		} else {
			o.Status, o.Reason = Incompatible, err.Error()
		}
	}
	return o, nil
}

func (d sourceLocations) Install(ctx context.Context, r Requirement) error {
	return d.Managed.Install(ctx, r)
}

func bootstrapModelSpec(source setup.ModelSpec) ModelSpec {
	spec := ModelSpec{Schema: source.Schema, Model: source.Model, Provider: source.Provider, Dimensions: source.Dimensions}
	if source.Repositories != nil {
		spec.Repositories = make([]ModelRepository, len(source.Repositories))
		for index, repo := range source.Repositories {
			target := ModelRepository{Repo: repo.Repo, Revision: repo.Revision}
			if repo.Files != nil {
				target.Files = make([]ModelFile, len(repo.Files))
				for fileIndex, file := range repo.Files {
					target.Files[fileIndex] = ModelFile(file)
				}
			}
			spec.Repositories[index] = target
		}
	}
	return spec
}

func discoverUV() string {
	home, _ := os.UserHomeDir()
	return discoverUVWith(exec.LookPath, home)
}

func discoverUVWith(lookPath func(string) (string, error), home string) string {
	if p, err := lookPath("uv"); err == nil && p != "" {
		return p
	}
	var candidates []string
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".local/bin/uv"),
			filepath.Join(home, ".cargo/bin/uv"),
		)
	}
	candidates = append(candidates, "/opt/homebrew/bin/uv", "/usr/local/bin/uv")
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode().Perm()&0111 != 0 {
			return p
		}
	}
	return ""
}
