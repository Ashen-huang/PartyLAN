package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const defaultGameCastBaseURL = "https://partylanapp.com"

type Config struct {
	DisplayName         string          `json:"displayName"`
	TailscaleAuthKey    string          `json:"tailscaleAuthKey,omitempty"`
	LastRoomID          string          `json:"lastRoomId,omitempty"`
	WindowAutoSized     bool            `json:"windowAutoSized,omitempty"`
	WindowLayoutVersion int             `json:"windowLayoutVersion,omitempty"`
	VideoQuality        int             `json:"videoQuality"` // 0=smooth, 1=balanced, 2=high, 3=original
	AudioBitrate        int             `json:"audioBitrate"` // kbps
	MicDeviceID         string          `json:"micDeviceId,omitempty"`
	AudioShareSources   []string        `json:"audioShareSources,omitempty"`
	AutoJoin            bool            `json:"autoJoin"`
	DERPRegionID        int             `json:"derpRegionId,omitempty"`
	DERPRegionCode      string          `json:"derpRegionCode,omitempty"`
	DERPHostName        string          `json:"derpHostName,omitempty"`
	DERPPort            int             `json:"derpPort,omitempty"`
	GameCast            GameCastProfile `json:"gamecast"`
}

type GameCastProfile struct {
	BaseURL                  string                     `json:"baseUrl"`
	DisplayName              string                     `json:"displayName,omitempty"`
	TrustedCertificateSHA256 string                     `json:"trustedCertificateSha256,omitempty"`
	RoomHistory              []GameCastRoomHistoryEntry `json:"roomHistory,omitempty"`
}

type GameCastRoomHistoryEntry struct {
	ID   string `json:"id"`
	Time int64  `json:"time"`
}

func DefaultConfig() *Config {
	return &Config{
		DisplayName:       "",
		VideoQuality:      1, // balanced
		AudioBitrate:      128,
		AudioShareSources: []string{},
		AutoJoin:          false,
		GameCast: GameCastProfile{
			BaseURL: defaultGameCastBaseURL,
		},
	}
}

func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".partylan"), nil
}

func configDir() (string, error) {
	return Dir()
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
	temporary, err := os.CreateTemp(dir, ".config.json-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
