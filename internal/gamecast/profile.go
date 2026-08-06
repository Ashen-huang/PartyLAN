package gamecast

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"strings"
)

const DefaultBaseURL = "https://partylanapp.com"

var legacyBaseURLs = map[string]struct{}{
	"https://113.142.217.42:5050": {},
	"https://85.137.240.159:5050": {},
}

type Profile struct {
	BaseURL                  string
	DisplayName              string
	TrustedCertificateSHA256 string
}

func NormalizeBaseURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = DefaultBaseURL
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse server url: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("unsupported server scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("server url must include a host")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

// MigrateLegacyBaseURL changes only the explicitly known historical servers.
func MigrateLegacyBaseURL(raw string) (string, bool) {
	normalized, err := NormalizeBaseURL(raw)
	if err != nil {
		return raw, false
	}
	if _, ok := legacyBaseURLs[strings.ToLower(normalized)]; !ok {
		return normalized, false
	}
	return DefaultBaseURL, true
}

func NormalizeFingerprint(raw string) string {
	cleaned := strings.ToLower(strings.TrimSpace(raw))
	cleaned = strings.ReplaceAll(cleaned, "sha256:", "")
	cleaned = strings.ReplaceAll(cleaned, ":", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	return cleaned
}

func FingerprintSHA256(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

func EqualFingerprint(a, b string) bool {
	return NormalizeFingerprint(a) == NormalizeFingerprint(b)
}

func URLForEndpoint(baseURL, endpoint string) (string, error) {
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + endpoint
	return parsed.String(), nil
}

func WSURL(baseURL, roomID, role, layer string) (string, error) {
	return WSURLWithName(baseURL, roomID, role, layer, "")
}

func WSURLWithName(baseURL, roomID, role, layer, name string) (string, error) {
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported websocket scheme %q", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/sfu-ws"
	query := parsed.Query()
	query.Set("room", strings.TrimSpace(roomID))
	query.Set("role", strings.TrimSpace(role))
	if layer = strings.TrimSpace(layer); layer != "" {
		query.Set("layer", layer)
	}
	if name = strings.TrimSpace(name); name != "" {
		query.Set("name", name)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func STUNURLs(baseURL string) []string {
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return []string{"stun:stun.l.google.com:19302"}
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return []string{"stun:stun.l.google.com:19302"}
	}
	host := parsed.Hostname()
	if net.ParseIP(host) == nil && host == "" {
		return []string{"stun:stun.l.google.com:19302"}
	}
	return []string{
		"stun:" + host + ":3478",
		"stun:stun.l.google.com:19302",
	}
}
