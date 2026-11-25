package reading

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/zwhu/memlane/app/api"
)

type recorderTransport struct{ http.Handler }

func (rt recorderTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		defer r.Body.Close()
	}
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)
	return w.Result(), nil
}

func readingTestClient(t *testing.T, h http.HandlerFunc) *api.Client {
	t.Helper()
	cfg := api.Config{ReadingIdentity: "reader"}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	switch {
	case err == nil:
		server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: h}}
		server.Start()
		t.Cleanup(server.Close)
		cfg.BaseURL, cfg.HTTPClient = server.URL, server.Client()
	case errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EACCES):
		cfg.BaseURL = "http://127.0.0.1"
		cfg.HTTPClient = &http.Client{Transport: recorderTransport{h}}
	default:
		t.Fatal(err)
	}
	client, err := api.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type readingTestFence struct{ auth atomic.Uint64 }

func (*readingTestFence) Epoch() uint64           { return 1 }
func (f *readingTestFence) AuthEpoch() uint64     { return f.auth.Load() }
func (*readingTestFence) ReadingIdentity() string { return "reader" }
func (f *readingTestFence) InvalidateReading()    { f.auth.Add(1) }

func take[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case x := <-ch:
		return x
	case <-time.After(3 * time.Second):
		t.Fatal("request did not complete")
		var zero T
		return zero
	}
}

func TestUnreadAPIWriterCoalescesChunksAndOrdersMarkAll(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	trace := make(chan string, 12)
	client := readingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Memlane-Reading-Identity") != "reader" {
			t.Error("reading identity missing")
		}
		switch r.URL.Path {
		case "/api/reading-state":
			_ = json.NewEncoder(w).Encode(api.ReadingStateResponse{Projects: []api.ReadingStateView{
				{ProjectID: r.URL.Query().Get("project_id"), DatasetID: "ds", UnreadCount: 6, StateRev: 3},
			}})
			return
		case "/api/reading-state/mark-read":
			var input api.MarkReadRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.ProjectID != "p" || input.DatasetID != "ds" {
				t.Errorf("invalid mark-read input %+v, %v", input, err)
			}
			trace <- fmt.Sprintf("read:%d", len(input.DocIDs))
			if calls.Add(1) == 1 {
				close(started)
				<-release
			}
		case "/api/reading-state/mark-all-read":
			trace <- "all"
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.MarkReadResponse{})
	})
	ctx := context.Background()
	state, err := client.ReadingState(ctx, "p")
	if err != nil || len(state.Projects) != 1 || state.Projects[0].UnreadCount != 6 || state.Projects[0].ProjectID != "p" {
		t.Fatalf("reading state: %+v, %v", state, err)
	}
	w := NewReadWriter(&readingTestFence{})
	req := api.MarkReadRequest{ProjectID: "p", DatasetID: "ds", ReadingIdentity: "reader", DocIDs: []string{"first"}}
	w.MarkRead(client, req, nil)
	take(t, started)
	for n := range 255 {
		req.DocIDs = []string{fmt.Sprint(n), fmt.Sprint(n)}
		w.MarkRead(client, req, nil)
	}
	w.MarkAllRead(client, req, nil)
	req.DocIDs = []string{"after"}
	w.MarkRead(client, req, nil)
	if !w.HasPending("p", "ds") || w.HasPending("other", "ds") {
		t.Fatal("pending write escaped its scope")
	}
	if err := w.FlushWithTimeout(time.Millisecond); err == nil {
		t.Fatal("flush waited past its timeout")
	}
	close(release)
	if err := w.FlushWithTimeout(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	close(trace)
	var got []string
	for call := range trace {
		got = append(got, call)
	}
	want := []string{"read:1", "read:200", "read:55", "all", "read:1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestReadWriterRetriesAndFlushesFailedMarks(t *testing.T) {
	var attempts atomic.Int32
	var recoverTransport atomic.Bool
	client := readingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if !recoverTransport.Load() {
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(api.MarkReadResponse{})
	})
	writer := NewReadWriter(&readingTestFence{})
	writer.MarkRead(client, api.MarkReadRequest{ProjectID: "p", DatasetID: "ds", DocIDs: []string{"a"}}, nil)
	writer.WaitFlush()
	if attempts.Load() != 3 || !writer.HasPending("p", "ds") {
		t.Fatalf("failed attempts=%d", attempts.Load())
	}
	recoverTransport.Store(true)
	if err := writer.FlushWithTimeout(time.Second); err != nil || attempts.Load() != 4 || writer.HasPending("p", "ds") {
		t.Fatalf("flush: %v, attempts=%d", err, attempts.Load())
	}
}

func TestUnreadRevisionBodyAndAuthFence(t *testing.T) {
	var state UnreadScopeState
	if !ApplyServerSnapshot(&state, 5, 10, false) || ApplyServerSnapshot(&state, 9, 9, false) ||
		ApplyServerSnapshot(&state, 9, 0, false) || ApplyServerSnapshot(&state, 9, 10, true) ||
		state.ServerUnreadCount != 5 || !ApplyServerSnapshot(&state, 3, 11, true) {
		t.Fatalf("revision guard failed: %+v", state)
	}
	if CanMarkRead("知识", true, true, false, false) || CanMarkRead("知识", true, true, true, true) ||
		CanMarkRead("文档", true, true, true, false) || !CanMarkRead("知识", true, true, true, false) {
		t.Fatal("incorrect full-body eligibility")
	}
	started, release := make(chan struct{}), make(chan struct{})
	fence := &readingTestFence{}
	var calls atomic.Int32
	client := readingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		_ = json.NewEncoder(w).Encode(api.MarkReadResponse{})
	})
	writer := NewReadWriter(fence)
	done := make(chan error, 2)
	req := api.MarkReadRequest{ProjectID: "p", DatasetID: "ds", DocIDs: []string{"queued"}}
	complete := func(_ api.MarkReadResponse, err error) { done <- err }
	writer.MarkRead(client, req, complete)
	take(t, started)
	writer.MarkRead(client, req, complete)
	fence.InvalidateReading()
	close(release)
	writer.WaitFlush()
	if !errors.Is(take(t, done), ErrReadingChanged) || !errors.Is(take(t, done), ErrReadingChanged) || calls.Load() != 1 {
		t.Fatal("revoked write escaped auth fence")
	}
}

func TestReadWriterBoundsFailedIDs(t *testing.T) {
	recovered := atomic.Bool{}
	received := make(chan []string, 30)
	client := readingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !recovered.Load() {
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		var req api.MarkReadRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		received <- req.DocIDs
		_ = json.NewEncoder(w).Encode(api.MarkReadResponse{})
	})
	req := api.MarkReadRequest{ProjectID: "p", DatasetID: "ds", DocIDs: make([]string, FailedReadLimit+100)}
	for n := range req.DocIDs {
		req.DocIDs[n] = fmt.Sprint(n)
	}
	writer := NewReadWriter(&readingTestFence{})
	writer.MarkRead(client, req, nil)
	writer.WaitFlush()
	recovered.Store(true)
	if err := writer.FlushWithTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	close(received)
	var got []string
	for ids := range received {
		if len(ids) > MarkReadBatchLimit {
			t.Fatalf("invalid batch length %d", len(ids))
		}
		got = append(got, ids...)
	}
	if !reflect.DeepEqual(got, req.DocIDs[100:]) {
		t.Fatalf("expected %d most recent failed IDs, got %d", FailedReadLimit, len(got))
	}
}
