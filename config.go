package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config is the persisted input selection. It lives in the user config
// directory (e.g. %AppData%\GuitarTuner\config.json on Windows).
type Config struct {
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	Channel    int    `json:"channel"`
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "GuitarTuner", "config.json"), nil
}

func loadConfig() (Config, bool) {
	p, err := configPath()
	if err != nil {
		return Config{}, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Config{}, false
	}
	var cfg Config
	if json.Unmarshal(data, &cfg) != nil || cfg.DeviceID == "" {
		return Config{}, false
	}
	if cfg.Channel < 0 {
		cfg.Channel = 0
	}
	return cfg, true
}

func saveConfig(cfg Config) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}
