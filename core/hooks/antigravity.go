package hooks

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const maxTranscriptTailBytes = 1 << 20

type antigravityTranscriptRecord struct {
	StepIndex       *int     `json:"step_index"`
	Source          string   `json:"source"`
	Type            string   `json:"type"`
	Status          string   `json:"status"`
	CreatedAt       string   `json:"created_at"`
	Content         string   `json:"content"`
	TruncatedFields []string `json:"truncated_fields"`
}

func antigravitySessionID(in object) string {
	id := stringField(in, "conversationId", "session_id")
	if id == "" {
		return ""
	}
	lower := strings.ToLower(id)
	if strings.HasPrefix(lower, "antigravity-") || strings.HasPrefix(lower, "antigravity_") ||
		strings.HasPrefix(lower, "agy-") || strings.HasPrefix(lower, "agy_") {
		return id
	}
	return "antigravity-" + id
}

func firstStringElem(raw any) string {
	switch list := raw.(type) {
	case []any:
		if len(list) > 0 {
			if val, ok := list[0].(string); ok {
				return strings.TrimSpace(val)
			}
		}
	case []string:
		if len(list) > 0 {
			return strings.TrimSpace(list[0])
		}
	}
	return ""
}

func intField(in object, keys ...string) (int, bool) {
	for _, key := range keys {
		raw, ok := in[key]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case float64:
			if math.Trunc(v) == v {
				return int(v), true
			}
		case int:
			return v, true
		case int64:
			return int(v), true
		case json.Number:
			if n, err := v.Int64(); err == nil {
				return int(n), true
			}
		}
	}
	return 0, false
}

func readAntigravityTranscriptTail(path string) ([]antigravityTranscriptRecord, error) {
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) {
		return nil, ErrInput
	}
	initial, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !initial.Mode().IsRegular() {
		return nil, ErrInput
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrInput
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(initial, info) {
		return nil, ErrInput
	}
	size := info.Size()
	if size == 0 {
		return nil, nil
	}
	start := max(int64(0), size-maxTranscriptTailBytes)
	prevNewline := true
	if start > 0 {
		if _, err := f.Seek(start-1, io.SeekStart); err != nil {
			return nil, ErrInput
		}
		var prev [1]byte
		if _, err := io.ReadFull(f, prev[:]); err != nil {
			return nil, ErrInput
		}
		prevNewline = prev[0] == '\n'
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxTranscriptTailBytes))
	if err != nil {
		return nil, ErrInput
	}
	if start > 0 && !prevNewline {
		idx := bytes.IndexByte(raw, '\n')
		if idx < 0 {
			return nil, nil
		}
		raw = raw[idx+1:]
	}
	var records []antigravityTranscriptRecord
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rec antigravityTranscriptRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			// Skip incomplete or malformed JSONL lines in the tail.
			continue
		}
		records = append(records, rec)
	}
	return records, nil
}

func isAntigravityUserRecord(rec antigravityTranscriptRecord) bool {
	return rec.Source == "USER_EXPLICIT" && rec.Type == "USER_INPUT" && rec.Status == "DONE"
}

func isAntigravityAssistantRecord(rec antigravityTranscriptRecord) bool {
	return rec.Source == "MODEL" && rec.Type == "PLANNER_RESPONSE" && rec.Status == "DONE" && strings.TrimSpace(rec.Content) != ""
}

func latestAntigravityUser(records []antigravityTranscriptRecord) (int, antigravityTranscriptRecord, bool) {
	for i := len(records) - 1; i >= 0; i-- {
		if isAntigravityUserRecord(records[i]) {
			return i, records[i], true
		}
	}
	return -1, antigravityTranscriptRecord{}, false
}

func antigravityUserMatchesQuery(user antigravityTranscriptRecord, query string) bool {
	content := strings.TrimSpace(user.Content)
	if content == query {
		return true
	}
	if len(user.TruncatedFields) > 0 && len(content) >= 64 && strings.HasPrefix(query, content[:64]) {
		return true
	}
	return false
}

func shouldSkipAntigravityRecall(in object, query string) (bool, error) {
	if path := stringField(in, "transcriptPath", "transcript_path"); path != "" {
		records, err := readAntigravityTranscriptTail(path)
		if err != nil {
			return false, err
		}
		if idx, user, ok := latestAntigravityUser(records); ok && antigravityUserMatchesQuery(user, query) {
			for _, rec := range records[idx+1:] {
				if rec.Source == "MODEL" {
					return true, nil
				}
			}
			return false, nil
		}
	}
	if inv, ok := intField(in, "invocationNum"); ok && inv > 0 {
		return true, nil
	}
	return false, nil
}

func normalizeAntigravityCapture(in object, event string) (object, bool, error) {
	if event == PostToolUse || event == PostToolUseFailure {
		return normalizeAntigravityToolCapture(in), true, nil
	}
	if event == Stop || event == SessionEnd {
		return normalizeAntigravityStopCapture(in)
	}
	return in, true, nil
}

func normalizeAntigravityToolCapture(in object) object {
	out := maps.Clone(in)
	if call, ok := in["toolCall"].(object); ok {
		if name := stringField(call, "name"); name != "" && stringField(out, "tool_name", "origin_function") == "" {
			out["tool_name"] = name
		}
		if callID := stringField(call, "id"); callID != "" && stringField(out, "tool_use_id", "call_id") == "" {
			out["tool_use_id"] = callID
		}
		if args, exists := call["args"]; exists {
			if _, hasInput := out["tool_input"]; !hasInput {
				if _, hasParams := out["method_params"]; !hasParams {
					out["tool_input"] = args
				}
			}
		}
	}
	if stringField(out, "tool_use_id", "call_id") == "" {
		if stepIdx, ok := intField(in, "stepIdx"); ok {
			out["tool_use_id"] = "step:" + strconv.Itoa(stepIdx)
		}
	}
	if _, hasResp := out["tool_response"]; !hasResp {
		if _, hasRet := out["method_return_value"]; !hasRet {
			if res, exists := in["result"]; exists {
				out["tool_response"] = res
			}
		}
	}
	return out
}

func normalizeAntigravityStopCapture(in object) (object, bool, error) {
	if stringField(in, "terminationReason") == "USER_CANCELED" {
		return nil, false, nil
	}
	if stringField(in, "answer", "last_assistant_message") != "" {
		out := maps.Clone(in)
		if stringField(out, "question", "prompt") == "" {
			if q := stringField(in, "lastUserInput"); q != "" {
				out["question"] = q
			}
		}
		if stringField(out, "turn_id", "event_id", "message_id") == "" {
			convID := stringField(in, "conversationId", "session_id")
			out["message_id"] = antigravityTurnMessageID(convID, nil, nil, stringField(out, "question", "prompt"), stringField(out, "answer", "last_assistant_message"))
		}
		return out, true, nil
	}
	path := stringField(in, "transcriptPath", "transcript_path")
	if path == "" {
		return nil, false, nil
	}
	records, err := readAntigravityTranscriptTail(path)
	if err != nil {
		return nil, false, err
	}
	userIdx, user, ok := latestAntigravityUser(records)
	if !ok {
		return nil, false, nil
	}
	var assistant antigravityTranscriptRecord
	foundAssistant := false
	for i := len(records) - 1; i > userIdx; i-- {
		if isAntigravityAssistantRecord(records[i]) {
			assistant = records[i]
			foundAssistant = true
			break
		}
	}
	if !foundAssistant {
		return nil, false, nil
	}
	question := strings.TrimSpace(user.Content)
	if lastInput := stringField(in, "lastUserInput", "question", "prompt"); lastInput != "" && (question == "" || len(user.TruncatedFields) > 0) {
		question = lastInput
	}
	answer := strings.TrimSpace(assistant.Content)
	if question == "" || answer == "" {
		return nil, false, nil
	}
	out := maps.Clone(in)
	out["question"] = question
	out["answer"] = answer
	convID := stringField(in, "conversationId", "session_id")
	out["message_id"] = antigravityTurnMessageID(convID, user.StepIndex, assistant.StepIndex, question, answer)
	if validRFC3339(user.CreatedAt) {
		out["prompt_time"] = user.CreatedAt
	}
	if validRFC3339(assistant.CreatedAt) {
		out["time"] = assistant.CreatedAt
	} else if validRFC3339(user.CreatedAt) {
		out["time"] = user.CreatedAt
	}
	return out, true, nil
}

func validRFC3339(value string) bool {
	if value == "" {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

func antigravityTurnMessageID(convID string, userStep, assistantStep *int, question, answer string) string {
	if userStep != nil && assistantStep != nil {
		if convID != "" {
			return fmt.Sprintf("turn:%s:%d:%d", convID, *userStep, *assistantStep)
		}
		return fmt.Sprintf("turn:%d:%d", *userStep, *assistantStep)
	}
	raw, err := json.Marshal([]string{convID, question, answer})
	if err != nil {
		raw = []byte(convID + "\x00" + question + "\x00" + answer)
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("turn:%x", sum[:12])
}

func formatAntigravityRecall(contextText string) ([]byte, error) {
	if contextText == "" {
		return []byte(neutral), nil
	}
	return json.Marshal(object{
		"injectSteps": []any{
			object{
				"systemMessage": object{
					"systemMessage": contextText,
				},
			},
		},
	})
}
