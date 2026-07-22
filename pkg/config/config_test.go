package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func setTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
	return home
}

func TestLoadReturnsDefaultConfigWhenMissing(t *testing.T) {
	setTempHome(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.VideoQuality != 1 || cfg.AudioBitrate != 128 || cfg.AutoJoin {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.AudioShareSources == nil || len(cfg.AudioShareSources) != 0 {
		t.Fatalf("expected empty audio share sources, got %#v", cfg.AudioShareSources)
	}
	if cfg.GameCast.BaseURL != "https://113.142.217.42:5050" {
		t.Fatalf("unexpected default GameCast URL: %q", cfg.GameCast.BaseURL)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	home := setTempHome(t)

	cfg := DefaultConfig()
	cfg.DisplayName = "Player One"
	cfg.TailscaleAuthKey = "tskey-auth-example"
	cfg.AudioShareSources = []string{"Game.exe", "Chrome.exe"}
	cfg.DERPRegionID = 901
	cfg.DERPRegionCode = "cn1"
	cfg.DERPHostName = "relay.example.com"
	cfg.DERPPort = 443
	cfg.GameCast.BaseURL = "https://gamecast.example.com:5050"
	cfg.GameCast.DisplayName = "Viewer"
	cfg.GameCast.TrustedCertificateSHA256 = "abcdef"
	cfg.GameCast.RoomHistory = []GameCastRoomHistoryEntry{
		{ID: "room-1", Time: 42},
		{ID: "room-2", Time: 24},
	}

	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !reflect.DeepEqual(loaded, cfg) {
		t.Fatalf("round trip mismatch:\n got: %#v\nwant: %#v", loaded, cfg)
	}

	path, err := configPath()
	if err != nil {
		t.Fatalf("configPath: %v", err)
	}
	wantPrefix := filepath.Join(home, ".partylan")
	if !strings.HasPrefix(path, wantPrefix) {
		t.Fatalf("config path %q does not use temp home prefix %q", path, wantPrefix)
	}
}
