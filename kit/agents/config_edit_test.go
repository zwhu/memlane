package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTOMLEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	input := `# heading
model = "old" # model comment
remove = true # retain this comment

[profiles."team.one"] # selected
model = 'profile'
lines = """
one
two"""
flags = [true, 8, 1.5, {label = 'x'}] # untouched

[obsolete] # retain section comment
enabled = false
[obsolete.child]
name = 'remove'

[unrelated]
key = "keep # literal"
`
	putFile(t, path, input, 0o600)
	doc, err := ParseTOML([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	profile := doc["profiles"].(map[string]any)["team.one"].(map[string]any)
	if profile["lines"] != "one\ntwo" || len(profile["flags"].([]any)) != 4 {
		t.Fatalf("%+v", profile)
	}
	changes := []TOMLChange{SetKey(nil, "model", "new"), DeleteKey(nil, "remove"), SetKey([]string{"profiles", "team.one"}, "new", int64(2)), DeleteTable([]string{"obsolete"})}
	preview, err := PreviewTOMLEdit(path, changes, "test")
	if err != nil || !preview.Changed {
		t.Fatalf("%+v %v", preview, err)
	}
	actual := string(preview.After)
	for _, want := range []string{"# heading\nmodel = \"new\" # model comment", "\n# retain this comment\n\n", "[profiles.\"team.one\"] # selected",
		"flags = [true, 8, 1.5, {label = 'x'}] # untouched", "# retain section comment", "[unrelated]\nkey = \"keep # literal\""} {
		if !strings.Contains(actual, want) {
			t.Errorf("missing %q in:\n%s", want, actual)
		}
	}
	if strings.Contains(actual, "[obsolete") {
		t.Fatalf("old table retained: %s", actual)
	}
	if err := ApplyEditPreview(preview, 0); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file permissions: %v %v", st, err)
	}
	if err := ApplyEditPreview(preview, 0); err == nil {
		t.Fatal("stale edit accepted")
	}
}

func TestJSONCEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	input := `// heading
{
  "nested": {
    "keep": "https://example.test", // keep comment
    "remove" /* key comment */ : /* value comment */ true, // trailing comment
  },
  "old": false,
  "last": 3 // last comment
}
// footer
`
	putFile(t, path, input, 0o600)
	preview, err := PreviewJSONEdit(path, func(doc map[string]any) error {
		nested := doc["nested"].(map[string]any)
		delete(nested, "remove")
		nested["new"] = true
		delete(doc, "last")
		doc["old"] = true
		doc["new"] = "added"
		return nil
	}, "update")
	if err != nil {
		t.Fatal(err)
	}
	for _, comment := range []string{"// heading", "// keep comment", "/* key comment */", "/* value comment */", "// trailing comment", "// last comment", "// footer"} {
		if !strings.Contains(string(preview.After), comment) {
			t.Errorf("lost %s", comment)
		}
	}
	doc, err := ParseJSONC(preview.After)
	if err != nil || doc["old"] != true || doc["last"] != nil || doc["new"] != "added" {
		t.Fatalf("%+v %v", doc, err)
	}
}

func TestSafeConfigPaths(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real", "config.toml")
	putFile(t, real, "x = 1\n", 0o600)
	for _, path := range []string{filepath.Join(root, "file-link"), filepath.Join(root, "dir-link", "config.toml")} {
		link, target := path, real
		if filepath.Base(path) == "config.toml" {
			link, target = filepath.Dir(path), filepath.Dir(real)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := SafeReadFile(path, 64); err == nil {
			t.Fatalf("read followed %s", path)
		}
		if err := AtomicWriteFile(path, []byte("changed"), 0); err == nil {
			t.Fatalf("write followed %s", path)
		}
	}
	if _, err := SafeReadFile(real, 1); err == nil {
		t.Fatal("size limit bypassed")
	}
	if _, err := SafeReadFile(filepath.Dir(real), 64); err == nil {
		t.Fatal("directory accepted")
	}
}
