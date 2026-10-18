package proguard

import (
	"container/list"
	"maps"
	"sync"
)

const (
	// maxCachedMappings bounds how many mappings stay parsed. Each holds only
	// the classes recent events mentioned, not the whole mapping.
	maxCachedMappings = 32

	// maxCoveredClasses bounds the classes one cached mapping is asked to keep,
	// so a mapping read by many different events cannot grow into a full parse.
	maxCoveredClasses = 2048
)

// mappingCache keeps the parsed part of each recently used mapping, so reading
// another event of the same issue does not fetch, decompress and scan the
// stored mapping again. A stored mapping never changes under its UUID (Put
// refuses different bytes), so an entry is never stale.
//
// An entry remembers every class it was asked to parse, found or not: a class
// the mapping does not contain is answered from the cache too, and a read that
// needs a class outside that set re-parses for the union instead.
type mappingCache struct {
	mu      sync.Mutex
	max     int
	order   *list.List // front = most recently used
	entries map[string]*list.Element
}

type cachedMapping struct {
	key     string
	mapping *Mapping
	covered map[string]struct{}
}

func newMappingCache(capacity int) *mappingCache {
	return &mappingCache{max: capacity, order: list.New(), entries: map[string]*list.Element{}}
}

// get returns the cached mapping when it was parsed for every class in classes.
func (c *mappingCache) get(key string, classes map[string]struct{}) (*Mapping, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	entry := entryOf(element.Value)
	for class := range classes {
		if _, covered := entry.covered[class]; !covered {
			return nil, false
		}
	}
	c.order.MoveToFront(element)
	return entry.mapping, true
}

// coverageFor returns the classes a fresh parse should keep: the requested ones
// plus whatever the cached entry already covers, unless that would exceed the
// class budget, in which case only the requested ones.
func (c *mappingCache) coverageFor(key string, classes map[string]struct{}) map[string]struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return classes
	}
	merged := maps.Clone(entryOf(element.Value).covered)
	maps.Copy(merged, classes)
	if len(merged) > maxCoveredClasses {
		return classes
	}
	return merged
}

func (c *mappingCache) put(key string, mapping *Mapping, covered map[string]struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		c.order.Remove(element)
	}
	c.entries[key] = c.order.PushFront(&cachedMapping{key: key, mapping: mapping, covered: covered})
	for c.order.Len() > c.max {
		evicted := c.order.Remove(c.order.Back())
		delete(c.entries, entryOf(evicted).key)
	}
}

func entryOf(value any) *cachedMapping {
	entry, _ := value.(*cachedMapping)
	return entry
}
