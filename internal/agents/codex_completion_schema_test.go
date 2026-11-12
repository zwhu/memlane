package agents

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestCodexSchemaOfficialCodingMemoryRequests(t *testing.T) {
	root := "testdata/coding-memory-schemas-ec6f408"
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Schemas []struct{ File, SHA256 string }
	}
	if err = json.Unmarshal(raw, &manifest); err != nil || len(manifest.Schemas) != 5 {
		t.Fatal("sealed schema manifest missing", err)
	}
	for _, entry := range manifest.Schemas {
		t.Run(entry.File, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, entry.File))
			if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != entry.SHA256 {
				t.Fatal("sealed official schema changed", err)
			}
			strict, err := codexCompletionSchema(raw)
			if err != nil {
				t.Fatal("official coding schema cannot reach Codex", err)
			}
			var schema jsonschema.Schema
			if err = json.Unmarshal(strict, &schema); err != nil {
				t.Fatal(err)
			}
			if _, err = schema.Resolve(nil); err != nil {
				t.Fatal("adapted official schema is invalid", err)
			}
			var check func(any)
			check = func(v any) {
				switch node := v.(type) {
				case map[string]any:
					if node["type"] == "object" {
						properties, _ := node["properties"].(map[string]any)
						required, _ := node["required"].([]any)
						if node["additionalProperties"] != false || len(properties) != len(required) {
							t.Fatal("non-strict object remains")
						}
					}
					for _, child := range node {
						check(child)
					}
				case []any:
					for _, child := range node {
						check(child)
					}
				}
			}
			var value any
			if err = json.Unmarshal(strict, &value); err != nil {
				t.Fatal(err)
			}
			check(value)
		})
	}
}

func TestCodexSchemaPydanticNestedModelsKeepConstraintsAndData(t *testing.T) {
	// A field named default and literal default keys are data, not annotations.
	raw := json.RawMessage(`{"type":"object","properties":{"lessons":{"type":"array","items":{"$ref":"#/$defs/Lesson"},"default":[]}},"$defs":{"Lesson":{"type":"object","properties":{"default":{"type":"string","minLength":3},"state_key":{"anyOf":[{"type":"string"},{"type":"null"}],"default":null},"literal":{"const":{"default":"keep"}}},"required":["default","literal"]}}}`)
	before := string(raw)
	strict, err := codexCompletionSchema(raw)
	if err != nil || string(raw) != before {
		t.Fatal("schema adaptation failed or mutated input", err)
	}
	if strings.Contains(string(strict), `"default":null`) || !strings.Contains(string(strict), `"const":{"default":"keep"}`) {
		t.Fatal("annotations and literal data confused", string(strict))
	}
	var schema jsonschema.Schema
	if err = json.Unmarshal(strict, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		json string
		ok   bool
	}{
		{`{"lessons":[{"default":"abc","state_key":null,"literal":{"default":"keep"}}]}`, true},
		{`{"lessons":[]}`, true},
		{`{}`, false},
		{`{"lessons":[{"default":"abc","literal":{"default":"keep"}}]}`, false},
		{`{"lessons":[{"default":"a","state_key":null,"literal":{"default":"keep"}}]}`, false},
		{`{"lessons":[],"extra":true}`, false},
	} {
		var value any
		if err = json.Unmarshal([]byte(test.json), &value); err != nil {
			t.Fatal(err)
		}
		if (resolved.Validate(value) == nil) != test.ok {
			t.Fatalf("wrong strict schema validation for %s", test.json)
		}
	}
}

func TestCodexSchemaDoesNotEraseOpenDictionaries(t *testing.T) {
	for _, raw := range []string{
		`{"type":"object"}`,
		`{"type":"object","properties":{},"additionalProperties":true}`,
		`{"type":"object","properties":{"labels":{"type":"object","additionalProperties":{"type":"string"}}}}`,
		`{"type":"object","properties":{"x":{"type":"string"}},"required":["unknown"]}`,
	} {
		if _, err := codexCompletionSchema(json.RawMessage(raw)); err == nil {
			t.Fatal("unsupported contract silently narrowed", raw)
		}
	}
	strict, err := codexCompletionSchema(json.RawMessage(`{"type":"object","properties":{"n":{"const":9007199254740993}},"required":["n"]}`))
	if err != nil || !strings.Contains(string(strict), "9007199254740993") {
		t.Fatal("numeric constraint changed", string(strict), err)
	}
}
