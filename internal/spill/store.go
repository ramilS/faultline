package spill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrSpillFull = errors.New("spill: capacity exceeded")

const segmentPrefix = "segment-"
const segmentSuffix = ".log"

// Options configures the Store.
type Options struct {
	Dir           string
	SegmentBytes  int64
	SegmentMaxAge time.Duration
	FsyncInterval time.Duration
	MaxBytes      int64
	Now           func() time.Time
}

// Store is an append-only WAL of accepted envelopes. The active segment is
// written by Append; sealed segments are immutable and consumed by the feeder.
type Store struct {
	opts Options

	mu           sync.Mutex
	sealed       []Segment
	active       *segmentFile
	activeSeq    uint64
	activeOpened time.Time
	sealedBytes  int64

	notify chan struct{}
	stop   chan struct{}
	done   chan struct{}
}

func Open(opts Options) (*Store, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, err
	}
	if err := secureMode(opts.Dir, 0o700); err != nil {
		return nil, err
	}

	store := &Store{opts: opts, notify: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	if err := store.sealExisting(); err != nil {
		return nil, err
	}
	if err := store.openActive(); err != nil {
		return nil, err
	}
	go store.fsyncLoop()
	return store, nil
}

// secureMode tightens an already-existing path to want. Neither MkdirAll nor
// OpenFile applies its mode argument to a target that exists, so a store first
// created before segment payloads were treated as secrets keeps its original,
// laxer mode indefinitely unless Open chmods it.
//
// A failed chmod is not fatal by itself: the Compose deployment bind-mounts the
// spill directory from the host, and chmod on a path the process does not own
// returns EPERM. What is fatal is a path still reachable by other local
// accounts afterwards, which is the exposure this exists to close. Open secures
// the directory before sealExisting runs, so by the time a segment is checked
// its parent is provably 0700.
func secureMode(path string, want os.FileMode) error {
	chmodErr := os.Chmod(path, want)
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	got := info.Mode().Perm()
	if got&^want == 0 {
		return nil
	}
	if chmodErr != nil {
		return fmt.Errorf("spill: %s is mode %04o, not %04o, and could not be tightened: %w", path, got, want, chmodErr)
	}
	return fmt.Errorf("spill: %s is mode %04o, not %04o, after chmod reported success", path, got, want)
}

// sealExisting turns every pre-existing segment file into a sealed segment, so
// restart replay is the feeder consuming these files.
func (s *Store) sealExisting() error {
	entries, err := os.ReadDir(s.opts.Dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, segmentPrefix) || !strings.HasSuffix(name, segmentSuffix) {
			continue
		}
		seq, parseErr := parseSeq(name)
		if parseErr != nil {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return statErr
		}
		path := filepath.Join(s.opts.Dir, name)
		if err := secureMode(path, 0o600); err != nil {
			return err
		}
		s.sealed = append(s.sealed, Segment{Seq: seq, Path: path})
		s.sealedBytes += info.Size()
		if seq > s.activeSeq {
			s.activeSeq = seq
		}
	}
	sort.Slice(s.sealed, func(i, j int) bool { return s.sealed[i].Seq < s.sealed[j].Seq })
	return nil
}

func (s *Store) openActive() error {
	s.activeSeq++
	path := filepath.Join(s.opts.Dir, segmentName(s.activeSeq))
	file, err := openSegmentFile(path)
	if err != nil {
		return err
	}
	s.active = file
	s.activeOpened = s.opts.Now()
	return nil
}

// Append writes one record, rolling the active segment on size or age first,
// and rejects with ErrSpillFull once the cap would be exceeded.
func (s *Store) Append(rec Record) error {
	frame := encodeRecord(rec)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sealedBytes+s.active.size+int64(len(frame)) > s.opts.MaxBytes {
		return ErrSpillFull
	}
	if s.active.size > 0 && (s.active.size+int64(len(frame)) > s.opts.SegmentBytes ||
		s.opts.Now().Sub(s.activeOpened) >= s.opts.SegmentMaxAge) {
		if err := s.rollLocked(); err != nil {
			return err
		}
	}
	if err := s.active.append(frame); err != nil {
		return err
	}
	return nil
}

// rollLocked seals the active segment and opens a fresh one. Caller holds mu.
//
// Seal + advance unconditionally: a failed sync/close must not wedge the
// store. The bytes are already in the page cache (a failed fsync does not
// unwrite them), and the feeder reads via the page cache, so the sealed
// segment is still replayable — consistent with the group-commit durability
// level (ack after write, fsync batched). Only a genuinely unrecoverable
// failure to open a new active segment should leave the store unable to
// proceed.
func (s *Store) rollLocked() error {
	sealedSize := s.active.size
	syncErr := s.active.sync()
	closeErr := s.active.close()
	s.sealed = append(s.sealed, Segment{Seq: s.activeSeq, Path: filepath.Join(s.opts.Dir, segmentName(s.activeSeq))})
	s.sealedBytes += sealedSize
	if err := s.openActive(); err != nil {
		return err // unrecoverable: cannot create a new active segment
	}
	s.signal()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func (s *Store) fsyncLoop() {
	defer close(s.done)
	ticker := time.NewTicker(s.opts.FsyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.active != nil {
				if s.active.size > 0 && s.opts.Now().Sub(s.activeOpened) >= s.opts.SegmentMaxAge {
					if err := s.rollLocked(); err != nil {
						// rollLocked now always advances s.active on a sync/close failure
						// (see its doc comment), so this fallback sync targets the fresh,
						// just-opened segment (a harmless no-op — nothing written to it
						// yet) rather than the old one. It only matters if openActive
						// itself failed, in which case s.active is still the old, closed
						// segment and the sync errors harmlessly; the real signal of that
						// failure is the next Append surfacing the error to its caller.
						_ = s.active.sync()
					}
				} else {
					_ = s.active.sync()
				}
			}
			s.mu.Unlock()
		}
	}
}

// Sealed returns the un-pruned sealed segments oldest → newest.
func (s *Store) Sealed() []Segment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Segment(nil), s.sealed...)
}

// Prune deletes every sealed segment with Seq <= throughSeq and reclaims its
// bytes toward the cap. Called by the feeder after a segment's records are all
// durably in Postgres.
func (s *Store) Prune(throughSeq uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	remaining := s.sealed[:0:0]
	for _, seg := range s.sealed {
		if seg.Seq > throughSeq {
			remaining = append(remaining, seg)
			continue
		}
		info, statErr := os.Stat(seg.Path)
		if statErr == nil {
			s.sealedBytes -= info.Size()
		}
		if err := os.Remove(seg.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	s.sealed = remaining
	return nil
}

func (s *Store) Notify() <-chan struct{} { return s.notify }

func (s *Store) signal() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *Store) Close() error {
	close(s.stop)
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return nil
	}
	if err := s.active.sync(); err != nil {
		return err
	}
	return s.active.close()
}

func segmentName(seq uint64) string {
	return fmt.Sprintf("%s%010d%s", segmentPrefix, seq, segmentSuffix)
}

func parseSeq(name string) (uint64, error) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(name, segmentPrefix), segmentSuffix)
	return strconv.ParseUint(trimmed, 10, 64)
}
