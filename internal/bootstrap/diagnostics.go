package bootstrap

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
)

var diagnosticSecrets = regexp.MustCompile(`(?im)(authorization\s*[:=]\s*|(?:api[_-]?key|token|password|passwd|secret|credential|cookie)\s*["']?\s*[:=]\s*["']?)([^\r\n,;}]+)`)
var diagnosticBearer = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[^\s"'<>]+`)
var diagnosticTokens = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{8,}|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)\b`)
var diagnosticURLs = regexp.MustCompile(`https?://[^\s"'<>]+`)
var diagnosticHomes = regexp.MustCompile(`/(?:Users|home)/[^/\s"'<>]+`)

// RedactInstallerText accepts installer output only. It is deliberately not a
// collector: callers must never pass chat, memory, login files or environment
// dumps. URL query/fragment/userinfo are removed even with unfamiliar key names.
func RedactInstallerText(s string) string {
	s = diagnosticURLs.ReplaceAllStringFunc(s, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[URL已脱敏]"
		}
		u.User, u.RawQuery, u.Fragment = nil, "", ""
		return u.String()
	})
	s = diagnosticSecrets.ReplaceAllString(s, "${1}[已脱敏]")
	s = diagnosticBearer.ReplaceAllString(s, "${1} [已脱敏]")
	s = diagnosticTokens.ReplaceAllString(s, "[已脱敏]")
	return diagnosticHomes.ReplaceAllString(s, "[用户目录]")
}

type DiagnosticInput struct {
	Operation    string   `json:"operation"`
	Issue        string   `json:"issue"`
	Stage        string   `json:"stage"`
	Platform     string   `json:"platform"`
	ExitCode     *int     `json:"exit_code"`
	Error        string   `json:"error"`
	InstallerLog string   `json:"installer_log"`
	Actions      []string `json:"attempted_actions"`
	Journal      *Journal `json:"installation,omitempty"`
	Plan         *Plan    `json:"detection,omitempty"`
}

// DiagnosticArchive is an offline, self-contained artifact. It reads no files
// and remains available before Python, Cognee or an installation journal exists.
func DiagnosticArchive(input DiagnosticInput) ([]byte, error) {
	report, prompt, err := Diagnostics(input)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	bundle := zip.NewWriter(&out)
	for _, file := range []struct{ Name, Body string }{{"diagnostic.json", report}, {"agent-prompt.md", prompt}, {"README.txt", "仅包含脱敏安装上下文。缺失字段表示尚未获得，不代表成功。修复后请在Memlane重新检测；Agent文字回复不能改变安装状态。\n"}} {
		header := &zip.FileHeader{Name: file.Name, Method: zip.Deflate}
		header.SetMode(0600)
		stream, err := bundle.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := stream.Write([]byte(file.Body)); err != nil {
			return nil, err
		}
	}
	if err := bundle.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func ExportDiagnostic(path string, input DiagnosticInput) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("absolute diagnostic export path required")
	}
	b, err := DiagnosticArchive(input)
	if err != nil {
		return err
	}
	return atomicPrivateFile(path, b)
}

// Diagnostics works with only the in-memory failure context, before even a
// state directory or Python exists. No key, config, memory or log discovery.
func Diagnostics(input DiagnosticInput) (report, prompt string, err error) {
	// Bound output size but retain the last failure lines. Caller may explicitly
	// select more installer output in a future details view.
	const limit = 32 * 1024
	if len(input.InstallerLog) > limit {
		input.InstallerLog = "[仅末尾32KiB]\n" + input.InstallerLog[len(input.InstallerLog)-limit:]
	}
	b, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return "", "", err
	}
	// Redact strings individually so replacing sensitive text cannot break JSON.
	var tree any
	if err := json.Unmarshal(b, &tree); err != nil {
		return "", "", err
	}
	var scrub func(any) any
	scrub = func(v any) any {
		switch v := v.(type) {
		case string:
			return RedactInstallerText(v)
		case []any:
			for n := range v {
				v[n] = scrub(v[n])
			}
			return v
		case map[string]any:
			for k := range v {
				v[k] = scrub(v[k])
			}
			return v
		default:
			return v
		}
	}
	b, err = json.MarshalIndent(scrub(tree), "", "  ")
	if err != nil {
		return "", "", err
	}
	report = string(b)
	prompt = fmt.Sprintf(`请帮助排查 Memlane 首次安装失败。以下是安装器生成的脱敏诊断；缺失字段表示尚未获得，不能推断成功。

先只读核对失败步骤、依赖兼容性、来源与路径，列出已证事实、假设和未知。优先复用已有兼容依赖，不擅自升级、替换或新装。涉及新增下载、安装、升级、配置或进程变更时，先说明用途、版本、来源、大小、位置及影响，并取得用户确认。
不要读取或复制凭证、登录文件、聊天记录、真实记忆内容，不创建替代日常库。保留已完成进度、原操作ID和同一问题累计失败次数；累计第4次验收失败就停止并汇报。
仅使用原安装器的重试／重新检测入口确认结果。你的文字回复不能将状态标为成功。给出可复核原因、建议动作、影响及待安装器核验项。

诊断材料：
%s`, report)
	return report, prompt, nil
}
