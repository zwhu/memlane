package bootstrap

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

func FirstInitializationComplete(configPath string) (bool, error) {
	config, err := loadFirstRuntimeConfig(configPath)
	if err != nil {
		return false, err
	}
	if config.First.Setup || config.GenerationMode == "unconfigured" {
		return false, nil
	}
	if (config.GenerationMode != "provider" && config.GenerationMode != "agent_bridge") || config.Model == "" || config.GenerationFile != filepath.Join(config.Root, "generation.json") {
		return false, errors.New("首次提炼配置尚未确认，保留原回执")
	}
	generationBytes, err := privateRead(config.GenerationFile)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var generation struct {
		Schema   int                       `json:"schema_version"`
		Active   *FirstGenerationReference `json:"active"`
		Profiles map[string][]struct {
			Revision int64 `json:"revision"`
			Profile  struct {
				Mode  string `json:"mode"`
				Model string `json:"model"`
			} `json:"profile"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(generationBytes, &generation); err != nil {
		return false, err
	}
	if generation.Schema != 2 {
		return false, errors.New("首次提炼配置格式不同")
	}
	if generation.Active == nil {
		return false, nil
	}
	applied := false
	for _, row := range generation.Profiles[generation.Active.ID] {
		if row.Revision == generation.Active.Revision && row.Profile.Mode == config.GenerationMode && row.Profile.Model == config.Model {
			applied = true
		}
	}
	if !applied {
		return false, errors.New("首次实际启用配置与客户端不同")
	}
	b, err := privateRead(filepath.Join(config.Root, "client-state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var state struct {
		Schema    int    `json:"schema_version"`
		Completed string `json:"initialization_completed_at"`
		Date      string `json:"first_used_date"`
		Zone      string `json:"first_used_timezone"`
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return false, err
	}
	if state.Schema != 1 || state.Zone == "" {
		return false, errors.New("初始化回执格式不同，保留原文件")
	}
	if _, err := time.Parse(time.RFC3339Nano, state.Completed); err != nil {
		return false, err
	}
	if _, err := time.Parse("2006-01-02", state.Date); err != nil {
		return false, err
	}
	return true, nil
}

type firstWizardProgress struct {
	Schema  int    `json:"schema"`
	StoreID string `json:"store_id"`
	Step    int    `json:"step"`
}

func FirstWizardStep(configPath string) (int, error) {
	config, err := loadFirstRuntimeConfig(configPath)
	if err != nil {
		return 1, err
	}
	b, err := privateRead(filepath.Join(config.Root, "first-install-wizard.json"))
	if errors.Is(err, os.ErrNotExist) {
		return 2, nil
	}
	if err != nil {
		return 1, err
	}
	var progress firstWizardProgress
	if err := json.Unmarshal(b, &progress); err != nil {
		return 1, err
	}
	if progress.Schema != 1 || progress.StoreID != config.First.StoreID || progress.Step < 1 || progress.Step > 3 {
		return 1, errors.New("首次配置进度归属不同，保留原文件")
	}
	return progress.Step, nil
}

func SaveFirstWizardStep(configPath string, step int) error {
	if step < 1 || step > 3 {
		return errors.New("无效的首次配置步骤")
	}
	config, err := loadFirstRuntimeConfig(configPath)
	if err != nil {
		return err
	}
	b, err := json.Marshal(firstWizardProgress{Schema: 1, StoreID: config.First.StoreID, Step: step})
	if err != nil {
		return err
	}
	return atomicPrivateFile(filepath.Join(config.Root, "first-install-wizard.json"), b)
}
