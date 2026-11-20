package simpledesktop

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type readingKey struct{ Identity, Dataset, ID, Version string }
type readingFlight struct {
	key               readingKey
	item              Item
	ctx               context.Context
	cancel            context.CancelFunc
	done              chan struct{}
	result            Item
	err               error
	waiters           int
	priority, running bool
	fetch             func(context.Context, Item) (Item, error)
}
type readingCacheEntry struct {
	item  Item
	stamp uint64
}

// ReadingTitles owns only process-local raw data; all raw readers share its 4 slots.
type ReadingTitles struct {
	mu            sync.Mutex
	flights       map[readingKey]*readingFlight
	queue         []*readingFlight
	cache         map[readingKey]readingCacheEntry
	active, bytes int
	clock         uint64
	timeout       time.Duration
	identity      string
}

func newReadingTitles() *ReadingTitles {
	return &ReadingTitles{flights: map[readingKey]*readingFlight{}, cache: map[readingKey]readingCacheEntry{}, timeout: 15 * time.Second}
}
func readingIdentityPair(c Client) (full, base string, err error) {
	var token []byte
	if c.Config.TokenFile != "" {
		token, err = os.ReadFile(c.Config.TokenFile)
		if err != nil {
			return "", "", fmt.Errorf("official credential unavailable")
		}
	}
	prefix := fmt.Sprintf("%s/%x/", strings.TrimRight(c.Config.ServiceURL, "/"), sha256.Sum256([]byte(strings.TrimSpace(string(token)))))
	return prefix + c.PolicyRevision, prefix, nil
}
func readingIdentity(c Client) (string, error) {
	full, _, err := readingIdentityPair(c)
	return full, err
}
func keyForReading(identity string, i Item, catalogGeneration string) readingKey {
	version := ""
	if i.Document != nil {
		version = i.Document.ContentHash + "/" + i.Document.Updated
	}
	if version == "/" || version == "" {
		version = "catalog/" + catalogGeneration
	}
	return readingKey{identity, i.Dataset, i.ID, version}
}
func (r *ReadingTitles) read(ctx context.Context, client Client, i Item, generation string, priority, useCache bool) (Item, error) {
	if err := ctx.Err(); err != nil {
		return i, err
	}
	identity, err := readingIdentity(client)
	if err != nil {
		r.Invalidate("")
		return i, err
	}
	key := keyForReading(identity, i, generation)
	r.mu.Lock()
	if err := ctx.Err(); err != nil {
		r.mu.Unlock()
		return i, err
	}
	if r.identity != identity {
		r.identity = identity
		r.evictLocked("")
		for key, f := range r.flights {
			f.cancel()
			delete(r.flights, key)
		}
	}
	if cached, ok := r.cache[key]; ok && useCache {
		r.clock++
		cached.stamp = r.clock
		r.cache[key] = cached
		r.mu.Unlock()
		if i.Session != nil {
			i.Title = cached.item.Title
			i.SessionSummary = cached.item.SessionSummary
			return i, nil
		}
		i.Body = cached.item.Body
		i.BodyLoaded = true
		i.Title = itemPresentation(i).DisplayTitle
		return i, nil
	}
	f := r.flights[key]
	if f == nil {
		flightCtx, cancel := context.WithCancel(context.Background())
		f = &readingFlight{key: key, item: i, ctx: flightCtx, cancel: cancel, done: make(chan struct{}), fetch: client.read}
		r.flights[key] = f
		r.queue = append(r.queue, f)
	}
	f.waiters++
	f.priority = f.priority || priority
	r.pumpLocked()
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		r.mu.Lock()
		f.waiters--
		if f.waiters == 0 {
			f.cancel()
			if r.flights[key] == f {
				delete(r.flights, key)
			}
		}
		r.mu.Unlock()
		return i, ctx.Err()
	case <-f.done:
		if ctx.Err() != nil {
			return i, ctx.Err()
		}
		current, e := readingIdentity(client)
		if e != nil || current != identity {
			r.Invalidate("")
			return i, fmt.Errorf("official reading identity changed")
		}
		return f.result, f.err
	}
}
func (r *ReadingTitles) pumpLocked() {
	for r.active < 4 && len(r.queue) > 0 {
		index := 0
		for n, f := range r.queue {
			if f.priority {
				index = n
				break
			}
		}
		f := r.queue[index]
		r.queue = append(r.queue[:index], r.queue[index+1:]...)
		if f.ctx.Err() != nil || r.flights[f.key] != f {
			f.result = f.item
			f.err = context.Canceled
			close(f.done)
			f.cancel()
			continue
		}
		r.active++
		f.running = true
		timeout := r.timeout
		go func() {
			ctx, cancel := context.WithTimeout(f.ctx, timeout)
			defer cancel()
			result, err := f.fetch(ctx, f.item)
			r.mu.Lock()
			if f.ctx.Err() != nil {
				err = f.ctx.Err()
			}
			f.result, f.err = result, err
			if r.flights[f.key] == f {
				delete(r.flights, f.key)
				if err == nil && len(result.Body) <= 256*1024 {
					r.putLocked(f.key, result)
				}
				if readingUnavailable(err) {
					if old, ok := r.cache[f.key]; ok {
						r.bytes -= len(old.item.Body)
						delete(r.cache, f.key)
					}
				}
			}
			r.active--
			close(f.done)
			f.cancel()
			r.pumpLocked()
			r.mu.Unlock()
		}()
	}
}
func (r *ReadingTitles) putLocked(key readingKey, i Item) {
	// Session preheating retains only the derived title, never tools or answers.
	if i.Session != nil {
		i.Body = ""
		i.Detail = nil
		i.BodyLoaded = false
	}
	if old, ok := r.cache[key]; ok {
		r.bytes -= len(old.item.Body)
	}
	r.clock++
	r.cache[key] = readingCacheEntry{i, r.clock}
	r.bytes += len(i.Body)
	for len(r.cache) > 128 || r.bytes > 8*1024*1024 {
		var oldest readingKey
		stamp := ^uint64(0)
		for k, v := range r.cache {
			if v.stamp < stamp {
				oldest, stamp = k, v.stamp
			}
		}
		r.bytes -= len(r.cache[oldest].item.Body)
		delete(r.cache, oldest)
	}
}
func (r *ReadingTitles) evictLocked(dataset string) {
	for key, entry := range r.cache {
		if dataset == "" || key.Dataset == dataset {
			r.bytes -= len(entry.item.Body)
			delete(r.cache, key)
		}
	}
}

// Invalidate is the integration seam for credential and policy revocation.
// Empty dataset invalidates all identities; late replies cannot refill the cache.
func (r *ReadingTitles) Invalidate(dataset string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictLocked(dataset)
	for key, f := range r.flights {
		if dataset == "" || key.Dataset == dataset {
			f.cancel()
			delete(r.flights, key)
		}
	}
}

func (r *ReadingTitles) InvalidateObject(dataset, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, entry := range r.cache {
		if key.Dataset == dataset && key.ID == id {
			r.bytes -= len(entry.item.Body)
			delete(r.cache, key)
		}
	}
	for key, f := range r.flights {
		if key.Dataset == dataset && key.ID == id {
			f.cancel()
			delete(r.flights, key)
		}
	}
}
