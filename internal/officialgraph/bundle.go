// Package officialgraph hosts the unchanged official graph in a local WebView.
package officialgraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const WebCommit = "8853378eae5366addb064e758e560a456146ae1a"

// Bundle is loaded and verified once. Serving never follows a filesystem path
// supplied by the page, or rereads files that could change after verification.
type Bundle struct{ files map[string][]byte }

func LoadBundle(directory string) (Bundle, error) {
	var empty Bundle
	if !filepath.IsAbs(directory) {
		return empty, errors.New("graph_bundle_absolute_path_required")
	}
	read := func(name string) ([]byte, error) {
		file, err := os.OpenFile(filepath.Join(directory, name), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return nil, errors.New("graph_bundle_unavailable")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return nil, errors.New("graph_bundle_invalid")
		}
		return io.ReadAll(io.LimitReader(file, (8<<20)+1))
	}
	raw, err := read("assets.json")
	if err != nil {
		return empty, err
	}
	var manifest struct {
		Protocol int               `json:"protocol"`
		Upstream string            `json:"upstream"`
		Files    map[string]string `json:"files"`
	}
	if len(raw) > 16384 || json.Unmarshal(raw, &manifest) != nil || manifest.Protocol != 1 || manifest.Upstream != WebCommit || len(manifest.Files) < 4 || len(manifest.Files) > 12 {
		return empty, errors.New("graph_bundle_manifest_invalid")
	}
	allowed := map[string]bool{"index.html": true, "app.js": true, "app.css": true, "host.css": true, "app.js.LEGAL.txt": true, "COGNEE-LICENSE": true, "THIRD-PARTY-LICENSES.txt": true, "build.json": true}
	bundle := Bundle{files: map[string][]byte{}}
	total := 0
	for name, digest := range manifest.Files {
		if !allowed[name] || len(digest) != 64 {
			return empty, errors.New("graph_bundle_manifest_invalid")
		}
		bytes, err := read(name)
		if err != nil {
			return empty, err
		}
		sum := sha256.Sum256(bytes)
		if hex.EncodeToString(sum[:]) != digest {
			return empty, errors.New("graph_bundle_digest_changed")
		}
		total += len(bytes)
		if total > 16<<20 {
			return empty, errors.New("graph_bundle_limit")
		}
		bundle.files[name] = bytes
	}
	for _, name := range []string{"index.html", "app.js", "app.css", "host.css"} {
		if bundle.files[name] == nil {
			return empty, errors.New("graph_bundle_incomplete")
		}
	}
	return bundle, nil
}
