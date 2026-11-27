package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func readingBodyJSON(w http.ResponseWriter, req *http.Request, content string) {
	parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/api/knowledge/"), "/")
	ds, docID := "dataset", ""
	if len(parts) >= 2 {
		ds, docID = parts[0], parts[1]
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"dataset_id":  ds,
		"document_id": docID,
		"content":     content,
		"mime_type":   "text/markdown",
	})
}

func readingTestItem(id, version string) Item {
	d := lessonDocument()
	d.ID = id
	d.ContentHash = version
	return Item{ID: id, Dataset: d.Dataset, Kind: "知识", Document: &d}
}
func TestReadingSchedulerConcurrencyDedupAndCache(t *testing.T) {
	var active, peak, calls atomic.Int32
	gate := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		<-gate
		readingBodyJSON(w, req, "# Session learning (session s)\n\nReadable content.")
	}))
	defer server.Close()
	r := newReadingTitles()
	c := Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}
	var wg sync.WaitGroup
	for n := 0; n < 30; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if _, err := r.read(context.Background(), c, readingTestItem(fmt.Sprint(n), "v1"), "1", false, true); err != nil {
				t.Error(err)
			}
		}(n)
	}
	deadline := time.After(3 * time.Second)
	for calls.Load() < 4 {
		select {
		case <-deadline:
			t.Fatal("workers not started")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("unbounded concurrency %d", calls.Load())
	}
	close(gate)
	wg.Wait()
	if peak.Load() > 4 || calls.Load() != 30 {
		t.Fatalf("peak %d calls %d", peak.Load(), calls.Load())
	}
	for n := 0; n < 2; n++ {
		if _, e := r.read(context.Background(), c, readingTestItem("0", "v1"), "next-page", false, true); e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 30 {
		t.Fatal("valid version cache not reused")
	}
	if _, e := r.read(context.Background(), c, readingTestItem("0", "v2"), "next-page", false, true); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 31 {
		t.Fatal("version reused stale raw")
	}
}
func TestReadingMergeCancelTimeoutAndDenied(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 10)
	release := make(chan struct{})
	var status atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
		case <-req.Context().Done():
			return
		}
		if s := status.Load(); s != 0 {
			w.WriteHeader(int(s))
			return
		}
		readingBodyJSON(w, req, "raw")
	}))
	defer server.Close()
	r := newReadingTitles()
	c := Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}
	i := readingTestItem("same", "v")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 2)
	go func() { _, e := r.read(ctx, c, i, "1", false, true); done <- e }()
	<-entered
	go func() { _, e := r.read(context.Background(), c, i, "1", true, false); done <- e }()
	// Wait for the second subscriber, rather than relying on a scheduling delay.
	deadline := time.Now().Add(time.Second)
	for {
		r.mu.Lock()
		waiters := 0
		for _, f := range r.flights {
			waiters = f.waiters
		}
		r.mu.Unlock()
		if waiters == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("merge not registered")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if e := <-done; e == nil {
		t.Fatal("cancel not observed")
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("selected item duplicated preheat")
	}
	status.Store(403)
	if _, e := r.read(context.Background(), c, i, "1", true, false); !readingUnavailable(e) {
		t.Fatal(e)
	}
	r.mu.Lock()
	cached := len(r.cache)
	r.mu.Unlock()
	if cached != 0 {
		t.Fatal("denied body cached")
	}
	status.Store(404)
	if _, e := r.read(context.Background(), c, i, "1", true, false); !readingUnavailable(e) {
		t.Fatal(e)
	}
	r.timeout = time.Nanosecond
	if _, e := r.read(context.Background(), c, readingTestItem("timeout", "v"), "1", true, false); e == nil {
		t.Fatal("timeout accepted")
	}
}
func TestReadingCredentialAndInvalidationBounds(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	os.WriteFile(token, []byte("first-management-token-0123456789abcdef"), 0600)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		readingBodyJSON(w, req, "raw")
	}))
	defer server.Close()
	c := Client{Config: Config{ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}}
	r := newReadingTitles()
	i := readingTestItem("d", "v")
	r.read(context.Background(), c, i, "1", false, true)
	os.WriteFile(token, []byte("second-management-token-9876543210fedcba"), 0600)
	r.read(context.Background(), c, i, "1", false, true)
	if calls.Load() != 2 {
		t.Fatal("credentials reused cache")
	}
	r.Invalidate("dataset")
	r.mu.Lock()
	if len(r.cache) != 0 {
		t.Fatal("revocation kept raw")
	}
	r.mu.Unlock()
	r.mu.Lock()
	for n := 0; n < 200; n++ {
		item := i
		item.Body = strings.Repeat("a", 100000)
		r.putLocked(readingKey{ID: fmt.Sprint(n)}, item)
	}
	if len(r.cache) > 128 || r.bytes > 8*1024*1024 {
		t.Fatal("unbounded cache")
	}
	r.mu.Unlock()
}

func TestReadingCredentialRotationRejectsInflightRaw(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	os.WriteFile(token, []byte("first-management-token-0123456789abcdef"), 0600)
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		close(entered)
		<-release
		readingBodyJSON(w, req, "old identity raw")
	}))
	defer server.Close()
	r := newReadingTitles()
	c := Client{Config: Config{ProjectControl: &ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}}
	done := make(chan error, 1)
	go func() {
		_, err := r.read(context.Background(), c, readingTestItem("d", "v"), "1", false, false)
		done <- err
	}()
	<-entered
	os.WriteFile(token, []byte("second-management-token-9876543210fedcba"), 0600)
	close(release)
	if err := <-done; err == nil || !readingUnavailable(err) {
		t.Fatal("old identity response accepted", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cache) != 0 {
		t.Fatal("old credential raw retained")
	}
}
