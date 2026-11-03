package server

import (
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/kraefte"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

// newTestServer baut den kompletten Handler (statische Dateien, Seiten,
// APIs, Sitzungen) wie in Produktion, nur mit Temp-Datenbank.
func newTestServer(t *testing.T) (*httptest.Server, *db.DB) {
	t.Helper()
	return newTestServerWith(t, nil)
}

// newTestServerWith erlaubt Anpassungen der Konfiguration (z.B. Mock-URLs
// fuer externe Dienste) vor dem Start.
func newTestServerWith(t *testing.T, adjust func(*config.Config)) (*httptest.Server, *db.DB) {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "t.sqlite"), luk.Schema)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	// Stammdaten: immer die Beispieldatei (LV Musterland), ueber STAMM_FILE wie
	// eine Datei auf dem Rechner, damit die Tests ohne die echte Datei laufen.
	stamm, err := fs.ReadFile(luk.Public, kraefte.StammExample)
	if err != nil {
		t.Fatal(err)
	}
	stammPath := filepath.Join(dir, "stammdaten.json")
	if err := os.WriteFile(stammPath, stamm, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Secret: "test-secret-0123456789abcdef0123456789", Name: "THW LuK", Version: "3.0.1", AssetVersion: "3.0.1.test",
		BaseURL: "http://localhost", CopyrightYear: "2026", DataDir: dir, SessionIdleMin: 60, StammFile: stammPath,
		SteinBaseURL: "http://127.0.0.1:9", DiveraBaseURL: "http://127.0.0.1:9", NinaBaseURL: "http://127.0.0.1:9", LHPBaseURL: "http://127.0.0.1:9",
		RSSAllowedHosts: config.DefaultRSSHosts(), TileURL: "/tiles/{z}/{x}/{y}.png",
		MapCenterLat: 51.1657, MapCenterLng: 10.4515, MapCenterName: "LV Test", MapCenterSet: true,
		TileSource: "http://127.0.0.1:9/{z}/{x}/{y}.png", TileCacheDir: filepath.Join(dir, "tiles"), TileCacheMaxMB: 100,
	}
	if adjust != nil {
		adjust(cfg)
	}
	s, err := New(cfg, d, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	lastServer = s
	srv := httptest.NewServer(s.Handler)
	t.Cleanup(srv.Close)
	return srv, d
}

// lastServer: der zuletzt gebaute Server (fuer Tests, die an den Katalog muessen).
var lastServer *Server

func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
}

var reCSRF = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func loginAs(t *testing.T, c *http.Client, base, username, password string) {
	t.Helper()
	res, err := c.Get(base + "/login")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	m := reCSRF.FindSubmatch(body)
	if m == nil {
		t.Fatalf("kein CSRF-Token in Login-Seite")
	}
	res, err = c.PostForm(base+"/login", url.Values{"csrf": {string(m[1])}, "username": {username}, "password": {password}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 302 {
		t.Fatalf("login %s: %d", username, res.StatusCode)
	}
}

// TestOhneSitzung prueft die oeffentliche Angriffsflaeche: Ohne Login sind
// nur Login-Seiten, Health-Check und echte Assets (CSS/JS/Bilder/Fonts/Geo)
// erreichbar. Stammdaten (public/daten/) und alle APIs brauchen eine Sitzung,
// und die frueheren .php-Pfade existieren nicht mehr.
func TestOhneSitzung(t *testing.T) {
	srv, _ := newTestServer(t)
	c := newClient()
	cases := []struct {
		path     string
		want     int
		location string // Prefix der Umleitung (bei 302)
	}{
		{"/healthz", 200, ""},
		{"/login", 200, ""},
		{"/css/luk.css", 200, ""},
		{"/js/luk_core.js", 200, ""},
		{"/geo/landkreise_simplify20.geojson", 200, ""},
		{"/geo/berlin_bezirke.geojson", 200, ""},
		{"/", 302, "/login"},
		{"/harke", 302, "/login"},
		{"/stein", 302, "/login"},
		{"/steinmap", 302, "/login"},
		{"/divera", 302, "/login"},
		{"/admin", 302, "/login"},
		// Stammdaten nur ueber die API
		{"/daten/stammdaten.json", 404, ""},
		{"/daten/stammdaten.example.json", 404, ""},
		{"/daten/changelog.json", 404, ""},
		{"/daten/", 404, ""},
		// APIs: 401 ohne Sitzung
		{"/api/kraefte?action=stamm", 401, ""},
		{"/api/kraefte?action=katalog&einsatz=00000000-0000-4000-8000-000000000000", 401, ""},
		{"/api/einsatz?action=list", 401, ""},
		{"/api/lagekarten?action=list&einsatz=00000000-0000-4000-8000-000000000000", 401, ""},
		{"/api/schadenskonten?action=list&einsatz=00000000-0000-4000-8000-000000000000", 401, ""},
		{"/api/changelog?action=list", 401, ""},
		{"/api/proxy?type=stein", 401, ""},
		{"/api/proxy?type=nina", 401, ""},
		{"/api/proxy?type=waldbrand", 401, ""},
		{"/api/proxy?type=rss&url=https://www.tagesschau.de/x.xml", 401, ""},
		{"/api/proxy?type=geocode&q=Berlin", 401, ""},
		{"/api/proxy?type=geocode-reverse&lat=52.5&lon=13.4", 401, ""},
		{"/api/proxy?type=route&from=52.5,13.4&to=52.6,13.5", 401, ""},
		{"/api/divera?action=all", 401, ""},
		{"/api/divera?action=vehicles", 401, ""},
		{"/tiles/3/1/2.png", 401, ""},
		{"/api/update?action=status", 401, ""},
		// keine .php-Pfade mehr
		{"/harke.php", 404, ""},
		{"/einsatz.php", 404, ""},
		{"/login.php", 404, ""},
		{"/index.php", 404, ""},
		{"/healthz.php", 404, ""},
		{"/view.php?t=x", 404, ""},
		{"/api/einsatz.php?action=list", 404, ""},
		{"/api/kraefte.php?action=stamm", 404, ""},
		{"/api/proxy.php?type=stein", 404, ""},
	}
	for _, tc := range cases {
		res, err := c.Get(srv.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Errorf("%s: Status %d, erwartet %d (%s)", tc.path, res.StatusCode, tc.want, strings.TrimSpace(string(body)))
			continue
		}
		if tc.location != "" && !strings.HasPrefix(res.Header.Get("Location"), tc.location) {
			t.Errorf("%s: Location %q, erwartet Prefix %q", tc.path, res.Header.Get("Location"), tc.location)
		}
		if tc.want == 401 && !strings.Contains(string(body), `"unauthorized"`) {
			t.Errorf("%s: Body %q, erwartet {error:unauthorized}", tc.path, body)
		}
	}
}

// TestStammdatenNachLogin: die Stammdaten (vormals /daten/stammdaten.json)
// kommen fuer Einsatz-Konten vollstaendig ueber api/kraefte?action=stamm.
func TestStammdatenNachLogin(t *testing.T) {
	srv, d := newTestServer(t)
	if _, err := user.Create(d, "leiter1", "geheim1234", nil, nil, "leiter", false); err != nil {
		t.Fatal(err)
	}
	c := newClient()
	loginAs(t, c, srv.URL, "leiter1", "geheim1234")

	res, err := c.Get(srv.URL + "/api/kraefte?action=stamm")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("stamm: %d", res.StatusCode)
	}
	var out struct {
		OK    bool `json:"ok"`
		Stamm struct {
			Regionen []map[string]any `json:"regionen"`
			OVs      []map[string]any `json:"ovs"`
		} `json:"stamm"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || len(out.Stamm.Regionen) == 0 || len(out.Stamm.OVs) == 0 {
		t.Fatalf("stamm unvollstaendig: ok=%v regionen=%d ovs=%d", out.OK, len(out.Stamm.Regionen), len(out.Stamm.OVs))
	}
	// Auch mit Sitzung bleibt die Datei selbst unerreichbar.
	res2, _ := c.Get(srv.URL + "/daten/stammdaten.json")
	res2.Body.Close()
	if res2.StatusCode != 404 {
		t.Fatalf("/daten/stammdaten.json mit Sitzung: %d", res2.StatusCode)
	}
}
