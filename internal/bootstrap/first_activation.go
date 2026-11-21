package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type FirstGenerationReference struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}
type FirstActivationRequest struct {
	Schema    int                      `json:"schema"`
	StoreID   string                   `json:"store_id"`
	Operation string                   `json:"operation_id"`
	Reference FirstGenerationReference `json:"reference"`
	Digest    string                   `json:"digest"`
	Revision  int64                    `json:"revision"`
	Status    string                   `json:"status"`
}

func ActivateFirstRuntime(ctx context.Context, prepared FirstRuntime, reference FirstGenerationReference, digest string, revision int64) error {
	if reference.ID == "" || reference.Revision < 1 || len(digest) != 64 || revision < 1 {
		return errors.New("请先验证并选择待启用的记忆提炼配置")
	}
	config, err := loadFirstRuntimeConfig(prepared.ConfigPath)
	if err != nil {
		return err
	}
	requestPath := filepath.Join(config.Root, "first-install-activation.json")
	err = (&Installer{Root: filepath.Dir(prepared.Resources)}).withLock(func() error {
		b, err := privateRead(requestPath)
		if err == nil {
			var existing FirstActivationRequest
			if err := json.Unmarshal(b, &existing); err != nil {
				return err
			}
			if existing.Schema != 1 || existing.StoreID != config.First.StoreID {
				return errors.New("首次启用操作归属不同，保留原回执")
			}
			if existing.Status == "completed" {
				return nil
			}
			if existing.Reference != reference || existing.Digest != digest || existing.Revision != revision || (existing.Status != "approved" && existing.Status != "stopping" && existing.Status != "starting") {
				return errors.New("原启用操作尚待核对，未创建另一项操作")
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		var identifier [16]byte
		if _, err := rand.Read(identifier[:]); err != nil {
			return err
		}
		request := FirstActivationRequest{Schema: 1, StoreID: config.First.StoreID, Operation: "first-" + hex.EncodeToString(identifier[:]), Reference: reference, Digest: digest, Revision: revision, Status: "approved"}
		b, err = json.Marshal(request)
		if err != nil {
			return err
		}
		return atomicPrivateFile(requestPath, b)
	})
	if err != nil {
		return err
	}
	prepared.ActivationFile = requestPath
	return StartFirstRuntime(ctx, prepared)
}
