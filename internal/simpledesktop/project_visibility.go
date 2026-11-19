package simpledesktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type projectVisibilityState struct {
	Version int             `json:"version"`
	Hidden  map[string]bool `json:"hidden_projects"`
	path    string
	loaded  bool
}

// Project/dataset identities survive credential refreshes. This is a local
// navigation preference; it does not alter the directory, permissions or data.
func (a *App) projectVisibilityPath() (string, error) {
	base := a.Config.ClientStateFile
	if base == "" {
		base = a.Config.ConfigFile
	}
	if base == "" {
		return "", fmt.Errorf("项目隐藏设置存储位置不可用")
	}
	return filepath.Join(filepath.Dir(base), "project-visibility.json"), nil
}

func readProjectVisibility(path string) (projectVisibilityState, error) {
	s := projectVisibilityState{Version: 1, Hidden: map[string]bool{}, path: path, loaded: true}
	if path == "" {
		return s, nil
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return s, fmt.Errorf("项目隐藏设置无法读取")
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil || json.Unmarshal(b, &s) != nil || s.Version != 1 || s.Hidden == nil {
		return projectVisibilityState{path: path, loaded: true}, fmt.Errorf("项目隐藏设置无法读取")
	}
	return s, nil
}

func (a *App) projectIsHidden(p Project) bool {
	path, _ := a.projectVisibilityPath()
	if !a.projectVisibility.loaded || a.projectVisibility.path != path {
		a.projectVisibility, _ = readProjectVisibility(path)
	}
	return a.projectVisibility.Hidden[p.ID+"/"+p.Dataset]
}

func (a *App) setProjectHidden(p Project, hidden bool) error {
	path, err := a.projectVisibilityPath()
	if err != nil {
		return err
	}
	// Reread at mutation to preserve decisions saved by another window.
	next, err := readProjectVisibility(path)
	if err != nil {
		return err
	}
	key := p.ID + "/" + p.Dataset
	if hidden {
		next.Hidden[key] = true
	} else {
		delete(next.Hidden, key)
	}
	b, err := json.Marshal(next)
	if err != nil || writeNoticeAcknowledgements(path, b) != nil {
		return fmt.Errorf("项目隐藏设置未保存")
	}
	a.projectVisibility = next
	return nil
}
