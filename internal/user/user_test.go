package user

import (
	"strings"
	"testing"
)

// Bestehende Datenbanken der PHP-Fassung enthalten bcrypt-Hashes mit dem
// Praefix $2y$ (password_hash, PASSWORD_DEFAULT). Die Go-Fassung muss sie
// beim Umstieg (deploy/migrate-from-php.sh) unveraendert akzeptieren.
func TestVerifyPasswordAcceptsPHPHashes(t *testing.T) {
	h, err := HashPassword("Geheim-123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$2a$10$") {
		t.Fatalf("unerwartetes Hash-Format: %s", h)
	}
	php := "$2y$" + strings.TrimPrefix(h, "$2a$")
	if !VerifyPassword(php, "Geheim-123") {
		t.Errorf("PHP-Hash ($2y$) wird nicht akzeptiert: %s", php)
	}
	if VerifyPassword(php, "falsch") {
		t.Error("falsches Passwort wurde gegen $2y$-Hash akzeptiert")
	}
	if VerifyPassword("", "Geheim-123") {
		t.Error("leerer Hash darf nie passen")
	}
}
