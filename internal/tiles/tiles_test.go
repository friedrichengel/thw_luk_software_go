package tiles

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/config"
)

// png: minimaler gueltiger Bildinhalt (nur Signatur, reicht fuer den Cache).
var png = append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("tile-data")...)

func newService(t *testing.T, upstreamURL string, public bool, token string) (*Service, *http.ServeMux) {
	t.Helper()
	cfg := &config.Config{
		Version: "test", BaseURL: "http://luk.test", MailFrom: "kontakt@example.org",
		TileSource: upstreamURL + "/{z}/{x}/{y}.png", TileCacheDir: filepath.Join(t.TempDir(), "tiles"),
		TilePublic: public, TileToken: token, TileCacheMaxMB: 2000,
	}
	s := New(cfg, log.New(io.Discard, "", 0))
	s.IsLoggedIn = func(r *http.Request) bool { return r.Header.Get("X-Test-Login") == "1" }
	mux := http.NewServeMux()
	s.Register(mux)
	return s, mux
}

func get(mux *http.ServeMux, path string, loggedIn bool, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	if loggedIn {
		req.Header.Set("X-Test-Login", "1")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestTiles(t *testing.T) {
	var hits int32
	var slow sync.WaitGroup
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		switch {
		case strings.HasPrefix(r.URL.Path, "/5/9/9"):
			http.NotFound(w, r)
			return
		case strings.HasPrefix(r.URL.Path, "/5/8/8"):
			w.Header().Set("Content-Type", "image/png")
			w.Write(make([]byte, MaxBytes+1))
			return
		case strings.HasPrefix(r.URL.Path, "/5/7/7"):
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<html>blocked</html>")
			return
		case strings.HasPrefix(r.URL.Path, "/6/"):
			slow.Wait() // Single-Flight: alle warten auf diesen einen Abruf
		}
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "THW-LuK/test (luk.test; kontakt@example.org)") {
			t.Errorf("User-Agent: %q", ua)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	}))
	defer up.Close()
	s, mux := newService(t, up.URL, false, "")

	// Ohne Sitzung: 401, kein Upstream-Abruf
	if rec := get(mux, "/tiles/3/1/2.png", false, nil); rec.Code != 401 || !strings.Contains(rec.Body.String(), "unauthorized") {
		t.Fatalf("ohne Sitzung: %d %s", rec.Code, rec.Body.String())
	}
	if hits != 0 {
		t.Fatal("Abruf ohne Sitzung")
	}
	// Ungueltige Koordinaten
	for _, p := range []string{"/tiles/20/0/0.png", "/tiles/3/8/0.png", "/tiles/3/0/-1.png", "/tiles/3/1/2.jpg", "/tiles/x/1/2.png"} {
		if rec := get(mux, p, true, nil); rec.Code != 400 {
			t.Errorf("%s: %d, erwartet 400", p, rec.Code)
		}
	}
	// MISS, dann HIT aus dem Platten-Cache
	rec := get(mux, "/tiles/3/1/2.png", true, nil)
	if rec.Code != 200 || rec.Header().Get("X-Cache") != "MISS" || rec.Header().Get("Content-Type") != "image/png" ||
		rec.Header().Get("Cache-Control") != cacheCtrl || rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Body.String() != string(png) {
		t.Fatalf("miss: %d %v", rec.Code, rec.Header())
	}
	if _, err := os.Stat(filepath.Join(s.Cfg.TileCacheDir, "3", "1", "2.png")); err != nil {
		t.Fatalf("cache-datei fehlt: %v", err)
	}
	rec = get(mux, "/tiles/3/1/2.png", true, nil)
	if rec.Code != 200 || rec.Header().Get("X-Cache") != "HIT" || hits != 1 {
		t.Fatalf("hit: %d %s hits=%d", rec.Code, rec.Header().Get("X-Cache"), hits)
	}
	// Upstream 404 -> 502; zu gross -> 502; kein Bild -> 502
	for _, p := range []string{"/tiles/5/9/9.png", "/tiles/5/8/8.png", "/tiles/5/7/7.png"} {
		if rec := get(mux, p, true, nil); rec.Code != 502 {
			t.Errorf("%s: %d, erwartet 502", p, rec.Code)
		}
	}
	// Single-Flight: 8 parallele Anfragen derselben Kachel -> 1 Upstream-Abruf
	before := atomic.LoadInt32(&hits)
	slow.Add(1)
	var wg sync.WaitGroup
	results := make([]int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = get(mux, "/tiles/6/1/1.png", true, nil).Code
		}(i)
	}
	time.Sleep(100 * time.Millisecond)
	slow.Done()
	wg.Wait()
	for i, c := range results {
		if c != 200 {
			t.Errorf("parallel #%d: %d", i, c)
		}
	}
	if n := atomic.LoadInt32(&hits) - before; n != 1 {
		t.Fatalf("single-flight: %d Upstream-Abrufe, erwartet 1", n)
	}
	// Veraltete Kachel: wird neu geholt; schlaegt das fehl, kommt die alte (STALE)
	old := filepath.Join(s.Cfg.TileCacheDir, "5", "9", "9.png")
	os.MkdirAll(filepath.Dir(old), 0o750)
	os.WriteFile(old, png, 0o640)
	past := time.Now().Add(-40 * 24 * time.Hour)
	os.Chtimes(old, past, past)
	if rec := get(mux, "/tiles/5/9/9.png", true, nil); rec.Code != 200 || rec.Header().Get("X-Cache") != "STALE" {
		t.Fatalf("stale: %d %s", rec.Code, rec.Header().Get("X-Cache"))
	}
	// Aufraeumen: Grenze unterschreiten, aelteste zuerst
	s.limit = int64(len(png)) * 2
	if _, err := os.Stat(filepath.Join(s.Cfg.TileCacheDir, "6", "1", "1.png")); err != nil {
		t.Fatalf("single-flight-kachel nicht gespeichert: %v", err)
	}
	deleted := s.Sweep()
	if deleted == 0 {
		t.Fatal("sweep hat nichts geloescht")
	}
	if _, err := os.Stat(old); err == nil {
		t.Fatal("aelteste Kachel sollte zuerst geloescht werden")
	}
}

func TestTilesPublic(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	}))
	defer up.Close()
	// oeffentlich ohne Token
	_, mux := newService(t, up.URL, true, "")
	if rec := get(mux, "/tiles/2/1/1.png", false, nil); rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("public: %d %v", rec.Code, rec.Header())
	}
	// oeffentlich mit Token
	_, mux = newService(t, up.URL, true, "geheim-token")
	if rec := get(mux, "/tiles/2/1/1.png", false, nil); rec.Code != 403 {
		t.Fatalf("token fehlt: %d", rec.Code)
	}
	if rec := get(mux, "/tiles/2/1/1.png?key=falsch", false, nil); rec.Code != 403 {
		t.Fatalf("token falsch: %d", rec.Code)
	}
	if rec := get(mux, "/tiles/2/1/1.png?key=geheim-token", false, nil); rec.Code != 200 {
		t.Fatalf("token query: %d", rec.Code)
	}
	if rec := get(mux, "/tiles/2/1/1.png", false, map[string]string{"X-Tile-Key": "geheim-token"}); rec.Code != 200 {
		t.Fatalf("token header: %d", rec.Code)
	}
	// angemeldete Nutzer brauchen nie einen Token
	if rec := get(mux, "/tiles/2/1/1.png", true, nil); rec.Code != 200 {
		t.Fatalf("eingeloggt ohne token: %d", rec.Code)
	}
}

// Check (Testabruf) und die Aufloesung der Einstellungen (Admin vor Umgebung).
func TestCheckAndResolvers(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/html/") {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<html>")
			return
		}
		if r.URL.Query().Get("key") != "k1" {
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	}))
	defer up.Close()
	s, mux := newService(t, up.URL, false, "")
	ctx := context.Background()
	if msg, err := s.Check(ctx, up.URL+"/{z}/{x}/{y}.png?key=k1"); err != nil || !strings.Contains(msg, "Testabruf OK") {
		t.Fatalf("check ok: %v %q", err, msg)
	}
	if _, err := s.Check(ctx, up.URL+"/{z}/{x}/{y}.png?key=falsch"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("check 403: %v", err)
	}
	if _, err := s.Check(ctx, up.URL+"/html/{z}/{x}/{y}.png?key=k1"); err == nil || !strings.Contains(err.Error(), "kein Bild") {
		t.Fatalf("check html: %v", err)
	}
	for _, bad := range []string{"ftp://x/{z}/{x}/{y}.png", "https://x.example/tiles/", "x"} {
		if _, err := s.Check(ctx, bad); err == nil {
			t.Fatalf("ungueltige Vorlage akzeptiert: %q", bad)
		}
	}
	// Aufloesung: Admin-Einstellung vor Umgebung, leer = Umgebung, beides leer = OSM
	s.SourceFn = func() string { return "" }
	if s.Source() != s.Cfg.TileSource {
		t.Fatal("leere Admin-Quelle muss auf die Umgebung zurueckfallen")
	}
	s.SourceFn = func() string { return "https://zentral.example/{z}/{x}/{y}.png" }
	if s.Source() != "https://zentral.example/{z}/{x}/{y}.png" {
		t.Fatal("Admin-Quelle muss vorgehen")
	}
	s.SourceFn = nil
	s.Cfg.TileSource = ""
	if s.Source() != DefaultSource {
		t.Fatal("ohne alles: OpenStreetMap")
	}
	s.PublicFn = func() bool { return true }
	s.TokenFn = func() string { return "tok" }
	if rec := get(mux, "/tiles/2/1/1.png?key=tok", false, nil); rec.Code == 401 || rec.Code == 403 {
		t.Fatalf("Admin-Freigabe mit Token: %d", rec.Code)
	}
}

func TestTileOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"/tiles/{z}/{x}/{y}.png":                                 "",
		"https://tiles.example.org/tiles/{z}/{x}/{y}.png":        "https://tiles.example.org",
		"http://10.0.0.5:8080/tiles/{z}/{x}/{y}.png?key=abc":     "http://10.0.0.5:8080",
		"https://luk-lv.example/tiles/{z}/{x}/{y}.png?key=TOKEN": "https://luk-lv.example",
	} {
		c := &config.Config{TileURL: in}
		if got := c.TileOrigin(); got != want {
			t.Errorf("TileOrigin(%q) = %q, erwartet %q", in, got, want)
		}
	}
}
