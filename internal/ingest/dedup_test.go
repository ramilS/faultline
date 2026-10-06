package ingest

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestDedupReportsFirstSeenThenDuplicate(t *testing.T) {
	dedup := NewDeduplicator()
	id := uuid.New()

	if dedup.Seen(1, id) {
		t.Fatal("first observation must report unseen")
	}
	if !dedup.Seen(1, id) {
		t.Fatal("second observation must report duplicate")
	}
}

func TestDedupIsolatesProjects(t *testing.T) {
	dedup := NewDeduplicator()
	id := uuid.New()

	if dedup.Seen(1, id) || dedup.Seen(2, id) {
		t.Fatal("the same event id in different projects must both be first-seen")
	}
	if !dedup.Seen(1, id) || !dedup.Seen(2, id) {
		t.Fatal("repeat observations must be duplicates per project")
	}
}

func TestDedupShardSelectionIsStableAndDistributed(t *testing.T) {
	id := uuid.New()
	sameValue, err := uuid.Parse(id.String())
	if err != nil {
		t.Fatalf("re-parse event id: %v", err)
	}
	if shardFor(1, id) != shardFor(1, sameValue) {
		t.Fatal("shard selection must depend on the (project, event id) value, not the instance")
	}

	occupied := make(map[int]bool)
	for index := range 2000 {
		shard := shardFor(int64(index), uuid.New())
		if shard < 0 || shard >= shardCount {
			t.Fatalf("shard %d out of range", shard)
		}
		occupied[shard] = true
	}
	if len(occupied) < shardCount/2 {
		t.Fatalf("shards used = %d, want a wide spread across %d", len(occupied), shardCount)
	}
}

func TestDedupEvictsBeyondCapacity(t *testing.T) {
	dedup, err := newDeduplicator(1)
	if err != nil {
		t.Fatalf("new deduplicator: %v", err)
	}
	first := eventInShard(t, 1, 0, nil)
	second := eventInShard(t, 1, 0, map[uuid.UUID]bool{first: true})

	if dedup.Seen(1, first) {
		t.Fatal("first insert must be unseen")
	}
	if dedup.Seen(1, second) {
		t.Fatal("second insert into the same shard must be unseen")
	}
	if dedup.Seen(1, first) {
		t.Fatal("capacity-one shard must have evicted the first entry")
	}
}

func TestDedupConcurrentDuplicatesYieldOneFirstSeen(t *testing.T) {
	dedup := NewDeduplicator()
	id := uuid.New()

	const callers = 64
	var firstSeen int64
	var group sync.WaitGroup
	start := make(chan struct{})
	for range callers {
		group.Go(func() {
			<-start
			if !dedup.Seen(7, id) {
				atomic.AddInt64(&firstSeen, 1)
			}
		})
	}
	close(start)
	group.Wait()

	if firstSeen != 1 {
		t.Fatalf("first-seen results = %d, want exactly 1", firstSeen)
	}
}

func eventInShard(t *testing.T, projectID int64, shard int, avoid map[uuid.UUID]bool) uuid.UUID {
	t.Helper()
	for range 1_000_000 {
		id := uuid.New()
		if shardFor(projectID, id) == shard && (avoid == nil || !avoid[id]) {
			return id
		}
	}
	t.Fatalf("could not find a uuid mapping to shard %d", shard)
	return uuid.UUID{}
}
