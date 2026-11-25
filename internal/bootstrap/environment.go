package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

type toolPins struct {
	Version string   `json:"version"`
	ARM     Artifact `json:"darwin_arm64"`
	Intel   Artifact `json:"darwin_amd64"`
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
	var pins struct {
		Schema    int            `json:"schema"`
		Python    toolPins       `json:"python"`
		UV        toolPins       `json:"uv"`
		NativeARM *NativeSupport `json:"native_openssl_arm64"`
	}
	var service struct {
		Service struct {
			SourceSpec
			SDKVersion string `json:"sdk_version"`
		} `json:"service"`
		Plugin struct {
			SourceSpec
			Version  string `json:"version"`
			Manifest string `json:"managed_patch_manifest"`
		} `json:"plugin"`
	}
	var model ModelSpec
	for name, target := range map[string]any{"first-install-components.json": &pins, "coding-memory-components.json": &service, "embedding-components.json": &model} {
		b, err := fs.ReadFile(assets, name)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, target); err != nil {
			return nil, err
		}
	}
	if pins.Schema != 1 {
		return nil, errors.New("unsupported bootstrap manifest")
	}
	if len(service.Service.Commit) != 40 || len(service.Service.SHA256) != 64 || len(service.Service.Packages) == 0 {
		return nil, errors.New("invalid fixed Cognee manifest")
	}
	if len(service.Plugin.Commit) != 40 || len(service.Plugin.SHA256) != 64 || filepath.Base(service.Plugin.Manifest) != service.Plugin.Manifest || service.Plugin.Manifest == "" {
		return nil, errors.New("invalid fixed Agent plugin manifest")
	}
	pluginManifest, err := fs.ReadFile(assets, service.Plugin.Manifest)
	if err != nil {
		return nil, err
	}
	var pluginIdentity struct {
		Commit string `json:"plugin_commit"`
	}
	if err := json.Unmarshal(pluginManifest, &pluginIdentity); err != nil || pluginIdentity.Commit != service.Plugin.Commit {
		return nil, errors.New("fixed Agent plugin source and patch receipt differ")
	}
	pluginDigest := sha256.Sum256(pluginManifest)
	pluginVersion := service.Plugin.Version + " · " + service.Plugin.Commit[:7] + " · " + hex.EncodeToString(pluginDigest[:])[:8]
	service.Plugin.Packages = []string{"integrations"}
	if err := validateComponentLocation(options); err != nil {
		return nil, err
	}
	lock, err := fs.ReadFile(assets, "python/requirements.lock")
	if err != nil {
		return nil, err
	}
	choose := func(p toolPins) (Artifact, error) {
		switch runtime.GOARCH {
		case "arm64":
			return p.ARM, nil
		case "amd64":
			return p.Intel, nil
		default:
			return Artifact{}, errors.New("不支持的 macOS 架构")
		}
	}
	pythonArtifact, err := choose(pins.Python)
	if err != nil {
		return nil, err
	}
	uvArtifact, err := choose(pins.UV)
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
	e := &Environment{Options: options, CogneeSDKVersion: service.Service.SDKVersion}
	e.Python = ToolDriver{ID: "python", Version: pins.Python.Version, Executable: "bin/python3.12", Artifact: pythonArtifact, ReusePath: options.Python, DownloadSource: sourceMode}
	e.UV = ToolDriver{ID: "uv", Version: pins.UV.Version, Executable: "uv", Artifact: uvArtifact, ReusePath: options.UV, DownloadSource: sourceMode}
	e.Runtime = RuntimeDriver{Lock: lock, ExistingPython: options.Python, Python: e.basePython, UV: e.uvPath, DownloadSource: sourceMode}
	if runtime.GOARCH == "arm64" && pins.NativeARM != nil {
		native := *pins.NativeARM
		native.DownloadSource = sourceMode
		e.Runtime.Native = &native
	}
	e.Source = sourceLocations{Managed: SourceDriver{Spec: service.Service.SourceSpec, DownloadSource: sourceMode}, ExistingSource: options.Source, ExistingArchive: options.Archive}
	e.Model = ModelDriver{Spec: model, ExistingRoot: options.ModelCache, Python: e.runtimePython, DownloadSource: sourceMode}
	e.Plugin = PluginDriver{Spec: service.Plugin.SourceSpec, Version: pluginVersion, Manifest: pluginManifest,
		ExistingPath: options.Plugin, ResourceRoot: filepath.Join(options.Root, "installation"), Python: e.runtimePython, DownloadSource: sourceMode}
	officialPyPI := "https://pypi.org/simple/"
	officialModel := "https://huggingface.co/Qdrant/bge-small-zh-v1.5"
	e.requirements = []Requirement{
		{ID: "uv", Name: "安装工具", Purpose: "下载并校验锁定的运行依赖", Version: pins.UV.Version, Source: DisplaySourceURL("uv", uvArtifact.URL, uvArtifact.SHA256, sourceMode), OfficialSource: uvArtifact.URL, Digest: uvArtifact.SHA256, DownloadBytes: uvArtifact.DownloadBytes, InstalledBytes: 60 << 20, Destination: filepath.Join(options.Root, "uv-"+pins.UV.Version), SizeNote: "占用为预估上限，兼容的现有工具直接复用"},
		{ID: "python", Name: "Python", Purpose: "运行本地记忆引擎", Version: pins.Python.Version, Source: DisplaySourceURL("python", pythonArtifact.URL, pythonArtifact.SHA256, sourceMode), OfficialSource: pythonArtifact.URL, Digest: pythonArtifact.SHA256, DownloadBytes: pythonArtifact.DownloadBytes, InstalledBytes: 100 << 20, Destination: filepath.Join(options.Root, "python-"+pins.Python.Version), SizeNote: "占用为预估上限，兼容的现有解释器直接复用"},
		{ID: "runtime", Name: "引擎运行库", Purpose: "提供本地数据库、向量计算及服务能力", Version: "requirements.lock", Source: DisplaySourceURL("runtime", officialPyPI, e.Runtime.Digest(), sourceMode), OfficialSource: officialPyPI, Digest: e.Runtime.Digest(), DownloadBytes: 2 << 30, InstalledBytes: 4 << 30, Destination: filepath.Join(options.Root, "runtime-"+e.Runtime.Digest()[:12]), SizeNote: "下载和占用为估算预算，按随附锁文件及哈希安装；不会修改已有 Python 环境"},
		{ID: "cognee-source", Name: "Cognee", Purpose: "提供官方记忆提炼与检索", Version: service.Service.Commit, Source: DisplaySourceURL("cognee-source", service.Service.URL, service.Service.SHA256, sourceMode), OfficialSource: service.Service.URL, Digest: service.Service.SHA256, DownloadBytes: 150 << 20, InstalledBytes: 300 << 20, Destination: filepath.Join(options.Root, "cognee-"+service.Service.Commit[:12]), SizeNote: "下载和占用为估算预算，固定提交及归档 SHA256"},
		{ID: "embedding", Name: "本地向量模型", Purpose: "在本机将文本转换为检索向量", Version: model.Model, Source: DisplaySourceURL("embedding", officialModel, model.Digest(), sourceMode), OfficialSource: officialModel, Digest: model.Digest(), DownloadBytes: model.Bytes(), InstalledBytes: model.Bytes(), Destination: filepath.Join(options.Root, "embedding-"+model.Digest()[:12]), SizeNote: "含固定 ONNX 模型及官方分词文件，复用原有向量空间"},
		{ID: "agent-plugin", Name: "Agent 插件", Purpose: "为已有 Agent 准备官方 Cognee 接入插件及固定权限适配", Version: pluginVersion, Source: DisplaySourceURL("agent-plugin", service.Plugin.URL, service.Plugin.SHA256, sourceMode), OfficialSource: service.Plugin.URL, Digest: service.Plugin.SHA256, DownloadBytes: 20 << 20, InstalledBytes: 60 << 20, Destination: filepath.Join(options.Root, "plugin-integrations-"+service.Plugin.Commit[:7]+"-"+hex.EncodeToString(pluginDigest[:])[:8]), SizeNote: "下载和占用为预估预算；固定官方归档及补丁，不包含账号或登录信息"},
	}
	if e.Runtime.Native != nil {
		for i := range e.requirements {
			if e.requirements[i].ID == "runtime" {
				e.requirements[i].Version = "requirements.lock + OpenSSL " + e.Runtime.Native.Version
				e.requirements[i].SizeNote += "; 包含独立 OpenSSL 原生库，官方 Homebrew 归档 " + e.Runtime.Native.Artifact.URL + " SHA256 " + e.Runtime.Native.Artifact.SHA256
			}
		}
	}

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

func (e *Environment) Probe(ctx context.Context, r Requirement) (Observation, error) {
	expected, err := e.Requirement(r.ID)
	if err != nil || expected != r {
		return Observation{}, errors.New("installation scope differs from fixed environment plan")
	}
	switch r.ID {
	case "python":
		return e.Python.Probe(ctx, r)
	case "uv":
		return e.UV.Probe(ctx, r)
	case "runtime":
		return e.Runtime.Probe(ctx, r)
	case "cognee-source":
		return e.Source.Probe(ctx, r)
	case "embedding":
		return e.Model.Probe(ctx, r)
	case "agent-plugin":
		return e.Plugin.Probe(ctx, r)
	}
	return Observation{}, errors.New("unknown bootstrap component")
}
func (e *Environment) Install(ctx context.Context, r Requirement) error {
	expected, err := e.Requirement(r.ID)
	if err != nil || expected != r {
		return errors.New("installation scope differs from fixed environment plan")
	}
	switch r.ID {
	case "python":
		return e.Python.Install(ctx, r)
	case "uv":
		return e.UV.Install(ctx, r)
	case "runtime":
		return e.Runtime.Install(ctx, r)
	case "cognee-source":
		return e.Source.Managed.Install(ctx, r)
	case "embedding":
		return e.Model.Install(ctx, r)
	case "agent-plugin":
		return e.Plugin.Install(ctx, r)
	}
	return errors.New("unknown bootstrap component")
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
func (p Plan) Complete() bool {
	if len(p.Items) == 0 {
		return false
	}
	for _, item := range p.Items {
		if item.Observation.Status != Available {
			return false
		}
	}
	return true
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
