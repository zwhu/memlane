package official

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

// Cognee 1.6.7 SessionAgentTraceEntry sanitizes strings to 1000 Unicode
// characters and containers to 20 items. Apply those real cache limits before
// creating an operation digest, so receipt recovery compares stored content.
func clipOfficialTraceRunes(value string) (string, bool) {
	if utf8.RuneCountInString(value) <= 1000 {
		return value, false
	}
	return string([]rune(value)[:997]) + "...", true
}

func normalizeOfficialTraceValue(value any, depth int) (any, bool, error) {
	if depth > 32 {
		return nil, false, errors.New("official trace nesting exceeds supported budget")
	}
	switch typed := value.(type) {
	case string:
		text, changed := clipOfficialTraceRunes(typed)
		return text, changed, nil
	case []any:
		count := len(typed)
		changed := count > 20
		if count > 20 {
			count = 20
		}
		out := make([]any, count)
		for i := range out {
			item, truncated, err := normalizeOfficialTraceValue(typed[i], depth+1)
			if err != nil {
				return nil, false, err
			}
			out[i] = item
			changed = changed || truncated
		}
		return out, changed, nil
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		changed := len(keys) > 20
		if len(keys) > 20 {
			keys = keys[:20]
		}
		out := map[string]any{}
		for _, key := range keys {
			// Oversized nested keys would be renamed by the SDK with collision
			// suffixes. Refuse this unsupported shape instead of inventing keys.
			if utf8.RuneCountInString(key) > 1000 {
				return nil, false, errors.New("official trace key exceeds supported budget")
			}
			item, truncated, err := normalizeOfficialTraceValue(typed[key], depth+1)
			if err != nil {
				return nil, false, err
			}
			out[key] = item
			changed = changed || truncated
		}
		return out, changed, nil
	case nil, bool, json.Number:
		return value, false, nil
	default:
		return nil, false, errors.New("unsupported official trace value")
	}
}

func normalizeOfficialTraceFields(fields map[string]json.RawMessage) (map[string]bool, error) {
	flags := map[string]bool{}
	for _, key := range []string{"origin_function", "error_message"} {
		var text string
		if json.Unmarshal(fields[key], &text) != nil {
			return nil, errors.New("typed official trace text required")
		}
		clean := strings.TrimSpace(text)
		clean, truncated := clipOfficialTraceRunes(clean)
		fields[key], _ = json.Marshal(clean)
		if key == "error_message" {
			flags["error_truncated"] = truncated
			flags["error_normalized"] = clean != text
		}
	}
	for _, item := range []struct{ key, flag string }{{"method_params", "params_truncated"}, {"method_return_value", "result_truncated"}} {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(fields[item.key]))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if item.key == "method_params" && value == nil {
			value = map[string]any{}
			flags["params_normalized"] = true
		}
		clean, changed, err := normalizeOfficialTraceValue(value, 0)
		if err != nil {
			return nil, err
		}
		fields[item.key], _ = json.Marshal(clean)
		flags[item.flag] = changed
	}
	return flags, nil
}
