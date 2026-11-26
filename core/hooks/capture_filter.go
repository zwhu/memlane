package hooks

import (
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type CaptureFilterInput struct {
	Host, Kind string
	Content    any
}

type CaptureFilterResult struct {
	Blocked                                                        bool
	Rule, Category, Source, ToolName, HostKind, Reason, NextAction string
}

var (
	privateKeyPattern = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)
	apiToken          = regexp.MustCompile(`(?:sk-[A-Za-z0-9_-]{20,}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16})`)
	bearer            = regexp.MustCompile(`(?i)\bauthorization[ "'\t]*:[ "'\t]*bearer[ \t]+([a-z0-9._~+/=-]+)`)
	toolIdentifier    = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	hunk              = regexp.MustCompile(`^@@ -[0-9]+(?:,([0-9]+))? \+[0-9]+(?:,([0-9]+))? @@`)
	patchHeaders      = []string{"*** Update File: ", "*** Add File: ", "*** Delete File: ", "*** Move to: "}
)

func EvaluateCaptureFilter(in CaptureFilterInput) CaptureFilterResult {
	fields, _ := in.Content.(map[string]any)
	tool, _ := fields["origin_function"].(string)
	alternate, _ := fields["tool_name"].(string)
	if tool == "" {
		tool = alternate
	}
	params := fields
	for _, key := range []string{"method_params", "tool_input", "params", "arguments"} {
		if value, ok := fields[key]; ok && value != nil {
			if structured, ok := value.(map[string]any); ok {
				params = structured
			} else {
				params = map[string]any{"input": value}
			}
			break
		}
	}
	explicitPatch := isPatchTool(tool) || isPatchTool(alternate)
	var patch bool
	var paths []string
	if in.Kind != "capture_qa" {
		patch, paths = patchTargets(params, explicitPatch)
	}
	host, label := filterHost(in.Host)
	result := CaptureFilterResult{HostKind: host}
	switch {
	case patch:
		result.ToolName = "apply_patch"
	case toolIdentifier.MatchString(tool) && secretRule(tool) == "":
		result.ToolName = tool
	}
	if rule := findSecret(in.Content); rule != "" {
		result.Blocked, result.Rule, result.Category, result.Source = true, rule, "secret_value", "content"
		result.Reason = label + " 记录内容命中敏感凭据规则（`" + rule + "`），已拦截未上传，原文保留在本机"
		result.NextAction = "请勿将真实密钥写入记忆库；如需保留普通说明，去除凭据后再记录。"
		return result
	}
	if in.Kind == "capture_qa" || in.Kind == "document" && tool == "" && !patch {
		return result
	}
	target, object := "工具路径", "文件"
	if patch {
		result.Rule, result.Source = sensitiveRule(paths), "apply_patch"
		target, object = "补丁路径", "补丁"
	} else {
		result.Rule, result.Source = toolPathRule(params)
	}
	if result.Rule == "" {
		result.Source = ""
		return result
	}
	result.Blocked, result.Category = true, "sensitive_path"
	result.Reason = label + " " + target + "命中 `" + result.Rule + "` 规则，记录保留在本机（规则命中本身不足以确认" + object + "包含密钥）"
	result.NextAction = "如需确认内容，请在本机工作区检查对应文件；记录未上传，无需手动重试。"
	return result
}

func filterHost(raw string) (string, string) {
	host := CanonicalHost(raw)
	switch host {
	case HostTraeX:
		return host, "TRAE CLI"
	case HostTraeIDE:
		return host, "TRAE CN"
	case HostCodex:
		return host, "Codex"
	case HostAntigravity:
		return host, "Gemini App"
	case HostPi:
		return host, "pi"
	default:
		return "", "Agent"
	}
}

func isPatchTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "apply_patch", "patch", "edit_patch":
		return true
	}
	return false
}

func patchTargets(params any, explicit bool) (bool, []string) {
	found := explicit
	var paths []string
	visitStrings(params, func(value string) {
		lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
		oldLines, newLines := 0, 0
		nativePatch := false
		for i := 0; i < len(lines); i++ {
			line := lines[i]
			if line == "*** Begin Patch" {
				found, nativePatch = true, true
				continue
			}
			for _, prefix := range patchHeaders {
				if target, ok := strings.CutPrefix(line, prefix); ok {
					found, nativePatch = true, true
					paths = append(paths, strings.TrimSpace(target))
					break
				}
			}
			if nativePatch {
				continue
			}
			if oldLines > 0 || newLines > 0 {
				switch {
				case strings.HasPrefix(line, " "):
					oldLines--
					newLines--
					continue
				case strings.HasPrefix(line, "-"):
					oldLines--
					continue
				case strings.HasPrefix(line, "+"):
					newLines--
					continue
				case strings.HasPrefix(line, `\ No newline`):
					continue
				default:
					oldLines, newLines = 0, 0
				}
			}
			if match := hunk.FindStringSubmatch(line); match != nil {
				oldLines, newLines = hunkCount(match[1], len(lines)), hunkCount(match[2], len(lines))
				continue
			}
			if i+1 < len(lines) && strings.HasPrefix(line, "--- ") && strings.HasPrefix(lines[i+1], "+++ ") {
				found = true
				paths = append(paths, diffPath(line[4:], "a/"), diffPath(lines[i+1][4:], "b/"))
				i++
			} else if explicit && strings.HasPrefix(line, "+++ ") {
				paths = append(paths, diffPath(line[4:], "b/"))
			} else if explicit && strings.HasPrefix(line, "--- ") {
				paths = append(paths, diffPath(line[4:], "a/"))
			}
		}
	})
	return found, paths
}

func hunkCount(value string, maxLines int) int {
	if value == "" {
		return 1
	}
	if count, err := strconv.Atoi(value); err == nil {
		return min(count, maxLines)
	}
	// Counts beyond int range still describe at least the rest of this patch.
	return maxLines
}

func diffPath(value, prefix string) string {
	value, _, _ = strings.Cut(value, "\t")
	value = strings.TrimSpace(value)
	if value == "/dev/null" {
		return ""
	}
	if decoded, err := strconv.Unquote(value); err == nil {
		value = decoded
	}
	return strings.TrimPrefix(value, prefix)
}

func visitStrings(value any, visit func(string)) {
	switch v := value.(type) {
	case string:
		visit(v)
	case []string:
		for _, item := range v {
			visit(item)
		}
	case []any:
		for _, item := range v {
			visitStrings(item, visit)
		}
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(v)) {
			visit(key)
			item := v[key]
			if text, ok := item.(string); ok && strings.EqualFold(key, "authorization") {
				visit("Authorization: " + text)
			}
			visitStrings(item, visit)
		}
	}
}

func findSecret(content any) string {
	var values []string
	visitStrings(content, func(value string) {
		if rule := secretRule(value); rule != "" {
			values = append(values, rule)
		}
	})
	for _, rule := range []string{"private_key", "api_token", "bearer_token"} {
		for _, found := range values {
			if rule == found {
				return rule
			}
		}
	}
	return ""
}

func secretRule(value string) string {
	switch {
	case privateKeyPattern.MatchString(value):
		return "private_key"
	case apiToken.MatchString(value):
		return "api_token"
	}
	for _, match := range bearer.FindAllStringSubmatch(value, -1) {
		switch strings.ToLower(match[1]) {
		case "redacted", "token", "placeholder", "your_token", "your-token", "example", "changeme", "none", "null", "undefined", "dummy", "example_token", "your_api_key":
			continue
		}
		return "bearer_token"
	}
	return ""
}

func sensitiveRule(paths []string) string {
	for _, value := range paths {
		name := strings.ToLower(path.Base(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")))
		switch name {
		case ".env", "id_rsa", "id_ed25519", "id_ecdsa", "id_dsa", ".netrc", ".npmrc", ".pypirc", "credentials.json":
			return name
		}
		if strings.HasPrefix(name, ".env.") {
			return ".env.*"
		}
		switch ext := path.Ext(name); ext {
		case ".key", ".pem", ".p12", ".pfx", ".jks":
			return "*" + ext
		}
	}
	return ""
}

func toolPathRule(params map[string]any) (string, string) {
	for _, key := range []string{"path", "file_path", "target_file", "filename", "file", "paths", "old_path", "new_path"} {
		var paths []string
		visitStrings(params[key], func(value string) { paths = append(paths, value) })
		if rule := sensitiveRule(paths); rule != "" {
			return rule, "tool_path"
		}
	}
	for _, key := range []string{"cmd", "command"} {
		if command, ok := params[key].(string); ok {
			if rule := sensitiveRule(shellPaths(command)); rule != "" {
				return rule, "shell_command"
			}
		}
	}
	return "", ""
}
