package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	DisplayName         string   `json:"displayName"`
	TailscaleAuthKey    string   `json:"tailscaleAuthKey,omitempty"`
	LastRoomID          string   `json:"lastRoomId,omitempty"`
	WindowAutoSized     bool     `json:"windowAutoSized,omitempty"`
	WindowLayoutVersion int      `json:"windowLayoutVersion,omitempty"`
	VideoQuality        int      `json:"videoQuality"` // 0=smooth, 1=balanced, 2=high, 3=original
	AudioBitrate        int      `json:"audioBitrate"` // kbps
	MicDeviceID         string   `json:"micDeviceId,omitempty"`
	AudioShareSources   []string `json:"audioShareSources,omitempty"`
	AutoJoin            bool     `json:"autoJoin"`
	DERPRegionID        int      `json:"derpRegionId,omitempty"`
	DERPRegionCode      string   `json:"derpRegionCode,omitempty"`
	DERPHostName        string   `json:"derpHostName,omitempty"`
	DERPPort            int      `json:"derpPort,omitempty"`
}

func DefaultConfig() *Config {
	return &Config{
		DisplayName:       "",
		VideoQuality:      1, // balanced
		AudioBitrate:      128,
		AudioShareSources: []string{},
		AutoJoin:          false,
	}
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".partylan"), nil
}

func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return DefaultConfig(), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, err
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func Save(cfg *Config) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	path, err := configPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
