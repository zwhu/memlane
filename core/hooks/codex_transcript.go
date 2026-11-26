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
	"strings"
	"syscall"
	"time"
)

type transcriptQA struct {
	Question    string
	Answer      string
	PromptTime  string
	AnswerTime  string
	TurnID      string
	MessageID   string
	Interrupted bool
}

const interruptedMarker = "[Request interrupted by user"

var nonRequestPrefixes = []string{
	"<command-",
	"<local-command",
	"<task-notification",
	"<system-reminder",
	"Caveat:",
	interruptedMarker,
}

var codexInjectedPrefixes = []string{
	"<environment_context",
	"<permissions",
	"<INSTRUCTIONS>",
	"<user_instructions",
	"<turn_aborted",
	"<collaboration_mode",
	"<apps_instructions",
	"<skills_instructions",
	"# AGENTS.md instructions",
}

func readTranscriptTailBytes(path string) ([]byte, error) {
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
	return raw, nil
}

func isNonRequestText(text string) bool {
	for _, prefix := range nonRequestPrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func isCodexInjectedPrompt(text string) bool {
	for _, prefix := range codexInjectedPrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func extractBlockText(content any, assistantOnlyText bool) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, block := range v {
			switch b := block.(type) {
			case string:
				if b != "" {
					parts = append(parts, b)
				}
			case object:
				bType := stringField(b, "type")
				if assistantOnlyText {
					if bType == "text" || bType == "output_text" || bType == "Text" {
						if txt, ok := b["text"].(string); ok && txt != "" {
							parts = append(parts, txt)
						}
					}
				} else if bType == "text" || bType == "input_text" || bType == "output_text" || bType == "Text" {
					if txt, ok := b["text"].(string); ok && txt != "" {
						parts = append(parts, txt)
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func hasToolResultBlock(content any) bool {
	blocks, ok := content.([]any)
	if !ok {
		return false
	}
	for _, block := range blocks {
		if b, ok := block.(object); ok && stringField(b, "type") == "tool_result" {
			return true
		}
	}
	return false
}

func boolField(in object, key string) bool {
	v, ok := in[key].(bool)
	return ok && v
}

func parseISO8601Time(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts, true
	}
	return time.Time{}, false
}

func codexRecordTime(rec, payload object) string {
	if ms, ok := payload["completed_at_ms"].(float64); ok && !math.IsNaN(ms) && !math.IsInf(ms, 0) && ms > 0 {
		return time.UnixMilli(int64(ms)).UTC().Format(time.RFC3339Nano)
	}
	return stringField(rec, "timestamp")
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func appendPromptUnit(units *[]transcriptQA, unit transcriptQA) {
	if n := len(*units); n > 0 {
		last := &(*units)[n-1]
		if !last.Interrupted && last.Answer == "" && normalizeWhitespace(last.Question) == normalizeWhitespace(unit.Question) {
			if last.PromptTime == "" {
				last.PromptTime = unit.PromptTime
			}
			if last.TurnID == "" {
				last.TurnID = unit.TurnID
			}
			if last.MessageID == "" {
				last.MessageID = unit.MessageID
			}
			return
		}
	}
	*units = append(*units, unit)
}

func setUnitAnswer(units []transcriptQA, answer, stamp, turnID, msgID string) {
	if len(units) == 0 {
		return
	}
	last := &units[len(units)-1]
	if last.Interrupted {
		return
	}
	last.Answer = answer
	if stamp != "" {
		last.AnswerTime = stamp
	}
	if last.TurnID == "" && turnID != "" {
		last.TurnID = turnID
	}
	if last.MessageID == "" && msgID != "" {
		last.MessageID = msgID
	}
}

func markUnitInterrupted(units []transcriptQA) {
	if len(units) == 0 {
		return
	}
	units[len(units)-1].Interrupted = true
}

func parseTranscriptUnits(raw []byte) []transcriptQA {
	var (
		codexEventUnits    []transcriptQA
		codexResponseUnits []transcriptQA
		claudeUnits        []transcriptQA
		forkPoint          time.Time
		hasForkPoint       bool
	)
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rec object
		if err := json.Unmarshal(line, &rec); err != nil || rec == nil {
			// Skip incomplete or malformed JSONL lines in the tail.
			continue
		}
		recType := stringField(rec, "type")
		switch recType {
		case "session_meta":
			if payload, ok := rec["payload"].(object); ok && !hasForkPoint {
				if stringField(payload, "forked_from_id") != "" && !strings.EqualFold(stringField(payload, "history_mode"), "paginated") {
					ts := stringField(payload, "timestamp")
					if ts == "" {
						ts = stringField(rec, "timestamp")
					}
					if parsed, ok := parseISO8601Time(ts); ok {
						forkPoint = parsed
						hasForkPoint = true
					}
				}
			}
		case "event_msg":
			payload, _ := rec["payload"].(object)
			if payload == nil {
				continue
			}
			stamp := codexRecordTime(rec, payload)
			if hasForkPoint {
				if at, ok := parseISO8601Time(stamp); ok && at.Before(forkPoint) {
					continue
				}
			}
			turnID := stringField(payload, "turn_id")
			switch stringField(payload, "type") {
			case "task_complete":
				if ans := strings.TrimSpace(stringField(payload, "last_agent_message")); ans != "" {
					ts := stringField(rec, "timestamp")
					if ts == "" {
						ts = stamp
					}
					setUnitAnswer(codexEventUnits, ans, ts, turnID, "")
					setUnitAnswer(codexResponseUnits, ans, ts, turnID, "")
				}
			case "turn_aborted":
				markUnitInterrupted(codexEventUnits)
				markUnitInterrupted(codexResponseUnits)
			case "user_message":
				text := stringField(payload, "message", "text")
				if text == "" {
					text = strings.TrimSpace(extractBlockText(payload["content"], false))
				}
				if text != "" && !isNonRequestText(text) {
					appendPromptUnit(&codexEventUnits, transcriptQA{Question: text, PromptTime: stamp, TurnID: turnID})
				}
			case "agent_message":
				text := stringField(payload, "message", "text")
				if text == "" {
					text = strings.TrimSpace(extractBlockText(payload["content"], true))
				}
				if text != "" {
					setUnitAnswer(codexEventUnits, text, stamp, turnID, "")
				}
			case "item_completed":
				item, _ := payload["item"].(object)
				if item == nil {
					continue
				}
				switch stringField(item, "type") {
				case "UserMessage":
					text := strings.TrimSpace(extractBlockText(item["content"], false))
					if text != "" && !isNonRequestText(text) {
						appendPromptUnit(&codexEventUnits, transcriptQA{
							Question:   text,
							PromptTime: stamp,
							TurnID:     turnID,
							MessageID:  stringField(item, "id"),
						})
					}
				case "AgentMessage":
					text := strings.TrimSpace(extractBlockText(item["content"], true))
					if text != "" {
						setUnitAnswer(codexEventUnits, text, stamp, turnID, "")
					}
				}
			}
		case "response_item":
			payload, _ := rec["payload"].(object)
			if payload == nil {
				continue
			}
			stamp := stringField(rec, "timestamp")
			if hasForkPoint {
				if at, ok := parseISO8601Time(stamp); ok && at.Before(forkPoint) {
					continue
				}
			}
			itemType := stringField(payload, "type")
			if itemType != "" && itemType != "message" {
				continue
			}
			switch stringField(payload, "role") {
			case "user":
				text := strings.TrimSpace(extractBlockText(payload["content"], false))
				if text != "" && !isNonRequestText(text) && !isCodexInjectedPrompt(text) {
					appendPromptUnit(&codexResponseUnits, transcriptQA{Question: text, PromptTime: stamp})
				}
			case "assistant":
				text := strings.TrimSpace(extractBlockText(payload["content"], true))
				if text != "" {
					setUnitAnswer(codexResponseUnits, text, stamp, "", "")
				}
			}
		case "user", "assistant":
			if rec["forkedFrom"] != nil || boolField(rec, "isSidechain") {
				continue
			}
			stamp := stringField(rec, "timestamp")
			msg, _ := rec["message"].(object)
			var content any
			if msg != nil {
				content = msg["content"]
			} else {
				content = rec["content"]
			}
			if recType == "assistant" {
				text := strings.TrimSpace(extractBlockText(content, true))
				if text != "" {
					setUnitAnswer(claudeUnits, text, stamp, "", "")
				}
				continue
			}
			rawText := strings.TrimSpace(extractBlockText(content, false))
			if strings.HasPrefix(rawText, interruptedMarker) {
				markUnitInterrupted(claudeUnits)
			}
			if hasToolResultBlock(content) || boolField(rec, "isMeta") || boolField(rec, "isCompactSummary") {
				continue
			}
			if rawText != "" && !isNonRequestText(rawText) {
				appendPromptUnit(&claudeUnits, transcriptQA{
					Question:   rawText,
					PromptTime: stamp,
					MessageID:  stringField(rec, "uuid"),
				})
			}
		}
	}
	if len(codexEventUnits) > 0 {
		if len(codexResponseUnits) > 0 {
			lastEvent := &codexEventUnits[len(codexEventUnits)-1]
			lastResp := codexResponseUnits[len(codexResponseUnits)-1]
			if !lastEvent.Interrupted && lastEvent.Answer == "" && lastResp.Answer != "" &&
				normalizeWhitespace(lastEvent.Question) == normalizeWhitespace(lastResp.Question) {
				lastEvent.Answer = lastResp.Answer
				if lastEvent.AnswerTime == "" {
					lastEvent.AnswerTime = lastResp.AnswerTime
				}
			}
		}
		return codexEventUnits
	}
	if len(codexResponseUnits) > 0 {
		return codexResponseUnits
	}
	return claudeUnits
}

type transcriptSelectStatus int

const (
	transcriptFallback transcriptSelectStatus = iota
	transcriptMatched
	transcriptSkip
)

func selectTranscriptQA(units []transcriptQA, hintQuestion, stopAnswer, hintTurnID string) (transcriptQA, transcriptSelectStatus) {
	if len(units) == 0 {
		return transcriptQA{}, transcriptFallback
	}
	if hintTurnID != "" {
		for i := len(units) - 1; i >= 0; i-- {
			u := units[i]
			if u.TurnID != hintTurnID && u.MessageID != hintTurnID {
				continue
			}
			if u.Interrupted || u.Question == "" {
				return transcriptQA{}, transcriptSkip
			}
			if stopAnswer != "" {
				if u.Answer == "" {
					u.AnswerTime = ""
				}
				u.Answer = stopAnswer
				return u, transcriptMatched
			}
			if u.Answer != "" {
				if hintQuestion != "" {
					u.Question = hintQuestion
				}
				return u, transcriptMatched
			}
			return transcriptQA{}, transcriptSkip
		}
	}
	if stopAnswer != "" {
		wantAns := normalizeWhitespace(stopAnswer)
		for i := len(units) - 1; i >= 0; i-- {
			u := units[i]
			if hintTurnID != "" && u.TurnID != "" && u.TurnID != hintTurnID {
				continue
			}
			if !u.Interrupted && u.Question != "" && u.Answer != "" && normalizeWhitespace(u.Answer) == wantAns {
				u.Answer = stopAnswer
				return u, transcriptMatched
			}
		}
		last := units[len(units)-1]
		if hintTurnID != "" && last.TurnID != "" && last.TurnID != hintTurnID {
			return transcriptQA{}, transcriptFallback
		}
		if last.Interrupted || last.Question == "" {
			return transcriptQA{}, transcriptSkip
		}
		if last.Answer == "" {
			last.AnswerTime = ""
		}
		last.Answer = stopAnswer
		return last, transcriptMatched
	}
	last := units[len(units)-1]
	if hintTurnID != "" && last.TurnID != "" && last.TurnID != hintTurnID {
		return transcriptQA{}, transcriptSkip
	}
	if !last.Interrupted && last.Question != "" && last.Answer != "" {
		if hintQuestion != "" {
			last.Question = hintQuestion
		}
		return last, transcriptMatched
	}
	return transcriptQA{}, transcriptSkip
}

func codexTurnMessageID(sessionID, promptTime, question, answer string) string {
	raw, err := json.Marshal([]string{sessionID, promptTime, question, answer})
	if err != nil {
		raw = []byte(sessionID + "\x00" + promptTime + "\x00" + question + "\x00" + answer)
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("turn:%x", sum[:12])
}

func normalizeStopCapture(in object, sessionID string) (object, bool, error) {
	fields := in
	if entry, ok := in["entry"].(object); ok {
		fields = maps.Clone(in)
		maps.Copy(fields, entry)
	}
	question := stringField(fields, "question", "prompt")
	answer := stringField(fields, "answer", "last_assistant_message")
	if question != "" && answer != "" {
		return in, true, nil
	}
	path := stringField(fields, "transcript_path", "transcriptPath")
	if path == "" {
		return in, true, nil
	}
	raw, err := readTranscriptTailBytes(path)
	if err != nil {
		return nil, false, err
	}
	if len(raw) == 0 {
		return in, true, nil
	}
	hintTurnID := stringField(fields, "turn_id", "turnId", "event_id", "message_id")
	qa, status := selectTranscriptQA(parseTranscriptUnits(raw), question, answer, hintTurnID)
	switch status {
	case transcriptSkip:
		return nil, false, nil
	case transcriptFallback:
		return in, true, nil
	}
	out := maps.Clone(in)
	if question == "" && qa.Question != "" {
		out["question"] = qa.Question
	}
	if answer == "" && qa.Answer != "" {
		out["answer"] = qa.Answer
	}
	if entry, ok := out["entry"].(object); ok {
		entryClone := maps.Clone(entry)
		if question == "" && qa.Question != "" {
			entryClone["question"] = qa.Question
		}
		if answer == "" && qa.Answer != "" {
			entryClone["answer"] = qa.Answer
		}
		out["entry"] = entryClone
	}
	if stringField(out, "prompt_time") == "" && validRFC3339(qa.PromptTime) {
		out["prompt_time"] = qa.PromptTime
	}
	if stringField(out, "time") == "" && validRFC3339(qa.AnswerTime) {
		out["time"] = qa.AnswerTime
	}
	if stringField(out, "turn_id", "turnId") == "" && qa.TurnID != "" {
		out["turn_id"] = qa.TurnID
	}
	if stringField(out, "turn_id", "turnId", "event_id", "message_id") == "" {
		if qa.MessageID != "" {
			out["message_id"] = "turn:" + qa.MessageID
		} else {
			out["message_id"] = codexTurnMessageID(
				sessionID,
				qa.PromptTime,
				stringField(out, "question", "prompt"),
				stringField(out, "answer", "last_assistant_message"),
			)
		}
	}
	return out, true, nil
}
