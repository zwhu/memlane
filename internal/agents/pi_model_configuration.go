package agents

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var errPiCommandConfiguration = errors.New("pi command-valued provider configuration is not supported by the isolated runner")

// copyPiModelDefinitions snapshots only checked provider definitions, never
// settings/hooks/extensions. The exact checked bytes are the consumed bytes.
// expected is a frozen project binding hash, or empty for an unbound runner.
func copyPiModelDefinitions(source, destination, expected string) error {
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) {
		return errors.New("absolute pi configuration directory required")
	}
	f, err := os.OpenFile(filepath.Join(source, "models.json"), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		if expected != "" {
			return errors.New("bound pi provider configuration changed")
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return errors.New("pi provider configuration invalid")
	}
	b, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil {
		return err
	}
	var configuration any
	if len(b) > 1024*1024 || json.Unmarshal(b, &configuration) != nil {
		return errors.New("pi provider configuration invalid")
	}
	var commandValue func(any) bool
	commandValue = func(v any) bool {
		switch x := v.(type) {
		case string:
			return strings.HasPrefix(x, "!")
		case []any:
			for _, v := range x {
				if commandValue(v) {
					return true
				}
			}
		case map[string]any:
			for _, v := range x {
				if commandValue(v) {
					return true
				}
			}
		}
		return false
	}
	if commandValue(configuration) {
		return errPiCommandConfiguration
	}
	if expected != "" && hash(b) != expected {
		return errors.New("bound pi provider configuration changed")
	}
	return os.WriteFile(filepath.Join(destination, "models.json"), b, 0400)
}
