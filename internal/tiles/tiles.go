// Package tiles: eingebauter Kachel-Cache fuer die Karten (OpenStreetMap).
//
//	GET /tiles/{z}/{x}/{y}.png
//
// Jede Installation liefert ihre Kacheln selbst aus und holt fehlende genau
// einmal bei der konfigurierten Quelle (TILE_SOURCE: OpenStreetMap, ein
// zentraler Kachel-Dienst oder ein anderer LuK-Server). So tritt
// ein Standort mit mehreren Installationen nach aussen nur mit einer Quelle
// auf (OSM Tile Usage Policy). Platten-Cache unter <DATA_DIR>/tiles/z/x/y.png,
// 30 Tage frisch, atomar geschrieben, ein Upstream-Abruf je Kachel
// (Single-Flight), 512 KB je Kachel, 8 s Zeitbudget.
//
// Zugriff: ohne TILE_PUBLIC nur mit gueltiger Sitzung (das Cookie kommt bei
// Bildern gleicher Herkunft automatisch mit) - kein offener Proxy. Mit
// TILE_PUBLIC=1 duerfen andere Installationen den Endpunkt als Quelle
// nutzen (CORS *), optional nur mit ?key=TILE_TOKEN bzw. Header X-Tile-Key.
package tiles

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/upstream"
)

const (
	MaxZoom   = 19
	MaxBytes  = 512 * 1024
	FreshFor  = 30 * 24 * time.Hour
	timeout   = 8 * time.Second
	sweepGap  = 10 * time.Minute
	cacheCtrl = "public, max-age=604800, immutable"
)

type call struct {
	done chan struct{}
	data []byte
	err  error
}

// Service bedient den Kachel-Endpunkt.
type Service struct {
	Cfg *config.Config
	Log *log.Logger
	// IsLoggedIn prueft die Sitzung des Requests (vom Server gesetzt).
	IsLoggedIn func(*http.Request) bool
	// SourceFn, PublicFn, TokenFn liefern die wirksamen Einstellungen
	// (Admin > Server geht der Umgebung vor); nil = Werte aus Cfg.
	SourceFn func() string
	PublicFn func() bool
	TokenFn  func() string

	mu       sync.Mutex
	inflight map[string]*call

	sweepMu   sync.Mutex
	lastSweep time.Time
	limit     int64 // Cache-Obergrenze in Bytes (0 = aus); Tests setzen sie direkt
}

// New baut den Dienst aus der Konfiguration.
func New(cfg *config.Config, logger *log.Logger) *Service {
	if logger == nil {
		logger = log.Default()
	}
	return &Service{Cfg: cfg, Log: logger, inflight: map[string]*call{}, limit: int64(cfg.TileCacheMaxMB) << 20}
}

// Register haengt den Endpunkt an den Mux.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /tiles/{z}/{x}/{y}", s.serve)
}

func fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}

// parse prueft z/x/y wie das Node-Skript: Zoom 0-19, Koordinaten im Bereich.
func parse(zs, xs, ys string) (z, x, y int, ok bool) {
	if !strings.HasSuffix(ys, ".png") {
		return 0, 0, 0, false
	}
	ys = strings.TrimSuffix(ys, ".png")
	var err error
	if z, err = strconv.Atoi(zs); err != nil || z < 0 || z > MaxZoom {
		return 0, 0, 0, false
	}
	if x, err = strconv.Atoi(xs); err != nil {
		return 0, 0, 0, false
	}
	if y, err = strconv.Atoi(ys); err != nil {
		return 0, 0, 0, false
	}
	max := 1 << uint(z)
	if x < 0 || y < 0 || x >= max || y >= max {
		return 0, 0, 0, false
	}
	return z, x, y, true
}

// Source: wirksame Kachelquelle ("" = OpenStreetMap).
func (s *Service) Source() string {
	if s.SourceFn != nil {
		if v := strings.TrimSpace(s.SourceFn()); v != "" {
			return v
		}
	}
	if strings.TrimSpace(s.Cfg.TileSource) != "" {
		return s.Cfg.TileSource
	}
	return DefaultSource
}

// Public: Endpunkt ohne Sitzung erreichbar?
func (s *Service) Public() bool {
	if s.PublicFn != nil {
		return s.PublicFn()
	}
	return s.Cfg.TilePublic
}

// Token: Zugriffstoken fuer Abrufe ohne Sitzung ("" = keines).
func (s *Service) Token() string {
	if s.TokenFn != nil {
		return strings.TrimSpace(s.TokenFn())
	}
	return s.Cfg.TileToken
}

// DefaultSource: OpenStreetMap-Kacheln.
const DefaultSource = "https://tile.openstreetmap.org/{z}/{x}/{y}.png"

func (s *Service) authorized(r *http.Request) (ok bool, status int) {
	if s.IsLoggedIn != nil && s.IsLoggedIn(r) {
		return true, 0
	}
	if !s.Public() {
		return false, 401
	}
	if tok := s.Token(); tok != "" {
		got := r.URL.Query().Get("key")
		if got == "" {
			got = r.Header.Get("X-Tile-Key")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(tok)) != 1 {
			return false, 403
		}
	}
	return true, 0
}

func (s *Service) serve(w http.ResponseWriter, r *http.Request) {
	z, x, y, ok := parse(r.PathValue("z"), r.PathValue("x"), r.PathValue("y"))
	if !ok {
		fail(w, 400, "invalid tile")
		return
	}
	if ok, status := s.authorized(r); !ok {
		if status == 401 {
			fail(w, 401, "unauthorized")
		} else {
			fail(w, 403, "forbidden")
		}
		return
	}
	if s.Public() {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	path := filepath.Join(s.Cfg.TileCacheDir, strconv.Itoa(z), strconv.Itoa(x), strconv.Itoa(y)+".png")
	if st, err := os.Stat(path); err == nil && st.Size() > 0 && time.Since(st.ModTime()) < FreshFor {
		if data, err := os.ReadFile(path); err == nil {
			s.write(w, data, "HIT")
			return
		}
	}
	data, err := s.fetchOnce(r.Context(), z, x, y, path)
	if err != nil {
		// Alte Kachel ist besser als keine.
		if old, rerr := os.ReadFile(path); rerr == nil && len(old) > 0 {
			s.write(w, old, "STALE")
			return
		}
		s.Log.Printf("tiles: %d/%d/%d: %v", z, x, y, err)
		if errors.Is(err, context.DeadlineExceeded) {
			fail(w, 504, "upstream timeout")
			return
		}
		fail(w, 502, "upstream error")
		return
	}
	s.write(w, data, "MISS")
	s.maybeSweep()
}

func (s *Service) write(w http.ResponseWriter, data []byte, xcache string) {
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("Cache-Control", cacheCtrl)
	h.Del("Pragma")
	h.Set("X-Cache", xcache)
	h.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(200)
	w.Write(data)
}

// fetchOnce holt eine Kachel und legt sie im Cache ab; parallele Anfragen
// derselben Kachel warten auf denselben Abruf (nur der Abrufer schreibt).
func (s *Service) fetchOnce(ctx context.Context, z, x, y int, path string) ([]byte, error) {
	key := fmt.Sprintf("%d/%d/%d", z, x, y)
	s.mu.Lock()
	if cl := s.inflight[key]; cl != nil {
		s.mu.Unlock()
		<-cl.done
		return cl.data, cl.err
	}
	cl := &call{done: make(chan struct{})}
	s.inflight[key] = cl
	s.mu.Unlock()

	// Eigener Kontext: der Abruf soll nicht abbrechen, nur weil der erste
	// Browser die Verbindung schliesst, solange andere darauf warten.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	cl.data, cl.err = s.fetch(fctx, z, x, y)
	cancel()
	if cl.err == nil {
		s.store(path, cl.data)
	}

	s.mu.Lock()
	delete(s.inflight, key)
	s.mu.Unlock()
	close(cl.done)
	return cl.data, cl.err
}

// SourceURL setzt z/x/y in die Quell-Vorlage ein.
func (s *Service) SourceURL(z, x, y int) string {
	return expand(s.Source(), z, x, y)
}

func expand(tpl string, z, x, y int) string {
	u := tpl
	u = strings.ReplaceAll(u, "{z}", strconv.Itoa(z))
	u = strings.ReplaceAll(u, "{x}", strconv.Itoa(x))
	u = strings.ReplaceAll(u, "{y}", strconv.Itoa(y))
	return u
}

// UserAgent: OSM verlangt eine Kennung mit Kontaktmoeglichkeit.
func (s *Service) UserAgent() string {
	ver := s.Cfg.Version
	if ver == "" {
		ver = "dev"
	}
	return "THW-LuK/" + ver + " (" + s.Cfg.Host() + "; " + s.Cfg.MailFrom + ")"
}

// ValidSource prueft eine Quell-Vorlage: http(s) und alle drei Platzhalter.
func ValidSource(tpl string) bool {
	tpl = strings.TrimSpace(tpl)
	if !strings.HasPrefix(tpl, "http://") && !strings.HasPrefix(tpl, "https://") {
		return false
	}
	return strings.Contains(tpl, "{z}") && strings.Contains(tpl, "{x}") && strings.Contains(tpl, "{y}")
}

// Check ruft eine Probekachel (Mitteleuropa, Zoom 6) von einer Quell-Vorlage ab und
// liefert einen Ergebnistext - fuer "Testabruf" in Admin > Server.
func (s *Service) Check(ctx context.Context, tpl string) (string, error) {
	tpl = strings.TrimSpace(tpl)
	if tpl == "" {
		tpl = DefaultSource
	}
	if !ValidSource(tpl) {
		return "", fmt.Errorf("Adresse muss mit http(s):// beginnen und {z}, {x} und {y} enthalten")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	data, ctype, status, err := upstream.Do(ctx, upstream.Request{URL: expand(tpl, 6, 34, 21), Headers: s.headers(), MaxBytes: MaxBytes})
	if err != nil {
		if status > 0 {
			return "", fmt.Errorf("Quelle antwortet mit HTTP %d", status)
		}
		return "", fmt.Errorf("Quelle nicht erreichbar: %v", err)
	}
	if len(data) == 0 || (ctype != "" && !strings.HasPrefix(strings.ToLower(ctype), "image/")) {
		return "", fmt.Errorf("Quelle liefert kein Bild (%q)", ctype)
	}
	return fmt.Sprintf("Testabruf OK: Kachel 6/34/21, %s, %d KB in %d ms", ctype, len(data)/1024, time.Since(start).Milliseconds()), nil
}

func (s *Service) headers() map[string]string {
	return map[string]string{
		"User-Agent": s.UserAgent(),
		"Referer":    s.Cfg.BaseURL,
		"Accept":     "image/png,image/*;q=0.9,*/*;q=0.5",
	}
}

func (s *Service) fetch(ctx context.Context, z, x, y int) ([]byte, error) {
	data, ctype, status, err := upstream.Do(ctx, upstream.Request{URL: s.SourceURL(z, x, y), Headers: s.headers(), MaxBytes: MaxBytes})
	if err != nil {
		if status > 0 {
			return nil, fmt.Errorf("upstream status %d", status)
		}
		return nil, err
	}
	if len(data) == 0 || (ctype != "" && !strings.HasPrefix(strings.ToLower(ctype), "image/")) {
		return nil, fmt.Errorf("upstream lieferte kein Bild (%q)", ctype)
	}
	return data, nil
}

// store schreibt die Kachel atomar (tmp + rename).
func (s *Service) store(path string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		s.Log.Printf("tiles: cache dir: %v", err)
		return
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		s.Log.Printf("tiles: cache write: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		s.Log.Printf("tiles: cache rename: %v", err)
	}
}

// maybeSweep prueft hoechstens alle 10 Minuten die Groesse des Platten-Caches
// und loescht bei Ueberschreitung die aeltesten Kacheln (bis 90 % der Grenze).
func (s *Service) maybeSweep() {
	if s.limit <= 0 {
		return
	}
	s.sweepMu.Lock()
	if time.Since(s.lastSweep) < sweepGap {
		s.sweepMu.Unlock()
		return
	}
	s.lastSweep = time.Now()
	s.sweepMu.Unlock()
	go s.Sweep()
}

type fileInfo struct {
	path string
	size int64
	mod  time.Time
}

// Sweep raeumt den Cache auf die konfigurierte Grenze zurueck und liefert
// die Anzahl geloeschter Kacheln.
func (s *Service) Sweep() int {
	if s.limit <= 0 {
		return 0
	}
	var files []fileInfo
	var total int64
	_ = filepath.WalkDir(s.Cfg.TileCacheDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".png") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, fileInfo{p, info.Size(), info.ModTime()})
		total += info.Size()
		return nil
	})
	if total <= s.limit {
		return 0
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	target := s.limit * 9 / 10
	deleted := 0
	for _, f := range files {
		if total <= target {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
			deleted++
		}
	}
	if deleted > 0 {
		s.Log.Printf("tiles: %d alte Kacheln geloescht (Cache jetzt %d MB)", deleted, total>>20)
	}
	return deleted
}
