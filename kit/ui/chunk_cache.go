package ui

import (
	"encoding/binary"
	"hash/maphash"
	"sync"
)

const (
	defaultChunkCacheEntries = 64
	defaultChunkCacheBytes   = 8 * 1024 * 1024
)

var (
	contentSeed1 = maphash.MakeSeed()
	contentSeed2 = maphash.MakeSeed()
)

type ContentKey struct {
	len1, len2, len3 int
	h1, h2           uint64
}

func MarkdownContentKey(src string) ContentKey {
	var h1, h2 maphash.Hash
	h1.SetSeed(contentSeed1)
	h2.SetSeed(contentSeed2)
	_, _ = h1.WriteString(src)
	_, _ = h2.WriteString(src)
	return ContentKey{len1: len(src), h1: h1.Sum64(), h2: h2.Sum64()}
}

// PartsContentKey hashes an ordered sequence of byte slices with their boundaries.
func PartsContentKey(parts ...[]byte) ContentKey {
	var h1, h2 maphash.Hash
	h1.SetSeed(contentSeed1)
	h2.SetSeed(contentSeed2)
	var size [8]byte
	for _, part := range parts {
		binary.LittleEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = h1.Write(size[:])
		_, _ = h2.Write(size[:])
		_, _ = h1.Write(part)
		_, _ = h2.Write(part)
	}
	return ContentKey{len1: len(parts), h1: h1.Sum64(), h2: h2.Sum64()}
}

func TraceContentKey(params, result []byte, errText string) ContentKey {
	var h1, h2 maphash.Hash
	h1.SetSeed(contentSeed1)
	h2.SetSeed(contentSeed2)
	_, _ = h1.Write(params)
	_ = h1.WriteByte(0)
	_, _ = h1.Write(result)
	_ = h1.WriteByte(0)
	_, _ = h1.WriteString(errText)
	_, _ = h2.Write(params)
	_ = h2.WriteByte(0)
	_, _ = h2.Write(result)
	_ = h2.WriteByte(0)
	_, _ = h2.WriteString(errText)
	return ContentKey{
		len1: len(params),
		len2: len(result),
		len3: len(errText),
		h1:   h1.Sum64(),
		h2:   h2.Sum64(),
	}
}

func chunksBytes(chunks []string) int {
	total := 0
	for _, ch := range chunks {
		total += len(ch)
	}
	return total
}

type chunkEntry struct {
	dataset string
	id      string
	slot    string
	content ContentKey
	chunks  []string
	bytes   int
	stamp   uint64
}

type ChunkCache struct {
	mu         sync.Mutex
	entries    []chunkEntry
	totalBytes int
	maxEntries int
	maxBytes   int
	clock      uint64
}

func NewChunkCache() *ChunkCache {
	return NewChunkCacheWithLimits(defaultChunkCacheEntries, defaultChunkCacheBytes)
}

func NewChunkCacheWithLimits(maxEntries, maxBytes int) *ChunkCache {
	return &ChunkCache{maxEntries: maxEntries, maxBytes: maxBytes}
}

func (c *ChunkCache) limits() (maxEntries, maxBytes int) {
	maxEntries = c.maxEntries
	if maxEntries <= 0 {
		maxEntries = defaultChunkCacheEntries
	}
	maxBytes = c.maxBytes
	if maxBytes <= 0 {
		maxBytes = defaultChunkCacheBytes
	}
	return maxEntries, maxBytes
}

func (c *ChunkCache) removeAtLocked(idx int) {
	last := len(c.entries) - 1
	c.totalBytes -= c.entries[idx].bytes
	if idx != last {
		c.entries[idx] = c.entries[last]
	}
	c.entries[last] = chunkEntry{}
	c.entries = c.entries[:last]
}

func (c *ChunkCache) evictOverflowLocked(maxEntries, maxBytes int, keepDataset, keepID, keepSlot string) {
	for len(c.entries) > maxEntries || c.totalBytes > maxBytes {
		oldest := -1
		for idx := range c.entries {
			e := &c.entries[idx]
			if len(c.entries) > 1 && e.dataset == keepDataset && e.id == keepID && e.slot == keepSlot {
				continue
			}
			if oldest < 0 || e.stamp < c.entries[oldest].stamp {
				oldest = idx
			}
		}
		if oldest < 0 {
			break
		}
		c.removeAtLocked(oldest)
	}
}

// GetOrSplit returns cached chunks when the slot and content key match.
// Callers treat the returned slice as read-only.
func (c *ChunkCache) GetOrSplit(dataset, id, slot string, content ContentKey, split func() []string) []string {
	if c == nil {
		return split()
	}
	c.mu.Lock()
	for idx := range c.entries {
		e := &c.entries[idx]
		if e.dataset == dataset && e.id == id && e.slot == slot && e.content == content {
			c.clock++
			e.stamp = c.clock
			chunks := e.chunks
			c.mu.Unlock()
			return chunks
		}
	}
	c.mu.Unlock()

	chunks := split()
	entryBytes := chunksBytes(chunks)

	c.mu.Lock()
	defer c.mu.Unlock()
	maxEntries, maxBytes := c.limits()
	for idx := range c.entries {
		e := &c.entries[idx]
		if e.dataset == dataset && e.id == id && e.slot == slot {
			if entryBytes > maxBytes {
				c.removeAtLocked(idx)
				return chunks
			}
			c.clock++
			c.totalBytes = c.totalBytes - e.bytes + entryBytes
			e.content = content
			e.chunks = chunks
			e.bytes = entryBytes
			e.stamp = c.clock
			c.evictOverflowLocked(maxEntries, maxBytes, dataset, id, slot)
			return chunks
		}
	}
	if entryBytes > maxBytes {
		return chunks
	}
	c.clock++
	c.entries = append(c.entries, chunkEntry{
		dataset: dataset,
		id:      id,
		slot:    slot,
		content: content,
		chunks:  chunks,
		bytes:   entryBytes,
		stamp:   c.clock,
	})
	c.totalBytes += entryBytes
	c.evictOverflowLocked(maxEntries, maxBytes, dataset, id, slot)
	return chunks
}

func (c *ChunkCache) GetOrSplitMarkdown(dataset, id, slot, src string) []string {
	return c.GetOrSplit(dataset, id, slot, MarkdownContentKey(src), func() []string {
		return SplitMarkdownChunks(src)
	})
}

func (c *ChunkCache) GetOrSplitJSONTrace(dataset, id, slot string, params, result []byte, errText string) []string {
	return c.GetOrSplit(dataset, id, slot, TraceContentKey(params, result, errText), func() []string {
		return SplitJSONTraceChunks(params, result, errText)
	})
}

func (c *ChunkCache) Invalidate(dataset string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if dataset == "" {
		clear(c.entries)
		c.entries = nil
		c.totalBytes = 0
		return
	}
	kept := c.entries[:0]
	total := 0
	for _, e := range c.entries {
		if e.dataset != dataset {
			kept = append(kept, e)
			total += e.bytes
		}
	}
	clear(c.entries[len(kept):])
	c.entries = kept
	c.totalBytes = total
}

func (c *ChunkCache) InvalidateObject(dataset, id string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.entries[:0]
	total := 0
	for _, e := range c.entries {
		if !(e.dataset == dataset && e.id == id) {
			kept = append(kept, e)
			total += e.bytes
		}
	}
	clear(c.entries[len(kept):])
	c.entries = kept
	c.totalBytes = total
}

func (c *ChunkCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *ChunkCache) TotalBytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalBytes
}

func (c *ChunkCache) MaxBytes() int {
	_, limit := c.limits()
	return limit
}

func (c *ChunkCache) Clock() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clock
}

func (c *ChunkCache) HasDataset(dataset string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.entries {
		if entry.dataset == dataset {
			return true
		}
	}
	return false
}
