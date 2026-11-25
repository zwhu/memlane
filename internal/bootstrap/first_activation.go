package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/zwhu/memlane/internal/supervisor"
)

type FirstGenerationReference = supervisor.GenerationReference
type FirstActivationRequest struct {
	Schema    int                      `json:"schema"`
	StoreID   string                   `json:"store_id"`
	Operation string                   `json:"operation_id"`
	Reference FirstGenerationReference `json:"reference"`
	Digest    string                   `json:"digest"`
	Revision  int64                    `json:"revision"`
	Status    string                   `json:"status"`
	Phase     string                   `json:"phase,omitempty"`
}

// ErrFirstActivationRejected identifies a matching receipt rejected before any activation write.
var ErrFirstActivationRejected = errors.New("首次启用在写入前被拒绝，请核对最新提炼配置")

func (r FirstActivationRequest) rejectedBeforeMutation() bool {
	return r.Status == "rejected" && r.Phase == "pre_mutation"
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
	var attempted FirstActivationRequest
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
			attempted = existing
			if existing.Status == "completed" {
				return nil
			}
			if !existing.rejectedBeforeMutation() {
				if existing.Reference != reference || existing.Digest != digest || existing.Revision != revision || (existing.Status != "approved" && existing.Status != "stopping" && existing.Status != "starting") {
					return errors.New("原启用操作尚待核对，未创建另一项操作")
				}
				return nil
			}
			if existing.Reference == reference && existing.Digest == digest && existing.Revision == revision {
				_ = os.Remove(requestPath)
				return ErrFirstActivationRejected
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		var identifier [16]byte
		if _, err := rand.Read(identifier[:]); err != nil {
			return err
		}
		attempted = FirstActivationRequest{Schema: 1, StoreID: config.First.StoreID, Operation: "first-" + hex.EncodeToString(identifier[:]), Reference: reference, Digest: digest, Revision: revision, Status: "approved"}
		b, err = json.Marshal(attempted)
		if err != nil {
			return err
		}
		return atomicPrivateFile(requestPath, b)
	})
	if err != nil {
		return err
	}
	prepared.ActivationFile = requestPath
	err = StartFirstRuntime(ctx, prepared)
	if err != nil {
		var receipt FirstActivationRequest
		b, readErr := privateRead(requestPath)
		if readErr == nil && json.Unmarshal(b, &receipt) == nil && receipt.rejectedBeforeMutation() &&
			receipt.Schema == attempted.Schema && receipt.StoreID == attempted.StoreID && receipt.Operation == attempted.Operation &&
			receipt.Reference == attempted.Reference && receipt.Digest == attempted.Digest && receipt.Revision == attempted.Revision {
			_ = (&Installer{Root: filepath.Dir(prepared.Resources)}).withLock(func() error {
				curBytes, curErr := privateRead(requestPath)
				if curErr != nil {
					return nil
				}
				var cur FirstActivationRequest
				if json.Unmarshal(curBytes, &cur) == nil && cur.Operation == attempted.Operation && cur.rejectedBeforeMutation() {
					_ = os.Remove(requestPath)
				}
				return nil
			})
			return ErrFirstActivationRejected
		}
	}
	return err
}
