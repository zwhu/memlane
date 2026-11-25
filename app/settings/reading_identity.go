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

func readingIdentityPair(c Client) (full, base string, err error) {
	var token string
	if c.Config.TokenFile != "" {
		token, err = readCachedToken(c.Config.TokenFile, 0, false)
		if err != nil {
			return "", "", fmt.Errorf("official credential unavailable")
		}
	}
	prefix := cachedReadingPrefix(c.Config.ServiceURL, c.Config.TokenFile, token)
	if c.PolicyRevision == "" {
		return prefix, prefix, nil
	}
	return prefix + c.PolicyRevision, prefix, nil
}

func readingIdentity(c Client) (string, error) {
	full, _, err := readingIdentityPair(c)
	return full, err
}
