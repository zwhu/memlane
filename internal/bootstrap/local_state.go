package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func ComponentRoot(root string) (string, error) {
	if root != "" {
		if !filepath.IsAbs(root) {
			return "", errors.New("组件位置必须为绝对路径")
		}
		return filepath.Clean(root), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library/Application Support/Memlane/components"), nil
}

func privateRead(path string) ([]byte, error) {
	return privateReadBounded(path, 2<<20)
}

func privateReadBounded(path string, maxBytes int64) ([]byte, error) {
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
	return io.ReadAll(f)
}

func atomicPrivateFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".memlane-state-")
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

// Selections contains only user-selected dependency paths, never environment
// variables, account credentials, memory data, or installation permission.
type Selections struct {
	SelectedAgentsConfigured bool     `json:"selected_agents_configured,omitempty"`
	SelectedAgents           []string `json:"selected_agents,omitempty"`
	Schema                   int      `json:"schema"`
	Binding                  string   `json:"binding"`
	Python                   string   `json:"python"`
	UV                       string   `json:"uv"`
	Source                   string   `json:"source"`
	Archive                  string   `json:"archive"`
	ModelCache               string   `json:"model_cache"`
	Plugin                   string   `json:"plugin"`
	DownloadSource           string   `json:"download_source,omitempty"`
}

func selectionBinding(configPath string) string {
	h := sha256.Sum256([]byte(configPath))
	return hex.EncodeToString(h[:])
}
func selectionPath(root, configPath string) string {
	return filepath.Join(root, "installation", "selection-"+selectionBinding(configPath)[:16]+".json")
}
func LoadSelections(root, configPath string) (Selections, error) {
	root, err := ComponentRoot(root)
	if err != nil {
		return Selections{}, err
	}
	b, err := privateRead(selectionPath(root, configPath))
	if err != nil {
		return Selections{}, err
	}
	return decodeSelections(b, configPath)
}

func decodeSelections(b []byte, configPath string) (Selections, error) {
	var s Selections
	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if s.Schema != 1 || s.Binding != selectionBinding(configPath) {
		return s, errors.New("dependency selection binding differs; original file retained")
	}
	s.SelectedAgents = NormalizeSelectedAgents(s.SelectedAgents)
	return s, nil
}
func SaveSelections(options EnvironmentOptions, configPath string) error {
	root, err := ComponentRoot(options.Root)
	if err != nil {
		return err
	}
	options.Root = root
	if err := validateComponentLocation(options); err != nil {
		return err
	}
	s := Selections{Schema: 1, Binding: selectionBinding(configPath), Python: options.Python, UV: options.UV, Source: options.Source, Archive: options.Archive, ModelCache: options.ModelCache, Plugin: options.Plugin}
	if options.DownloadSource != "" {
		s.DownloadSource = NormalizeDownloadSource(options.DownloadSource)
	}
	i := Installer{Root: filepath.Join(root, "installation")}
	return i.withLock(func() error {
		existing, err := LoadSelections(root, configPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if options.SelectedAgents == nil {
			s.SelectedAgents = existing.SelectedAgents
			s.SelectedAgentsConfigured = existing.SelectedAgentsConfigured
		} else {
			s.SelectedAgents = NormalizeSelectedAgents(options.SelectedAgents)
			s.SelectedAgentsConfigured = true
		}
		b, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return err
		}
		return atomicPrivateFile(selectionPath(root, configPath), b)
	})
}

// SaveSelectedAgents updates only the host selections for this configuration.
func SaveSelectedAgents(root, configPath string, agents []string) error {
	root, err := ComponentRoot(root)
	if err != nil {
		return err
	}
	i := Installer{Root: filepath.Join(root, "installation")}
	return i.withLock(func() error {
		path := selectionPath(root, configPath)
		raw, err := privateRead(path)
		doc := map[string]json.RawMessage{}
		switch {
		case errors.Is(err, os.ErrNotExist):
			raw, err = json.Marshal(Selections{Schema: 1, Binding: selectionBinding(configPath)})
			if err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			if _, err := decodeSelections(raw, configPath); err != nil {
				return err
			}
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return err
		}
		doc["selected_agents_configured"] = json.RawMessage("true")
		if selected := NormalizeSelectedAgents(agents); len(selected) == 0 {
			delete(doc, "selected_agents")
		} else {
			doc["selected_agents"], err = json.Marshal(selected)
			if err != nil {
				return err
			}
		}
		updated, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		return atomicPrivateFile(path, updated)
	})
}
