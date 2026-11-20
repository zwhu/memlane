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
	"testing"
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
	if err := os.WriteFile(path, []byte(first+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readCachedToken(path, 32, true); err == nil {
		t.Fatal("expected 0644 private token to be rejected")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readCachedToken(path, 32, true)
	if err != nil || got != first {
		t.Fatalf("unexpected token read: %q %v", got, err)
	}
	// Cached hit still checks permissions/symlink via Lstat.
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readCachedToken(path, 32, true); err == nil {
		t.Fatal("expected warmed cache to reject widened permissions")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	second := strings.Repeat("b", 40)
	if err := os.WriteFile(path, []byte(second+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got2, err := readCachedToken(path, 32, true)
	if err != nil || got2 != second {
		t.Fatalf("expected rotated token %q, got %q %v", second, got2, err)
	}
	link := filepath.Join(root, "link.token")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readCachedToken(link, 32, true); err == nil {
		t.Fatal("expected symlink token to be rejected")
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
