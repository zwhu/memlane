// Package credentials stores independent Provider secrets in the OS vault.
// Agent and gateway credentials never enter this package.
package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

type Reference struct {
	ID       string
	Revision int64
}

// Target binds a supplier's credential version to its exact service address
// and protocol. Models from the same supplier may reuse this version.
type Target struct {
	Provider string `json:"provider"`
	Endpoint string `json:"endpoint"`
	Protocol string `json:"protocol"`
}

type Vault struct{ namespace string }

func New(root string) (*Vault, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, errors.New("absolute clean credential owner root required")
	}
	h := sha256.Sum256([]byte(root))
	return &Vault{namespace: "io.github.zwhu.memlane.processing." + hex.EncodeToString(h[:16])}, nil
}

func validate(ref Reference, target Target) error {
	id, err := uuid.Parse(ref.ID)
	if err != nil || id.String() != ref.ID || ref.Revision < 1 || strings.TrimSpace(target.Provider) != target.Provider || target.Provider == "" || len(target.Provider) > 128 || !utf8.ValidString(target.Provider) {
		return errors.New("invalid credential reference or supplier")
	}
	u, err := url.Parse(target.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(target.Endpoint) > 2048 ||
		(u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return errors.New("invalid credential service address")
	}
	if target.Protocol != "openai-chat" && target.Protocol != "openai-responses" && target.Protocol != "anthropic-messages" {
		return errors.New("unsupported credential protocol")
	}
	return nil
}

type envelope struct {
	Target Target `json:"target"`
	Secret []byte `json:"secret"`
}

// Put is immutable: duplicate references fail instead of updating secrets
// currently referenced by a saved candidate, current choice or running task.
func (v *Vault) Put(ref Reference, target Target, secret []byte) error {
	if v == nil || v.namespace == "" {
		return errors.New("credential owner namespace required")
	}
	if err := validate(ref, target); err != nil {
		return err
	}
	if len(secret) == 0 || len(secret) > 8192 {
		return errors.New("bounded independent supplier credential required")
	}
	raw, err := json.Marshal(envelope{Target: target, Secret: secret})
	if err != nil {
		return errors.New("credential encoding failed")
	}
	defer clear(raw)
	return v.put(ref, raw)
}

// Get does not return a credential after a supplier address/protocol change.
func (v *Vault) Get(ref Reference, target Target) ([]byte, error) {
	if v == nil || v.namespace == "" {
		return nil, errors.New("credential owner namespace required")
	}
	if err := validate(ref, target); err != nil {
		return nil, err
	}
	raw, err := v.get(ref)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	var value envelope
	if json.Unmarshal(raw, &value) != nil || len(value.Secret) == 0 || len(value.Secret) > 8192 {
		clear(value.Secret)
		return nil, errors.New("invalid vault credential")
	}
	if value.Target != target {
		clear(value.Secret)
		return nil, errors.New("supplier address or protocol changed; provide a new credential version")
	}
	return value.Secret, nil
}

// Delete is only for an explicitly retired/unreferenced version or an owned
// synthetic probe. Callers must check durable references before removing it.
func (v *Vault) Delete(ref Reference) error {
	if v == nil || v.namespace == "" {
		return errors.New("credential owner namespace required")
	}
	id, err := uuid.Parse(ref.ID)
	if err != nil || id.String() != ref.ID || ref.Revision < 1 {
		return errors.New("invalid credential reference")
	}
	return v.delete(ref)
}
