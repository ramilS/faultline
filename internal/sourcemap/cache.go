package sourcemap

import (
	"bytes"
	"compress/gzip"
	"container/list"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"sync"

	gosourcemap "github.com/go-sourcemap/sourcemap"
)

// ParsedMapCache holds parsed source-map consumers under a weighted LRU budget.
// Concurrent misses for the same key share a single parse.
type ParsedMapCache struct {
	mu        sync.Mutex
	maxWeight int64
	weight    int64
	order     *list.List               // front = most recently used
	entries   map[string]*list.Element // key -> *cacheEntry element
	inflight  map[string]*loadCall
}

type cacheEntry struct {
	key      string
	consumer *gosourcemap.Consumer
	weight   int64
}

type loadCall struct {
	done     chan struct{}
	consumer *gosourcemap.Consumer
	err      error
}

// NewParsedMapCache creates a cache that retains at most maxWeight bytes of
// uncompressed source-map content.
func NewParsedMapCache(maxWeight int64) *ParsedMapCache {
	return &ParsedMapCache{
		maxWeight: maxWeight,
		order:     list.New(),
		entries:   make(map[string]*list.Element),
		inflight:  make(map[string]*loadCall),
	}
}

func cacheKey(m Map) string {
	return strconv.FormatInt(m.BlobID, 10) + ":" + hex.EncodeToString(m.ContentSHA256[:])
}

// Get returns the parsed consumer for m, parsing and caching it on a miss.
func (c *ParsedMapCache) Get(ctx context.Context, m Map) (*gosourcemap.Consumer, error) {
	key := cacheKey(m)

	c.mu.Lock()
	if element, ok := c.entries[key]; ok {
		c.order.MoveToFront(element)
		//nolint:errcheck,forcetypeassert // c.order only ever holds *cacheEntry
		consumer := element.Value.(*cacheEntry).consumer
		c.mu.Unlock()
		return consumer, nil
	}
	if call, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		return waitForLoad(ctx, call)
	}
	call := &loadCall{done: make(chan struct{})}
	c.inflight[key] = call
	c.mu.Unlock()

	call.consumer, call.err = parseMap(m)
	close(call.done)

	c.mu.Lock()
	delete(c.inflight, key)
	if call.err == nil {
		c.store(key, m, call.consumer)
	}
	c.mu.Unlock()

	return call.consumer, call.err
}

func waitForLoad(ctx context.Context, call *loadCall) (*gosourcemap.Consumer, error) {
	select {
	case <-call.done:
		return call.consumer, call.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// store inserts a freshly parsed consumer, evicting least-recently-used entries
// to stay within the weight budget. Maps larger than the budget are not retained.
func (c *ParsedMapCache) store(key string, m Map, consumer *gosourcemap.Consumer) {
	weight := m.UncompressedBytes
	if weight <= 0 {
		weight = 1
	}
	if weight > c.maxWeight {
		return
	}
	for c.weight+weight > c.maxWeight {
		back := c.order.Back()
		if back == nil {
			break
		}
		//nolint:errcheck,forcetypeassert // c.order only ever holds *cacheEntry
		evicted := c.order.Remove(back).(*cacheEntry)
		delete(c.entries, evicted.key)
		c.weight -= evicted.weight
	}
	entry := &cacheEntry{key: key, consumer: consumer, weight: weight}
	c.entries[key] = c.order.PushFront(entry)
	c.weight += weight
}

func parseMap(m Map) (*gosourcemap.Consumer, error) {
	reader, err := gzip.NewReader(bytes.NewReader(m.CompressedMap))
	if err != nil {
		return nil, fmt.Errorf("open source map: %w", err)
	}
	defer reader.Close()

	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("inflate source map: %w", err)
	}
	consumer, err := gosourcemap.Parse("", raw)
	if err != nil {
		return nil, fmt.Errorf("parse source map: %w", err)
	}
	return consumer, nil
}
