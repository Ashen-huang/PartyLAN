package diaglog

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

func Setup() (string, error) {
	logDir := resolveDefaultLogDir()
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		fallbackDir := resolveFallbackLogDir()
		if fallbackDir == "" {
			return "", fmt.Errorf("create log dir %q: %w", logDir, err)
		}
		if mkErr := os.MkdirAll(fallbackDir, 0o755); mkErr != nil {
			return "", fmt.Errorf("create log dir %q failed: %w; fallback %q failed: %v", logDir, err, fallbackDir, mkErr)
		}
		logDir = fallbackDir
	}

	filePath := filepath.Join(logDir, fmt.Sprintf("partylan-%s.log", time.Now().Format("20060102")))
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("open log file: %w", err)
	}

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if os.Getenv("PARTYLAN_LOG_STDOUT") == "1" {
		// Keep file first so stdout failures do not block file logging.
		log.SetOutput(io.MultiWriter(file, os.Stdout))
	} else {
		log.SetOutput(file)
	}
	log.Printf("[diag] file logging enabled: %s", filePath)
	return filePath, nil
}

func resolveDefaultLogDir() string {
	if envDir := os.Getenv("PARTYLAN_LOG_DIR"); envDir != "" {
		return filepath.Join(envDir, "logs")
	}

	if exePath, err := os.Executable(); err == nil && exePath != "" {
		return filepath.Join(filepath.Dir(exePath), "logs")
	}

	return resolveFallbackLogDir()
}

func resolveFallbackLogDir() string {
	if baseDir, err := os.UserConfigDir(); err == nil && baseDir != "" {
		return filepath.Join(baseDir, "PartyLAN", "logs")
	}
	return filepath.Join(os.TempDir(), "PartyLAN", "logs")
}
