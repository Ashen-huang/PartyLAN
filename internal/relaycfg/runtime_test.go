package relaycfg

import (
	"reflect"
	"testing"

	"PartyLAN/pkg/config"
)

func TestResolveUsesConfigWhenEnvironmentMissing(t *testing.T) {
	cfg := &config.Config{
		DERPRegionID:   901,
		DERPRegionCode: "cn1",
		DERPHostName:   "relay.example.com",
		DERPPort:       443,
	}

	resolved := Resolve(cfg)
	if resolved.DERPRegionID != 901 || resolved.DERPRegionCode != "cn1" || resolved.DERPHostName != "relay.example.com" || resolved.DERPPort != 443 {
		t.Fatalf("unexpected DERP values: %#v", resolved)
	}
	if resolved.TSControlURL != "" {
		t.Fatalf("expected empty ts control url, got %q", resolved.TSControlURL)
	}
	if resolved.HasICEServers() {
		t.Fatalf("expected no ICE servers, got %#v", resolved.ICEServers)
	}
}

func TestResolveEnvironmentOverridesConfig(t *testing.T) {
	t.Setenv("PARTYLAN_DERP_REGION_ID", "902")
	t.Setenv("PARTYLAN_DERP_REGION_CODE", "cn2")
	t.Setenv("PARTYLAN_DERP_HOSTNAME", "103.236.92.32")
	t.Setenv("PARTYLAN_DERP_PORT", "8443")
	t.Setenv("PARTYLAN_TS_CONTROL_URL", "https://control.example.com")
	t.Setenv("PARTYLAN_ICE_SERVERS", " stun:stun.example.com:3478, turn:turn.example.com:3478 ")
	t.Setenv("PARTYLAN_TURN_USERNAME", "user")
	t.Setenv("PARTYLAN_TURN_CREDENTIAL", "secret")

	cfg := &config.Config{
		DERPRegionID:   901,
		DERPRegionCode: "cn1",
		DERPHostName:   "relay.example.com",
		DERPPort:       443,
	}

	resolved := Resolve(cfg)
	if resolved.DERPRegionID != 902 || resolved.DERPRegionCode != "cn2" || resolved.DERPHostName != "103.236.92.32" || resolved.DERPPort != 8443 {
		t.Fatalf("environment did not override DERP values: %#v", resolved)
	}
	if resolved.TSControlURL != "https://control.example.com" {
		t.Fatalf("unexpected ts control url: %q", resolved.TSControlURL)
	}
	if !resolved.HasICEServers() || !resolved.HasTURN() || !resolved.HasTURNCredentials() {
		t.Fatalf("expected ICE/TURN config to be present, got %#v", resolved.ICEServers)
	}
	want := []ICEServer{{
		URLs:       []string{"stun:stun.example.com:3478", "turn:turn.example.com:3478"},
		Username:   "user",
		Credential: "secret",
	}}
	if !reflect.DeepEqual(resolved.ICEServers, want) {
		t.Fatalf("unexpected ICE servers:\n got: %#v\nwant: %#v", resolved.ICEServers, want)
	}
}

func TestMediaHealthLinesWithoutTURN(t *testing.T) {
	resolved := RuntimeConfig{
		ICEServers: []ICEServer{{
			URLs: []string{"stun:stun.example.com:3478"},
		}},
	}

	got := resolved.MediaHealthLines()
	want := []string{
		"media ICE servers configured: groups=1 urls=1 turn=false",
		"media TURN not configured",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected media health:\n got: %#v\nwant: %#v", got, want)
	}
}
