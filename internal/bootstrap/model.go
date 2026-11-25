package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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

type ModelDriver struct {
	Spec           ModelSpec
	ExistingRoot   string
	Python         func(context.Context) (string, error)
	DownloadSource string
}

func modelSnapshot(root string, r ModelRepository) string {
	return filepath.Join(root, "hub", "models--"+strings.ReplaceAll(r.Repo, "/", "--"), "snapshots", r.Revision)
}

func VerifyModelFiles(ctx context.Context, root string, spec ModelSpec) error {
	if spec.Schema != 1 || len(spec.Repositories) == 0 || spec.Dimensions <= 0 {
		return errors.New("invalid fixed model manifest")
	}
	for _, r := range spec.Repositories {
		if len(r.Revision) != 40 || len(r.Files) == 0 || strings.Contains(r.Repo, "..") || strings.ContainsAny(r.Repo, "\\") {
			return errors.New("invalid model repository pin")
		}
		for _, entry := range r.Files {
			if err := ctx.Err(); err != nil {
				return err
			}
			if filepath.Base(entry.Name) != entry.Name || entry.Name == ".." || len(entry.SHA256) != 64 {
				return errors.New("invalid model file pin")
			}
			p := filepath.Join(modelSnapshot(root, r), entry.Name)
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			h := sha256.New()
			n, err := io.Copy(h, f)
			f.Close()
			if err != nil {
				return err
			}
			if n != entry.DownloadBytes || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
				return fmt.Errorf("模型文件校验失败：%s", entry.Name)
			}
		}
	}
	return nil
}
func (d ModelDriver) Probe(ctx context.Context, r Requirement) (Observation, error) {
	if r.ID != "embedding" || r.Digest != d.Spec.Digest() {
		return Observation{}, errors.New("model requirement differs from fixed manifest")
	}
	o := Observation{Status: Missing, Path: r.Destination, Version: d.Spec.Model, Fingerprint: d.Spec.Digest()}
	roots := []string{r.Destination}
	if d.ExistingRoot != "" {
		roots = append(roots, d.ExistingRoot)
	}
	for _, root := range roots {
		if err := VerifyModelFiles(ctx, root, d.Spec); err != nil {
			if ctx.Err() != nil {
				return o, ctx.Err()
			}
			if !errors.Is(err, os.ErrNotExist) {
				o.Status, o.Reason = Incompatible, err.Error()
			}
			continue
		}
		o.Path = root
		python, err := d.Python(ctx)
		if err != nil {
			o.Status, o.Reason = Incompatible, "模型文件已保留，需先准备运行库再验证"
			return o, nil
		}
		if err := ProbeModel(ctx, python, root, d.Spec); err != nil {
			if ctx.Err() != nil {
				return o, ctx.Err()
			}
			o.Status, o.Reason = Incompatible, err.Error()
			return o, nil
		}
		o.Status, o.Reason = Available, ""
		return o, nil
	}
	return o, nil
}
func (d ModelDriver) Install(ctx context.Context, r Requirement) error {
	progressComponent(ctx, r.ID, false)
	if r.ID != "embedding" || r.Digest != d.Spec.Digest() {
		return errors.New("model requirement differs from fixed manifest")
	}
	progressStage(ctx, "verifying")
	// Valid existing files remain external and read-only, even if the previous
	// probe could not yet run because Python/dependencies were still missing.
	if d.ExistingRoot != "" && VerifyModelFiles(ctx, d.ExistingRoot, d.Spec) == nil {
		return nil
	}
	identity, _ := json.Marshal(d.Spec)
	if err := claimComponentRoot(r.Destination, ".memlane-model.json", identity); err != nil {
		return err
	}
	progressBatch(ctx, d.Spec.Bytes())
	for _, repo := range d.Spec.Repositories {
		snapshot := modelSnapshot(r.Destination, repo)
		if err := os.MkdirAll(snapshot, 0700); err != nil {
			return err
		}
		for _, file := range repo.Files {
			progressDetail(ctx, file.Name)
			target := filepath.Join(snapshot, file.Name)
			if d.DownloadSource != "" {
				candidates := ModelFileCandidates(repo.Repo, file, d.DownloadSource)
				if _, err := fetchArtifactCandidates(ctx, candidates, func(ArtifactCandidate) string { return target }); err != nil {
					return err
				}
			} else if err := fetchArchive(ctx, file.URL, file.SHA256, target); err != nil {
				return err
			}
		}
		refs := filepath.Join(r.Destination, "hub", "models--"+strings.ReplaceAll(repo.Repo, "/", "--"), "refs")
		if err := os.MkdirAll(refs, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(refs, "main"), []byte(repo.Revision), 0600); err != nil {
			return err
		}
	}
	progressStage(ctx, "verifying")
	return VerifyModelFiles(ctx, r.Destination, d.Spec)
}

// ProbeModel encodes synthetic text offline from explicit, verified snapshots.
// No library is initialized, no credentials are inherited, and no model fetch
// or cache mutation is needed in an externally owned cache.
func ProbeModel(ctx context.Context, python, root string, spec ModelSpec) error {
	var onnx, tokenizer string
	for _, repo := range spec.Repositories {
		for _, f := range repo.Files {
			if strings.HasSuffix(f.Name, ".onnx") {
				onnx = modelSnapshot(root, repo)
			}
			if repo.Repo == spec.Model && f.Name == "tokenizer.json" {
				tokenizer = filepath.Join(modelSnapshot(root, repo), f.Name)
			}
		}
	}
	if onnx == "" || tokenizer == "" {
		return errors.New("model/tokenizer snapshot missing")
	}
	code := `import json,math,sys
from fastembed import TextEmbedding
from tokenizers import Tokenizer
model=TextEmbedding(model_name=sys.argv[1],specific_model_path=sys.argv[2],local_files_only=True,threads=1)
vectors=list(model.embed(['安装环境检测']))
assert len(vectors)==1 and len(vectors[0])==int(sys.argv[4])
assert all(math.isfinite(float(x)) for x in vectors[0]) and sum(float(x)**2 for x in vectors[0])>0
tokenizer=Tokenizer.from_file(sys.argv[3]); assert len(tokenizer.encode('安装环境检测').ids)>0
print(json.dumps(dict(dimensions=len(vectors[0]),offline=True)))`
	b, err := isolatedPython(ctx, python, code, spec.Model, onnx, tokenizer, fmt.Sprint(spec.Dimensions))
	if err != nil {
		return fmt.Errorf("本地模型能力验证未通过：%w", err)
	}
	var result struct {
		Dimensions int
		Offline    bool
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return err
	}
	if result.Dimensions != spec.Dimensions || !result.Offline {
		return errors.New("invalid embedding probe receipt")
	}
	return nil
}
