package official

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOfficialCapturePostPolicyBoundsPreserveUTF8AndSourceFlags(t *testing.T) {
	input := captureFixture("qa")
	var fields map[string]any
	json.Unmarshal(input.Entry, &fields)
	fields["question"] = strings.Repeat("蓝", 3000)
	fields["answer"] = strings.Repeat("<", 9000)
	input.Entry, _ = json.Marshal(fields)
	bounded, err := BoundCaptureAfterPolicy(input)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(bounded.Entry, &fields)
	q, a := fields["question"].(string), fields["answer"].(string)
	if len(q) > 4000 || len(a) > 8000 || !utf8.ValidString(q) || !strings.HasSuffix(q, "...") {
		t.Fatal("post-policy UTF-8 boundary invalid")
	}
	var source map[string]any
	json.Unmarshal([]byte(fields["context"].(string)), &source)
	if source["question_truncated"] != true || source["answer_truncated"] != true || source["answer_entry"] != "a1" {
		t.Fatal("source flags or identity lost", source)
	}
	if _, err := CaptureIdentity(bounded); err != nil {
		t.Fatal("bounded HTML-sensitive text rejected", err)
	}
}

func TestOfficialCapturePostPolicyParameterAndResultBounds(t *testing.T) {
	input := captureFixture("trace")
	var fields map[string]any
	json.Unmarshal(input.Entry, &fields)
	params := map[string]any{"long": strings.Repeat("色", 3000), "number": json.Number("9007199254740993")}
	for i := 0; i < 40; i++ {
		params[string(rune('A'+i))] = strings.Repeat("x", 1000)
	}
	fields["method_params"] = params
	fields["method_return_value"] = strings.Repeat("回", 4000)
	fields["error_message"] = strings.Repeat("错", 500)
	input.Entry, _ = json.Marshal(fields)
	bounded, err := BoundCaptureAfterPolicy(input)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	json.Unmarshal(bounded.Entry, &raw)
	if len(raw["method_params"]) > 16000 {
		t.Fatal("parameter object exceeds byte budget")
	}
	var contextText string
	json.Unmarshal(raw["memory_context"], &contextText)
	var mixed map[string]json.RawMessage
	json.Unmarshal([]byte(contextText), &mixed)
	for _, key := range []string{"params_truncated", "result_truncated", "error_truncated"} {
		var flag bool
		json.Unmarshal(mixed[key], &flag)
		if !flag {
			t.Fatal("missing truncation flag", key)
		}
	}
	var result, failure string
	json.Unmarshal(raw["method_return_value"], &result)
	json.Unmarshal(raw["error_message"], &failure)
	if len(result) > 8000 || len(failure) > 500 || !utf8.ValidString(result) || !utf8.ValidString(failure) {
		t.Fatal("tool bounds invalid")
	}
}

func TestOfficialCapturePostPolicyPreservesUntruncatedNumericParameters(t *testing.T) {
	input := captureFixture("trace")
	var fields map[string]json.RawMessage
	json.Unmarshal(input.Entry, &fields)
	fields["method_params"] = json.RawMessage(`{"number":9007199254740993,"flag":true,"nested":{"value":null}}`)
	input.Entry, _ = json.Marshal(fields)
	bounded, err := BoundCaptureAfterPolicy(input)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(bounded.Entry, &fields)
	if !strings.Contains(string(fields["method_params"]), "9007199254740993") || !strings.Contains(string(fields["method_params"]), `"flag":true`) {
		t.Fatal("untruncated parameter semantics changed", string(fields["method_params"]))
	}
}

func TestOfficialCapturePostPolicyMatchesActualTraceCacheLimits(t *testing.T) {
	input := captureFixture("trace")
	var fields map[string]any
	json.Unmarshal(input.Entry, &fields)
	items := make([]string, 25)
	for i := range items {
		items[i] = strings.Repeat("字", 1500)
	}
	fields["method_params"] = map[string]any{"nested": items, "long": strings.Repeat("x", 2000)}
	fields["method_return_value"] = strings.Repeat("回", 1500)
	fields["error_message"] = "  synthetic error  "
	input.Entry, _ = json.Marshal(fields)
	bounded, err := BoundCaptureAfterPolicy(input)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(bounded.Entry, &fields)
	result := fields["method_return_value"].(string)
	if utf8.RuneCountInString(result) != 1000 {
		t.Fatal("did not apply actual SDK character limit", utf8.RuneCountInString(result))
	}
	params := fields["method_params"].(map[string]any)
	if utf8.RuneCountInString(params["long"].(string)) != 1000 {
		t.Fatal("parameter string exceeded actual cache limit")
	}
	// The nested object can be converted into a bounded string by the
	// published per-parameter byte cap; either shape must fit SDK limits.
	switch nested := params["nested"].(type) {
	case string:
		if utf8.RuneCountInString(nested) > 1000 {
			t.Fatal("fallback exceeds SDK limit")
		}
	case []any:
		if len(nested) > 20 {
			t.Fatal("container exceeds SDK limit")
		}
	default:
		t.Fatal("unexpected normalized shape")
	}
	if fields["error_message"] != "synthetic error" {
		t.Fatal("SDK whitespace normalization not applied")
	}
}

func TestOfficialCaptureRejectsUnrecoverablyLongTraceMarker(t *testing.T) {
	input := captureFixture("trace")
	var fields map[string]any
	json.Unmarshal(input.Entry, &fields)
	source, _ := json.Marshal(map[string]any{"origin": "memlane_pi_capture_v1", "extra": strings.Repeat("x", 950)})
	fields["memory_context"] = string(source)
	input.Entry, _ = json.Marshal(fields)
	if _, err := BoundCaptureAfterPolicy(input); err == nil {
		t.Fatal("SDK would clip the operation marker but input was accepted")
	}
}
