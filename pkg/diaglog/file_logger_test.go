package diaglog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDefaultLogDirUsesExplicitEnvDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PARTYLAN_LOG_DIR", root)

	got := resolveDefaultLogDir()
	want := filepath.Join(root, "logs")
	if got != want {
		t.Fatalf("resolveDefaultLogDir() = %q, want %q", got, want)
	}
}

func TestResolveFallbackLogDirHasPartyLANLogsSuffix(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	t.Setenv("XDG_CONFIG_HOME", root)

	got := resolveFallbackLogDir()
	wantSuffix := filepath.Join("PartyLAN", "logs")
	if !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("fallback log dir %q does not end with %q", got, wantSuffix)
	}
	if !strings.Contains(got, root) && !strings.Contains(got, os.TempDir()) {
		t.Fatalf("fallback log dir %q should use configured root %q or temp dir", got, root)
	}
}
