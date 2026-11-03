// Package db oeffnet die SQLite-Datenbank (WAL, Fremdschluessel, Busy-
// Timeout), legt das Schema idempotent an, zieht Spalten nach und bietet
// kleine Helfer fuer Zeilen als map[string]any - die Repositories arbeiten
// damit wie die PHP-Fassung mit PDO::FETCH_ASSOC.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Q ist die gemeinsame Schnittstelle von *DB und *sql.Tx. Alle Repository-
// Funktionen nehmen ein Q, damit sie innerhalb und ausserhalb einer
// Transaktion laufen koennen.
type Q interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

type DB struct {
	*sql.DB
	Path string
}

// Open oeffnet (oder erzeugt) die Datenbank und fuehrt Schema + Migrationen aus.
func Open(path string, schema string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("db: Verzeichnis: %w", err)
	}
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}
	sdb.SetMaxOpenConns(8)
	sdb.SetConnMaxLifetime(0)
	d := &DB{DB: sdb, Path: path}
	if err := d.Ping(); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("db: %w", err)
	}
	if err := d.bootstrap(schema); err != nil {
		sdb.Close()
		return nil, err
	}
	return d, nil
}

// bootstrap fuehrt das Schema (nur CREATE ... IF NOT EXISTS) bei jedem Start
// aus und ergaenzt Spalten, die nach dem ersten Release dazukamen.
func (d *DB) bootstrap(schema string) error {
	if err := d.migrateTimeColumns(); err != nil {
		return err
	}
	if _, err := d.Exec(schema); err != nil {
		return fmt.Errorf("db: schema: %w", err)
	}
	if err := d.restoreSessions(); err != nil {
		return err
	}
	// Webhook-Secrets aus der Uebergangsform "stein_webhook:<Label>" in
	// org_credentials in die eigene Tabelle uebernehmen.
	if _, err := d.Exec(`INSERT OR IGNORE INTO org_webhook_secrets (org_id, label, value_enc, hint, set_by, set_at)
		SELECT org_id, substr(kind, 15), value_enc, hint, set_by, set_at FROM org_credentials WHERE kind LIKE 'stein_webhook:%'`); err != nil {
		return fmt.Errorf("db: webhook secrets: %w", err)
	}
	if _, err := d.Exec("DELETE FROM org_credentials WHERE kind LIKE 'stein_webhook:%'"); err != nil {
		return fmt.Errorf("db: webhook secrets: %w", err)
	}
	cols := []struct{ table, col, def string }{
		{"einsaetze", "vorwarn_min", "INTEGER NOT NULL DEFAULT 180"},
		{"einsatz_schichten", "status", "TEXT NOT NULL DEFAULT 'geplant'"},
		{"einsatz_schichten", "status_at", "TEXT"},
		{"einsatz_nodes", "kraft_key", "TEXT NOT NULL DEFAULT ''"},
		{"einsatz_mitglieder", "kraft_key", "TEXT NOT NULL DEFAULT ''"},
		{"einsatz_schichten", "kraft_key", "TEXT NOT NULL DEFAULT ''"},
		{"einsaetze", "bereich_key", "TEXT NOT NULL DEFAULT ''"},
		{"einsaetze", "ort_name", "TEXT NOT NULL DEFAULT ''"},
		{"einsaetze", "ort_lat", "REAL"},
		{"einsaetze", "ort_lng", "REAL"},
		{"einsaetze", "stein_aktiv", "INTEGER NOT NULL DEFAULT 1"},
		{"users", "changelog_seen", "TEXT NOT NULL DEFAULT ''"},
		{"users", "org_id", "INTEGER REFERENCES organisationen (id) ON DELETE SET NULL"},
		{"users", "org_admin", "INTEGER NOT NULL DEFAULT 0"},
		{"users", "divera_access", "TEXT NOT NULL DEFAULT 'off'"},
		{"einsaetze", "org_id", "INTEGER REFERENCES organisationen (id) ON DELETE SET NULL"},
		{"organisationen", "stein_mode", "TEXT NOT NULL DEFAULT 'poll'"},
		{"organisationen", "stamm_key", "TEXT NOT NULL DEFAULT ''"},
		{"organisationen", "divera_hidden_groups", "TEXT NOT NULL DEFAULT '[]'"},
		{"organisationen", "map_lat", "REAL"},
		{"organisationen", "map_lng", "REAL"},
		{"organisationen", "map_name", "TEXT NOT NULL DEFAULT ''"},
		{"organisationen", "rss_feeds", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, c := range cols {
		if err := d.addColumn(c.table, c.col, c.def); err != nil {
			return err
		}
	}
	// Indizes auf nachgezogenen Spalten erst jetzt: im Schema wuerden sie bei
	// einer Datenbank der PHP-Fassung (ohne org_id) am fehlenden Feld scheitern.
	for _, idx := range []string{
		"CREATE INDEX IF NOT EXISTS idx_users_org ON users (org_id)",
		"CREATE INDEX IF NOT EXISTS idx_einsaetze_org ON einsaetze (org_id)",
		"CREATE INDEX IF NOT EXISTS idx_organisationen_stamm ON organisationen (stamm_key)",
	} {
		if _, err := d.Exec(idx); err != nil {
			return fmt.Errorf("db: index: %w", err)
		}
	}
	return nil
}

// columnType liefert den deklarierten Typ einer Spalte ("" = Tabelle/Spalte fehlt).
func (d *DB) columnType(table, col string) (string, error) {
	maps, err := QueryMaps(d, "PRAGMA table_info("+table+")")
	if err != nil {
		return "", fmt.Errorf("db: table_info %s: %w", table, err)
	}
	for _, m := range maps {
		if Str(m, "name") == col {
			return strings.ToUpper(Str(m, "type")), nil
		}
	}
	return "", nil
}

// migrateTimeColumns stellt die drei Tabellen mit Unix-Zeitstempeln aus der
// PHP-Zeit (login_attempts, tfa_attempts, sessions) auf das einheitliche
// Textformat datetime('now') um. Laeuft VOR dem Schema: die fluechtigen
// Fehlversuch-Tabellen werden einfach neu angelegt, die Sitzungen zur Seite
// gestellt und von restoreSessions nach dem Schema konvertiert uebernommen.
func (d *DB) migrateTimeColumns() error {
	for _, t := range []string{"login_attempts", "tfa_attempts"} {
		typ, err := d.columnType(t, "created_at")
		if err != nil {
			return err
		}
		if typ == "INTEGER" {
			if _, err := d.Exec("DROP TABLE " + t); err != nil {
				return fmt.Errorf("db: migrate %s: %w", t, err)
			}
		}
	}
	typ, err := d.columnType("sessions", "last_seen")
	if err != nil {
		return err
	}
	if typ == "INTEGER" {
		for _, stmt := range []string{
			"DROP TABLE IF EXISTS sessions_old",
			"DROP INDEX IF EXISTS idx_sessions_last_seen",
			"ALTER TABLE sessions RENAME TO sessions_old",
		} {
			if _, err := d.Exec(stmt); err != nil {
				return fmt.Errorf("db: migrate sessions: %w", err)
			}
		}
	}
	return nil
}

// restoreSessions uebernimmt zur Seite gestellte Sitzungen (Unix-Zeit) in die
// neue Tabelle (Textzeit), damit Anmeldungen die Umstellung ueberleben.
func (d *DB) restoreSessions() error {
	typ, err := d.columnType("sessions_old", "last_seen")
	if err != nil || typ == "" {
		return err
	}
	for _, stmt := range []string{
		`INSERT OR IGNORE INTO sessions (id, data, created_at, last_seen)
			SELECT id, data, datetime(created_at, 'unixepoch'), datetime(last_seen, 'unixepoch') FROM sessions_old`,
		"DROP TABLE sessions_old",
	} {
		if _, err := d.Exec(stmt); err != nil {
			return fmt.Errorf("db: restore sessions: %w", err)
		}
	}
	return nil
}

// Ago liefert den Zeitstempel "vor d" im Format der Datenbank.
func Ago(dur time.Duration) string {
	return time.Now().Add(-dur).UTC().Format("2006-01-02 15:04:05")
}

// ParseTime liest einen Zeitstempel im Format der Datenbank (UTC).
func ParseTime(s string) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(s), time.UTC)
	return t, err == nil
}

func (d *DB) addColumn(table, col, def string) error {
	maps, err := QueryMaps(d, "PRAGMA table_info("+table+")")
	if err != nil {
		return fmt.Errorf("db: table_info %s: %w", table, err)
	}
	for _, m := range maps {
		if Str(m, "name") == col {
			return nil
		}
	}
	if _, err := d.Exec("ALTER TABLE " + table + " ADD COLUMN " + col + " " + def); err != nil {
		return fmt.Errorf("db: alter %s.%s: %w", table, col, err)
	}
	return nil
}

// Tx fuehrt fn in einer Transaktion (BEGIN IMMEDIATE) aus; Fehler rollen zurueck.
func (d *DB) Tx(fn func(q Q) error) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}

// InTx fuehrt fn in der bestehenden Transaktion aus, wenn q bereits eine ist
// (verschachtelte Aufrufe wie mitgliedToNode -> createNode), sonst in einer neuen.
func InTx(q Q, fn func(q Q) error) error {
	switch v := q.(type) {
	case *sql.Tx:
		return fn(v)
	case *DB:
		return v.Tx(fn)
	default:
		return fn(q)
	}
}

// Now liefert den Zeitstempel im Format der Datenbank (UTC, wie datetime(now)).
func Now() string {
	return time.Now().UTC().Format("2006-01-02 15:04:05")
}

// ---------------------------------------------------------------------------
// Zeilen als map[string]any

func scanMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			v := vals[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			m[c] = v
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// QueryMaps liefert alle Zeilen als Maps (Spaltenname => Wert).
func QueryMaps(q Q, query string, args ...any) ([]map[string]any, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("db: %w (%s)", err, strings.SplitN(query, "\n", 2)[0])
	}
	defer rows.Close()
	return scanMaps(rows)
}

// QueryMap liefert die erste Zeile oder nil.
func QueryMap(q Q, query string, args ...any) (map[string]any, error) {
	rows, err := QueryMaps(q, query, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// QueryInt liefert den ersten Spaltenwert der ersten Zeile als int64 (0 bei NULL).
func QueryInt(q Q, query string, args ...any) (int64, error) {
	var v sql.NullInt64
	if err := q.QueryRow(query, args...).Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return v.Int64, nil
}

// QueryStr liefert den ersten Spaltenwert der ersten Zeile als string.
func QueryStr(q Q, query string, args ...any) (string, error) {
	var v sql.NullString
	if err := q.QueryRow(query, args...).Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return v.String, nil
}

// LastID liefert die zuletzt vergebene AUTOINCREMENT-ID.
func LastID(res sql.Result) int64 {
	id, _ := res.LastInsertId()
	return id
}

// Affected liefert die Zahl geaenderter Zeilen.
func Affected(res sql.Result) int64 {
	n, _ := res.RowsAffected()
	return n
}

// Str liest einen Textwert (NULL -> "").
func Str(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	case int64:
		return fmt.Sprintf("%d", v)
	case float64:
		return fmt.Sprintf("%v", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// StrPtr liest einen nullbaren Textwert.
func StrPtr(m map[string]any, k string) *string {
	if m[k] == nil {
		return nil
	}
	s := Str(m, k)
	return &s
}

// Int liest einen Ganzzahlwert (NULL -> 0, Text wird geparst).
func Int(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case nil:
		return 0
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case string:
		var n int64
		fmt.Sscanf(v, "%d", &n)
		return n
	default:
		return 0
	}
}

// IntPtr liest einen nullbaren Ganzzahlwert.
func IntPtr(m map[string]any, k string) *int64 {
	if m[k] == nil {
		return nil
	}
	n := Int(m, k)
	return &n
}

// Float liest einen Gleitkommawert (NULL -> 0).
func Float(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case nil:
		return 0
	case float64:
		return v
	case int64:
		return float64(v)
	case string:
		var f float64
		fmt.Sscanf(v, "%g", &f)
		return f
	default:
		return 0
	}
}

// FloatPtr liest einen nullbaren Gleitkommawert.
func FloatPtr(m map[string]any, k string) *float64 {
	if m[k] == nil {
		return nil
	}
	f := Float(m, k)
	return &f
}

// IsNull meldet, ob die Spalte NULL ist.
func IsNull(m map[string]any, k string) bool {
	return m[k] == nil
}

// NullIfZero liefert nil fuer 0 (fuer created_by/updated_by bei uid 0).
func NullIfZero(uid int64) any {
	if uid > 0 {
		return uid
	}
	return nil
}

// Placeholders liefert "?, ?, ?" fuer n Werte.
func Placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?, ", n-1) + "?"
}
