// Package serviceipc contains lightweight local transport and service settings.
package serviceipc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	Root         string        `json:"root"`
	Worker       engine.Config `json:"worker"`
	RetryDelayMS int           `json:"retry_delay_ms,omitempty"`
}

type Request struct {
	MergeList        *records.MergeListQuery `json:"merge_list,omitempty"`
	MergeStart       records.MergeStart      `json:"merge_start,omitempty"`
	MergeReview      records.MergeReview     `json:"merge_review,omitempty"`
	MergeTask        string                  `json:"merge_task,omitempty"`
	Protocol         int                     `json:"protocol"`
	ID               string                  `json:"id"`
	Op               string                  `json:"op"`
	Mutation         records.Mutation        `json:"mutation,omitempty"`
	Datasets         []string                `json:"datasets,omitempty"`
	Query            string                  `json:"query,omitempty"`
	Limit            int                     `json:"limit,omitempty"`
	Memory           string                  `json:"memory,omitempty"`
	UserConfirmed    bool                    `json:"user_confirmed,omitempty"`
	ListQuery        records.ListQuery       `json:"list_query,omitempty"`
	Project          records.ScopeDraft      `json:"project,omitempty"`
	Scope            string                  `json:"scope,omitempty"`
	AllScopes        bool                    `json:"all_scopes,omitempty"`
	BeforeRevision   int64                   `json:"before_revision,omitempty"`
	ExpectedRevision int64                   `json:"expected_revision,omitempty"`
	Timezone         string                  `json:"timezone,omitempty"`
	Connection       records.ConnectionDraft `json:"connection,omitempty"`
	ConnectionID     string                  `json:"connection_id,omitempty"`
	AgentRequest     records.AgentRequest    `json:"agent_request,omitempty"`
}

type Response struct {
	Protocol int    `json:"protocol"`
	ID       string `json:"id"`
	OK       bool   `json:"ok"`
	Result   any    `json:"result,omitempty"`
	Error    string `json:"error,omitempty"`
	Code     string `json:"code,omitempty"`
}

type State struct {
	Stopped  bool        `json:"stopped"`
	Restarts []time.Time `json:"restarts"`
}

func SocketPath(root string) string {
	h := sha256.Sum256([]byte(root))
	return filepath.Join("/tmp", fmt.Sprintf("memlane-%d-%s", os.Getuid(), hex.EncodeToString(h[:6])), "service.sock")
}

func WriteJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".state-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Resume is an explicit user action; it clears only the active stop marker.
func Resume(root string) error {
	if !filepath.IsAbs(root) {
		return errors.New("absolute root required")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	path := filepath.Join(root, "service-state.json")
	state := State{}
	if data, err := os.ReadFile(path); err == nil {
		if err = json.Unmarshal(data, &state); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	state.Stopped = false
	state.Restarts = nil
	return WriteJSON(path, state)
}

func MarkStopped(root string) error {
	if !filepath.IsAbs(root) {
		return errors.New("absolute root required")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	path := filepath.Join(root, "service-state.json")
	state := State{}
	if data, err := os.ReadFile(path); err == nil {
		if err = json.Unmarshal(data, &state); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	state.Stopped = true
	return WriteJSON(path, state)
}
