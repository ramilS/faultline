package ingest

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"

	"github.com/google/uuid"
	lru "github.com/hashicorp/golang-lru/v2"
)

const (
	// shardCount lock-isolates dedup state to reduce contention under load.
	shardCount = 16
	// shardCapacity bounds each shard; 16 × 65,536 ≈ 1M entries (~48 MB). The
	// LRU covers SDK retry storms, the only realistic duplicate source. Dups
	// surviving eviction or a restart merely inflate counters and are accepted.
	shardCapacity = 1 << 16
)

type dedupKey struct {
	projectID int64
	eventID   uuid.UUID
}

// Deduplicator records recently admitted (project, event) pairs in a sharded
// bounded LRU. It is safe for concurrent use.
type Deduplicator struct {
	shards [shardCount]*lru.Cache[dedupKey, struct{}]
}

// NewDeduplicator builds a deduplicator with the documented fixed capacity.
func NewDeduplicator() *Deduplicator {
	dedup, err := newDeduplicator(shardCapacity)
	if err != nil {
		// shardCapacity is a positive constant, so construction cannot fail.
		panic(fmt.Sprintf("ingest: build deduplicator: %v", err))
	}
	return dedup
}

func newDeduplicator(perShardCapacity int) (*Deduplicator, error) {
	dedup := &Deduplicator{}
	for index := range dedup.shards {
		cache, err := lru.New[dedupKey, struct{}](perShardCapacity)
		if err != nil {
			return nil, err
		}
		dedup.shards[index] = cache
	}
	return dedup, nil
}

// Seen reports whether (projectID, eventID) was already recorded, inserting it
// atomically when unseen. It returns true only for an existing entry.
func (d *Deduplicator) Seen(projectID int64, eventID uuid.UUID) bool {
	shard := d.shards[shardFor(projectID, eventID)]
	existed, _ := shard.ContainsOrAdd(dedupKey{projectID: projectID, eventID: eventID}, struct{}{})
	return existed
}

func shardFor(projectID int64, eventID uuid.UUID) int {
	hasher := fnv.New32a()
	var scratch [8]byte
	binary.LittleEndian.PutUint64(scratch[:], uint64(projectID))
	_, _ = hasher.Write(scratch[:])
	_, _ = hasher.Write(eventID[:])
	return int(hasher.Sum32() % shardCount)
}
