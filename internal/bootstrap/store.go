package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
		resources, err := prepareStoreResources(installer.Root, assets)
		if err != nil {
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
		if err := runManaged(ctx, root, python, "-I", "-B", filepath.Join(resources, "python/first_install_store.py"), "--config", input); err != nil {
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
		result = StoreResult{Receipt: receipt, Root: root, TokenFile: filepath.Join(root, "owner.token"), Python: python, Source: sourcePath, Archive: archive, ModelCache: model.Path}
		return nil
	})
	return result, err
}

func prepareStoreResources(root string, assets fs.FS) (string, error) {
	files := []string{"python/first_install_store.py", "python/official_service_dev.py", "coding-memory-components.json"}
	data := map[string][]byte{}
	hash := sha256.New()
	for _, name := range files {
		b, err := fs.ReadFile(assets, name)
		if err != nil {
			return "", err
		}
		data[name] = b
		hash.Write([]byte(name))
		hash.Write(b)
	}
	id := hex.EncodeToString(hash.Sum(nil))
	destination := filepath.Join(root, "resources-"+id[:12])
	identity, _ := json.Marshal(map[string]string{"sha256": id})
	if err := claimComponentRoot(destination, ".memlane-adapters.json", identity); err != nil {
		return "", err
	}
	for _, name := range files {
		p := filepath.Join(destination, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			return "", err
		}
		if b, err := privateRead(p); err == nil {
			if string(b) != string(data[name]) {
				return "", errors.New("首次准备适配资源已被修改，保留原件")
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err := atomicPrivateFile(p, data[name]); err != nil {
			return "", err
		}
	}
	return destination, nil
}
