package agents

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Read only scalar model selection; project selection overrides shared values.
// Credentials and provider configuration are never copied into the run root.
func selectedTraexModel(workspace string) (string, error) {
	shared := os.Getenv("TRAE_HOME")
	if shared == "" {
		home, _ := os.UserHomeDir()
		shared = filepath.Join(home, ".trae")
	}
	var selected struct {
		Model    string `toml:"model"`
		Provider string `toml:"model_provider"`
	}
	for _, path := range []string{filepath.Join(shared, "traecli.toml"), filepath.Join(workspace, ".trae", "traecli.toml")} {
		b, _, err := readConfig(path)
		if err != nil {
			return "", err
		}
		if len(b) == 0 {
			continue
		}
		var layer struct {
			Model    string `toml:"model"`
			Provider string `toml:"model_provider"`
		}
		if err = toml.Unmarshal(b, &layer); err != nil {
			return "", errors.New("Agent model selection TOML is invalid")
		}
		if layer.Model != "" {
			selected.Model = layer.Model
		}
		if layer.Provider != "" {
			selected.Provider = layer.Provider
		}
	}
	if selected.Provider != "" && selected.Provider != "trae" {
		return "", errors.New("this isolated TRAE CLI adapter requires the built-in trae provider; custom provider must be adapted separately")
	}
	return selected.Model, nil
}
