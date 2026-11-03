package upstream

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/config"
)

// Feeds buendelt die oeffentlichen Hilfsmittel-Quellen des Dashboards:
// NINA/MOWAS (Warnmeldungen und Warn-Geometrien), Laenderuebergreifendes
// Hochwasserportal (Pegel und Meldungen), RSS-Nachrichten sowie Adresssuche
// und Routing (geo.go). Alle Quellen sind oeffentlich, brauchen keinen
// Schluessel und werden pro Installation gecacht.
type Feeds struct {
	Cfg   *config.Config
	Cache *Cache

	wbxMu           sync.Mutex
	wbxETag         string
	wbxLastModified string

	// Adresssuche/Routing (geo.go): Mindestabstand der Abrufe (0 = GeoMinGap)
	// und je Dienst eine Bremse mit Sperre nach Ablehnung.
	GeoMinGap     time.Duration
	nominatimGate geoGate
	osrmGate      geoGate
}

const (
	ninaTTL   = 60 * time.Second
	ninaStale = time.Hour
	geoTTL    = 10 * time.Minute
	geoStale  = 6 * time.Hour
	lhpTTL    = 60 * time.Second
	lhpStale  = time.Hour
	rssTTL    = 60 * time.Second
	rssStale  = 2 * time.Hour
	// Waldbrandkarte: der DWD aktualisiert einmal taeglich gegen 05:00 UTC,
	// gelegentlich spaeter. Stuendlich bedingt nachfragen (If-None-Match /
	// If-Modified-Since): unveraendert kostet nur ein 304 ohne Bilddaten.
	wbxTTL   = time.Hour
	wbxStale = 48 * time.Hour
)

// WaldbrandURL: Karte des Waldbrandgefahrenindex (WBI) des DWD, stuendlich aktualisiert.
const WaldbrandURL = "https://www.dwd.de/DWD/warnungen/agrar/wbx/wbx_stationen.png"

// Waldbrand liefert die WBI-Karte des DWD als PNG (ueber den Server, damit
// das Dashboard bei Ausfall eine Meldung statt eines leeren Feldes zeigt).
// Liegt bereits ein Bild im Cache, wird bedingt angefragt; antwortet der DWD
// mit 304, bleibt das Bild und gilt wieder eine Stunde.
func (f *Feeds) Waldbrand(ctx context.Context) (Result, error) {
	return f.Cache.Get("waldbrand", wbxTTL, wbxStale, func() ([]byte, string, error) {
		headers := map[string]string{"Accept": "image/png,image/*;q=0.9,*/*;q=0.5"}
		prev, havePrev := f.Cache.Peek("waldbrand")
		f.wbxMu.Lock()
		etag, lastMod := f.wbxETag, f.wbxLastModified
		f.wbxMu.Unlock()
		if havePrev {
			if etag != "" {
				headers["If-None-Match"] = etag
			}
			if lastMod != "" {
				headers["If-Modified-Since"] = lastMod
			}
		}
		body, hdr, status, err := DoHeaders(ctx, Request{URL: f.waldbrandURL(), Headers: headers, MaxBytes: 4 << 20})
		if status == 304 && havePrev {
			return prev.Body, prev.ContentType, nil
		}
		if err != nil {
			return nil, "", err
		}
		if len(body) < 8 || string(body[1:4]) != "PNG" {
			return nil, "", errors.New("waldbrand: Antwort ist kein PNG")
		}
		ctype := hdr.Get("Content-Type")
		if ctype == "" {
			ctype = "image/png"
		}
		f.wbxMu.Lock()
		f.wbxETag, f.wbxLastModified = hdr.Get("ETag"), hdr.Get("Last-Modified")
		f.wbxMu.Unlock()
		return body, ctype, nil
	})
}

// WaldbrandLastModified: Stand des Bildes laut DWD (Last-Modified), "" wenn unbekannt.
func (f *Feeds) WaldbrandLastModified() string {
	f.wbxMu.Lock()
	defer f.wbxMu.Unlock()
	return f.wbxLastModified
}

// waldbrandURL: Tests koennen die Quelle ueber WaldbrandBaseURL in der
// Konfiguration umbiegen (leer = DWD).
func (f *Feeds) waldbrandURL() string {
	if f.Cfg.WaldbrandURL != "" {
		return f.Cfg.WaldbrandURL
	}
	return WaldbrandURL
}

// ErrNotAllowed: Parameter ausserhalb des Erlaubten (400 beim Aufrufer).
var ErrNotAllowed = errors.New("not allowed")

var reWarnID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,120}$`)

// Nina liefert warnung.bund.de/api31/mowas/mapData.json.
func (f *Feeds) Nina(ctx context.Context) (Result, error) {
	u := f.Cfg.NinaBaseURL + "/mowas/mapData.json"
	return f.Cache.Get("nina", ninaTTL, ninaStale, func() ([]byte, string, error) {
		return GetJSON(ctx, u, nil)
	})
}

// NinaGeo liefert die GeoJSON-Geometrie einer Warnung.
func (f *Feeds) NinaGeo(ctx context.Context, id string) (Result, error) {
	id = strings.TrimSpace(id)
	if !reWarnID.MatchString(id) {
		return Result{}, ErrNotAllowed
	}
	u := f.Cfg.NinaBaseURL + "/warnings/" + url.PathEscape(id) + ".geojson"
	return f.Cache.Get("nina-geo:"+id, geoTTL, geoStale, func() ([]byte, string, error) {
		return GetJSON(ctx, u, nil)
	})
}

var lhpEndpoints = map[string]string{
	"alerts":   "/public/v1/data/alerts",
	"stations": "/public/v1/data/stations",
}

// LHP liefert Meldungen (alerts) oder Stationen (stations) des Hochwasserportals
// als GeoJSON (format=geojson: FeatureCollection mit geometry, properties und
// style.color), so wie das Dashboard-Skript es zeichnet. Ohne den Parameter
// liefert die API 1.0 flache Objekte in "data".
func (f *Feeds) LHP(ctx context.Context, kind string) (Result, error) {
	path, ok := lhpEndpoints[strings.ToLower(strings.TrimSpace(kind))]
	if !ok {
		return Result{}, ErrNotAllowed
	}
	// Die API gewichtet den Accept-Header hoeher als format=geojson: mit
	// "application/json" kaeme wieder das flache Format, deshalb geo+json anfordern.
	u := f.Cfg.LHPBaseURL + path + "?format=geojson"
	return f.Cache.Get("lhp:"+kind, lhpTTL, lhpStale, func() ([]byte, string, error) {
		return GetJSON(ctx, u, map[string]string{"Accept": "application/geo+json, application/json;q=0.8"})
	})
}

// fetchFeed holt einen Feed und folgt bis zu drei Weiterleitungen (Sender
// verschieben ihre Feed-Adressen, z.B. MDR index-rss.xml -> index~rss2.xml),
// aber nur auf erlaubte Hosts.
func (f *Feeds) fetchFeed(ctx context.Context, feedURL string, allowed func(host string) bool) ([]byte, error) {
	headers := map[string]string{"Accept": "application/rss+xml, application/atom+xml, application/xml, text/xml, */*"}
	cur := feedURL
	for hop := 0; hop < 4; hop++ {
		raw, _, status, err := Do(ctx, Request{URL: cur, Headers: headers})
		if err == nil {
			return raw, nil
		}
		var se *StatusError
		if !errors.As(err, &se) || (status != 301 && status != 302 && status != 307 && status != 308) {
			return nil, err
		}
		loc := strings.TrimSpace(se.Location)
		if loc == "" {
			return nil, err
		}
		next, perr := url.Parse(cur)
		if perr != nil {
			return nil, err
		}
		target, perr := next.Parse(loc)
		if perr != nil || (target.Scheme != "http" && target.Scheme != "https") || !allowed(target.Hostname()) {
			return nil, ErrNotAllowed
		}
		cur = target.String()
	}
	return nil, fmt.Errorf("%w: zu viele Weiterleitungen", ErrUpstream)
}

// RSS holt einen Feed und liefert ihn als JSON ({status:"ok", feed:{...},
// items:[...]}). Erlaubt sind nur http(s)-URLs auf konfigurierten Hosts
// (RSS_ALLOWED_HOSTS) oder solchen, die extra zusaetzlich zulaesst (eigene
// Feeds der Organisation; nil = keine).
func (f *Feeds) RSS(ctx context.Context, feedURL string, extra func(host string) bool) (Result, error) {
	feedURL = strings.TrimSpace(feedURL)
	pu, err := url.Parse(feedURL)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" || pu.User != nil {
		return Result{}, ErrNotAllowed
	}
	allowed := func(host string) bool {
		return f.Cfg.RSSHostAllowed(host) || (extra != nil && extra(host))
	}
	if !allowed(pu.Hostname()) {
		return Result{}, ErrNotAllowed
	}
	return f.Cache.Get("rss:"+feedURL, rssTTL, rssStale, func() ([]byte, string, error) {
		raw, err := f.fetchFeed(ctx, feedURL, allowed)
		if err != nil {
			return nil, "", err
		}
		out, err := ParseFeedJSON(raw, feedURL)
		if err != nil {
			return nil, "", err
		}
		return out, "application/json; charset=utf-8", nil
	})
}
