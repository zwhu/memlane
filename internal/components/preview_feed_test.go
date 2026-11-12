package components

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPreviewFeedVerifiesCandidateWithoutReplacingRunningCache(t *testing.T) {
	m, d, _ := inspectionFixture(t)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m.PublicKey = public
	prior := []byte(`{"source":"running-source-cache-must-remain"}`)
	cache := filepath.Join(m.Root, "feed-cache.json")
	if err = os.WriteFile(cache, prior, 0600); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(Feed{Version: 1, Components: []Descriptor{d}})
	if err != nil {
		t.Fatal(err)
	}
	signed := SignedFeed{Payload: payload, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(signed) }))
	defer server.Close()
	feed, err := m.PreviewFeed(context.Background(), server.URL)
	if err != nil || len(feed.Components) != 1 {
		t.Fatal("candidate verification failed", err)
	}
	installed, err := m.Prepare(context.Background(), feed.Components[0], nil)
	if err != nil || !installed.Reused || installed.DownloadedBytes != 0 {
		t.Fatal("preview descriptors could not reuse installed assets", err)
	}
	now, err := os.ReadFile(cache)
	if err != nil || !bytes.Equal(now, prior) {
		t.Fatal("preview replaced live source cache", err)
	}
	// A different signing identity remains a rejection, not an offline bypass.
	m.PublicKey = make(ed25519.PublicKey, ed25519.PublicKeySize)
	if _, err = m.PreviewFeed(context.Background(), server.URL); err == nil {
		t.Fatal("invalid candidate signature accepted")
	}
	now, err = os.ReadFile(cache)
	if err != nil || !bytes.Equal(now, prior) {
		t.Fatal("failed preview changed running cache", err)
	}
}
