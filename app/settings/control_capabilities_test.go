package settings

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestControlCapabilitiesNotReadyShortTTLInvalidateAndEpochRefresh(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "management.token")
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("k", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	baseNow := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	var currentNow time.Time
	currentNow = baseNow
	origNow := capabilitiesNow
	capabilitiesNow = func() time.Time { return currentNow }
	t.Cleanup(func() {
		capabilitiesNow = origNow
		InvalidateControlCapabilitiesCache("")
	})
	InvalidateControlCapabilitiesCache("")

	var calls atomic.Int32
	var engineReady atomic.Bool
	var epoch atomic.Int64
	epoch.Store(1)

	controlURL := "http://127.0.0.1:61299"
	client := ProjectControlClient{
		Config: ProjectControlConfig{URL: controlURL, ManagementTokenFile: tokenFile},
		HTTP: &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/capabilities" {
				http.NotFound(w, r)
				return
			}
			calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"api_versions":       []string{"v2", "v1"},
				"reading_contract":   ReadingContract,
				"engine_ready":       engineReady.Load(),
				"capabilities_epoch": epoch.Load(),
				"features": map[string]bool{
					"async_capture": true,
					"local_body":    engineReady.Load(),
				},
			})
		})},
	}

	// 1. Engine not ready: cached for short 5s TTL, NOT 5 minutes.
	ok, err := client.ProbeFeature(context.Background(), "local_body")
	if err != nil || ok {
		t.Fatalf("initial ProbeFeature(local_body) = (%v, %v), want (false, nil)", ok, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 capabilities fetch, got %d", calls.Load())
	}

	// Within 4 seconds, cache is still valid.
	currentNow = baseNow.Add(4 * time.Second)
	ok, err = client.ProbeFeature(context.Background(), "local_body")
	if err != nil || ok || calls.Load() != 1 {
		t.Fatalf("expected cached not-ready result at +4s: ok=%v err=%v calls=%d", ok, err, calls.Load())
	}

	// At +6 seconds (> 5s short TTL), cache expires and refetches with new epoch & engine_ready=true.
	engineReady.Store(true)
	epoch.Store(2)
	currentNow = baseNow.Add(6 * time.Second)
	ok, err = client.ProbeFeature(context.Background(), "local_body")
	if err != nil || !ok || calls.Load() != 2 {
		t.Fatalf("expected refreshed ready result at +6s: ok=%v err=%v calls=%d", ok, err, calls.Load())
	}

	// 2. Engine ready: cached for full 5 minutes (at +2m, no refetch).
	currentNow = baseNow.Add(2 * time.Minute)
	ok, err = client.ProbeFeature(context.Background(), "local_body")
	if err != nil || !ok || calls.Load() != 2 {
		t.Fatalf("expected ready cache hit at +2m: ok=%v err=%v calls=%d", ok, err, calls.Load())
	}

	// 3. Explicit InvalidateControlCapabilitiesCache evicts immediately even within 5m TTL.
	epoch.Store(3)
	InvalidateControlCapabilitiesCache(controlURL + "/")
	ok, err = client.ProbeFeature(context.Background(), "local_body")
	if err != nil || !ok || calls.Load() != 3 {
		t.Fatalf("expected immediate refetch after invalidation: ok=%v err=%v calls=%d", ok, err, calls.Load())
	}

	// Verify stored entry recorded updated CapabilitiesEpoch.
	controlCapabilitiesCache.mu.Lock()
	var gotEpoch int64
	for key, entry := range controlCapabilitiesCache.entries {
		if strings.HasPrefix(key, controlURL+"|") {
			gotEpoch = entry.caps.CapabilitiesEpoch
		}
	}
	controlCapabilitiesCache.mu.Unlock()
	if gotEpoch != 3 {
		t.Fatalf("cached CapabilitiesEpoch = %d, want 3", gotEpoch)
	}
}

func TestControlCapabilitiesConcurrentInvalidationAndEpochMonotonicity(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "management.token")
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("r", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	controlURL := "http://127.0.0.1:61298"
	InvalidateControlCapabilitiesCache("")
	t.Cleanup(func() { InvalidateControlCapabilitiesCache("") })

	reqAStarted := make(chan struct{})
	releaseReqA := make(chan struct{})
	var reqCount atomic.Int32

	client := ProjectControlClient{
		Config: ProjectControlConfig{URL: controlURL, ManagementTokenFile: tokenFile},
		HTTP: &http.Client{Transport: queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/capabilities" {
				http.NotFound(w, r)
				return
			}
			n := reqCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if n == 1 {
				close(reqAStarted)
				<-releaseReqA
				_ = json.NewEncoder(w).Encode(map[string]any{
					"api_versions":       []string{"v2", "v1"},
					"reading_contract":   ReadingContract,
					"engine_ready":       false,
					"capabilities_epoch": 1,
					"features":           map[string]bool{"local_body": false},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"api_versions":       []string{"v2", "v1"},
				"reading_contract":   ReadingContract,
				"engine_ready":       true,
				"capabilities_epoch": 2,
				"features":           map[string]bool{"local_body": true},
			})
		})},
	}

	reqADone := make(chan struct{})
	var okA bool
	var errA error
	go func() {
		defer close(reqADone)
		okA, errA = client.ProbeFeature(context.Background(), "local_body")
	}()
	<-reqAStarted

	// Engine recovers and invalidates cache while request A is still in flight.
	InvalidateControlCapabilitiesCache(controlURL)

	// Request B completes with epoch=2, engine_ready=true and populates cache.
	okB, errB := client.ProbeFeature(context.Background(), "local_body")
	if errB != nil || !okB {
		t.Fatalf("request B ProbeFeature = (%v, %v), want (true, nil)", okB, errB)
	}

	// Now release stale request A (epoch=1, engine_ready=false); it must NOT overwrite cache
	// AND must return the fresher cached result (true, nil) rather than stale false.
	close(releaseReqA)
	<-reqADone
	if errA != nil || !okA {
		t.Fatalf("lagged request A returned stale result: (%v, %v), want (true, nil)", okA, errA)
	}

	// Subsequent probe must hit cached epoch=2, engine_ready=true without refetching.
	okAfter, errAfter := client.ProbeFeature(context.Background(), "local_body")
	if errAfter != nil || !okAfter {
		t.Fatalf("post-race ProbeFeature = (%v, %v), want (true, nil)", okAfter, errAfter)
	}
	if reqCount.Load() != 2 {
		t.Fatalf("expected cache hit after race (2 total fetches), got %d", reqCount.Load())
	}

	controlCapabilitiesCache.mu.Lock()
	var cachedEntry cachedCapabilitiesEntry
	var found bool
	for key, entry := range controlCapabilitiesCache.entries {
		if strings.HasPrefix(key, controlURL+"|") {
			cachedEntry = entry
			found = true
		}
	}
	controlCapabilitiesCache.mu.Unlock()
	if !found || cachedEntry.caps.CapabilitiesEpoch != 2 || !cachedEntry.caps.EngineReady {
		t.Fatalf("stale request A poisoned cache: found=%v entry=%+v", found, cachedEntry)
	}
}

func TestControlCapabilitiesNormalizedURLKeyAndGatewayRestartEpochReset(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "management.token")
	if err := os.WriteFile(tokenFile, []byte(strings.Repeat("n", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	baseNow := time.Date(2026, 11, 27, 13, 0, 0, 0, time.UTC)
	currentNow := baseNow
	origNow := capabilitiesNow
	capabilitiesNow = func() time.Time { return currentNow }
	t.Cleanup(func() {
		capabilitiesNow = origNow
		InvalidateControlCapabilitiesCache("")
	})
	InvalidateControlCapabilitiesCache("")

	var calls atomic.Int32
	var epoch atomic.Int64
	var localBody atomic.Bool
	epoch.Store(8)
	localBody.Store(true)

	transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"api_versions":       []string{"v2", "v1"},
			"reading_contract":   ReadingContract,
			"engine_ready":       true,
			"capabilities_epoch": epoch.Load(),
			"features":           map[string]bool{"local_body": localBody.Load()},
		})
	})

	cNoSlash := ProjectControlClient{
		Config: ProjectControlConfig{URL: "http://127.0.0.1:61297", ManagementTokenFile: tokenFile},
		HTTP:   &http.Client{Transport: transport},
	}
	cWithSlash := ProjectControlClient{
		Config: ProjectControlConfig{URL: "http://127.0.0.1:61297/", ManagementTokenFile: tokenFile},
		HTTP:   &http.Client{Transport: transport},
	}

	// Trailing-slash and non-trailing-slash URLs share the same normalized cache key.
	if ok, err := cNoSlash.ProbeFeature(context.Background(), "local_body"); err != nil || !ok {
		t.Fatalf("cNoSlash ProbeFeature = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, err := cWithSlash.ProbeFeature(context.Background(), "local_body"); err != nil || !ok {
		t.Fatalf("cWithSlash ProbeFeature = (%v, %v), want (true, nil)", ok, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected shared normalized cache key (1 HTTP call), got %d", calls.Load())
	}

	// After TTL expiry (simulating gateway restart resetting epoch 8 -> 1), lower epoch must refresh cache.
	epoch.Store(1)
	localBody.Store(false)
	currentNow = baseNow.Add(6 * time.Minute)
	if ok, err := cNoSlash.ProbeFeature(context.Background(), "local_body"); err != nil || ok {
		t.Fatalf("post-restart ProbeFeature = (%v, %v), want (false, nil)", ok, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected refetch after TTL expiry, got %d", calls.Load())
	}
}
