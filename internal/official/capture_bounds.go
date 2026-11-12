package official

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"unicode/utf8"
)

func clipCaptureText(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	end := limit - 3
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + "...", true
}

// BoundCaptureAfterPolicy must receive the result of the fixed official
// policy, not raw host data. Keeping this stage separate lets sensitive paths
// and full private keys be inspected/redacted before a truncation can hide them.
// Identity validation is repeated before the prepared input can be dispatched.
func BoundCaptureAfterPolicy(input CaptureEntry) (CaptureEntry, error) {
	empty := CaptureEntry{}
	fail := errors.New("post-policy capture bounds could not be verified")
	if len(input.Entry) > 1024*1024 || !utf8.Valid(input.Entry) {
		return empty, fail
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(input.Entry, &fields) != nil || fields == nil {
		return empty, fail
	}
	var kind string
	if json.Unmarshal(fields["type"], &kind) != nil || (kind != "qa" && kind != "trace") {
		return empty, fail
	}
	contextKey := "context"
	if kind == "trace" {
		contextKey = "memory_context"
	}
	var contextText string
	if json.Unmarshal(fields[contextKey], &contextText) != nil || len(contextText) > 4096 {
		return empty, fail
	}
	var source map[string]json.RawMessage
	if json.Unmarshal([]byte(contextText), &source) != nil || source == nil {
		return empty, fail
	}
	flag := func(key string, truncated bool) {
		var old bool
		json.Unmarshal(source[key], &old)
		source[key], _ = json.Marshal(old || truncated)
	}
	clip := func(key string, limit int, flagKey string) error {
		var text string
		if json.Unmarshal(fields[key], &text) != nil {
			return fail
		}
		bounded, truncated := clipCaptureText(text, limit)
		fields[key], _ = json.Marshal(bounded)
		flag(flagKey, truncated)
		return nil
	}
	if kind == "qa" {
		if clip("question", 4000, "question_truncated") != nil || clip("answer", 8000, "answer_truncated") != nil {
			return empty, fail
		}
	} else {
		normalized, err := normalizeOfficialTraceFields(fields)
		if err != nil {
			return empty, err
		}
		for key, value := range normalized {
			flag(key, value)
		}
		if clip("method_return_value", 8000, "result_truncated") != nil || clip("error_message", 500, "error_truncated") != nil {
			return empty, fail
		}
		var params map[string]json.RawMessage
		if json.Unmarshal(fields["method_params"], &params) != nil {
			return empty, fail
		}
		if params != nil {
			keys := make([]string, 0, len(params))
			for key := range params {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			bounded := map[string]json.RawMessage{}
			truncated := false
			for _, key := range keys {
				if len(bounded) >= 20 || len(key) > 256 {
					truncated = true
					continue
				}
				value := params[key]
				var text string
				if len(bytes.TrimSpace(value)) > 0 && bytes.TrimSpace(value)[0] == '"' {
					if json.Unmarshal(value, &text) != nil {
						return empty, fail
					}
					clipped, changed := clipCaptureText(text, 4000)
					value, _ = json.Marshal(clipped)
					truncated = truncated || changed
				} else {
					var compact bytes.Buffer
					if json.Compact(&compact, value) != nil {
						return empty, fail
					}
					value = compact.Bytes()
					if len(value) > 4000 {
						clipped, _ := clipCaptureText(string(value), 4000)
						clipped, _ = clipOfficialTraceRunes(clipped)
						value, _ = json.Marshal(clipped)
						truncated = true
					}
				}
				bounded[key] = value
				encoded, _ := json.Marshal(bounded)
				if len(encoded) > 16000 {
					delete(bounded, key)
					truncated = true
				}
			}
			fields["method_params"], _ = json.Marshal(bounded)
			flag("params_truncated", truncated)
		}
	}
	sourceJSON, _ := json.Marshal(source)
	fields[contextKey], _ = json.Marshal(string(sourceJSON))
	input.Entry, _ = json.Marshal(fields)
	if _, _, err := prepareCapture(input); err != nil {
		return empty, err
	}
	return input, nil
}
