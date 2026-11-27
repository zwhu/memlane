package settings

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
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
	APIVersions       []string        `json:"api_versions"`
	ReadingContract   string          `json:"reading_contract"`
	Features          controlFeatures `json:"features"`
	EngineReady       bool            `json:"engine_ready"`
	CapabilitiesEpoch int64           `json:"capabilities_epoch"`
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

const (
	controlCapabilitiesReadyTTL    = 5 * time.Minute
	controlCapabilitiesNotReadyTTL = 5 * time.Second
)

var capabilitiesNow = time.Now

type cachedCapabilitiesEntry struct {
	caps         ControlCapabilities
	supported    bool
	expiresAt    time.Time
	updatedAtSeq uint64
}

var controlCapabilitiesCache = struct {
	mu        sync.Mutex
	globalGen uint64
	writeSeq  uint64
	urlGen    map[string]uint64
	entries   map[string]cachedCapabilitiesEntry
}{
	urlGen:  map[string]uint64{},
	entries: map[string]cachedCapabilitiesEntry{},
}

func normalizeControlURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// InvalidateControlCapabilitiesCache evicts cached /v1/capabilities entries for
// controlURL (or all entries when controlURL is empty) and bumps generation
// counters so in-flight probes cannot write stale entries back.
func InvalidateControlCapabilitiesCache(controlURL string) {
	prefix := normalizeControlURL(controlURL)
	controlCapabilitiesCache.mu.Lock()
	defer controlCapabilitiesCache.mu.Unlock()
	if prefix == "" {
		clear(controlCapabilitiesCache.entries)
		controlCapabilitiesCache.globalGen++
		return
	}
	controlCapabilitiesCache.urlGen[prefix]++
	for key := range controlCapabilitiesCache.entries {
		if keyURL, _, ok := strings.Cut(key, "|"); ok {
			if normalizeControlURL(keyURL) == prefix {
				delete(controlCapabilitiesCache.entries, key)
			}
		} else if strings.HasPrefix(key, prefix) {
			delete(controlCapabilitiesCache.entries, key)
		}
	}
}

func capabilitiesCacheTTL(caps ControlCapabilities) time.Duration {
	if !caps.EngineReady {
		return controlCapabilitiesNotReadyTTL
	}
	return controlCapabilitiesReadyTTL
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
		// downgrade to 引擎直连接口 with a separate API key.
		return false, fmt.Errorf("项目管理服务凭据不可用：%w", tokErr)
	}
	tokenDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
	normURL := normalizeControlURL(c.Config.URL)
	cacheKey := normURL + "|" + tokenDigest

	for attempt := 0; attempt < 2; attempt++ {
		now := capabilitiesNow()
		controlCapabilitiesCache.mu.Lock()
		entry, ok := controlCapabilitiesCache.entries[cacheKey]
		startGlobalGen := controlCapabilitiesCache.globalGen
		startURLGen := controlCapabilitiesCache.urlGen[normURL]
		var startWriteSeq uint64
		if ok {
			startWriteSeq = entry.updatedAtSeq
		}
		controlCapabilitiesCache.mu.Unlock()
		if ok && now.Before(entry.expiresAt) && (feature != ReadingContract || entry.caps.ReadingContract == ReadingContract) {
			return entry.supported && entry.caps.HasFeature(feature), nil
		}
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		caps, err := c.capabilities(probeCtx)
		cancel()
		if err != nil {
			if isControlNotFound(err) {
				// Confirmed legacy control server without /v1/capabilities: safe to cache fallback
				// unless an invalidation or newer supported write happened while in flight.
				writeNow := capabilitiesNow()
				controlCapabilitiesCache.mu.Lock()
				if controlCapabilitiesCache.globalGen != startGlobalGen || controlCapabilitiesCache.urlGen[normURL] != startURLGen {
					cur, curOK := controlCapabilitiesCache.entries[cacheKey]
					controlCapabilitiesCache.mu.Unlock()
					if curOK && writeNow.Before(cur.expiresAt) {
						return cur.supported && cur.caps.HasFeature(feature), nil
					}
					if attempt == 0 {
						continue
					}
					return false, nil
				}
				if prev, exists := controlCapabilitiesCache.entries[cacheKey]; exists && prev.updatedAtSeq != startWriteSeq && writeNow.Before(prev.expiresAt) {
					if prev.supported {
						controlCapabilitiesCache.mu.Unlock()
						return prev.caps.HasFeature(feature), nil
					}
				}
				if len(controlCapabilitiesCache.entries) >= 128 {
					clear(controlCapabilitiesCache.entries)
					controlCapabilitiesCache.globalGen++
				}
				controlCapabilitiesCache.writeSeq++
				controlCapabilitiesCache.entries[cacheKey] = cachedCapabilitiesEntry{
					supported:    false,
					expiresAt:    writeNow.Add(controlCapabilitiesReadyTTL),
					updatedAtSeq: controlCapabilitiesCache.writeSeq,
				}
				controlCapabilitiesCache.mu.Unlock()
				return false, nil
			}
			// All other errors (401/403, 5xx, network failure, cancellation) fail closed
			// without caching supported=false or falling back to 引擎直连接口.
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
		writeNow := capabilitiesNow()
		controlCapabilitiesCache.mu.Lock()
		if controlCapabilitiesCache.globalGen != startGlobalGen || controlCapabilitiesCache.urlGen[normURL] != startURLGen {
			cur, curOK := controlCapabilitiesCache.entries[cacheKey]
			controlCapabilitiesCache.mu.Unlock()
			if curOK && writeNow.Before(cur.expiresAt) {
				return cur.supported && cur.caps.HasFeature(feature), nil
			}
			if attempt == 0 {
				continue
			}
			return caps.HasFeature(feature), nil
		}
		if prev, exists := controlCapabilitiesCache.entries[cacheKey]; exists && prev.updatedAtSeq != startWriteSeq && writeNow.Before(prev.expiresAt) {
			if prev.supported && caps.CapabilitiesEpoch <= prev.caps.CapabilitiesEpoch {
				controlCapabilitiesCache.mu.Unlock()
				return prev.caps.HasFeature(feature), nil
			}
		}
		if len(controlCapabilitiesCache.entries) >= 128 {
			clear(controlCapabilitiesCache.entries)
			controlCapabilitiesCache.globalGen++
		}
		controlCapabilitiesCache.writeSeq++
		controlCapabilitiesCache.entries[cacheKey] = cachedCapabilitiesEntry{
			caps:         caps,
			supported:    true,
			expiresAt:    writeNow.Add(capabilitiesCacheTTL(caps)),
			updatedAtSeq: controlCapabilitiesCache.writeSeq,
		}
		controlCapabilitiesCache.mu.Unlock()
		return caps.HasFeature(feature), nil
	}
	return false, nil
}
