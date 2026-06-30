package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"waveloggate/internal/config"
	"waveloggate/internal/wavelog"
)

// wavelogHandler mimics Wavelog's /api/qso endpoint.
// mode: "ok" → created, "reject" → permanent reject, "transient" → 500 server error.
func wavelogHandler(mode string, count *int, mu *sync.Mutex) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]string
		_ = json.Unmarshal(body, &p)

		mu.Lock()
		*count++
		mu.Unlock()

		switch mode {
		case "ok":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "created"})
		case "reject":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "failed", "reason": "duplicate"})
		case "transient":
			http.Error(w, "server error", http.StatusInternalServerError)
		}
	})
}

func newClient(t *testing.T, baseURL string) *wavelog.Client {
	t.Helper()
	cfg := &config.Profile{
		WavelogURL: baseURL + "/index.php",
		WavelogKey: "testkey",
		WavelogID:  "1",
	}
	return wavelog.New(cfg, "test")
}

func tmpQueuePath(t *testing.T) string {
	dir := t.TempDir()
	return filepath.Join(dir, "queue.jsonl")
}

// TestQueueDrainSuccess: queue with one item drains successfully when Wavelog is up.
func TestQueueDrainSuccess(t *testing.T) {
	var count int
	var mu sync.Mutex
	srv := httptest.NewServer(wavelogHandler("ok", &count, &mu))
	defer srv.Close()

	client := newClient(t, srv.URL)
	path := tmpQueuePath(t)

	var results []*wavelog.QSOResult
	var pendingCalls []int
	q := New(path, client,
		func(r *wavelog.QSOResult) { results = append(results, r) },
		func(n int) { pendingCalls = append(pendingCalls, n) },
	)

	q.Push("<call:5>TEST1 <eor>")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go q.Run(ctx, 100*time.Millisecond)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if q.Pending() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if q.Pending() != 0 {
		t.Fatalf("queue should be empty, has %d", q.Pending())
	}
	if len(results) != 1 || !results[0].Success {
		t.Fatalf("expected 1 successful result, got %+v", results)
	}
}

// TestQueueTransientKeepsItem: a transient server failure keeps the item queued.
func TestQueueTransientKeepsItem(t *testing.T) {
	var count int
	var mu sync.Mutex
	srv := httptest.NewServer(wavelogHandler("transient", &count, &mu))
	defer srv.Close()

	client := newClient(t, srv.URL)
	path := tmpQueuePath(t)

	q := New(path, client, nil, nil)
	q.Push("<call:5>TEST2 <eor>")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	q.Run(ctx, 100*time.Millisecond) // synchronous — should exit on ctx after retries

	if q.Pending() != 1 {
		t.Fatalf("transient failure should keep item queued, got pending=%d", q.Pending())
	}
}

// TestQueuePermanentDropsItem: a permanent Wavelog rejection drops the item.
func TestQueuePermanentDropsItem(t *testing.T) {
	var count int
	var mu sync.Mutex
	srv := httptest.NewServer(wavelogHandler("reject", &count, &mu))
	defer srv.Close()

	client := newClient(t, srv.URL)
	path := tmpQueuePath(t)

	q := New(path, client, nil, nil)
	q.Push("<call:5>TEST3 <eor>")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go q.Run(ctx, 100*time.Millisecond)

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if q.Pending() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if q.Pending() != 0 {
		t.Fatalf("permanent failure should drop item, got pending=%d", q.Pending())
	}
}

// TestQueuePersistsAcrossRestart: pushed items survive a New+Load cycle.
func TestQueuePersistsAcrossRestart(t *testing.T) {
	path := tmpQueuePath(t)

	q1 := New(path, nil, nil, nil)
	q1.Push("<call:5>PERSIST1 <eor>")
	q1.Push("<call:5>PERSIST2 <eor>")

	q2 := New(path, nil, nil, nil)
	q2.Load()

	if got := q2.Pending(); got != 2 {
		t.Fatalf("expected 2 items after reload, got %d", got)
	}
}

// TestQueueFlush: Flush clears items and removes the file.
func TestQueueFlush(t *testing.T) {
	path := tmpQueuePath(t)
	q := New(path, nil, nil, nil)
	q.Push("<call:5>FLUSH1 <eor>")
	q.Push("<call:5>FLUSH2 <eor>")

	q.Flush()

	if got := q.Pending(); got != 0 {
		t.Fatalf("expected 0 pending after flush, got %d", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("queue file should be removed after flush, got err=%v", err)
	}
}

// TestQueueWakeTriggersImmediateDrain: Wake on Push triggers drain without waiting for ticker.
func TestQueueWakeTriggersImmediateDrain(t *testing.T) {
	var count int
	var mu sync.Mutex
	srv := httptest.NewServer(wavelogHandler("ok", &count, &mu))
	defer srv.Close()

	client := newClient(t, srv.URL)
	path := tmpQueuePath(t)

	q := New(path, client, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go q.Run(ctx, 1*time.Hour) // long ticker — only Wake should trigger drain

	q.Push("<call:5>WAKE1 <eor>")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		c := count
		mu.Unlock()
		if c >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	c := count
	mu.Unlock()
	if c < 1 {
		t.Fatalf("Wake should trigger immediate drain; server got %d requests", c)
	}
	fmt.Println("server received", c, "requests via wake-triggered drain")
}
