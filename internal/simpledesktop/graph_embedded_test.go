package simpledesktop

import (
	"io/fs"
	"testing"

	"github.com/zwhu/memlane"
)

func TestEmbeddedGraphAssetsVerifyAndLazyLoad(t *testing.T) {
	// Constructing App without GraphAssets must not eagerly allocate or load graph assets.
	a := New(Config{}, nil)
	if a.graph != nil {
		t.Fatal("expected New(Config{}, nil) not to eagerly allocate GraphHost")
	}

	sub, err := fs.Sub(memlane.RuntimeAssets, "web/official-graph/dist")
	if err != nil {
		t.Fatalf("fs.Sub embedded graph assets failed: %v", err)
	}
	host, err := RegisterGraphFS(sub)
	if err != nil {
		t.Fatalf("RegisterGraphFS failed: %v", err)
	}
	for _, required := range []string{"index.html", "app.js", "app.css", "host.css", "build.json"} {
		if len(host.assets[required]) == 0 {
			t.Fatalf("missing embedded graph asset %q", required)
		}
	}

	cached, err := loadEmbeddedGraphHost()
	if err != nil {
		t.Fatalf("loadEmbeddedGraphHost failed: %v", err)
	}
	if len(cached.assets["app.js"]) == 0 || cached == host {
		t.Fatalf("expected fresh GraphHost backed by cached verified embedded assets")
	}
}
