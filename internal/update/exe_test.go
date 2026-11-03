package update

import (
	"strings"
	"testing"
)

// Der Installationspfad wird einmal bestimmt und bleibt stabil; er zeigt nie
// auf eine umbenannte Sicherung (.old/.new).
func TestExecutablePathStable(t *testing.T) {
	a, err := ExecutablePath()
	if err != nil || a == "" {
		t.Fatalf("ExecutablePath: %q %v", a, err)
	}
	b, _ := ExecutablePath()
	if a != b {
		t.Fatalf("Pfad nicht stabil: %q != %q", a, b)
	}
	if strings.HasSuffix(a, ".old") || strings.HasSuffix(a, ".new") {
		t.Fatalf("Pfad zeigt auf Sicherung: %q", a)
	}
}
