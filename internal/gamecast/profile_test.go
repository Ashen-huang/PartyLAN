package gamecast

import "testing"

func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "default", raw: "", want: DefaultBaseURL},
		{name: "adds https", raw: "example.com:5050", want: "https://example.com:5050"},
		{name: "trims slash", raw: "https://example.com:5050/", want: "https://example.com:5050"},
		{name: "rejects ws", raw: "ws://example.com", wantErr: true},
		{name: "rejects missing host", raw: "https://", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeBaseURL(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeBaseURL(%q) succeeded, want error", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeBaseURL(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeBaseURL(%q)=%q want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestMigrateLegacyBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		want     string
		migrated bool
	}{
		{name: "legacy ip", raw: "https://113.142.217.42:5050/", want: DefaultBaseURL, migrated: true},
		{name: "legacy saved node", raw: "85.137.240.159:5050", want: DefaultBaseURL, migrated: true},
		{name: "new domain", raw: "https://partylanapp.com", want: "https://partylanapp.com", migrated: false},
		{name: "custom server", raw: "https://gamecast.example.com:5050", want: "https://gamecast.example.com:5050", migrated: false},
		{name: "invalid", raw: "not a url", want: "not a url", migrated: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, migrated := MigrateLegacyBaseURL(tt.raw)
			if got != tt.want || migrated != tt.migrated {
				t.Fatalf("MigrateLegacyBaseURL(%q)=(%q, %t), want (%q, %t)", tt.raw, got, migrated, tt.want, tt.migrated)
			}
		})
	}
}

func TestWSURL(t *testing.T) {
	got, err := WSURL("https://example.com:5050/", "room 1", "viewer", "h")
	if err != nil {
		t.Fatalf("WSURL: %v", err)
	}
	want := "wss://example.com:5050/sfu-ws?layer=h&role=viewer&room=room+1"
	if got != want {
		t.Fatalf("WSURL=%q want %q", got, want)
	}
}

func TestWSURLHostOmitsViewerLayer(t *testing.T) {
	got, err := WSURL("https://example.com:5050/", "room 1", "host", "")
	if err != nil {
		t.Fatalf("WSURL: %v", err)
	}
	want := "wss://example.com:5050/sfu-ws?role=host&room=room+1"
	if got != want {
		t.Fatalf("WSURL=%q want %q", got, want)
	}
}

func TestWSURLWithName(t *testing.T) {
	got, err := WSURLWithName("https://example.com:5050/", "room 1", "viewer", "f", "Ashen Player")
	if err != nil {
		t.Fatalf("WSURLWithName: %v", err)
	}
	want := "wss://example.com:5050/sfu-ws?layer=f&name=Ashen+Player&role=viewer&room=room+1"
	if got != want {
		t.Fatalf("WSURLWithName=%q want %q", got, want)
	}
}

func TestNormalizeFingerprint(t *testing.T) {
	got := NormalizeFingerprint("SHA256:AA:BB cc-dd")
	if got != "aabbccdd" {
		t.Fatalf("NormalizeFingerprint=%q", got)
	}
	if !EqualFingerprint("AA:BB", "aabb") {
		t.Fatal("expected fingerprints to match")
	}
}
