package settings

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"
)

type controlFeatures []string

func (f *controlFeatures) UnmarshalJSON(raw []byte) error {
	var names []string
	if err := json.Unmarshal(raw, &names); err == nil {
		*f = names
		return nil
	}
	var flags map[string]bool
	if err := json.Unmarshal(raw, &flags); err != nil {
		return err
	}
	names = make([]string, 0, len(flags))
	for name, enabled := range flags {
		if enabled {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	*f = names
	return nil
}

type ControlCapabilities struct {
	APIVersions     []string        `json:"api_versions"`
	ReadingContract string          `json:"reading_contract"`
	Features        controlFeatures `json:"features"`
}

func (caps ControlCapabilities) HasFeature(feature string) bool {
	if feature == ReadingContract {
		return caps.ReadingContract == ReadingContract
	}

	for _, f := range caps.Features {
		if f == feature {
			return true
		}
	}
	return false
}

const ReadingContract = "catalog-cursor-v1"

func (c ProjectControlClient) RequireReadingContract(ctx context.Context) error {
	ok, err := c.ProbeFeature(ctx, ReadingContract)
	if err != nil {
		return err
	}
	if !ok {
		return readingContractMismatch()
	}
	return nil
}

func readingContractMismatch() error {
	return &ControlResponseError{
		Status: http.StatusServiceUnavailable, Code: "reading_contract_mismatch",
		Message: "本地后台仍在使用旧版读取接口，请停止并重启后台服务",
	}
}

type cachedCapabilitiesEntry struct {
	caps      ControlCapabilities
	supported bool
	expiresAt time.Time
}

var controlCapabilitiesCache = struct {
	mu      sync.Mutex
	entries map[string]cachedCapabilitiesEntry
}{
	entries: map[string]cachedCapabilitiesEntry{},
}

func (c ProjectControlClient) capabilities(ctx context.Context) (ControlCapabilities, error) {
	var result ControlCapabilities
	err := c.request(ctx, "GET", "/v1/capabilities", nil, &result)
	return result, err
}

func (c ProjectControlClient) ProbeFeature(ctx context.Context, feature string) (bool, error) {
	token, tokErr := readCachedToken(c.Config.ManagementTokenFile, 32, true)
	if tokErr != nil {
		// Fail closed when management credentials are unavailable; never silently
		// downgrade to official /api/v1/* with a separate API key.
		return false, fmt.Errorf("项目管理服务凭据不可用：%w", tokErr)
	}
	tokenDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
	cacheKey := c.Config.URL + "|" + tokenDigest
	now := time.Now()
	controlCapabilitiesCache.mu.Lock()
	entry, ok := controlCapabilitiesCache.entries[cacheKey]
	controlCapabilitiesCache.mu.Unlock()
	if ok && now.Before(entry.expiresAt) && (feature != ReadingContract || entry.caps.ReadingContract == ReadingContract) {
		return entry.supported && entry.caps.HasFeature(feature), nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	caps, err := c.capabilities(probeCtx)
	if err != nil {
		if isControlNotFound(err) {
			// Confirmed legacy control server without /v1/capabilities: safe to cache fallback.
			controlCapabilitiesCache.mu.Lock()
			if len(controlCapabilitiesCache.entries) >= 128 {
				clear(controlCapabilitiesCache.entries)
			}
			controlCapabilitiesCache.entries[cacheKey] = cachedCapabilitiesEntry{supported: false, expiresAt: now.Add(5 * time.Minute)}
			controlCapabilitiesCache.mu.Unlock()
			return false, nil
		}
		// All other errors (401/403, 5xx, network failure, cancellation) fail closed
		// without caching supported=false or falling back to official /api/v1/*.
		if IsControlAuthDenied(err) {
			controlCapabilitiesCache.mu.Lock()
			delete(controlCapabilitiesCache.entries, cacheKey)
			controlCapabilitiesCache.mu.Unlock()
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, err
	}
	controlCapabilitiesCache.mu.Lock()
	if len(controlCapabilitiesCache.entries) >= 128 {
		clear(controlCapabilitiesCache.entries)
	}
	controlCapabilitiesCache.entries[cacheKey] = cachedCapabilitiesEntry{
		caps:      caps,
		supported: true,
		expiresAt: now.Add(5 * time.Minute),
	}
	controlCapabilitiesCache.mu.Unlock()
	return caps.HasFeature(feature), nil
}
