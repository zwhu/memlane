// This checker exercises packaged Cognee initialization in a marked synthetic root.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zwhu/memlane/internal/official"
)

func main() {
	var input struct {
		Root   string          `json:"root"`
		Config official.Config `json:"config"`
	}
	if json.NewDecoder(os.Stdin).Decode(&input) != nil || !filepath.IsAbs(input.Root) {
		os.Exit(2)
	}
	var marker struct {
		Synthetic bool `json:"synthetic_only"`
	}
	b, err := os.ReadFile(filepath.Join(input.Root, "synthetic-only.json"))
	if err != nil || json.Unmarshal(b, &marker) != nil || !marker.Synthetic {
		os.Exit(2)
	}
	c := &official.Controller{Root: filepath.Join(input.Root, "official"), Config: input.Config}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var status official.Status
	err = c.PrepareEnvironment(ctx, func(value official.Status) { status = value })
	settings, settingsErr := c.Settings()
	cleanup, stop := context.WithTimeout(context.Background(), 25*time.Second)
	defer stop()
	stopErr := c.Stop(cleanup)
	result := map[string]any{"status": status.Phase, "service_ready": status.Phase == "service_ready", "model_bridge_selected": settings.ModelBridge != nil, "cloud_model_key_present": settings.Environment["LLM_API_KEY"] != "", "cleanup_passed": stopErr == nil, "packaged_service": true, "synthetic_only": true}
	if err != nil {
		result["error"] = err.Error()
	}
	if settingsErr != nil {
		result["settings_error"] = settingsErr.Error()
	}
	if stopErr != nil {
		result["cleanup_error"] = stopErr.Error()
	}
	if e := json.NewEncoder(os.Stdout).Encode(result); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	if err != nil || settingsErr != nil || stopErr != nil || status.Phase != "service_ready" || settings.ModelBridge != nil || settings.Environment["LLM_API_KEY"] != "" {
		os.Exit(1)
	}
}
