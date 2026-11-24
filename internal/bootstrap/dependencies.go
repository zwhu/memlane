package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var lockedDistribution = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.-]*)==([^\s;\\]+)`)

// LockedVersions reads only pinned requirement lines; it doesn't resolve indexes
// or execute a package manager. The lock bytes themselves remain consent scope.
func LockedVersions(lock []byte) (map[string]string, error) {
	versions := map[string]string{}
	for _, line := range strings.Split(string(lock), "\n") {
		if match := lockedDistribution.FindStringSubmatch(line); match != nil {
			name := strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(match[1]))
			if old, ok := versions[name]; ok && old != match[2] {
				return nil, errors.New("ambiguous locked dependency")
			}
			versions[name] = match[2]
		}
	}
	if len(versions) == 0 {
		return nil, errors.New("empty locked dependency set")
	}
	return versions, nil
}

// ProbeDependencies checks pinned distributions and native imports in an
// isolated subprocess. It never invokes pip/uv or downloads missing libraries.
func ProbeDependencies(ctx context.Context, python string, lock []byte) (Observation, error) {
	o, err := ProbePython(ctx, python)
	if err != nil || o.Status != Available {
		return o, err
	}
	versions, err := LockedVersions(lock)
	if err != nil {
		return Observation{}, err
	}
	encoded, err := json.Marshal(versions)
	if err != nil {
		return Observation{}, err
	}
	code := `import importlib.metadata,json,sys
required=json.loads(sys.argv[1]); mismatch=[]
for name,expected in sorted(required.items()):
    try: actual=importlib.metadata.version(name)
    except importlib.metadata.PackageNotFoundError: actual='missing'
    if actual!=expected: mismatch.append(dict(name=name,expected=expected,actual=actual))
native_error=None
if not mismatch:
    try: import ladybug._lbug,lancedb,fastembed,uvicorn
    except Exception as error: native_error=type(error).__name__
print(json.dumps(dict(mismatch=mismatch,native_error=native_error)))`
	b, err := isolatedPython(ctx, python, code, string(encoded))
	if err != nil {
		if ctx.Err() != nil {
			return o, ctx.Err()
		}
		o.Status, o.Reason = Incompatible, "运行库探测无法启动"
		var exit *exec.ExitError
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			o.Reason = "运行库探测超时（" + probeTimeoutText(ctx) + "）"
			o.transientTimeout = true
		case errors.As(err, &exit):
			o.Reason = fmt.Sprintf("运行库探测进程退出（exit_code=%d）", exit.ExitCode())
			if category := classifyDependencyProbeStderr(exit.Stderr); category != "" {
				o.Reason += "：" + category
			}
		}
		return o, nil
	}
	var result struct {
		Mismatch    []struct{ Name, Expected, Actual string }
		NativeError *string `json:"native_error"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return o, err
	}
	if len(result.Mismatch) > 0 {
		o.Status = Incompatible
		first := result.Mismatch[0]
		o.Reason = fmt.Sprintf("%d项运行库与固定依赖不符；%s需要%s，已有%s", len(result.Mismatch), first.Name, first.Expected, first.Actual)
		return o, nil
	}
	if result.NativeError != nil {
		o.Status, o.Reason = Incompatible, "原生运行库加载失败："+*result.NativeError
		return o, nil
	}
	h := sha256.New()
	h.Write([]byte(o.Fingerprint))
	h.Write(lock)
	h.Write(b)
	o.Fingerprint = hex.EncodeToString(h.Sum(nil))
	return o, nil
}

// classifyDependencyProbeStderr maps probe stderr to fixed diagnostic categories
// so arbitrary paths or secrets from stderr are never persisted in Observations.
func classifyDependencyProbeStderr(stderr []byte) string {
	lower := strings.ToLower(string(stderr))
	switch {
	case strings.Contains(lower, "dyld") || strings.Contains(lower, "library not loaded") || strings.Contains(lower, "symbol not found") || strings.Contains(lower, "dlopen"):
		return "动态库加载失败"
	case strings.Contains(lower, "bad cpu type") || strings.Contains(lower, "incompatible architecture") || strings.Contains(lower, "mach-o"):
		return "架构不兼容"
	case strings.Contains(lower, "permission denied") || strings.Contains(lower, "operation not permitted") || strings.Contains(lower, "code signature"):
		return "系统权限或签名限制"
	case strings.Contains(lower, "modulenotfounderror") || strings.Contains(lower, "importerror"):
		return "模块导入失败"
	case strings.Contains(lower, "segmentation fault") || strings.Contains(lower, "abort trap") || strings.Contains(lower, "killed"):
		return "进程异常中止"
	default:
		return ""
	}
}
