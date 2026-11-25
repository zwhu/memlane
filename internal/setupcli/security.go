package setupcli

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
)

func secretInput(args []string, environ func(string) string) bool {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		name = strings.ReplaceAll(strings.ToLower(name), "_", "-")
		if oneOf(name, "api-key", "apikey", "llm-api-key", "openai-api-key", "anthropic-api-key", "deepseek-api-key", "gemini-api-key",
			"token", "access-token", "bearer", "x-api-key", "secret", "password", "passwd", "credential", "private-key") {
			return true
		}
		for _, suffix := range []string{"-api-key", "-token", "-secret", "-password"} {
			if strings.HasSuffix(name, suffix) {
				return true
			}
		}
	}
	for _, name := range []string{"MEMLANE_API_KEY", "MEMLANE_LLM_API_KEY", "MEMLANE_TOKEN", "MEMLANE_OWNER_TOKEN", "MEMLANE_MANAGEMENT_TOKEN", "MEMLANE_SECRET", "MEMLANE_PASSWORD"} {
		if environ(name) != "" {
			return true
		}
	}
	return false
}

type runtimeConfig struct {
	First          *bootstrap.FirstInstallation `json:"first_install"`
	Root           string                       `json:"official_data"`
	ServiceCommit  string                       `json:"service_commit"`
	ServiceURL     string                       `json:"service_url"`
	CompletionURL  string                       `json:"completion_url"`
	OwnerTokenFile string                       `json:"token_file"`
	GenerationFile string                       `json:"generation_file"`
	Control        controlplane.Config          `json:"project_control"`
}

var errUnsafePath = errors.New("unsafe path")

func checkPath(path string) error {
	_, err := resolveSafePath(path)
	return err
}

// Validate lexical components before normalization so traversal cannot hide a symlink.
func resolveSafePath(path string) (string, error) {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return "", errUnsafePath
	}
	root := string(filepath.Separator)
	current := root
	for part := range strings.SplitSeq(path, root) {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", errUnsafePath
		}
		first := current == root
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if runtime.GOOS != "darwin" || !first || !oneOf(current, "/tmp", "/var") {
			return "", errUnsafePath
		}
		resolved, err := filepath.EvalSymlinks(current)
		if err != nil || resolved != "/private"+current {
			return "", errUnsafePath
		}
		current = resolved
	}
	return current, nil
}

func readBoundedJSON(path string, dst any) error {
	path, err := resolveSafePath(path)
	if err != nil {
		return err
	}
	dir, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	f, err := dir.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	const limit = 2 << 20
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return errUnsafePath
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if len(raw) > limit {
		return errUnsafePath
	}
	return json.Unmarshal(raw, dst)
}

func (cfg runtimeConfig) firstInstall() bool {
	if cfg.First == nil || cfg.First.Schema != 1 || len(cfg.First.StoreID) != 32 || !filepath.IsAbs(cfg.Root) || checkPath(cfg.Root) != nil {
		return false
	}
	var receipt bootstrap.StoreReceipt
	return readBoundedJSON(filepath.Join(cfg.Root, "store-identity.json"), &receipt) == nil &&
		receipt.Schema == 1 && receipt.StorageInitialized && receipt.OwnerID != "" && receipt.KeyID != "" &&
		receipt.StoreID == cfg.First.StoreID && receipt.SourceCommit == cfg.ServiceCommit &&
		cfg.OwnerTokenFile == filepath.Join(cfg.Root, "owner.token")
}

func (s *session) loadPaths() bool {
	s.configPath, s.root = s.get("config"), s.get("component-root")
	var err error
	if s.configPath == "" {
		s.configPath, err = s.DefaultConfigFn()
		if err != nil {
			s.block(ExitFatal, "config_path_unavailable")
			return false
		}
	}
	if s.root == "" {
		s.root, err = s.DefaultComponentRootFn()
		if err != nil {
			s.block(ExitFatal, "component_root_unavailable")
			return false
		}
	}
	for _, path := range []*string{&s.configPath, &s.root} {
		*path, err = resolveSafePath(*path)
		if err != nil {
			s.block(ExitFatal, "unsafe_local_path")
			return false
		}
	}
	s.Environment.ConfigPath = SanitizeString(s.configPath, s.HomeDir)
	s.Environment.ComponentRoot = SanitizeString(s.root, s.HomeDir)
	switch err := readBoundedJSON(s.configPath, &s.config); {
	case err == nil:
		switch {
		case !s.config.firstInstall():
			s.Environment.StoreMode = "external_readonly"
		case s.config.First.Setup:
			s.Environment.StoreMode = "first_install_setup"
		default:
			s.Environment.StoreMode = "ready"
		}
	case !errors.Is(err, os.ErrNotExist):
		s.block(ExitFatal, "runtime_config_unreadable")
		return false
	}
	if s.Mode == "apply" && s.Environment.StoreMode == "external_readonly" {
		s.block(ExitFatal, "non_first_install_config_read_only")
		return false
	}
	if s.firstInstall() {
		s.opsDir = controlplane.OperationsDir(s.config.GenerationFile, s.config.Control.ManagementTokenFile)
	}
	return true
}

func (s *session) firstInstall() bool {
	return oneOf(s.Environment.StoreMode, "first_install_setup", "ready")
}

func (s *session) loadSelections() bool {
	sel, err := bootstrap.LoadSelections(s.root, s.configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.block(ExitFatal, "component_selections_unreadable")
		return false
	}
	s.selections = sel
	s.Environment.SelectedAgents = append([]string{}, slices.DeleteFunc(slices.Clone(sel.SelectedAgents), func(kind string) bool {
		return s.has("agent") && kind != s.get("agent")
	})...)
	s.Environment.DownloadSource = bootstrap.NormalizeDownloadSource(sel.DownloadSource)
	if s.has("mirror") {
		s.Environment.DownloadSource = s.get("mirror")
	}
	return true
}

func diskFree(path string) (uint64, error) {
	for {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(path, &stat); err == nil {
			return uint64(stat.Bavail) * uint64(stat.Bsize), nil
		} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return 0, err
		}
		parent := filepath.Dir(path)
		if path == parent {
			return 0, os.ErrNotExist
		}
		path = parent
	}
}
