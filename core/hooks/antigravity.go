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
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	maxTranscriptTailBytes = 1 << 20
	maxTranscriptHeadBytes = 64 << 10
	maxSwarmMembersBytes   = 128 << 10
	maxHeadSystemScan      = 8
)

type antigravityTranscriptRecord struct {
	StepIndex       *int     `json:"step_index"`
	Source          string   `json:"source"`
	Type            string   `json:"type"`
	Status          string   `json:"status"`
	CreatedAt       string   `json:"created_at"`
	Content         string   `json:"content"`
	TruncatedFields []string `json:"truncated_fields"`
}

func normalizeAntigravitySessionID(id string) string {
	id = strings.TrimSpace(id)
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

func antigravitySessionID(in object) string {
	return normalizeAntigravitySessionID(stringField(in, "conversationId", "session_id"))
}

func stripXMLTagBlock(s, tag string) string {
	openTag := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	for {
		start := strings.Index(s, openTag)
		if start < 0 {
			return s
		}
		after := s[start+len(openTag):]
		end := strings.Index(after, closeTag)
		if end < 0 {
			return s[:start]
		}
		s = s[:start] + after[end+len(closeTag):]
	}
}

func cleanAntigravityUserText(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	const openReq = "<USER_REQUEST>"
	const closeReq = "</USER_REQUEST>"
	if start := strings.Index(trimmed, openReq); start >= 0 {
		rest := trimmed[start+len(openReq):]
		if end := strings.LastIndex(rest, closeReq); end >= 0 {
			return strings.TrimSpace(rest[:end])
		}
		rest = stripXMLTagBlock(rest, "ADDITIONAL_METADATA")
		rest = stripXMLTagBlock(rest, "USER_SETTINGS_CHANGE")
		return strings.TrimSpace(rest)
	}
	cleaned := stripXMLTagBlock(trimmed, "ADDITIONAL_METADATA")
	cleaned = stripXMLTagBlock(cleaned, "USER_SETTINGS_CHANGE")
	return strings.TrimSpace(cleaned)
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

func readRegularFileHead(path string, maxBytes int64) ([]byte, error) {
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
	if info.Size() == 0 {
		return nil, nil
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return nil, ErrInput
	}
	return raw, nil
}

func readAntigravityTranscriptHead(path string) ([]antigravityTranscriptRecord, error) {
	raw, err := readRegularFileHead(path, maxTranscriptHeadBytes)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	var records []antigravityTranscriptRecord
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rec antigravityTranscriptRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			// Skip incomplete or malformed JSONL lines in the bounded head.
			continue
		}
		records = append(records, rec)
		if len(records) >= maxHeadSystemScan {
			break
		}
	}
	return records, nil
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

func isValidRoleName(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if i == 0 {
			if !isAlphaNum {
				return false
			}
			continue
		}
		if !isAlphaNum && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func extractAntigravityMessageSender(content string) string {
	header := content
	if idx := strings.Index(header, "content="); idx >= 0 {
		header = header[:idx]
	}
	const prefix = "sender="
	for pos := 0; pos < len(header); {
		idx := strings.Index(header[pos:], prefix)
		if idx < 0 {
			return ""
		}
		abs := pos + idx
		if abs == 0 || header[abs-1] == ' ' || header[abs-1] == '\t' || header[abs-1] == '\n' || header[abs-1] == '\r' {
			rest := header[abs+len(prefix):]
			end := strings.IndexAny(rest, " \t\r\n")
			token := rest
			if end >= 0 {
				token = rest[:end]
			}
			if isUUID(token) {
				return token
			}
		}
		pos = abs + len(prefix)
	}
	return ""
}

func antigravityMessageOpening(content string) string {
	body := content
	if idx := strings.Index(body, "content="); idx >= 0 {
		body = body[idx+len("content="):]
	}
	if sep := strings.Index(body, "\n\n"); sep >= 0 {
		body = body[:sep]
	}
	return body
}

func extractAntigravitySwarmRoleFromContent(content string) string {
	opening := antigravityMessageOpening(content)
	const marker = "for @"
	for pos := 0; pos < len(opening); {
		idx := strings.Index(opening[pos:], marker)
		if idx < 0 {
			return ""
		}
		start := pos + idx + len(marker)
		end := start
		for end < len(opening) {
			c := opening[end]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
				end++
				continue
			}
			break
		}
		name := opening[start:end]
		tail := strings.TrimLeft(opening[end:], " \t")
		if isValidRoleName(name) && (strings.HasPrefix(tail, ":") || strings.HasPrefix(tail, "):")) {
			return name
		}
		pos = start
	}
	return ""
}

func extractAntigravitySubagentRoleLine(content string) string {
	opening := antigravityMessageOpening(content)
	for _, line := range strings.Split(opening, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		for _, prefix := range []string{"subagent_role:", "subagent_role=", "subagent role:"} {
			if strings.HasPrefix(lower, prefix) {
				candidate := strings.TrimSpace(trimmed[len(prefix):])
				if sep := strings.IndexAny(candidate, " \t,;.)"); sep >= 0 {
					candidate = candidate[:sep]
				}
				if isValidRoleName(candidate) {
					return candidate
				}
			}
		}
	}
	return ""
}

func antigravityBrainRootAndConvID(transcriptPath string) (string, string, bool) {
	if !filepath.IsAbs(transcriptPath) {
		return "", "", false
	}
	clean := filepath.Clean(transcriptPath)
	logsDir := filepath.Dir(clean)
	sysDir := filepath.Dir(logsDir)
	convDir := filepath.Dir(sysDir)
	brainRoot := filepath.Dir(convDir)
	if filepath.Base(logsDir) != "logs" || filepath.Base(sysDir) != ".system_generated" {
		return "", "", false
	}
	convID := filepath.Base(convDir)
	if convID == "" || convID == "." || convID == string(filepath.Separator) || brainRoot == convDir {
		return "", "", false
	}
	return brainRoot, convID, true
}

func lookupAntigravitySwarmMemberRole(transcriptPath, parentUUID, currentSessionID string) (string, error) {
	brainRoot, pathConvID, ok := antigravityBrainRootAndConvID(transcriptPath)
	if !ok || !isUUID(parentUUID) {
		return "", nil
	}
	if currentSessionID == "" {
		currentSessionID = normalizeAntigravitySessionID(pathConvID)
	}
	if currentSessionID == "" {
		return "", nil
	}
	membersPath := filepath.Join(brainRoot, parentUUID, ".system_generated", "swarm", "members.json")
	raw, err := readRegularFileHead(membersPath, maxSwarmMembersBytes)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", nil
	}
	var doc struct {
		Members map[string]struct {
			ConversationID string `json:"conversation_id"`
		} `json:"members"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		// Malformed members.json should not block capture; allow role lookup to continue.
		return "", nil
	}
	for _, key := range slices.Sorted(maps.Keys(doc.Members)) {
		member := doc.Members[key]
		if member.ConversationID == "" {
			continue
		}
		if strings.EqualFold(normalizeAntigravitySessionID(member.ConversationID), currentSessionID) {
			return strings.TrimSpace(key), nil
		}
	}
	return "", nil
}

func resolveAntigravityParent(in object) (string, string, error) {
	transcriptPath := stringField(in, "transcriptPath", "transcript_path")
	currentSessionID := antigravitySessionID(in)
	if currentSessionID == "" && transcriptPath != "" {
		if _, pathConvID, ok := antigravityBrainRootAndConvID(transcriptPath); ok {
			currentSessionID = normalizeAntigravitySessionID(pathConvID)
		}
	}
	explicitRole := stringField(in, "subagent_role", "subagentRole")
	if explicitParent := stringField(in, "parentConversationId", "parent_session_id"); explicitParent != "" {
		parentSessionID := normalizeAntigravitySessionID(explicitParent)
		if currentSessionID != "" && strings.EqualFold(parentSessionID, currentSessionID) {
			return "", "", nil
		}
		role := explicitRole
		if role == "" {
			role = "subagent"
		}
		return parentSessionID, role, nil
	}
	if transcriptPath == "" {
		return "", "", nil
	}
	records, err := readAntigravityTranscriptHead(transcriptPath)
	if err != nil {
		return "", "", err
	}
	var parentUUID, matchedContent string
	for _, rec := range records {
		if rec.Source == "USER_EXPLICIT" || rec.Type == "USER_INPUT" || rec.Source == "MODEL" {
			break
		}
		if rec.Type != "SYSTEM_MESSAGE" {
			continue
		}
		sender := extractAntigravityMessageSender(rec.Content)
		if sender == "" {
			continue
		}
		if currentSessionID != "" && strings.EqualFold(normalizeAntigravitySessionID(sender), currentSessionID) {
			continue
		}
		parentUUID = sender
		matchedContent = rec.Content
		break
	}
	if parentUUID == "" {
		return "", "", nil
	}
	parentSessionID := normalizeAntigravitySessionID(parentUUID)
	role := explicitRole
	if role == "" {
		role = extractAntigravitySwarmRoleFromContent(matchedContent)
	}
	if role == "" {
		role, err = lookupAntigravitySwarmMemberRole(transcriptPath, parentUUID, currentSessionID)
		if err != nil {
			return "", "", err
		}
	}
	if role == "" {
		role = extractAntigravitySubagentRoleLine(matchedContent)
	}
	if role == "" {
		role = "subagent"
	}
	return parentSessionID, role, nil
}

func antigravityUserMatchesQuery(user antigravityTranscriptRecord, query string) bool {
	content := cleanAntigravityUserText(user.Content)
	cleanQuery := cleanAntigravityUserText(query)
	if content == "" || cleanQuery == "" {
		return false
	}
	if content == cleanQuery {
		return true
	}
	if len(user.TruncatedFields) > 0 && len(content) >= 64 && strings.HasPrefix(cleanQuery, content[:64]) {
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
		q := cleanAntigravityUserText(stringField(out, "question", "prompt"))
		if q == "" {
			q = cleanAntigravityUserText(stringField(in, "lastUserInput"))
		}
		if q == "" {
			return nil, false, nil
		}
		out["question"] = q
		if stringField(out, "turn_id", "event_id", "message_id") == "" {
			convID := stringField(in, "conversationId", "session_id")
			out["message_id"] = antigravityTurnMessageID(convID, nil, nil, q, stringField(out, "answer", "last_assistant_message"))
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
	question := cleanAntigravityUserText(user.Content)
	if lastInput := cleanAntigravityUserText(stringField(in, "lastUserInput", "question", "prompt")); lastInput != "" && (question == "" || len(user.TruncatedFields) > 0) {
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
