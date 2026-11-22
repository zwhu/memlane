package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

type StoreIdentity struct {
	Schema       int    `json:"schema"`
	StoreID      string `json:"store_id"`
	SourceCommit string `json:"source_commit"`
	ModelDigest  string `json:"model_digest"`
}
type StoreReceipt struct {
	Schema             int    `json:"schema"`
	StoreID            string `json:"store_id"`
	OwnerID            string `json:"owner_id"`
	KeyID              string `json:"key_id"`
	SourceCommit       string `json:"source_commit"`
	StorageInitialized bool   `json:"storage_initialized"`
}
type StoreResult struct {
	Receipt                                              StoreReceipt
	Root, TokenFile, Python, Source, Archive, ModelCache string
	Resources                                            string
}

// InitializeStore is an explicit first-use operation, not a normal startup
// fallback. It refuses any environment already bound to a daily content store.
// SDK initialization is resumed under one lock; no running service is replaced.
func (e *Environment) InitializeStore(ctx context.Context, root string, assets fs.FS) (StoreResult, error) {
	var result StoreResult
	if e.Options.ProtectedStore != "" {
		return result, errors.New("已有日常库必须沿用现有配置，不能创建另一套库")
	}
	if !filepath.IsAbs(root) {
		return result, errors.New("首次存储位置必须为绝对路径")
	}
	if err := validateComponentLocation(EnvironmentOptions{Root: e.Options.Root, ProtectedStore: root}); err != nil {
		return result, err
	}
	installer := e.Installer()
	err := installer.withLock(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		plan, err := e.Detect(ctx)
		if err != nil {
			return err
		}
		if !plan.Complete() {
			return errors.New("环境组件尚未准备完成，未创建内容库")
		}
		python, err := e.runtimePython(ctx)
		if err != nil {
			return err
		}
		sourceRequirement, _ := e.Requirement("cognee-source")
		source, err := e.Source.Probe(ctx, sourceRequirement)
		if err != nil {
			return err
		}
		sourcePath, archive := source.Path, e.Options.Archive
		if source.Path == sourceRequirement.Destination {
			sourcePath, archive = filepath.Join(source.Path, "source"), filepath.Join(source.Path, "service.tar.gz")
		}
		if err := VerifySource(archive, sourcePath, e.Source.Managed.Spec.SHA256, e.Source.Managed.Spec.Packages); err != nil {
			return err
		}
		modelRequirement, _ := e.Requirement("embedding")
		model, err := e.Model.Probe(ctx, modelRequirement)
		if err != nil {
			return err
		}
		if model.Status != Available {
			return errors.New("模型能力尚未确认，未创建内容库")
		}
		resources, err := prepareRuntimeResources(ctx, installer.Root, assets)
		if err != nil {
			return err
		}
		identity := StoreIdentity{Schema: 1, SourceCommit: e.Source.Managed.Spec.Commit, ModelDigest: e.Model.Spec.Digest()}
		marker := filepath.Join(root, ".memlane-first-store.json")
		b, err := privateRead(marker)
		if err == nil {
			var existing StoreIdentity
			if err := json.Unmarshal(b, &existing); err != nil {
				return err
			}
			if existing.Schema != 1 || existing.SourceCommit != identity.SourceCommit || existing.ModelDigest != identity.ModelDigest || len(existing.StoreID) != 32 {
				return errors.New("首次存储归属或固定组件已变化，保留原目录")
			}
			identity = existing
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		} else {
			var id [16]byte
			if _, err := rand.Read(id[:]); err != nil {
				return err
			}
			identity.StoreID = hex.EncodeToString(id[:])
		}
		b, err = json.Marshal(identity)
		if err != nil {
			return err
		}
		if err := claimComponentRoot(root, ".memlane-first-store.json", b); err != nil {
			return err
		}
		spec := struct {
			StoreIdentity
			Root       string `json:"root"`
			Source     string `json:"source"`
			Archive    string `json:"archive"`
			ModelCache string `json:"model_cache"`
		}{identity, root, sourcePath, archive, model.Path}
		b, err = json.MarshalIndent(spec, "", "  ")
		if err != nil {
			return err
		}
		input := filepath.Join(root, "first-install-input.json")
		if err := atomicPrivateFile(input, b); err != nil {
			return err
		}
		if err := runFirstStore(ctx, root, python, resources, "--config", input); err != nil {
			return err
		}
		b, err = privateRead(filepath.Join(root, "store-identity.json"))
		if err != nil {
			return err
		}
		var receipt StoreReceipt
		if err := json.Unmarshal(b, &receipt); err != nil {
			return err
		}
		if receipt.Schema != 1 || !receipt.StorageInitialized || receipt.StoreID != identity.StoreID || receipt.SourceCommit != identity.SourceCommit || receipt.OwnerID == "" || receipt.KeyID == "" {
			return errors.New("官方存储初始化回执不完整")
		}
		if _, err := privateRead(filepath.Join(root, "owner.token")); err != nil {
			return err
		}
		result = StoreResult{Receipt: receipt, Root: root, TokenFile: filepath.Join(root, "owner.token"), Python: python, Source: sourcePath, Archive: archive, ModelCache: model.Path, Resources: resources}
		return nil
	})
	return result, err
}

func runFirstStore(ctx context.Context, root, python, resources string, args ...string) error {
	command := []string{"-I", "-B", filepath.Join(resources, "engine/python/first_install_store.py")}
	return runManaged(ctx, root, python, append(command, args...)...)
}

// CheckFirstStoreResources exercises the packaged initializer in a disposable
// directory using only the application's verified runtime resources.
func CheckFirstStoreResources(ctx context.Context, python string, assets fs.FS) error {
	if !filepath.IsAbs(python) {
		return errors.New("first-store resource check needs an absolute Python executable path")
	}
	temp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return err
	}
	root, err := os.MkdirTemp(temp, "memlane-first-store-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	resources, err := PrepareRuntimeResources(ctx, filepath.Join(root, "installation"), assets)
	if err != nil {
		return err
	}
	return runFirstStore(ctx, root, python, resources, "--check-resources")
}
