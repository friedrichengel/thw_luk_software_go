// Package config laedt die Konfiguration aus der Prozessumgebung und einer
// optionalen .env-Datei. Reihenfolge: echte Umgebungsvariablen gewinnen,
// danach die erste gefundene .env (LUK_ENV_FILE, ./config/.env, ./.env,
// <Binary>/config/.env, <Binary>/.env). Die Schluessel entsprechen denen der
// Vorlage config/.env.sample (APP_SECRET, APP_BASE_URL, SMTP_*, ...).
package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Version, BuildStamp und BuildDate werden beim Bauen per -ldflags gesetzt:
//
//	-X github.com/friedrichengel/thw_luk_software_go/internal/config.Version=3.0.0
//	-X github.com/friedrichengel/thw_luk_software_go/internal/config.BuildStamp=abc1234
var (
	Version    = ""
	BuildStamp = ""
	BuildDate  = ""
)

type Config struct {
	EnvFile string // geladene .env (leer = keine)
	DataDir string
	DBPath  string

	Secret  string
	BaseURL string
	Name    string

	MailFrom     string
	MailFromName string
	SMTPHost     string
	SMTPPort     int
	SMTPSecurity string // ssl | tls | none
	SMTPUser     string
	SMTPPass     string

	Version       string
	CopyrightYear string
	AssetVersion  string
	Deployed      string // Build-Stempel fuer die Anzeige

	TrustedProxies []*net.IPNet
	// Kartenmittelpunkt des Servers (MAP_CENTER_*). MapCenterSet sagt, ob die
	// Umgebung ihn gesetzt hat; sonst gilt der Abschnitt "karte" der
	// Stammdaten (kraefte.Katalog.Center) vor dem eingebauten Standard.
	MapCenterLat   float64
	MapCenterLng   float64
	MapCenterName  string
	MapCenterSet   bool
	SyncIntervalMs int
	// StammFile: Pfad einer Stammdaten-Datei auf dem Rechner (STAMM_FILE);
	// leer = eingebettete public/daten/stammdaten.json bzw. die Beispieldatei.
	StammFile string

	// Externe Dienste werden direkt angesprochen (kein Node-Proxy mehr);
	// die Basis-URLs sind nur fuer Tests und Sonderfaelle konfigurierbar.
	SteinBaseURL    string   // https://stein.app/api/api/ext
	DiveraBaseURL   string   // https://app.divera247.com/api/v2
	NinaBaseURL     string   // https://warnung.bund.de/api31
	LHPBaseURL      string   // https://api.hochwasserzentralen.de
	WaldbrandURL    string   // Karte des DWD-Waldbrandgefahrenindex (leer = Standard)
	NominatimURL    string   // https://nominatim.openstreetmap.org (Adresssuche)
	OSRMURL         string   // https://router.project-osrm.org (Routing)
	RSSAllowedHosts []string // Hosts, deren Feeds der RSS-Abruf zulaesst

	// Kacheln: TileURL ist die URL fuer den Browser (Standard: eingebauter
	// Cache /tiles/...), TileSource die Quelle, aus der der eingebaute Cache
	// fehlende Kacheln holt (OSM, Node-Skript oder anderer LuK-Server).
	TileURL        string
	TileSource     string
	TilePublic     bool   // Endpunkt ohne Sitzung erreichbar (fuer andere Installationen)
	TileToken      string // mit TilePublic: ?key= bzw. X-Tile-Key fuer Zugriffe ohne Sitzung
	TileCacheMaxMB int    // Obergrenze des Platten-Caches (0 = unbegrenzt)
	TileCacheDir   string // <DataDir>/tiles

	UpdateDir    string // Docker-Update-Kanal (inbox fuer den Host-Dienst), leer = aus
	UpdateMode   string // docker | binary | "" (aus)
	UpdatePubKey string // minisign-Public-Key (Docker: Anzeige, Binary: Pruefung)
	Image        string
	UpdateMaxMB  int

	ListenAddr     string
	SessionIdleMin int
}

// env liest eine Variable: Prozessumgebung vor .env-Datei.
type env struct {
	file map[string]string
}

func (e *env) get(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	if v, ok := e.file[key]; ok {
		return v
	}
	return def
}

func (e *env) int(key string, def int) int {
	s := strings.TrimSpace(e.get(key, ""))
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func (e *env) float(key string, def float64) float64 {
	s := strings.TrimSpace(e.get(key, ""))
	if s == "" {
		return def
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return f
}

// ParseEnvFile liest KEY=VALUE-Zeilen (Kommentare mit #, Werte optional in
// einfachen oder doppelten Anfuehrungszeichen). Die Compose-Schreibweise aus
// setup.sh ($$ fuer $) wird zurueckgewandelt.
func ParseEnvFile(content string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || !strings.Contains(t, "=") {
			continue
		}
		k, v, _ := strings.Cut(t, "=")
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
			v = v[1 : len(v)-1]
		} else if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
			v = strings.ReplaceAll(v, `\"`, `"`)
			v = strings.ReplaceAll(v, `\\`, `\`)
			v = strings.ReplaceAll(v, "$$", "$")
		}
		out[k] = v
	}
	return out
}

func findEnvFile() string {
	cands := []string{}
	if p := os.Getenv("LUK_ENV_FILE"); p != "" {
		cands = append(cands, p)
	}
	cands = append(cands, filepath.Join("config", ".env"), ".env")
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		cands = append(cands, filepath.Join(dir, "config", ".env"), filepath.Join(dir, ".env"))
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// Load baut die Konfiguration auf. Fehlt APP_SECRET, wird einmalig ein
// Zufallswert erzeugt und in <DataDir>/.app_secret abgelegt - so laeuft die
// Anwendung auch ohne jede Konfiguration (lokaler Rechner).
func Load() (*Config, error) {
	e := &env{file: map[string]string{}}
	envFile := findEnvFile()
	if envFile != "" {
		b, err := os.ReadFile(envFile)
		if err != nil {
			return nil, fmt.Errorf("config: %s: %w", envFile, err)
		}
		e.file = ParseEnvFile(string(b))
	}
	c := &Config{EnvFile: envFile}

	c.DataDir = e.get("DATA_DIR", "")
	if c.DataDir == "" {
		// Datenverzeichnis neben config/ bzw. im Arbeitsverzeichnis.
		if envFile != "" && filepath.Base(filepath.Dir(envFile)) == "config" {
			c.DataDir = filepath.Join(filepath.Dir(filepath.Dir(envFile)), "data")
		} else {
			c.DataDir = "data"
		}
	}
	if abs, err := filepath.Abs(c.DataDir); err == nil {
		c.DataDir = abs
	}
	c.DBPath = e.get("DB_PATH", filepath.Join(c.DataDir, "auth.sqlite"))

	c.Secret = e.get("APP_SECRET", "")
	if c.Secret == "" || c.Secret == "changeme" || strings.HasPrefix(c.Secret, "bitte") {
		sec, err := loadOrCreateSecret(filepath.Join(c.DataDir, ".app_secret"))
		if err != nil {
			return nil, err
		}
		c.Secret = sec
	}
	c.BaseURL = strings.TrimRight(e.get("APP_BASE_URL", "http://localhost:8080"), "/")
	c.Name = e.get("APP_NAME", "THW LuK")

	c.MailFrom = e.get("MAIL_FROM", "no-reply@luk.example.org")
	c.MailFromName = e.get("MAIL_FROM_NAME", "THW LuK Stabsansicht")
	c.SMTPHost = strings.TrimSpace(e.get("SMTP_HOST", ""))
	c.SMTPPort = e.int("SMTP_PORT", 587)
	defSec := "tls"
	if c.SMTPPort == 465 {
		defSec = "ssl"
	}
	c.SMTPSecurity = strings.ToLower(strings.TrimSpace(e.get("SMTP_SECURITY", defSec)))
	c.SMTPUser = e.get("SMTP_USER", "")
	c.SMTPPass = e.get("SMTP_PASS", "")

	c.Version = e.get("APP_VERSION", Version)
	c.CopyrightYear = e.get("APP_COPYRIGHT_YEAR", "2026")
	c.Deployed = strings.TrimSpace(BuildStamp + " " + BuildDate)
	stamp := BuildStamp
	if stamp == "" {
		// Entwicklungsbuild: Aenderungszeit des Binaries als Cache-Buster.
		if exe, err := os.Executable(); err == nil {
			if st, err := os.Stat(exe); err == nil {
				stamp = strconv.FormatInt(st.ModTime().Unix(), 10)
			}
		}
	}
	h := sha256.Sum256([]byte(stamp))
	c.AssetVersion = c.Version + "." + hex.EncodeToString(h[:])[:8]

	for _, entry := range strings.Split(e.get("TRUSTED_PROXIES", ""), ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if !strings.Contains(entry, "/") {
			if ip := net.ParseIP(entry); ip != nil {
				bits := 32
				if ip.To4() == nil {
					bits = 128
				}
				entry = fmt.Sprintf("%s/%d", ip.String(), bits)
			}
		}
		if _, n, err := net.ParseCIDR(entry); err == nil {
			c.TrustedProxies = append(c.TrustedProxies, n)
		}
	}
	c.MapCenterLat = e.float("MAP_CENTER_LAT", DefaultMapCenter.Lat)
	c.MapCenterLng = e.float("MAP_CENTER_LNG", DefaultMapCenter.Lng)
	c.MapCenterName = e.get("MAP_CENTER_NAME", DefaultMapCenter.Name)
	c.MapCenterSet = strings.TrimSpace(e.get("MAP_CENTER_LAT", "")) != "" || strings.TrimSpace(e.get("MAP_CENTER_LNG", "")) != "" ||
		strings.TrimSpace(e.get("MAP_CENTER_NAME", "")) != ""
	c.StammFile = strings.TrimSpace(e.get("STAMM_FILE", ""))
	c.SyncIntervalMs = e.int("SYNC_INTERVAL_MS", 7000)

	c.SteinBaseURL = strings.TrimRight(e.get("STEIN_BASE_URL", "https://stein.app/api/api/ext"), "/")
	c.DiveraBaseURL = strings.TrimRight(e.get("DIVERA_BASE_URL", "https://app.divera247.com/api/v2"), "/")
	c.NinaBaseURL = strings.TrimRight(e.get("NINA_BASE_URL", "https://warnung.bund.de/api31"), "/")
	c.LHPBaseURL = strings.TrimRight(e.get("LHP_BASE_URL", "https://api.hochwasserzentralen.de"), "/")
	c.WaldbrandURL = strings.TrimSpace(e.get("WALDBRAND_URL", ""))
	c.NominatimURL = strings.TrimRight(e.get("NOMINATIM_BASE_URL", "https://nominatim.openstreetmap.org"), "/")
	c.OSRMURL = strings.TrimRight(e.get("OSRM_BASE_URL", "https://router.project-osrm.org"), "/")
	c.TileURL = strings.TrimSpace(e.get("TILE_URL", "/tiles/{z}/{x}/{y}.png"))
	c.TileSource = strings.TrimSpace(e.get("TILE_SOURCE", "https://tile.openstreetmap.org/{z}/{x}/{y}.png"))
	c.TilePublic = truthy(e.get("TILE_PUBLIC", "0"))
	c.TileToken = strings.TrimSpace(e.get("TILE_TOKEN", ""))
	c.TileCacheMaxMB = e.int("TILE_CACHE_MAX_MB", 2000)
	if c.TileCacheMaxMB < 0 {
		c.TileCacheMaxMB = 0
	}
	c.TileCacheDir = filepath.Join(c.DataDir, "tiles")
	c.RSSAllowedHosts = DefaultRSSHosts()
	for _, h := range strings.Split(e.get("RSS_ALLOWED_HOSTS", ""), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			c.RSSAllowedHosts = append(c.RSSAllowedHosts, h)
		}
	}

	c.UpdateDir = strings.TrimRight(e.get("UPDATE_DIR", ""), "/")
	c.UpdateMode = strings.ToLower(strings.TrimSpace(e.get("UPDATE_MODE", "")))
	if c.UpdateMode == "" && c.UpdateDir != "" {
		c.UpdateMode = "docker"
	}
	c.UpdatePubKey = strings.TrimSpace(e.get("UPDATE_PUBKEY", ""))
	c.Image = e.get("APP_IMAGE", "")
	c.UpdateMaxMB = e.int("UPDATE_MAX_MB", 500)
	if c.UpdateMaxMB < 1 {
		c.UpdateMaxMB = 1
	}

	c.ListenAddr = e.get("LISTEN_ADDR", "127.0.0.1:8080")
	c.SessionIdleMin = e.int("SESSION_IDLE_MIN", 720)
	if c.SessionIdleMin < 5 {
		c.SessionIdleMin = 5
	}
	return c, nil
}

func loadOrCreateSecret(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		s := strings.TrimSpace(string(b))
		if len(s) >= 32 {
			return s, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", fmt.Errorf("config: Datenverzeichnis: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	s := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(s+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("config: APP_SECRET konnte nicht abgelegt werden (%s): %w", path, err)
	}
	fmt.Fprintf(os.Stderr, "config: APP_SECRET erzeugt und in %s abgelegt.\n", path)
	return s, nil
}

// truthy liest Schalter aus der Umgebung (1, true, yes, on).
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "ja":
		return true
	}
	return false
}

// TileOrigin liefert Schema und Host einer externen Kachel-URL
// (z.B. "https://luk-lv.example"), "" beim eingebauten Cache.
func (c *Config) TileOrigin() string {
	u := c.TileURL
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return ""
	}
	rest := u[strings.Index(u, "://")+3:]
	if i := strings.IndexAny(rest, "/?"); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		return ""
	}
	return u[:strings.Index(u, "://")+3] + rest
}

// DefaultRSSHosts: Feed-Quellen des Dashboards (rss_reader.js).
func DefaultRSSHosts() []string {
	return []string{"www.tagesschau.de", "gdacs.org", "www.gdacs.org"}
}

// MapCenter: ein Kartenmittelpunkt mit Bezeichnung.
type MapCenter struct {
	Lat  float64
	Lng  float64
	Name string
}

// DefaultMapCenter: eingebauter Standard ohne MAP_CENTER_* und ohne Abschnitt
// "karte" in den Stammdaten (Mitte Deutschlands).
var DefaultMapCenter = MapCenter{Lat: 51.1657, Lng: 10.4515, Name: "THW"}

// MapCenter liefert MAP_CENTER_* des Servers.
func (c *Config) MapCenter() MapCenter {
	return MapCenter{Lat: c.MapCenterLat, Lng: c.MapCenterLng, Name: c.MapCenterName}
}

// RSSHostAllowed prueft, ob ein Feed-Host abgerufen werden darf.
func (c *Config) RSSHostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, h := range c.RSSAllowedHosts {
		if h == host {
			return true
		}
	}
	return false
}

// IsTrustedProxy prueft eine Adresse gegen TRUSTED_PROXIES.
func (c *Config) IsTrustedProxy(ip string) bool {
	p := net.ParseIP(ip)
	if p == nil {
		return false
	}
	for _, n := range c.TrustedProxies {
		if n.Contains(p) {
			return true
		}
	}
	return false
}

// Host liefert den Hostnamen aus APP_BASE_URL (fuer Mails, EHLO).
func (c *Config) Host() string {
	s := c.BaseURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/:"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "localhost"
	}
	return s
}
