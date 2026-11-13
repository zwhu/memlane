package simpledesktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// Opt-in read-only evidence against the already running official service.
// It never invokes search, a model, ingestion or indexing, and logs no raw text.
func TestReadingOfficialReadonly(t *testing.T) {
	path := os.Getenv("MEMLANE_READING_CONFIG")
	if path == "" {
		t.Skip("requires explicit existing official config")
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	client := Client{Config: config}
	loader := newReadingTitles()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	type receipt struct {
		Document     string `json:"document_id"`
		Hash         string `json:"raw_sha256"`
		Versioned    bool   `json:"versioned"`
		Recognized   bool   `json:"recognized"`
		TitleDerived bool   `json:"title_derived"`
		CopiesDiffer bool   `json:"copies_differ"`
	}
	receipts := []receipt{}
	for _, project := range config.Projects {
		items, _, err := client.documents(ctx, project, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			read, err := loader.read(ctx, client, item, "official-readonly", false, false)
			if err != nil {
				t.Fatal(err)
			}
			p := itemPresentation(read)
			if read.Body == "" || !read.BodyLoaded || p.DisplayTitle == "" {
				t.Fatal("missing official readable data")
			}
			receipts = append(receipts, receipt{read.ID, fmt.Sprintf("%x", sha256.Sum256([]byte(read.Body))), read.Document.ContentHash != "" || read.Document.Updated != "", p.WrapperRecognized, read.Title == p.DisplayTitle, p.VisibleBody != read.Body})
		}
	}
	encoded, _ := json.Marshal(receipts)
	t.Log("READING_OFFICIAL_METADATA " + string(encoded))
}
