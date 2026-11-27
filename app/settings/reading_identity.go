package settings

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
)

type readingPrefixCacheEntry struct {
	serviceURL string
	token      string
	prefix     string
}

var (
	readingPrefixMu    sync.RWMutex
	readingPrefixCache = map[string]readingPrefixCacheEntry{}
)

func cachedReadingPrefix(serviceURL, tokenFile, token string) string {
	normalizedURL := strings.TrimRight(serviceURL, "/")
	cacheKey := tokenFile
	if cacheKey == "" {
		cacheKey = "\x00" + normalizedURL
	}
	readingPrefixMu.RLock()
	entry, ok := readingPrefixCache[cacheKey]
	readingPrefixMu.RUnlock()
	if ok && entry.serviceURL == normalizedURL && entry.token == token {
		return entry.prefix
	}
	prefix := fmt.Sprintf("%s/%x/", normalizedURL, sha256.Sum256([]byte(token)))
	readingPrefixMu.Lock()
	if len(readingPrefixCache) >= 64 {
		clear(readingPrefixCache)
	}
	readingPrefixCache[cacheKey] = readingPrefixCacheEntry{
		serviceURL: normalizedURL,
		token:      token,
		prefix:     prefix,
	}
	readingPrefixMu.Unlock()
	return prefix
}

// EffectiveReadingEndpoint returns the single authoritative (url, tokenFile)
// pair used for desktop reading identity and token-rotation polling.
// When ProjectControl is configured, all desktop reading goes through the local
// gateway using ManagementTokenFile; ServiceURL/TokenFile are used only when
// ProjectControl is absent.
func EffectiveReadingEndpoint(cfg Config) (endpointURL, tokenFile string, usingControl bool) {
	if cfg.ProjectControl != nil && strings.TrimSpace(cfg.ProjectControl.URL) != "" {
		return strings.TrimSpace(cfg.ProjectControl.URL), strings.TrimSpace(cfg.ProjectControl.ManagementTokenFile), true
	}
	return strings.TrimSpace(cfg.ServiceURL), strings.TrimSpace(cfg.TokenFile), false
}

func readingIdentityPair(c Client) (full, base string, err error) {
	serviceURL, tokenFile, usingControl := EffectiveReadingEndpoint(c.Config)
	if usingControl && tokenFile == "" {
		return "", "", fmt.Errorf("local gateway credential unavailable")
	}
	var token string
	if tokenFile != "" {
		if usingControl {
			token, err = readCachedToken(tokenFile, 32, true)
		} else {
			token, err = readCachedToken(tokenFile, 0, false)
		}
		if err != nil {
			if usingControl {
				return "", "", fmt.Errorf("local gateway credential unavailable")
			}
			return "", "", fmt.Errorf("official credential unavailable")
		}
	}
	prefix := cachedReadingPrefix(serviceURL, tokenFile, token)
	if c.PolicyRevision == "" {
		return prefix, prefix, nil
	}
	return prefix + c.PolicyRevision, prefix, nil
}

func readingIdentity(c Client) (string, error) {
	full, _, err := readingIdentityPair(c)
	return full, err
}
