package simpledesktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOfficialHTTPErrorAndStatusCodeHelper(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("  token expired: " + strings.Repeat("x", 300)))
	}))
	defer server.Close()

	c := Client{Config: Config{ServiceURL: server.URL}}
	_, err := c.request(context.Background(), "GET", "/api/v1/datasets", nil)
	if err == nil || err.Error() != "official HTTP 401" {
		t.Fatalf("expected 'official HTTP 401', got %v", err)
	}
	var official *OfficialHTTPError
	if !errors.As(err, &official) {
		t.Fatalf("expected OfficialHTTPError, got %T", err)
	}
	if official.Status != 401 || len(official.Body) > 256 || !strings.HasPrefix(official.Body, "token expired:") {
		t.Fatalf("unexpected OfficialHTTPError fields: %+v", official)
	}
	if code, ok := httpStatusCode(err); !ok || code != 401 {
		t.Fatalf("httpStatusCode failed for OfficialHTTPError: %d %v", code, ok)
	}
	if code, ok := httpStatusCode(&ControlResponseError{Status: 403, Message: "forbidden"}); !ok || code != 403 {
		t.Fatalf("httpStatusCode failed for ControlResponseError: %d %v", code, ok)
	}
	if code, ok := httpStatusCode(errors.New("official HTTP 404")); !ok || code != 404 {
		t.Fatalf("httpStatusCode failed for string fallback: %d %v", code, ok)
	}
}

func TestReadCachedTokenValidationAndRotation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "secret.token")
	first := strings.Repeat("a", 32)
	if err := os.WriteFile(path, []byte(first+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	checkToken := func(want string) {
		t.Helper()
		got, err := readCachedToken(path, 32, true)
		if err != nil || got != want {
			t.Fatalf("readCachedToken() = %q, %v; want %q", got, err, want)
		}
	}
	checkUnavailable := func() {
		t.Helper()
		got, err := readCachedToken(path, 32, true)
		if got != "" || err == nil || err.Error() != "private credential unavailable" {
			t.Fatalf("readCachedToken() = %q, %v; want private credential unavailable", got, err)
		}
	}

	checkToken(first)
	checkToken(first)
	for _, mode := range []os.FileMode{0640, 0604, 0644} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		checkUnavailable()
	}

	second := strings.Repeat("b", 40)
	next := filepath.Join(root, "next.token")
	if err := os.WriteFile(next, []byte(second+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, path); err != nil {
		t.Fatal(err)
	}
	checkToken(second)

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	checkUnavailable()
	if err := os.WriteFile(next, []byte(second), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(next, path); err != nil {
		t.Fatal(err)
	}
	checkUnavailable()
}

func TestReadCachedTokenFileBoundaries(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "secret.token")
	for _, tc := range []struct {
		name           string
		content        string
		requirePrivate bool
		wantToken      string
		wantError      string
	}{
		{name: "private directory", requirePrivate: true, wantError: "private credential unavailable"},
		{name: "public directory", wantError: "credential unavailable"},
		{name: "private oversized", content: strings.Repeat("a", maxCachedTokenBytes+1), requirePrivate: true, wantError: "credential too large"},
		{name: "public oversized", content: strings.Repeat("a", maxCachedTokenBytes+1), wantError: "credential too large"},
		{name: "private exact max size", content: strings.Repeat("m", maxCachedTokenBytes), requirePrivate: true, wantToken: strings.Repeat("m", maxCachedTokenBytes)},
		{name: "too short after trimming", content: " short \n", requirePrivate: true, wantError: "credential too short"},
		{name: "public trimmed", content: " \tpublic-token\n", wantToken: "public-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := root
			if tc.content != "" {
				target = path
				mode := os.FileMode(0644)
				if tc.requirePrivate {
					mode = 0600
				}
				if err := os.WriteFile(target, []byte(tc.content), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(target, mode); err != nil {
					t.Fatal(err)
				}
			}
			token, err := readCachedToken(target, 10, tc.requirePrivate)
			if tc.wantError != "" {
				if token != "" || err == nil || err.Error() != tc.wantError {
					t.Fatalf("readCachedToken() = %q, %v; want %q error", token, err, tc.wantError)
				}
				return
			}
			if token != tc.wantToken || err != nil {
				t.Fatalf("readCachedToken() = %q, %v; want %q", token, err, tc.wantToken)
			}
		})
	}
}

func TestReadCachedTokenRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "secret.fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name           string
		requirePrivate bool
		wantErr        string
	}{
		{name: "private fifo", requirePrivate: true, wantErr: "private credential unavailable"},
		{name: "public fifo", requirePrivate: false, wantErr: "credential unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				tok, err := readCachedToken(fifo, 10, tc.requirePrivate)
				if tok != "" {
					done <- errors.New("unexpected non-empty token from fifo")
					return
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("readCachedToken(fifo) error = %v; want %q", err, tc.wantErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("readCachedToken blocked on FIFO instead of rejecting non-regular file")
			}
		})
	}
}

func TestClientGetDoesNotPersistKnowledgeDaysSideEffect(t *testing.T) {
	root := t.TempDir()
	stateFile := filepath.Join(root, "client-state.json")
	token := filepath.Join(root, "token")
	if err := os.WriteFile(token, []byte("official-key"), 0600); err != nil {
		t.Fatal(err)
	}
	docs := []Document{{
		ID:      "d1",
		Dataset: "ds",
		Name:    "记忆 1",
		Created: "2026-11-18T10:00:00Z",
		Metadata: map[string]any{
			"node_set":     []any{"session_learnings"},
			"session_date": "2026-11-18",
		},
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(docs)
	}))
	defer server.Close()

	c := Client{Config: Config{ServiceURL: server.URL, TokenFile: token, ClientStateFile: stateFile}}
	var raw []Document
	if err := c.get(context.Background(), "/api/v1/datasets/ds/data", &raw); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "knowledge-source-dates-*.json"))
	if len(matches) != 0 {
		t.Fatalf("Client.get unexpectedly wrote knowledge dates side effect: %v", matches)
	}
	if _, _, err := c.documents(context.Background(), Project{ID: "p1", Dataset: "ds"}, 0, false); err != nil {
		t.Fatal(err)
	}
	matches, _ = filepath.Glob(filepath.Join(root, "knowledge-source-dates-*.json"))
	if len(matches) != 1 {
		t.Fatalf("expected Client.documents to record knowledge dates, got %v", matches)
	}
}
