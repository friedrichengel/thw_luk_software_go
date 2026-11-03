package httpx

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

func init() {
	_ = mime.AddExtensionType(".geojson", "application/geo+json; charset=utf-8")
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	_ = mime.AddExtensionType(".woff", "font/woff")
	_ = mime.AddExtensionType(".ttf", "font/ttf")
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
	_ = mime.AddExtensionType(".json", "application/json; charset=utf-8")
}

const (
	cacheImmutable = "public, max-age=604800, immutable"  // 7 Tage, wie nginx
	cacheAssets    = "public, max-age=2592000, immutable" // 30 Tage (taktische Zeichen, GeoJSON)
)

// staticHandler liefert Dateien aus einem (eingebetteten) Dateisystem mit
// festem Cache-Header. Verzeichnisse werden nicht gelistet.
func staticHandler(fsys fs.FS, cache string) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		st, err := fs.Stat(fsys, p)
		if err != nil || st.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", cache)
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + p
		fileServer.ServeHTTP(w, r2)
	})
}

// RegisterStatic haengt alle statischen Routen an den Mux: public/ (CSS,
// JS, Bilder, Fonts, Manifest, Service Worker) und die grossen Assets
// (taktische Zeichen, GeoJSON). public/daten/ wird bewusst NICHT ausgeliefert:
// Stammdaten und Changelog gehen nur ueber die sitzungsgeschuetzte API
// (api/kraefte?action=stamm, api/changelog).
func RegisterStatic(mux *http.ServeMux, public fs.FS, assets fs.FS) {
	pub := staticHandler(public, cacheImmutable)
	for _, prefix := range []string{"/css/", "/js/", "/images/", "/fonts/"} {
		mux.Handle("GET "+prefix, pub)
	}
	// Service Worker: nie cachen, Scope auf "/" erlauben.
	mux.HandleFunc("GET /service-worker.js", func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(public, "service-worker.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Write(b)
	})
	mux.HandleFunc("GET /manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(public, "manifest.webmanifest")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Write(b)
	})
	// Taktische Zeichen und GeoJSON: URLs wie auf dem PHP-Server (nginx-Alias).
	if sub, err := fs.Sub(assets, "taktische_zeichen_thumbs"); err == nil {
		mux.Handle("GET /images/taktische_zeichen_thumbs/", http.StripPrefix("/images/taktische_zeichen_thumbs", staticHandler(sub, cacheAssets)))
	}
	if sub, err := fs.Sub(assets, "taktische_zeichen"); err == nil {
		mux.Handle("GET /images/taktische_zeichen/", http.StripPrefix("/images/taktische_zeichen", staticHandler(sub, cacheAssets)))
	}
	if sub, err := fs.Sub(assets, "geo"); err == nil {
		mux.Handle("GET /geo/", http.StripPrefix("/geo", staticHandler(sub, cacheAssets)))
	}
}

// SecurityHeaders setzt die Header der nginx-Konfiguration auf jede Antwort;
// dynamische Antworten bekommen zusaetzlich Cache-Control: no-store (statische
// Handler ueberschreiben das).
func SecurityHeaders(next http.Handler) http.Handler {
	return SecurityHeadersWith("")(next)
}

// SecurityHeadersWith erlaubt zusaetzlich den Ursprung einer externen
// Kachel-URL in der CSP: als Bildquelle (https-Quellen sind ohnehin erlaubt)
// und fuer connect-src, weil die PDF-Erzeugung der Karten (dom-to-image,
// html2canvas) die Kacheln per fetch nachlaedt; ohne den Eintrag bleibt die
// Karte im PDF leer. Kein 'unsafe-eval': eingebettete Bibliotheken muessen
// ohne eval/Function() laden (exceljs.min.js ist dafuer angepasst).
func SecurityHeadersWith(tileOrigin string) func(http.Handler) http.Handler {
	imgSrc := "img-src 'self' data: blob: https:"
	connectSrc := "connect-src 'self'"
	if tileOrigin = strings.TrimSpace(tileOrigin); tileOrigin != "" {
		if !strings.HasPrefix(tileOrigin, "https://") {
			imgSrc += " " + tileOrigin
		}
		connectSrc += " " + tileOrigin
	}
	// script-src ohne 'unsafe-inline': jedes Inline-Script traegt den Nonce des
	// Requests (httpx.Nonce), Event-Handler-Attribute (onclick=...) sind damit
	// ausgeschlossen. style-src bleibt bei 'unsafe-inline' (viele style=-Attribute).
	cspHead := "default-src 'self'; script-src 'self' 'nonce-"
	cspTail := "'; style-src 'self' 'unsafe-inline'; " + imgSrc + "; font-src 'self' data:; " + connectSrc + "; worker-src 'self'; manifest-src 'self'; frame-ancestors 'self'; form-action 'self'; base-uri 'self'; object-src 'none'"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nonce := newNonce()
			r = r.WithContext(context.WithValue(r.Context(), nonceKey{}, nonce))
			h := w.Header()
			h.Set("X-Frame-Options", "SAMEORIGIN")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=(), usb=(), interest-cohort=()")
			h.Set("Content-Security-Policy", cspHead+nonce+cspTail)
			if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			h.Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
			h.Set("Pragma", "no-cache")
			next.ServeHTTP(w, r)
		})
	}
}

type nonceKey struct{}

// newNonce: 16 Zufallsbytes, base64url ohne Padding - der CSP-Nonce eines
// Requests. Kein "+", "/" oder "=": html/template wuerde sie in Attributen
// als Entities schreiben, und der Nonce soll in Header und Attribut gleich sein.
func newNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("csp nonce: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// Nonce liefert den CSP-Nonce des Requests (leer ausserhalb der Middleware).
// Jedes Inline-Script muss ihn als nonce-Attribut tragen, sonst fuehrt der
// Browser es nicht aus.
func Nonce(r *http.Request) string {
	n, _ := r.Context().Value(nonceKey{}).(string)
	return n
}

// NonceAttr: ` nonce="..."` fuer handgebaute <script>-Tags.
func NonceAttr(r *http.Request) string {
	return ` nonce="` + E(Nonce(r)) + `"`
}
