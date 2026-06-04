package relaycfg

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"PartyLAN/pkg/config"
)

type ICEServer struct {
	URLs       []string
	Username   string
	Credential string
}

type RuntimeConfig struct {
	DERPRegionID   int
	DERPRegionCode string
	DERPHostName   string
	DERPPort       int
	TSControlURL   string
	ICEServers     []ICEServer
}

func Resolve(cfg *config.Config) RuntimeConfig {
	resolved := RuntimeConfig{}
	if cfg != nil {
		resolved.DERPRegionID = cfg.DERPRegionID
		resolved.DERPRegionCode = strings.TrimSpace(cfg.DERPRegionCode)
		resolved.DERPHostName = strings.TrimSpace(cfg.DERPHostName)
		resolved.DERPPort = cfg.DERPPort
	}

	if value := envInt("PARTYLAN_DERP_REGION_ID"); value != 0 {
		resolved.DERPRegionID = value
	}
	if value := strings.TrimSpace(os.Getenv("PARTYLAN_DERP_REGION_CODE")); value != "" {
		resolved.DERPRegionCode = value
	}
	if value := strings.TrimSpace(os.Getenv("PARTYLAN_DERP_HOSTNAME")); value != "" {
		resolved.DERPHostName = value
	}
	if value := envInt("PARTYLAN_DERP_PORT"); value != 0 {
		resolved.DERPPort = value
	}

	resolved.TSControlURL = strings.TrimSpace(os.Getenv("PARTYLAN_TS_CONTROL_URL"))
	resolved.ICEServers = parseICEServers(
		os.Getenv("PARTYLAN_ICE_SERVERS"),
		os.Getenv("PARTYLAN_TURN_USERNAME"),
		os.Getenv("PARTYLAN_TURN_CREDENTIAL"),
	)

	return resolved
}

func (c RuntimeConfig) HasICEServers() bool {
	return len(c.ICEServers) > 0
}

func (c RuntimeConfig) HasTURN() bool {
	for _, server := range c.ICEServers {
		for _, rawURL := range server.URLs {
			if isTURNURL(rawURL) {
				return true
			}
		}
	}
	return false
}

func (c RuntimeConfig) HasTURNCredentials() bool {
	for _, server := range c.ICEServers {
		if !c.serverHasTURN(server) {
			continue
		}
		if strings.TrimSpace(server.Username) != "" || strings.TrimSpace(server.Credential) != "" {
			return true
		}
	}
	return false
}

func (c RuntimeConfig) MediaHealthLines() []string {
	if len(c.ICEServers) == 0 {
		return []string{
			"media ICE servers not configured",
			"media TURN not configured",
		}
	}

	urlCount := 0
	for _, server := range c.ICEServers {
		urlCount += len(server.URLs)
	}

	lines := []string{
		fmt.Sprintf("media ICE servers configured: groups=%d urls=%d turn=%t", len(c.ICEServers), urlCount, c.HasTURN()),
	}
	switch {
	case c.HasTURN() && c.HasTURNCredentials():
		lines = append(lines, "media TURN credentials configured")
	case c.HasTURN():
		lines = append(lines, "media TURN configured without credentials")
	default:
		lines = append(lines, "media TURN not configured")
	}
	return lines
}

func (c RuntimeConfig) serverHasTURN(server ICEServer) bool {
	for _, rawURL := range server.URLs {
		if isTURNURL(rawURL) {
			return true
		}
	}
	return false
}

func parseICEServers(rawServers string, rawUsername string, rawCredential string) []ICEServer {
	rawServers = strings.TrimSpace(rawServers)
	if rawServers == "" {
		return nil
	}

	urls := make([]string, 0)
	for _, part := range strings.Split(rawServers, ",") {
		if value := strings.TrimSpace(part); value != "" {
			urls = append(urls, value)
		}
	}
	if len(urls) == 0 {
		return nil
	}

	server := ICEServer{
		URLs: urls,
	}
	if username := strings.TrimSpace(rawUsername); username != "" {
		server.Username = username
		server.Credential = rawCredential
	}

	return []ICEServer{server}
}

func envInt(name string) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}

func isTURNURL(rawURL string) bool {
	rawURL = strings.TrimSpace(strings.ToLower(rawURL))
	return strings.HasPrefix(rawURL, "turn:") || strings.HasPrefix(rawURL, "turns:")
}
