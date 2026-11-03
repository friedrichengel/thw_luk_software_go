package db_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
)

// Eine Datenbank der Version 3.1/3.2 (organisationen ohne stamm_key und
// stein_mode, org_credentials mit Uebergangs-Webhook-Secret) muss sich
// oeffnen lassen: Spalten werden nachgezogen, Indizes danach angelegt.
func TestOpenOlderOrgSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alt.sqlite")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE organisationen (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE COLLATE NOCASE, kurz TEXT NOT NULL DEFAULT '',
			ebene TEXT NOT NULL DEFAULT 'ov', created_at TEXT NOT NULL DEFAULT (datetime('now')), updated_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`INSERT INTO organisationen (name, ebene) VALUES ('OV Alt', 'ov')`,
		`CREATE TABLE org_credentials (org_id INTEGER NOT NULL, kind TEXT NOT NULL, value_enc TEXT NOT NULL, hint TEXT NOT NULL DEFAULT '', set_by INTEGER,
			set_at TEXT NOT NULL DEFAULT (datetime('now')), PRIMARY KEY (org_id, kind))`,
		`INSERT INTO org_credentials (org_id, kind, value_enc, hint) VALUES (1, 'stein_webhook:OV Alt', 'enc', '…1234')`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL UNIQUE COLLATE NOCASE, password_hash TEXT NOT NULL,
			email TEXT, display_name TEXT, role TEXT NOT NULL DEFAULT 'user', two_factor_enabled INTEGER NOT NULL DEFAULT 0, two_factor_secret TEXT,
			two_factor_last_counter INTEGER NOT NULL DEFAULT 0, must_change_password INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT (datetime('now')), updated_at TEXT NOT NULL DEFAULT (datetime('now')))`,
	} {
		if _, err := old.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	old.Close()
	d, err := db.Open(path, luk.Schema)
	if err != nil {
		t.Fatalf("open aelteres Schema: %v", err)
	}
	defer d.Close()
	if v, _ := db.QueryStr(d, "SELECT stamm_key FROM organisationen WHERE id = 1"); v != "" {
		t.Fatalf("stamm_key nachgezogen: %q", v)
	}
	if v, _ := db.QueryStr(d, "SELECT rss_feeds || '|' || map_name || '|' || IFNULL(map_lat, 'null') FROM organisationen WHERE id = 1"); v != "||null" {
		t.Fatalf("Einstellungs-Spalten nachgezogen: %q", v)
	}
	if n, _ := db.QueryInt(d, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_organisationen_stamm'"); n != 1 {
		t.Fatal("Index idx_organisationen_stamm fehlt")
	}
	if l, _ := db.QueryStr(d, "SELECT label FROM org_webhook_secrets WHERE org_id = 1"); l != "OV Alt" {
		t.Fatalf("Webhook-Secret nicht uebernommen: %q", l)
	}
	if n, _ := db.QueryInt(d, "SELECT COUNT(*) FROM org_credentials"); n != 0 {
		t.Fatal("Uebergangs-Eintrag nicht entfernt")
	}
}

// Eine Datenbank im alten Format (Unix-Zeitstempel in sessions,
// login_attempts, tfa_attempts) wird beim Oeffnen auf Textzeit umgestellt;
// bestehende Sitzungen bleiben erhalten, Fehlversuche werden verworfen.
func TestMigrateTimeColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alt.sqlite")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, data TEXT NOT NULL DEFAULT '{}', created_at INTEGER NOT NULL, last_seen INTEGER NOT NULL)`,
		`CREATE INDEX idx_sessions_last_seen ON sessions (last_seen)`,
		`INSERT INTO sessions VALUES ('abc', '{"uid":1}', 1761393600, 1761397200)`, // 2025-10-25 12:00 / 13:00 UTC
		`CREATE TABLE login_attempts (id INTEGER PRIMARY KEY AUTOINCREMENT, ip_hash TEXT NOT NULL, username TEXT, created_at INTEGER NOT NULL)`,
		`INSERT INTO login_attempts (ip_hash, username, created_at) VALUES ('x', 'y', 1761397200)`,
		`CREATE TABLE tfa_attempts (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL, created_at INTEGER NOT NULL)`,
	} {
		if _, err := old.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	old.Close()

	d, err := db.Open(path, luk.Schema)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	for _, tc := range []struct{ table, col string }{{"sessions", "last_seen"}, {"sessions", "created_at"}, {"login_attempts", "created_at"}, {"tfa_attempts", "created_at"}} {
		rows, err := db.QueryMaps(d, "PRAGMA table_info("+tc.table+")")
		if err != nil {
			t.Fatal(err)
		}
		typ := ""
		for _, m := range rows {
			if db.Str(m, "name") == tc.col {
				typ = strings.ToUpper(db.Str(m, "type"))
			}
		}
		if typ != "TEXT" {
			t.Errorf("%s.%s: Typ %q, erwartet TEXT", tc.table, tc.col, typ)
		}
	}
	seen, err := db.QueryStr(d, "SELECT last_seen FROM sessions WHERE id = 'abc'")
	if err != nil || seen != "2025-10-25 13:00:00" {
		t.Fatalf("sitzung nicht uebernommen: %q %v", seen, err)
	}
	if n, _ := db.QueryInt(d, "SELECT COUNT(*) FROM login_attempts"); n != 0 {
		t.Fatalf("alte Fehlversuche nicht verworfen: %d", n)
	}
	if n, _ := db.QueryInt(d, "SELECT COUNT(*) FROM sqlite_master WHERE name = 'sessions_old'"); n != 0 {
		t.Fatal("sessions_old nicht aufgeraeumt")
	}
	if n, _ := db.QueryInt(d, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_sessions_last_seen' AND tbl_name = 'sessions'"); n != 1 {
		t.Fatal("Index auf sessions fehlt")
	}
	// zweites Oeffnen: idempotent
	d2, err := db.Open(path, luk.Schema)
	if err != nil {
		t.Fatalf("zweites open: %v", err)
	}
	d2.Close()
	// Zeit-Helfer
	if ts, ok := db.ParseTime(db.Now()); !ok || ts.IsZero() {
		t.Fatal("ParseTime(Now) schlaegt fehl")
	}
	if len(db.Ago(0)) != 19 {
		t.Fatalf("Ago-Format: %q", db.Ago(0))
	}
}
