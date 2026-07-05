// Package queue buffers QSO ADIF strings that failed transient submission
// (network down) and retries them in FIFO order until success or permanent error.
package queue

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"waveloggate/internal/debug"
	"waveloggate/internal/wavelog"
)

// Queue is a disk-backed FIFO of ADIF strings awaiting submission to Wavelog.
type Queue struct {
	mu        sync.Mutex
	persistMu sync.Mutex // serialises snapshot+write+rename in persist()
	items     []string
	path      string
	client    *wavelog.Client
	wake      chan struct{}
	onResult  func(*wavelog.QSOResult)
	onPending func(int)
}

// New creates a Queue persisted at path. Callbacks are optional; onResult fires
// per drained QSO, onPending fires with the current size after any change.
func New(path string, client *wavelog.Client, onResult func(*wavelog.QSOResult), onPending func(int)) *Queue {
	return &Queue{
		path:      path,
		client:    client,
		wake:      make(chan struct{}, 1), // ponytail: buffered=1 → Wake never blocks
		onResult:  onResult,
		onPending: onPending,
	}
}

// Load reads any previously persisted queue from disk into memory.
// Missing file is not an error.
func (q *Queue) Load() {
	q.mu.Lock()
	f, err := os.Open(q.path)
	if err != nil {
		q.mu.Unlock()
		return
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 65536), 65536)
	for sc.Scan() {
		line := sc.Text()
		if line != "" {
			q.items = append(q.items, line)
		}
	}
	n := len(q.items)
	q.mu.Unlock()

	f.Close()
	debug.Log("[QUEUE] loaded %d pending QSOs from disk", n)
	q.notifyPending()
}

// Push appends an ADIF string, persists, and wakes the retry loop.
func (q *Queue) Push(adif string) {
	q.mu.Lock()
	q.items = append(q.items, adif)
	n := len(q.items)
	q.mu.Unlock()

	q.persist()
	q.notifyPending()
	q.Wake()

	debug.Log("[QUEUE] buffered QSO (%d pending)", n)
}

// Wake signals the retry loop to attempt a drain immediately.
// Non-blocking; extra wakes coalesce.
func (q *Queue) Wake() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Flush drops all buffered QSOs and removes the queue file.
func (q *Queue) Flush() {
	q.mu.Lock()
	q.items = nil
	q.mu.Unlock()

	q.persistMu.Lock()
	_ = os.Remove(q.path)
	q.persistMu.Unlock()
	q.notifyPending()
	debug.Log("[QUEUE] flushed — all pending QSOs dropped")
}

// Pending returns the current queue depth. Safe for concurrent use.
func (q *Queue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Run drains the queue on wake or every retryInterval. Returns when ctx is cancelled.
// Stops on first transient failure (net still down); continues past permanent failures.
func (q *Queue) Run(ctx context.Context, retryInterval time.Duration) {
	t := time.NewTicker(retryInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-q.wake:
			q.drain()
		case <-t.C:
			if q.Pending() > 0 {
				q.drain()
			}
		}
	}
}

// drain sends every queued QSO in order. Stops at the first transient failure
// (leaves remaining items queued). Permanent failures are dropped + logged.
func (q *Queue) drain() {
	for {
		q.mu.Lock()
		if len(q.items) == 0 {
			q.mu.Unlock()
			return
		}
		adif := q.items[0]
		q.mu.Unlock()

		result, err := q.client.SendQSO(adif, false)
		if err != nil {
			debug.Log("[QUEUE] drain send error: %v (will retry)", err)
			return // treat client error same as transient — keep, retry later
		}

		if result.Success {
			q.shiftHead(adif)
			if q.onResult != nil {
				q.onResult(result)
			}
			continue
		}

		if wavelog.IsTransient(result.Reason) {
			debug.Log("[QUEUE] drain stopped — transient failure: %s", result.Reason)
			return // net still down; keep item, wait for next wake
		}

		// Permanent failure: drop to avoid infinite loop on junk.
		debug.Log("[QUEUE] dropping QSO — permanent failure: %s", result.Reason)
		q.shiftHead(adif)
		if q.onResult != nil {
			q.onResult(result)
		}
	}
}

// shiftHead removes items[0] only if it still equals the item that was just
// sent. The lock is released across the SendQSO network call, so a concurrent
// Flush (which nils the slice) or a later Push could have changed the head.
// Appends never move the head; only a Flush does, and in that case the sent
// item belonged to a flushed slice and must not mutate the new one.
func (q *Queue) shiftHead(expected string) {
	q.mu.Lock()
	removed := false
	if len(q.items) > 0 && q.items[0] == expected {
		q.items = q.items[1:]
		removed = true
	}
	q.mu.Unlock()
	if removed {
		q.persist()
		q.notifyPending()
	}
}

// persist rewrites the queue file atomically from the in-memory slice.
// persistMu is held across snapshot+write+rename so that concurrent callers
// (Push on UDP handler goroutines, shiftHead on the Run goroutine) cannot
// interleave writes to the shared tmp file or rename an older snapshot over
// a newer one. Never acquire persistMu while holding q.mu.
func (q *Queue) persist() {
	q.persistMu.Lock()
	defer q.persistMu.Unlock()

	q.mu.Lock()
	snapshot := make([]string, len(q.items))
	copy(snapshot, q.items)
	path := q.path
	q.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		debug.Log("[QUEUE] persist mkdir error: %v", err)
		return
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		debug.Log("[QUEUE] persist create error: %v", err)
		return
	}
	w := bufio.NewWriter(f)
	for _, s := range snapshot {
		w.WriteString(s)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		debug.Log("[QUEUE] persist flush error: %v", err)
		return
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		debug.Log("[QUEUE] persist close error: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		debug.Log("[QUEUE] persist rename error: %v", err)
		return
	}
}

func (q *Queue) notifyPending() {
	if q.onPending == nil {
		return
	}
	q.mu.Lock()
	n := len(q.items)
	q.mu.Unlock()
	q.onPending(n)
}
