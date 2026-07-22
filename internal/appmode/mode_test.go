package appmode

import "testing"

func TestParseMode(t *testing.T) {
	tests := []struct {
		input string
		want  Mode
		ok    bool
	}{
		{input: "gamecast-client", want: ModeGameCastClient, ok: true},
		{input: "loopback", want: ModeLoopback, ok: true},
		{input: "lan", want: ModeLAN, ok: true},
		{input: "networked", want: ModeNetworked, ok: true},
		{input: "tailscale-system", want: ModeTailscaleSystem, ok: true},
		{input: " NETWORKED ", want: ModeNetworked, ok: true},
		{input: "unknown", want: "", ok: false},
	}

	for _, tt := range tests {
		got, ok := ParseMode(tt.input)
		if ok != tt.ok {
			t.Fatalf("ParseMode(%q) ok=%v want %v", tt.input, ok, tt.ok)
		}
		if got != tt.want {
			t.Fatalf("ParseMode(%q)=%q want %q", tt.input, got, tt.want)
		}
	}
}

func TestResolveModeDefaultsToGameCastClient(t *testing.T) {
	t.Setenv("PARTYLAN_MODE", "")

	if got := ResolveMode(); got != ModeGameCastClient {
		t.Fatalf("ResolveMode()=%q want %q", got, ModeGameCastClient)
	}
}

func TestResolveModeUsesDefaultModeName(t *testing.T) {
	t.Setenv("PARTYLAN_MODE", "")

	old := DefaultModeName
	DefaultModeName = string(ModeNetworked)
	t.Cleanup(func() {
		DefaultModeName = old
	})

	if got := ResolveMode(); got != ModeNetworked {
		t.Fatalf("ResolveMode()=%q want %q", got, ModeNetworked)
	}
}
