package aiinstaller

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

type AppBrand struct {
	Name                string
	ExecutablePath      string
	UserAgent           string
	StateDirName        string
	CLIEnvPrefix        string
	DoctorArgs          []string
	PrivacyGuardNote    string
	DiagnosticRetryNote string
}

func (b AppBrand) Normalize() AppBrand {
	if b.Name == "" {
		b.Name = "App"
	}
	if b.ExecutablePath == "" {
		b.ExecutablePath = "app"
	}
	if b.UserAgent == "" {
		b.UserAgent = defaultUserAgent
	}
	if len(b.DoctorArgs) == 0 {
		b.DoctorArgs = []string{"doctor", "--json"}
	}
	if b.PrivacyGuardNote == "" {
		b.PrivacyGuardNote = "不要读取或复制凭证、登录文件或私人用户数据。"
	}
	if b.DiagnosticRetryNote == "" {
		b.DiagnosticRetryNote = "保留已完成进度和原操作ID。"
	}
	return b
}

func (b AppBrand) commandPath() string {
	if strings.ContainsAny(b.ExecutablePath, " \\\t\n'\"$`;&|<>*?()[]{}") {
		return ShellLiteral(b.ExecutablePath)
	}
	return b.ExecutablePath
}

func ShellLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func ManualCLIInstallCommand(brand AppBrand, componentID, downloadSource, lockNote string) string {
	brand = brand.Normalize()
	mode := NormalizeDownloadSource(downloadSource)
	component := componentID
	if strings.ContainsAny(component, " \\\t\n'\"$`;&|<>*?()[]{}") {
		component = ShellLiteral(component)
	}
	target := " --component " + component + " --mirror " + mode + " --json"
	executable := brand.commandPath()
	var lines []string
	if lockNote != "" {
		lines = append(lines, "# "+lockNote)
	}
	return strings.Join(append(lines,
		executable+" install --plan"+target,
		"# 确认安装计划后，将上一步输出中的 artifacts.plan_digest 填入 <PLAN_DIGEST> 执行：",
		executable+" install --apply --plan-digest <PLAN_DIGEST>"+target,
	), "\n")
}

func ManualInstallScript(brand AppBrand, plan Plan, renderItem func(Item) string) string {
	var sections []string
	for _, item := range plan.Items {
		if item.Observation.Status == Available {
			continue
		}
		command := renderItem(item)
		if command != "" {
			sections = append(sections, "# === "+item.Requirement.Name+" ===\n"+command)
		}
	}
	if len(sections) == 0 {
		return "# 全部依赖均已就绪，无需手动安装。"
	}
	return strings.Join(sections, "\n\n") + "\n\n# 完成后请回到 " + brand.Normalize().Name + " 点击「重新检测」校验完整性。\n"
}

var diagnosticSecrets = regexp.MustCompile(`(?im)(authorization\s*[:=]\s*|(?:api[_-]?key|token|password|passwd|secret|credential|cookie)\s*["']?\s*[:=]\s*["']?)([^\r\n,;}]+)`)
var diagnosticBearer = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[^\s"'<>]+`)
var diagnosticTokens = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{8,}|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)\b`)
var diagnosticProxyKV = regexp.MustCompile(`(?im)(\b(?:https?|all|ftp|socks)_proxy\b\s*[:=]\s*|\bproxy(?:[_-]?url|\s+url)?\s*[:=]\s*)(["']?)(?:(https?|socks(?:4a?|5h?))://[^\s"'<>,;]+|[^\s"'<>,;]+)(["']?)`)
var diagnosticProxyConnect = regexp.MustCompile(`(?i)(\bproxyconnect\s+\w+\s*:\s*dial\s+\w+\s+)(?:\[[^\]]+\]:\d+|[^\s"'<>:;]+:\d+)`)
var diagnosticProxyPhrase = regexp.MustCompile(`(?i)(\b(?:via|using|connecting\s+to|through)\s+proxy\s+)(["']?)(?:(https?|socks(?:4a?|5h?))://[^\s"'<>,;]+|[^\s"'<>,;]+)(["']?)`)
var diagnosticSocksURLs = regexp.MustCompile(`(?i)\b(socks(?:4a?|5h?))://[^\s"'<>]+`)
var diagnosticURLs = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)
var diagnosticHomes = regexp.MustCompile(`/(?:Users|home)/[^/\s"'<>]+`)

func redactProxyMatch(re *regexp.Regexp, s string) string {
	return re.ReplaceAllStringFunc(s, func(match string) string {
		parts := re.FindStringSubmatch(match)
		if len(parts) < 5 {
			return "[代理已脱敏]"
		}
		prefix, openQuote, scheme, closeQuote := parts[1], parts[2], parts[3], parts[4]
		if scheme != "" {
			return prefix + openQuote + strings.ToLower(scheme) + "://[代理已脱敏]" + closeQuote
		}
		return prefix + openQuote + "[代理已脱敏]" + closeQuote
	})
}

// RedactText accepts installer output only. It is deliberately not a
// collector: callers must never pass chat, memory, login files or environment
// dumps. Proxy URLs (including socks5/socks5h/http/https userinfo and host:port)
// and URL query/fragment/userinfo are removed even with unfamiliar key names.
func RedactText(s string) string {
	s = redactProxyMatch(diagnosticProxyKV, s)
	s = diagnosticProxyConnect.ReplaceAllString(s, "${1}[代理已脱敏]")
	s = redactProxyMatch(diagnosticProxyPhrase, s)
	s = diagnosticSocksURLs.ReplaceAllStringFunc(s, func(raw string) string {
		parts := diagnosticSocksURLs.FindStringSubmatch(raw)
		if len(parts) >= 2 && parts[1] != "" {
			return strings.ToLower(parts[1]) + "://[代理已脱敏]"
		}
		return "[代理已脱敏]"
	})
	s = diagnosticURLs.ReplaceAllStringFunc(s, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[URL已脱敏]"
		}
		hasCreds := u.User != nil
		isProxyHost := strings.Contains(strings.ToLower(u.Hostname()), "proxy")
		u.User, u.RawQuery, u.Fragment = nil, "", ""
		if hasCreds || isProxyHost {
			u.Host = ""
			scheme := strings.ToLower(u.Scheme)
			if isProxyHost {
				return scheme + "://[代理已脱敏]"
			}
			return scheme + "://[已脱敏主机]" + u.Path
		}
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
// and remains available before any components or an installation journal exist.
func DiagnosticArchive(brand AppBrand, input DiagnosticInput) ([]byte, error) {
	report, prompt, err := Diagnostics(brand, input)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	bundle := zip.NewWriter(&out)
	for _, file := range []struct{ Name, Body string }{{"diagnostic.json", report}, {"agent-prompt.md", prompt}, {"README.txt", "仅包含脱敏安装上下文。缺失字段表示尚未获得，不代表成功。修复后请在" + brand.Normalize().Name + "重新检测；Agent文字回复不能改变安装状态。\n"}} {
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

func ExportDiagnostic(brand AppBrand, path string, input DiagnosticInput) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("absolute diagnostic export path required")
	}
	b, err := DiagnosticArchive(brand, input)
	if err != nil {
		return err
	}
	return AtomicPrivateFile(path, b)
}

// Diagnostics works with only the in-memory failure context, before even a
// state directory exists. All inputs are supplied by the caller.
func Diagnostics(brand AppBrand, input DiagnosticInput) (report, prompt string, err error) {
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
			return RedactText(v)
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
	brand = brand.Normalize()
	prompt = fmt.Sprintf(`请帮助排查 %s 首次安装失败。以下是安装器生成的脱敏诊断；缺失字段表示尚未获得，不能推断成功。

优先运行 %s %s 获取只读诊断。先只读核对失败步骤、依赖兼容性、来源与路径，列出已证事实、假设和未知。优先复用已有兼容依赖，不擅自升级、替换或新装。涉及新增下载、安装、升级、配置或进程变更时，先说明用途、版本、来源、大小、位置及影响，并取得用户确认。
%s%s
仅使用原安装器的重试／重新检测入口确认结果。你的文字回复不能将状态标为成功。给出可复核原因、建议动作、影响及待安装器核验项。

诊断材料：
%s`, brand.Name, brand.commandPath(), strings.Join(brand.DoctorArgs, " "), brand.PrivacyGuardNote, brand.DiagnosticRetryNote, report)
	return report, prompt, nil
}

func PrivateRead(path string) ([]byte, error) {
	return PrivateReadBounded(path, 2<<20)
}

func PrivateReadBounded(path string, maxBytes int64) ([]byte, error) {
	for p := filepath.Dir(path); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("private state parent must not be a symlink")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxBytes {
		return nil, errors.New("private state must be a bounded private regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) == maxBytes {
		var extra [1]byte
		if _, err := io.ReadFull(f, extra[:]); err == nil {
			return nil, errors.New("private state exceeds size limit")
		} else if !errors.Is(err, io.EOF) {
			return nil, err
		}
	}
	return data, nil
}

func AtomicPrivateFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".aiinstaller-state-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// WorkerReceipt persists synchronized worker state updates.
type WorkerReceipt struct {
	mu    sync.Mutex
	path  string
	state WorkerState
}

// NewWorkerReceipt creates a receipt backed by the supplied path.
func NewWorkerReceipt(path string, state WorkerState) *WorkerReceipt {
	return &WorkerReceipt{path: path, state: state}
}

func (w *WorkerReceipt) writeWorkerState(update func(*WorkerState)) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	update(&w.state)
	w.state.HeartbeatAt = time.Now().UTC().Format(time.RFC3339Nano)
	w.state.SafeMessage = RedactText(w.state.SafeMessage)
	b, err := json.Marshal(w.state)
	if err != nil {
		return err
	}
	return AtomicPrivateFile(w.path, b)
}

// Fail records a terminal failure, returning the underlying error and any write error.
func (w *WorkerReceipt) Fail(err error, code string, classify ErrorClassifier) error {
	state, code, message := classifyWorkerError(err, code, classify)
	writeErr := w.writeWorkerState(func(s *WorkerState) {
		s.State, s.ErrorCode, s.SafeMessage = state, code, message
	})
	if writeErr != nil {
		return errors.Join(err, writeErr)
	}
	return err
}

func classifyWorkerError(err error, code string, classify ErrorClassifier) (string, string, string) {
	if classify != nil {
		if state, code, message, ok := classify(err); ok {
			return state, code, message
		}
	}
	state, message := "failed", "Component installation failed."
	switch {
	case errors.Is(err, ErrInstallWorkerBusy):
		state, code, message = "failed", "install_worker_active", "Another component installation is in progress; retry shortly."
	case errors.Is(err, ErrOrphanInstallProcess):
		state, code, message = "needs_review", "orphan_install_process", ErrOrphanInstallProcess.Error()
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		state, code, message = "cancelled", "cancelled", "Installation cancelled."
	case code == "runtime_start_failed":
		message = "Installed components could not start the runtime."
	case errors.Is(err, ErrPlanChanged):
		code, message = "plan_changed", ErrPlanChanged.Error()
	case errors.Is(err, ErrConsent):
		code, message = "consent_required", ErrConsent.Error()
	case errors.Is(err, ErrOperationIDMismatch):
		code, message = "operation_id_mismatch", ErrOperationIDMismatch.Error()
	}
	return state, code, message
}

type summarizedArtifactError struct{ msg string }

func (e summarizedArtifactError) Error() string { return e.msg }

func SummarizeArtifactError(err error) string {
	if err == nil {
		return ""
	}
	var summarized summarizedArtifactError
	if errors.As(err, &summarized) {
		return summarized.msg
	}
	if errors.Is(err, ErrArtifactStalled) {
		return err.Error()
	}
	var httpErr ArtifactHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Error()
	}
	for _, sentinel := range []error{
		ErrArtifactDigestMismatch,
		ErrArtifactHTTPSRequired,
		ErrArtifactDigestRequired,
		ErrUnsafeArtifactRedirect,
	} {
		if errors.Is(err, sentinel) {
			return sentinel.Error()
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "request timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "request canceled"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsTimeout {
			return "DNS lookup timeout"
		}
		return "DNS lookup failed"
	}
	var escErr url.EscapeError
	var hostErr url.InvalidHostError
	if errors.As(err, &escErr) || errors.As(err, &hostErr) {
		return "invalid redirect location"
	}
	var unknownAuth x509.UnknownAuthorityError
	var hostCert x509.HostnameError
	var invalidCert x509.CertificateInvalidError
	var tlsRecord tls.RecordHeaderError
	if errors.As(err, &unknownAuth) || errors.As(err, &hostCert) || errors.As(err, &invalidCert) || errors.As(err, &tlsRecord) {
		return "TLS verification failed"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Timeout() {
			return fmt.Sprintf("network %s timeout", opErr.Op)
		}
		return fmt.Sprintf("network %s failed", opErr.Op)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "unexpected EOF"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		if urlErr.Timeout() {
			return "network timeout"
		}
		lower := strings.ToLower(urlErr.Err.Error())
		switch {
		case strings.Contains(lower, "proxy"):
			return "proxy connection failed"
		case strings.Contains(lower, "location") || strings.Contains(lower, "redirect") || strings.Contains(lower, "parse") || strings.Contains(lower, "escape"):
			return "invalid redirect location"
		case strings.Contains(lower, "tls") || strings.Contains(lower, "x509") || strings.Contains(lower, "certificate"):
			return "TLS verification failed"
		default:
			return "HTTP request failed"
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "network timeout"
		}
		return "network error"
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return "local file error"
	}
	return "download failed"
}
