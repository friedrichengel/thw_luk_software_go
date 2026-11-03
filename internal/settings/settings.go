// Package settings: Server-Einstellungen des globalen Admins in der Tabelle
// settings (Schluessel/Wert). Sie gehen den Umgebungsvariablen vor, damit
// z.B. die Kachelquelle ohne Neustart und ohne Zugriff auf die .env in der
// Oberflaeche gesetzt werden kann.
package settings

import (
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/db"
)

// Schluessel der Einstellungen.
const (
	TileSource = "tile_source" // externe Kachelquelle fuer den eingebauten Cache ("" = OpenStreetMap)
	TilePublic = "tile_public" // "1" = Kachel-Endpunkt ohne Sitzung erreichbar
	TileToken  = "tile_token"  // Token fuer Zugriffe ohne Sitzung
)

// Get liefert einen Wert ("" wenn nicht gesetzt).
func Get(q db.Q, key string) string {
	v, err := db.QueryStr(q, "SELECT value FROM settings WHERE key = ?", key)
	if err != nil {
		return ""
	}
	return v
}

// Set speichert einen Wert ("" loescht den Eintrag, dann gilt wieder die Umgebung).
func Set(q db.Q, key, value string, by int64) error {
	value = strings.TrimSpace(value)
	if value == "" {
		_, err := q.Exec("DELETE FROM settings WHERE key = ?", key)
		return err
	}
	_, err := q.Exec(`INSERT INTO settings (key, value, updated_at, updated_by) VALUES (?, ?, datetime('now'), ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at, updated_by = excluded.updated_by`,
		key, value, db.NullIfZero(by))
	return err
}

// Bool liest einen Schalter ("1", "true", "on").
func Bool(q db.Q, key string) (value bool, set bool) {
	v := strings.ToLower(Get(q, key))
	if v == "" {
		return false, false
	}
	return v == "1" || v == "true" || v == "on" || v == "ja", true
}
