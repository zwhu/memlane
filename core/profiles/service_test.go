package profiles

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

type runnerFunc func(context.Context, RunRequest, func(Progress)) (string, error)

func (f runnerFunc) Run(ctx context.Context, r RunRequest, report func(Progress)) (string, error) {
	return f(ctx, r, report)
}

type profileEngine struct {
	engine.MemoryEngine
	mu       sync.Mutex
	items    map[string]engine.Item
	onIngest func(context.Context, engine.IngestRequest) (engine.IngestResult, error)
}

func (e *profileEngine) Ingest(ctx context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
	if e.onIngest != nil {
		return e.onIngest(ctx, req)
	}
	return e.ingest(req)
}

func (e *profileEngine) ingest(req engine.IngestRequest) (engine.IngestResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	body := req.Content.(string)
	id := req.IdempotencyKey
	digest := fmt.Sprintf("%x", md5.Sum([]byte(body)))
	e.items[id] = engine.Item{ID: id, DatasetID: req.Scope.DatasetID, Content: body, ContentHash: digest}
	return engine.IngestResult{OK: true, DocumentID: id, ContentHash: digest}, nil
}

func (e *profileEngine) GetItem(_ context.Context, scope engine.Scope, id string) (engine.Item, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	item, ok := e.items[id]
	if !ok || scope.DatasetID != item.DatasetID {
		return engine.Item{}, engine.ErrNotFound
	}
	return item, nil
}

func (e *profileEngine) DeleteItem(_ context.Context, scope engine.Scope, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.items[id].DatasetID != scope.DatasetID {
		return engine.ErrNotFound
	}
	delete(e.items, id)
	return nil
}

func profileFixture(t *testing.T) (*domain.Store, *profileEngine) {
	t.Helper()
	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "domain.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, &profileEngine{items: make(map[string]engine.Item)}
}

func serviceFixture(t *testing.T, cfg Config) *Service {
	t.Helper()
	service, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return service
}

func waitFor[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for test signal")
		var zero T
		return zero
	}
}

func waitStatus(t *testing.T, store *domain.Store, id, status string) domain.UserProfile {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		row, err := store.GetUserProfile(context.Background(), id)
		if err == nil && row.Status == status {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatalf("profile %s expected %s, got %+v (%v)", id, status, row, err)
		}
		time.Sleep(time.Millisecond)
	}
}

func profileRequest(h http.Handler, method, path, input string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(input)))
	return rec
}

func assertHTTP(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("HTTP %d, want %d: %s", rec.Code, status, rec.Body)
	}
}

func TestExportProgressAndNonCancellableSaving(t *testing.T) {
	store, eng := profileFixture(t)
	step := make(chan string)
	started := make(chan bool, 1)
	returned := make(chan struct{})
	saving := make(chan context.Context, 1)
	release := make(chan struct{})
	defer close(returned)
	eng.onIngest = func(ctx context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
		saving <- ctx
		select {
		case <-release:
			return eng.ingest(req)
		case <-returned:
			return engine.IngestResult{}, errors.New("test ended")
		}
	}
	runner := runnerFunc(func(ctx context.Context, req RunRequest, report func(Progress)) (string, error) {
		_, hasDeadline := ctx.Deadline()
		started <- hasDeadline || req.Prompt != "custom" || req.IdleThreshold != time.Millisecond
		for {
			select {
			case state := <-step:
				if state == "return" {
					return " # exported\n\n", nil
				}
				report(Progress{State: state, LastActivityAt: 42, ElapsedSeconds: 7, IdleSeconds: 2})
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	})
	s := serviceFixture(t, Config{Store: store, Engine: eng, Runner: runner, Prompt: "custom", IdleThreshold: time.Millisecond})
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	id, replay, err := s.Import(ctx, "codex", "export")
	if id != "export" || replay || err != nil || waitFor(t, started) {
		t.Fatalf("start = %q %t %v, or runner received an invalid request", id, replay, err)
	}
	waitStatus(t, store, "codex", "queued")
	for _, state := range []string{"activity", "waiting", "exited"} {
		step <- state
		row := waitStatus(t, store, "codex", state)
		var p Progress
		wantProcess := "running"
		if state == "exited" {
			wantProcess = "exited"
		}
		if json.Unmarshal([]byte(row.ProgressJSON), &p) != nil || p.ProcessState != wantProcess || p.LastActivityAt != 42 || p.ElapsedSeconds != 7 || p.IdleSeconds != 2 {
			t.Fatalf("progress for %s: %+v", state, p)
		}
	}
	step <- "return"
	saveCtx := waitFor(t, saving)
	waitStatus(t, store, "codex", "saving")
	if err := s.Cancel(t.Context(), "codex", id); !errors.Is(err, ErrCancelUnavailable) {
		t.Fatalf("Cancel while saving: %v", err)
	}
	h := s.Handler()
	assertHTTP(t, profileRequest(h, "POST", "/api/profiles/codex/cancel", ""), 409)
	assertHTTP(t, profileRequest(h, "DELETE", "/api/profiles/codex", ""), 409)
	cancel()
	closeCtx, stopClosing := context.WithCancel(context.Background())
	stopClosing()
	if err := s.Close(closeCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close did not wait for saving: %v", err)
	}
	if saveCtx.Err() != nil || saveCtx.Done() != nil {
		t.Fatal("saving context was cancellable")
	}
	close(release)
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	row := waitStatus(t, store, "codex", "saved")
	attempt, err := store.GetUserProfileAttempt(t.Context(), id)
	if err != nil || attempt.Status != "saved" || attempt.Document != row.Document || attempt.ContentHash != row.ContentHash {
		t.Fatalf("attempt: %+v, %v", attempt, err)
	}
	_, body, err := s.Get(t.Context(), "codex")
	if err != nil || body != " # exported\n\n" {
		t.Fatalf("read body %q, %v", body, err)
	}
}

func TestCancelBeforeSavingAndWhileQueuedForSlot(t *testing.T) {
	store, eng := profileFixture(t)
	started := make(chan string, 2)
	s := serviceFixture(t, Config{Store: store, Engine: eng, Runner: runnerFunc(func(ctx context.Context, req RunRequest, report func(Progress)) (string, error) {
		report(Progress{State: "activity"})
		started <- req.Source
		<-ctx.Done()
		return "must not be saved", nil
	})})
	ctx := t.Context()
	if err := s.Manual(ctx, "codex", "previous", "# previous"); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	assertHTTP(t, profileRequest(h, "POST", "/api/profiles/codex/import", `{"operation_id":"run"}`), 202)
	if source := waitFor(t, started); source != "codex" {
		t.Fatal(source)
	}
	assertHTTP(t, profileRequest(h, "POST", "/api/profiles/codex/import", `{"operation_id":"run"}`), 200)
	assertHTTP(t, profileRequest(h, "POST", "/api/profiles/codex/import", "{}"), 409)
	assertHTTP(t, profileRequest(h, "POST", "/pi/import", ""), 202)
	waitStatus(t, store, "pi", "queued")
	assertHTTP(t, profileRequest(h, "POST", "/pi/cancel", ""), 200)
	waitStatus(t, store, "pi", "cancelled")
	assertHTTP(t, profileRequest(h, "POST", "/codex/cancel", `{"operation_id":"other"}`), 409)
	assertHTTP(t, profileRequest(h, "POST", "/codex/cancel", `{"operation_id":"run"}`), 200)
	row := waitStatus(t, store, "codex", "cancelled")
	if row.Document.DataID != "previous" || row.Previous != row.Document || len(started) > 0 {
		t.Fatalf("previous or serialization lost: %+v", row)
	}
	attempt, err := store.GetUserProfileAttempt(ctx, "run")
	if err != nil || attempt.ErrorCode != "profile_cancelled" || attempt.Document != row.Document {
		t.Fatalf("cancel attempt %+v (%v)", attempt, err)
	}
}

func TestManualSaveVerificationAndHTTP(t *testing.T) {
	store, eng := profileFixture(t)
	const body = " \n# 中文\r\n\n"
	eng.onIngest = func(_ context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
		if req.Scope != (engine.Scope{DatasetID: "protected-id"}) || req.Kind != engine.IngestKindDocument {
			t.Errorf("bad profile scope: %+v", req)
		}
		switch req.IdempotencyKey {
		case "bad-receipt":
			return engine.IngestResult{OK: true, DocumentID: "original", ContentHash: "invalid"}, nil
		case "dedup":
			eng.mu.Lock()
			item := eng.items["original"]
			item.ContentHash = ""
			eng.items["original"] = item
			eng.mu.Unlock()
			return engine.IngestResult{OK: true, DocumentID: "original", Deduplicated: true}, nil
		case "bad-item":
			doc, err := eng.ingest(req)
			eng.mu.Lock()
			item := eng.items[doc.DocumentID]
			item.ContentHash = "invalid"
			eng.items[doc.DocumentID] = item
			eng.mu.Unlock()
			return doc, err
		case "missing-item":
			marked := req.Content.(string)
			return engine.IngestResult{OK: true, DocumentID: "missing", ContentHash: fmt.Sprintf("%x", sha256.Sum256([]byte(marked)))}, nil
		default:
			want := markerPrefix + `{"source":"manual:chatgpt","operation":"original"}` + markerEnd + body
			if req.Content != want || req.SourceMeta.ExternalMetadata["profile_operation"] != "original" ||
				req.SourceMeta.ExternalMetadata["memlane_kind"] != "user_profile" {
				t.Errorf("ingested %+v, want %q", req, want)
			}
			return eng.ingest(req)
		}
	}
	s := serviceFixture(t, Config{Store: store, Engine: eng, DatasetResolver: func(context.Context) (string, error) { return "protected-id", nil }})
	h := s.Handler()
	list := profileRequest(h, "GET", "/api/profiles", "")
	assertHTTP(t, list, 200)
	var payload struct {
		Profiles []profileView
		Prompt   string
	}
	prompt, err := os.ReadFile("../../engine/prompts/user-profile-export.md")
	if err != nil || json.Unmarshal(list.Body.Bytes(), &payload) != nil || payload.Prompt != strings.TrimSpace(string(prompt)) || len(payload.Profiles) != len(sources) {
		t.Fatalf("invalid profiles directory: %s", list.Body)
	}
	assertHTTP(t, profileRequest(h, "GET", "/api/profiles/unknown", ""), 404)
	assertHTTP(t, profileRequest(h, "GET", "/api/profiles/chatgpt", ""), 404)
	assertHTTP(t, profileRequest(h, "POST", "/api/profiles/cursor/import", ""), 409)
	assertHTTP(t, profileRequest(h, "POST", "/api/profiles/chatgpt/manual", `{"body":" "}`), 400)
	badUTF8 := `{"body":"` + string([]byte{0xff}) + `"}`
	assertHTTP(t, profileRequest(h, "POST", "/chatgpt/manual", badUTF8), 400)
	assertHTTP(t, profileRequest(h, "POST", "/chatgpt/manual", `{"body":"`+strings.Repeat("a", MaxDocumentBytes+1)+`"}`), 400)

	input, _ := json.Marshal(map[string]string{"content": body, "operation_id": "original"})
	post := profileRequest(h, "POST", "/api/profiles/chatgpt/manual", string(input))
	assertHTTP(t, post, 200)
	for _, id := range []string{"bad-receipt", "dedup", "bad-item", "missing-item"} {
		if err := s.Manual(t.Context(), "chatgpt", id, "changed"); !errors.Is(err, ErrSaveUnconfirmed) {
			t.Fatalf("save %s returned %v", id, err)
		}
		a, err := store.GetUserProfileAttempt(t.Context(), id)
		if err != nil || a.ErrorCode != "profile_save_unconfirmed" || a.Document.DataID != "original" || a.Previous != a.Document || a.ContentHash == "" {
			t.Fatalf("failure attempt %+v (%v)", a, err)
		}
	}
	rec := profileRequest(h, "GET", "/manual:chatgpt", "")
	assertHTTP(t, rec, 200)
	var profile profileView
	if json.Unmarshal(rec.Body.Bytes(), &profile) != nil || profile.Body != body || profile.Document.DataID != "original" {
		t.Fatalf("read after failure: %s", rec.Body)
	}
	assertHTTP(t, profileRequest(h, "DELETE", "/api/profiles/chatgpt", ""), 200)
	assertHTTP(t, profileRequest(h, "GET", "/chatgpt", ""), 404)
	if _, err := eng.GetItem(t.Context(), engine.Scope{DatasetID: "protected-id"}, "original"); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestRestartAndSanitizedFailureTelemetry(t *testing.T) {
	store, eng := profileFixture(t)
	ctx := t.Context()
	for i, status := range []string{"queued", "waiting", "activity", "exited", "saving"} {
		id := sources[i]
		row := domain.UserProfile{InstanceID: id, Kind: id, OperationID: status, Status: status,
			Document: domain.DocumentRef{DatasetID: "protected-id", DataID: "prior"}}
		if err := store.UpsertUserProfile(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	runs := make(chan string, 8)
	stderr := "\x1b[31mERROR authentication failed at https://private.example/path with Bearer bear-token api_key='api value' sk-sensitive user@example.com, /Users/Hidden Name/config\x1b[0m"
	s := serviceFixture(t, Config{Store: store, Engine: eng, Runner: runnerFunc(func(_ context.Context, req RunRequest, _ func(Progress)) (string, error) {
		runs <- req.Source
		code := 7
		return "", agentFailure(&code, []byte(stderr))
	})})
	for i, status := range []string{"queued", "waiting", "activity", "exited", "saving"} {
		row := waitStatus(t, store, sources[i], "unknown")
		a, err := store.GetUserProfileAttempt(ctx, status)
		if err != nil || a.ErrorCode != "profile_interrupted" || a.Document != row.Document || row.Document.DataID != "prior" {
			t.Fatalf("recovery %+v %+v (%v)", row, a, err)
		}
	}
	if len(runs) != 0 {
		t.Fatal("runner started without an explicit import")
	}
	assertHTTP(t, profileRequest(s.Handler(), "POST", "/api/profiles/claude/import", `{"operation_id":"failed-run"}`), 202)
	if source := waitFor(t, runs); source != "claude_code" {
		t.Fatal(source)
	}
	row := waitStatus(t, store, "claude_code", "failed")
	a, err := store.GetUserProfileAttempt(ctx, "failed-run")
	if err != nil || a.ExitCode == nil || *a.ExitCode != 7 || a.FailureReason != "authentication_required" || a.ErrorCode != "profile_agent_failed" {
		t.Fatalf("attempt %+v (%v)", a, err)
	}
	for _, secret := range []string{"private.example", "bear-token", "api value", "sk-sensitive", "user@example.com", "/Users", "Hidden Name", "\x1b"} {
		if strings.Contains(row.ExtraJSON, secret) || strings.Contains(SanitizeFailureDetail(stderr), secret) {
			t.Fatalf("leaked %q: %s", secret, row.ExtraJSON)
		}
	}
}

func TestManualSaveSurvivesCallerCancellation(t *testing.T) {
	store, eng := profileFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	eng.onIngest = func(ctx context.Context, req engine.IngestRequest) (engine.IngestResult, error) {
		close(started)
		<-release
		if err := ctx.Err(); err != nil {
			return engine.IngestResult{}, err
		}
		return eng.ingest(req)
	}
	s := serviceFixture(t, Config{Store: store, Engine: eng})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Manual(ctx, "pi", "manual", " #-pasted ") }()
	waitFor(t, started)
	cancel()
	close(release)
	if err := waitFor(t, done); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, store, "pi", "saved")
}
