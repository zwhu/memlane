package cognee

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/zwhu/memlane/core/engine"
	"golang.org/x/sys/unix"
)

func TestReadBody(t *testing.T) {
	t.Setenv("FILE_ENCRYPTION_ENABLED", "")
	root := t.TempDir()
	data := filepath.Join(root, "data")
	mkdirBodyDir(t, data)
	doc := filepath.Join(data, "hello space.txt")
	writeBody(t, doc, "a界🙂z")
	resolved, err := filepath.EvalSymlinks(doc)
	if err != nil {
		t.Fatal(err)
	}

	reader := NewBodyReader(root, nil)
	for _, location := range []string{
		"hello space.txt", doc, resolved,
		(&url.URL{Scheme: "file", Path: doc}).String(),
		(&url.URL{Scheme: "file", Host: "localhost", Path: doc}).String(),
	} {
		t.Run(location, func(t *testing.T) {
			got, err := reader.ReadBody(t.Context(), engine.LocalBodyRef{Location: location, MimeType: "Text/plain; charset=UTF-8"})
			if err != nil || got.Body != "a界🙂z" || got.Truncated || got.BytesTotal != int64(len("a界🙂z")) {
				t.Fatalf("ReadBody = %+v, %v", got, err)
			}
		})
	}
	for limit, want := range map[int]string{2: "a", 4: "a界", 7: "a界", 8: "a界🙂"} {
		got, err := reader.ReadBody(t.Context(), engine.LocalBodyRef{Location: doc, MaxBytes: limit})
		if err != nil || !got.Truncated || got.Body != want {
			t.Errorf("ReadBody with limit %d = %+v, %v", limit, got, err)
		}
	}
}

func TestReadBodyFromRelocatedStore(t *testing.T) {
	t.Setenv("FILE_ENCRYPTION_ENABLED", "")
	root := t.TempDir()
	mkdirBodyDir(t, filepath.Join(root, "data", "sub"))
	writeBody(t, filepath.Join(root, "data", "text_abc.txt"), "one")
	writeBody(t, filepath.Join(root, "data", "sub", "doc.md"), "two")
	reader := NewBodyReader(root, nil)
	for location, want := range map[string]string{
		"/old/location/official-store/data/text_abc.txt":      "one",
		"file:///old/location/official-store/data/sub/doc.md": "two",
	} {
		got, err := reader.ReadBody(t.Context(), engine.LocalBodyRef{Location: location})
		if err != nil || got.Body != want {
			t.Errorf("ReadBody(%q) = %+v, %v", location, got, err)
		}
	}
}

func TestReadBodyWithSymlinkedStore(t *testing.T) {
	t.Setenv("FILE_ENCRYPTION_ENABLED", "")
	root := t.TempDir()
	data := filepath.Join(root, "data")
	mkdirBodyDir(t, data)
	writeBody(t, filepath.Join(data, "text"), "hello")
	storePath := filepath.Join(t.TempDir(), "store")
	symlinkBody(t, root, storePath)
	reader := NewBodyReader(storePath, nil)
	ref := engine.LocalBodyRef{Location: filepath.Join(storePath, "data", "text")}
	if got, err := reader.ReadBody(t.Context(), ref); err != nil || got.Body != "hello" {
		t.Fatalf("ReadBody through linked store = %+v, %v", got, err)
	}

	movedData := filepath.Join(root, "moved-data")
	if err := os.Rename(data, movedData); err != nil {
		t.Fatal(err)
	}
	symlinkBody(t, movedData, data)
	if _, err := reader.ReadBody(t.Context(), ref); !errors.Is(err, engine.ErrPathDenied) {
		t.Fatalf("ReadBody through linked store and linked data error = %v", err)
	}
}

func TestReadBodyPathDenied(t *testing.T) {
	t.Setenv("FILE_ENCRYPTION_ENABLED", "")
	root := t.TempDir()
	data := filepath.Join(root, "data")
	mkdirBodyDir(t, data)
	outside := t.TempDir()
	outsideDoc := filepath.Join(outside, "text")
	writeBody(t, outsideDoc, "outside root")
	symlinkBody(t, outsideDoc, filepath.Join(data, "leaf-symlink"))
	symlinkBody(t, outside, filepath.Join(data, "dir-symlink"))
	fifo := filepath.Join(data, "fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	reader := NewBodyReader(root, nil)
	for _, path := range []string{
		"", ".", data, "leaf-symlink", "dir-symlink/text", "../text", "a/../text",
		"dir\\text", "bad\x00name", "fifo", outsideDoc, "s3://bucket/text",
		"/old/../store/data/text", "/old/store/data/../secret", "/old/store/data/a/../text",
		"/old/store/data/leaf-symlink", "/old/store/data/dir-symlink/text", "/old/store/data/",
		"/old/store/data/sub//doc.md", "/old/store/text",
		"file://remote" + outsideDoc, "file://" + data + "/%00", "file://" + data + "/%2E%2E/text",
	} {
		t.Run(path, func(t *testing.T) {
			if _, err := reader.ReadBody(t.Context(), engine.LocalBodyRef{Location: path}); !errors.Is(err, engine.ErrPathDenied) {
				t.Fatalf("ReadBody(%q) error = %v", path, err)
			}
		})
	}

	linkedRoot := t.TempDir()
	symlinkBody(t, data, filepath.Join(linkedRoot, "data"))
	if _, err := NewBodyReader(linkedRoot, nil).ReadBody(t.Context(), engine.LocalBodyRef{Location: "fifo"}); !errors.Is(err, engine.ErrPathDenied) {
		t.Fatalf("ReadBody through linked data error = %v", err)
	}
}

func TestReadBodyTextAndEncryption(t *testing.T) {
	t.Setenv("FILE_ENCRYPTION_ENABLED", "")
	root := t.TempDir()
	data := filepath.Join(root, "data")
	mkdirBodyDir(t, data)
	writeBody(t, filepath.Join(data, "nul"), "a\x00b")
	writeBody(t, filepath.Join(data, "binary"), "a\xffb")
	writeBody(t, filepath.Join(data, "text"), "hello")

	reader := NewBodyReader(root, nil)
	for _, ref := range []engine.LocalBodyRef{
		{Location: "nul"}, {Location: "binary"}, {Location: "binary", MaxBytes: 2},
		{Location: "text", MimeType: "application/octet-stream"},
	} {
		if _, err := reader.ReadBody(t.Context(), ref); !errors.Is(err, engine.ErrNotUTF8Text) {
			t.Errorf("ReadBody(%+v) error = %v", ref, err)
		}
	}
	ref := engine.LocalBodyRef{Location: "text"}
	for _, encrypted := range []string{"1", "true", " yes ", "ON"} {
		t.Setenv("FILE_ENCRYPTION_ENABLED", encrypted)
		if _, err := reader.ReadBody(t.Context(), ref); !errors.Is(err, engine.ErrUnsupportedCapability) {
			t.Errorf("ReadBody with encryption %q error = %v", encrypted, err)
		}
	}
	disabled := false
	got, err := NewBodyReader(root, &disabled).ReadBody(t.Context(), ref)
	if err != nil || got.Body != "hello" {
		t.Fatalf("ReadBody with explicit override = %+v, %v", got, err)
	}
}

func mkdirBodyDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeBody(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func symlinkBody(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}
