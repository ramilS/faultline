package sourcemap

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"os"
	"path/filepath"
	"sync"
	"testing"

	gosourcemap "github.com/go-sourcemap/sourcemap"
)

func gzipMap(t *testing.T, id int64, raw []byte, uncompressed int64) Map {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(raw); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return Map{
		ID:                id,
		BlobID:            id,
		CompressedMap:     buf.Bytes(),
		UncompressedBytes: uncompressed,
		ContentSHA256:     sha256.Sum256(raw),
	}
}

func fixtureMap(t *testing.T, id int64) Map {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "app.js.map"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return gzipMap(t, id, raw, int64(len(raw)))
}

func TestCacheReturnsSameConsumerOnHit(t *testing.T) {
	cache := NewParsedMapCache(1 << 20)
	m := fixtureMap(t, 1)

	first, err := cache.Get(t.Context(), m)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	second, err := cache.Get(t.Context(), m)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if first != second {
		t.Fatalf("cache hit returned a different consumer: %p vs %p", first, second)
	}
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "app.js.map"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	// Budget holds exactly one weighted entry.
	cache := NewParsedMapCache(int64(len(raw)))
	a := gzipMap(t, 1, raw, int64(len(raw)))
	b := gzipMap(t, 2, raw, int64(len(raw)))

	firstA, err := cache.Get(t.Context(), a)
	if err != nil {
		t.Fatalf("Get a: %v", err)
	}
	if _, err := cache.Get(t.Context(), b); err != nil {
		t.Fatalf("Get b: %v", err)
	}
	// a must have been evicted; a fresh parse yields a new pointer.
	secondA, err := cache.Get(t.Context(), a)
	if err != nil {
		t.Fatalf("re-Get a: %v", err)
	}
	if firstA == secondA {
		t.Fatalf("expected a to be evicted and re-parsed, got identical consumer %p", firstA)
	}
}

func TestCacheDoesNotRetainOversizeMap(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "app.js.map"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	cache := NewParsedMapCache(int64(len(raw)) - 1)
	m := gzipMap(t, 1, raw, int64(len(raw)))

	first, err := cache.Get(t.Context(), m)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	second, err := cache.Get(t.Context(), m)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if first == second {
		t.Fatalf("oversize map should never be retained, got identical consumer %p", first)
	}
}

func TestCacheSingleFlightsConcurrentMisses(t *testing.T) {
	cache := NewParsedMapCache(1 << 20)
	m := fixtureMap(t, 42)

	const workers = 16
	results := make([]*gosourcemap.Consumer, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := range workers {
		go func(i int) {
			defer wg.Done()
			consumer, err := cache.Get(t.Context(), m)
			if err != nil {
				t.Errorf("concurrent Get: %v", err)
				return
			}
			results[i] = consumer
		}(i)
	}
	wg.Wait()

	for i := 1; i < workers; i++ {
		if results[i] == nil || results[0] == nil || results[i] != results[0] {
			t.Fatalf("single-flight expected one shared consumer; worker %d differs", i)
		}
	}
}

// Two refs (a debug-id one and a release+path one) on the same blob must share
// one parsed Consumer. Keying on Map.ID would parse the same content twice and
// charge the weight budget twice, with no assertion anywhere else catching it.
func TestCacheSharesOneParseAcrossRefsOfOneBlob(t *testing.T) {
	cache := NewParsedMapCache(1 << 20)
	blob := fixtureMap(t, 1)

	byRelease := blob
	byRelease.ID = 10
	byRelease.GeneratedPath = "/assets/app.js"

	byDebugID := blob
	byDebugID.ID = 11
	byDebugID.GeneratedPath = ""

	first, err := cache.Get(t.Context(), byRelease)
	if err != nil {
		t.Fatalf("get by release: %v", err)
	}
	second, err := cache.Get(t.Context(), byDebugID)
	if err != nil {
		t.Fatalf("get by debug id: %v", err)
	}
	if first != second {
		t.Fatal("refs on one blob returned different consumers")
	}
}
