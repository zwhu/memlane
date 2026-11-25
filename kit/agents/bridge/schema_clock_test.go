package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestStrictifyAndValidate(t *testing.T) {
	schema := map[string]any{
		"type": "object", "required": []string{"choice"}, "properties": map[string]any{
			"z": map[string]any{"type": "string", "default": "x"},
			"choice": map[string]any{
				"discriminator": map[string]any{"propertyName": "tag"},
				"oneOf":         []any{map[string]any{"$ref": "#/$defs/positive"}, map[string]any{"const": "other"}},
			},
			"a": map[string]any{"type": "array", "prefixItems": []any{map[string]any{"const": "first"}},
				"items": map[string]any{"type": "integer"}, "minItems": int64(2), "maxItems": int64(3)},
		},
		"$defs": map[string]any{"positive": map[string]any{"allOf": []any{map[string]any{"type": "integer", "minimum": 1},
			map[string]any{"exclusiveMaximum": 5}, "notSchemaPlaceholder"}}},
	}
	def := object(object(schema["$defs"])["positive"])
	def["allOf"].([]any)[2] = map[string]any{"not": map[string]any{"enum": []int{3, 4}}}
	strict, err := StrictifySchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	if strict["additionalProperties"] != false || encodeJSON(strict["required"]) != `["choice","a","z"]` {
		t.Fatalf("%+v", strict)
	}
	properties := object(strict["properties"])
	if object(properties["z"])["default"] != nil || object(properties["choice"])["oneOf"] != nil ||
		object(object(schema["properties"])["z"])["default"] != "x" {
		t.Fatal("strictification mutated the source or retained unsupported keywords")
	}
	for _, test := range []struct {
		raw  string
		pass bool
	}{
		{`{"a":["first",2],"choice":2,"z":"keep"}`, true},
		{`{"a":["first",2],"choice":"other","z":"keep"}`, true},
		{`{"a":["first",2],"choice":3,"z":"keep"}`, false},
		{`{"a":["first",2],"choice":5,"z":"keep"}`, false},
		{`{"a":["first",2],"choice":2,"z":"keep","extra":0}`, false},
		{`{"a":["first",2.5],"choice":2,"z":"keep"}`, false},
		{`{"a":["first",2],"choice":2}`, false},
	} {
		out, err := ValidateOutput(test.raw, schema, strict, 0)
		if (err == nil) != test.pass || err == nil && out != test.raw {
			t.Errorf("%s: %q %v", test.raw, out, err)
		}
	}
	for _, bad := range []map[string]any{
		{"$ref": "https://example.test/a"}, {"oneOf": []any{map[string]any{"const": "x"}}},
		{"type": "object", "additionalProperties": true}, {"type": "invalid"},
	} {
		if _, err := StrictifySchema(bad); err == nil {
			t.Errorf("accepted invalid schema: %v", bad)
		}
	}
	if err := ValidateJSON(map[string]any{"type": "string", "minLength": 2, "maxLength": 2, "pattern": "^é"}, "éx"); err != nil {
		t.Fatal(err)
	}
}

func TestRequestSamplingAndText(t *testing.T) {
	raw := `{"model":"m","messages":[{"role":"system","content":"system"},{"role":"developer","content":"dev"},{"role":"user","content":[{"type":"text","text":"content"}]},{"role":"assistant","content":"reply"}]`
	for _, suffix := range []string{`}`, `,"temperature":0,"top_p":0,"max_output_tokens":0}`} {
		req, err := ParseChatRequest([]byte(raw+suffix), "m", 0)
		if err != nil {
			t.Fatal(err)
		}
		if req.System != "system\n\ndev" || req.Prompt != `[{"role":"user","content":"content"},{"role":"assistant","content":"reply"}]` {
			t.Fatalf("%+v", req)
		}
		sample := req.Sampling
		if suffix == "}" {
			if sample.Temperature != nil || sample.TopP != nil || sample.MaxOutputTokens != nil {
				t.Fatal("sampling supplied by default")
			}
		} else {
			if sample.Temperature == nil || sample.TopP == nil || sample.MaxOutputTokens == nil || *sample.Temperature != 0 || *sample.TopP != 0 || *sample.MaxOutputTokens != 0 {
				t.Fatalf("explicit zero omitted: %+v", sample)
			}
			_, err := Execute(context.Background(), ExecOptions{BridgeKind: "codex"}, req)
			requireBridgeError(t, err, "bridge_sampling_unsupported")
		}
		out, err := ValidateOutput(`{"text":"hi"}`, req.OriginalSchema, req.TransportSchema, 0)
		if err != nil || out != "hi" {
			t.Fatalf("%q %v", out, err)
		}
	}
	for _, suffix := range []string{`,"stream":true}`, `,"tools":[{}]}`, `,"functions":[{}]}`, `,"max_tokens":3,"max_output_tokens":0}`} {
		if _, err := ParseChatRequest([]byte(raw+suffix), "m", 0); err == nil {
			t.Errorf("accepted %s", suffix)
		}
	}
	var exact any
	if err := decode([]byte("9007199254740993"), &exact); err != nil {
		t.Fatal(err)
	}
	if ValidateJSON(map[string]any{"enum": []any{json.Number("9007199254740992")}}, exact) == nil {
		t.Fatal("lost numeric precision")
	}
}

func TestActivityAcrossStdoutAndStderr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clock := NewActivityClock(300 * time.Millisecond)
	cmd := exec.Command("/bin/sh", "-c", `printf 'part'; for n in 1 2 3 4 5 6; do
   /bin/sleep 0.08
   if [ $((n % 2)) = 0 ]; then printf x; else printf activity >&2; fi
 done
 printf '\n'
 /bin/sleep 3`)
	start := time.Now()
	process, err := StartIsolatedProcess(ctx, cmd, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := process.Reap(ctx, time.Second, time.Second); err != nil {
			t.Error(err)
		}
	}()
	line, err := ReadLineWithIdle(ctx, process.Stdout, clock, 64)
	if err != nil || string(line) != "partxxx\n" || time.Since(start) < 300*time.Millisecond {
		t.Fatalf("line: %q %v", line, err)
	}
	if _, err = ReadLineWithIdle(ctx, process.Stdout, clock, 64); !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("silence returned %v", err)
	}
	if !clock.Expired() {
		t.Fatal("deadline not expired")
	}
	if scrubbed := ScrubSecrets(`Authorization: Bearer hidden, "api_key": "sk-mytoken", plain Bearer pretend`); strings.Contains(scrubbed, "hidden") || strings.Contains(scrubbed, "mytoken") || strings.Contains(scrubbed, "pretend") {
		t.Fatal(scrubbed)
	}
}
