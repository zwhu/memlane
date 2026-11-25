package setup

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/zwhu/memlane/kit/aiinstaller"
)

type Artifact struct {
	URL           string `json:"url"`
	SHA256        string `json:"sha256"`
	DownloadBytes int64  `json:"download_bytes"`
}

type SourceSpec struct {
	Commit   string   `json:"commit"`
	URL      string   `json:"archive_url"`
	SHA256   string   `json:"archive_sha256"`
	Packages []string `json:"packages"`
}

type ModelFile struct {
	Name          string `json:"name"`
	SHA256        string `json:"sha256"`
	DownloadBytes int64  `json:"download_bytes"`
	URL           string `json:"url"`
}

type ModelRepository struct {
	Repo     string      `json:"repo"`
	Revision string      `json:"revision"`
	Files    []ModelFile `json:"files"`
}

type ModelSpec struct {
	Schema       int               `json:"schema"`
	Model        string            `json:"model"`
	Provider     string            `json:"provider"`
	Dimensions   int               `json:"dimensions"`
	Repositories []ModelRepository `json:"repositories"`
}

func (s ModelSpec) Digest() string {
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s ModelSpec) Bytes() int64 {
	var sum int64
	for _, r := range s.Repositories {
		for _, f := range r.Files {
			sum += f.DownloadBytes
		}
	}
	return sum
}

// NativeSupportSpec identifies the separately downloaded OpenSSL runtime support.
type NativeSupportSpec struct {
	Version       string   `json:"version"`
	Artifact      Artifact `json:"artifact"`
	ArchivePrefix string   `json:"archive_prefix"`
}

type ToolSpec struct {
	Version  string
	Artifact Artifact
}

type ServiceSpec struct {
	SourceSpec
	SDKVersion string `json:"sdk_version"`
}

type ManifestSet struct {
	Python, UV ToolSpec
	Native     *NativeSupportSpec
	Service    ServiceSpec
	Model      ModelSpec
	Lock       []byte
}

//go:embed requirements.lock
var defaultRequirementsLock []byte

type ToolPins struct {
	Version string   `json:"version"`
	ARM     Artifact `json:"darwin_arm64"`
	Intel   Artifact `json:"darwin_amd64"`
}

func LoadManifests(assets fs.FS, goarch string) (ManifestSet, error) {
	var m ManifestSet
	var pins struct {
		Schema    int                `json:"schema"`
		Python    ToolPins           `json:"python"`
		UV        ToolPins           `json:"uv"`
		NativeARM *NativeSupportSpec `json:"native_openssl_arm64"`
	}
	var sources struct {
		Service ServiceSpec `json:"service"`
	}
	for _, entry := range []struct {
		name   string
		target any
	}{
		{"first-install-components.json", &pins},
		{"coding-memory-components.json", &sources},
		{"embedding-components.json", &m.Model},
	} {
		b, err := fs.ReadFile(assets, entry.name)
		if err != nil {
			return ManifestSet{}, err
		}
		if err := json.Unmarshal(b, entry.target); err != nil {
			return ManifestSet{}, err
		}
	}
	if pins.Schema != 1 {
		return ManifestSet{}, errors.New("unsupported bootstrap manifest")
	}
	m.Service = sources.Service
	if len(m.Service.Commit) != 40 || len(m.Service.SHA256) != 64 || len(m.Service.Packages) == 0 {
		return ManifestSet{}, errors.New("invalid fixed Cognee manifest")
	}
	lock, err := fs.ReadFile(assets, "requirements.lock")
	if errors.Is(err, fs.ErrNotExist) {
		lock = bytes.Clone(defaultRequirementsLock)
	} else if err != nil {
		return ManifestSet{}, err
	}
	m.Lock = lock
	m.Python.Version, m.UV.Version = pins.Python.Version, pins.UV.Version
	switch goarch {
	case "arm64":
		m.Python.Artifact, m.UV.Artifact = pins.Python.ARM, pins.UV.ARM
		m.Native = pins.NativeARM
	case "amd64":
		m.Python.Artifact, m.UV.Artifact = pins.Python.Intel, pins.UV.Intel
	default:
		return ManifestSet{}, errors.New("不支持的 macOS 架构")
	}
	if len(bytes.TrimSpace(m.Lock)) == 0 {
		return ManifestSet{}, errors.New("empty runtime requirements lock")
	}
	if m.Model.Schema != 1 || m.Model.Model == "" || m.Model.Dimensions <= 0 || len(m.Model.Repositories) == 0 {
		return ManifestSet{}, errors.New("invalid fixed model manifest")
	}
	for _, tool := range []ToolSpec{m.Python, m.UV} {
		if tool.Version == "" || tool.Artifact.URL == "" || len(tool.Artifact.SHA256) != 64 {
			return ManifestSet{}, errors.New("invalid fixed bootstrap tool manifest")
		}
	}
	return m, nil
}

// BuildRequirements assembles consent scopes from the loaded manifests and runtime lock digest.
func BuildRequirements(root string, m ManifestSet, runtimeDigest, downloadSource string, native *NativeSupportSpec) []aiinstaller.ComponentSpec {
	const officialPyPI = "https://pypi.org/simple/"
	const officialModel = "https://huggingface.co/Qdrant/bge-small-zh-v1.5"
	modelDigest := m.Model.Digest()
	modelBytes := m.Model.Bytes()
	runtimeVersion := "requirements.lock"
	runtimeSizeNote := "下载和占用为估算预算，按随附锁文件及哈希安装；不会修改已有 Python 环境"
	if native != nil {
		runtimeVersion += " + OpenSSL " + native.Version
		runtimeSizeNote += "; 包含独立 OpenSSL 原生库，官方 Homebrew 归档 " + native.Artifact.URL + " SHA256 " + native.Artifact.SHA256
	}
	return []aiinstaller.ComponentSpec{
		{
			ID: "uv", Name: "安装工具", Purpose: "下载并校验锁定的运行依赖", Version: m.UV.Version,
			Source:         DisplaySourceURL("uv", m.UV.Artifact.URL, m.UV.Artifact.SHA256, downloadSource),
			OfficialSource: m.UV.Artifact.URL, Digest: m.UV.Artifact.SHA256,
			DownloadBytes: m.UV.Artifact.DownloadBytes, InstalledBytes: 60 << 20,
			Destination: filepath.Join(root, "uv-"+m.UV.Version), SizeNote: "占用为预估上限，兼容的现有工具直接复用",
		},
		{
			ID: "python", Name: "Python", Purpose: "运行本地记忆引擎", Version: m.Python.Version,
			Source:         DisplaySourceURL("python", m.Python.Artifact.URL, m.Python.Artifact.SHA256, downloadSource),
			OfficialSource: m.Python.Artifact.URL, Digest: m.Python.Artifact.SHA256,
			DownloadBytes: m.Python.Artifact.DownloadBytes, InstalledBytes: 100 << 20,
			Destination: filepath.Join(root, "python-"+m.Python.Version), SizeNote: "占用为预估上限，兼容的现有解释器直接复用",
		},
		{
			ID: "runtime", Name: "引擎运行库", Purpose: "提供本地数据库、向量计算及服务能力", Version: runtimeVersion,
			Source:         DisplaySourceURL("runtime", officialPyPI, runtimeDigest, downloadSource),
			OfficialSource: officialPyPI, Digest: runtimeDigest,
			DownloadBytes: 2 << 30, InstalledBytes: 4 << 30,
			Destination: filepath.Join(root, "runtime-"+runtimeDigest[:12]), SizeNote: runtimeSizeNote,
		},
		{
			ID: "cognee-source", Name: "Cognee", Purpose: "提供官方记忆提炼与检索", Version: m.Service.Commit,
			Source:         DisplaySourceURL("cognee-source", m.Service.URL, m.Service.SHA256, downloadSource),
			OfficialSource: m.Service.URL, Digest: m.Service.SHA256,
			DownloadBytes: 150 << 20, InstalledBytes: 300 << 20,
			Destination: filepath.Join(root, "cognee-"+m.Service.Commit[:12]), SizeNote: "下载和占用为估算预算，固定提交及归档 SHA256",
		},
		{
			ID: "embedding", Name: "本地向量模型", Purpose: "在本机将文本转换为检索向量", Version: m.Model.Model,
			Source:         DisplaySourceURL("embedding", officialModel, modelDigest, downloadSource),
			OfficialSource: officialModel, Digest: modelDigest,
			DownloadBytes: modelBytes, InstalledBytes: modelBytes,
			Destination: filepath.Join(root, "embedding-"+modelDigest[:12]), SizeNote: "含固定 ONNX 模型及官方分词文件，复用原有向量空间",
		},
	}
}

// DriverRouter dispatches only component specs from the fixed environment plan.
type DriverRouter struct {
	Specs   []aiinstaller.ComponentSpec
	Drivers map[string]aiinstaller.Driver
}

func (r DriverRouter) driver(spec aiinstaller.ComponentSpec) (aiinstaller.Driver, error) {
	for _, expected := range r.Specs {
		if expected.ID != spec.ID {
			continue
		}
		if expected != spec {
			break
		}
		driver := r.Drivers[spec.ID]
		if driver == nil {
			return nil, errors.New("unknown bootstrap component")
		}
		return driver, nil
	}
	return nil, errors.New("installation scope differs from fixed environment plan")
}

func (r DriverRouter) Probe(ctx context.Context, spec aiinstaller.ComponentSpec) (aiinstaller.Observation, error) {
	driver, err := r.driver(spec)
	if err != nil {
		return aiinstaller.Observation{}, err
	}
	return driver.Probe(ctx, spec)
}

func (r DriverRouter) Install(ctx context.Context, spec aiinstaller.ComponentSpec) error {
	driver, err := r.driver(spec)
	if err != nil {
		return err
	}
	return driver.Install(ctx, spec)
}
