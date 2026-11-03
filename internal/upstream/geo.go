package upstream

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Adresssuche (Nominatim) und Routing (OSRM) fuer STEINmap und die Ortswahl
// der Organisation. Beide Dienste sind oeffentlich, verlangen aber Ruecksicht:
// Nominatim erlaubt hoechstens eine Anfrage je Sekunde und einen
// aussagekraeftigen User-Agent mit Kontakt, der OSRM-Demo-Server nur leichte
// Nutzung. Der Browser ruft deshalb nie direkt an (die Content Security
// Policy laesst nur den eigenen Server zu); der Server bremst prozessweit auf
// einen Abruf je Sekunde und Dienst, cacht jede Anfrage und pausiert nach
// einer Ablehnung (429/403) fuer den ganzen Dienst, nicht nur je Schluessel.

const (
	geocodeTTL   = 24 * time.Hour
	geocodeStale = 7 * 24 * time.Hour
	routeTTL     = time.Hour
	routeStale   = 24 * time.Hour
	// GeoMinGap: Mindestabstand zweier Abrufe desselben Dienstes.
	GeoMinGap = time.Second
	// GeoMaxWait: laenger wartet ein Aufrufer nicht in der Warteschlange,
	// danach bekommt er ErrBusy (429 fuer den Browser).
	GeoMaxWait = 8 * time.Second
	// GeoQueryMax: Laenge einer Suchanfrage.
	GeoQueryMax = 200
)

// ErrBusy: der Dienst ist gerade ausgelastet (Warteschlange zu lang oder
// Dienst hat uns vor kurzem abgewiesen); der Aufrufer soll es spaeter erneut
// versuchen.
var ErrBusy = errors.New("upstream busy")

// geoGate bremst alle Abrufe eines Dienstes auf einen je minGap und merkt
// sich eine Sperre nach Ablehnung durch den Dienst.
type geoGate struct {
	mu           sync.Mutex
	next         time.Time // fruehester Zeitpunkt des naechsten Abrufs
	blockedUntil time.Time
	blockErr     error
}

// reserve belegt den naechsten freien Zeitschlitz und liefert, wie lange der
// Aufrufer bis dahin warten muss. Waere die Wartezeit laenger als maxWait,
// bleibt der Schlitz frei und es kommt ErrBusy.
func (g *geoGate) reserve(now time.Time, minGap, maxWait time.Duration) (time.Duration, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now.Before(g.blockedUntil) {
		return 0, g.blockErr
	}
	slot := g.next
	if slot.Before(now) {
		slot = now
	}
	wait := slot.Sub(now)
	if wait > maxWait {
		return 0, ErrBusy
	}
	g.next = slot.Add(minGap)
	return wait, nil
}

// block sperrt den Dienst bis until (Ablehnung durch den Dienst).
func (g *geoGate) block(until time.Time, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blockedUntil = until
	g.blockErr = err
}

// wait wartet bis zum reservierten Zeitschlitz (oder bis ctx endet).
func (f *Feeds) wait(ctx context.Context, g *geoGate) error {
	minGap := f.GeoMinGap
	if minGap <= 0 {
		minGap = GeoMinGap
	}
	d, err := g.reserve(f.Cache.clock(), minGap, GeoMaxWait)
	if err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// geoFetch holt JSON von einem der beiden Dienste: erst den Zeitschlitz
// abwarten, dann abrufen; eine Ablehnung (401/403/429) sperrt den Dienst
// prozessweit, damit die naechste Anfrage ihn nicht sofort wieder trifft.
func (f *Feeds) geoFetch(ctx context.Context, g *geoGate, u string) ([]byte, string, error) {
	if err := f.wait(ctx, g); err != nil {
		return nil, "", err
	}
	body, ctype, _, err := Do(ctx, Request{URL: u, Headers: map[string]string{
		"User-Agent":      f.userAgent(),
		"Accept":          "application/json",
		"Accept-Language": "de",
	}})
	if err != nil {
		var se *StatusError
		if errors.As(err, &se) && IsAuthStatus(se.Status) {
			cooldown := AuthCooldown
			if se.Status == 429 {
				cooldown = RateLimitCooldown
			}
			g.block(f.Cache.clock().Add(cooldown), &AuthError{Status: se.Status})
		}
		return nil, "", err
	}
	if ctype == "" {
		ctype = "application/json; charset=utf-8"
	}
	return body, ctype, nil
}

// userAgent nennt dem Dienst Programm, Version, Host und Kontakt (Pflicht
// nach der Nominatim Usage Policy, dieselbe Form wie beim Kachel-Cache).
func (f *Feeds) userAgent() string {
	ver, host, mail := "dev", "localhost", ""
	if f.Cfg != nil {
		if f.Cfg.Version != "" {
			ver = f.Cfg.Version
		}
		host, mail = f.Cfg.Host(), f.Cfg.MailFrom
	}
	return "THW-LuK/" + ver + " (" + host + "; " + mail + ")"
}

var reCountryCodes = regexp.MustCompile(`^[a-z]{2}(,[a-z]{2})*$`)

// Geocode sucht Adressen (Nominatim /search, format=json: Liste mit lat,
// lon, display_name, ...). limit 1..10, countryCodes optional ("de" oder
// "de,at"). Gleiche Anfragen kommen einen Tag lang aus dem Cache.
func (f *Feeds) Geocode(ctx context.Context, q string, limit int, countryCodes string) (Result, error) {
	q = strings.Join(strings.Fields(q), " ")
	if q == "" || len(q) > GeoQueryMax {
		return Result{}, ErrNotAllowed
	}
	if limit < 1 || limit > 10 {
		return Result{}, ErrNotAllowed
	}
	countryCodes = strings.ToLower(strings.TrimSpace(countryCodes))
	if countryCodes != "" && !reCountryCodes.MatchString(countryCodes) {
		return Result{}, ErrNotAllowed
	}
	v := url.Values{}
	v.Set("format", "json")
	v.Set("limit", strconv.Itoa(limit))
	v.Set("accept-language", "de")
	v.Set("q", q)
	if countryCodes != "" {
		v.Set("countrycodes", countryCodes)
	}
	u := f.nominatimURL() + "/search?" + v.Encode()
	key := fmt.Sprintf("geocode:%d:%s:%s", limit, countryCodes, strings.ToLower(q))
	return f.Cache.Get(key, geocodeTTL, geocodeStale, func() ([]byte, string, error) {
		return f.geoFetch(ctx, &f.nominatimGate, u)
	})
}

// Reverse liefert die Adresse zu einer Koordinate (Nominatim /reverse,
// format=json: Objekt mit display_name, address, ...).
func (f *Feeds) Reverse(ctx context.Context, lat, lon float64) (Result, error) {
	if !validLatLon(lat, lon) {
		return Result{}, ErrNotAllowed
	}
	// auf ~1 m runden, damit nahe Klicks denselben Cache-Eintrag treffen
	latS, lonS := strconv.FormatFloat(lat, 'f', 5, 64), strconv.FormatFloat(lon, 'f', 5, 64)
	v := url.Values{}
	v.Set("format", "json")
	v.Set("accept-language", "de")
	v.Set("lat", latS)
	v.Set("lon", lonS)
	u := f.nominatimURL() + "/reverse?" + v.Encode()
	return f.Cache.Get("geocode-reverse:"+latS+","+lonS, geocodeTTL, geocodeStale, func() ([]byte, string, error) {
		return f.geoFetch(ctx, &f.nominatimGate, u)
	})
}

// Route liefert die Fahrtroute zwischen zwei Punkten (OSRM /route/v1/driving,
// overview=false: routes[0].distance in m, .duration in s).
func (f *Feeds) Route(ctx context.Context, fromLat, fromLon, toLat, toLon float64) (Result, error) {
	if !validLatLon(fromLat, fromLon) || !validLatLon(toLat, toLon) {
		return Result{}, ErrNotAllowed
	}
	coord := func(lat, lon float64) string {
		return strconv.FormatFloat(lon, 'f', 5, 64) + "," + strconv.FormatFloat(lat, 'f', 5, 64)
	}
	pair := coord(fromLat, fromLon) + ";" + coord(toLat, toLon)
	u := f.osrmURL() + "/route/v1/driving/" + pair + "?overview=false"
	return f.Cache.Get("route:"+pair, routeTTL, routeStale, func() ([]byte, string, error) {
		return f.geoFetch(ctx, &f.osrmGate, u)
	})
}

func validLatLon(lat, lon float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lon) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}

// ParseLatLon liest "lat,lon" (Dezimalpunkt) aus einem Query-Parameter.
func ParseLatLon(s string) (lat, lon float64, ok bool) {
	parts := strings.Split(strings.TrimSpace(s), ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lon, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil || !validLatLon(lat, lon) {
		return 0, 0, false
	}
	return lat, lon, true
}

func (f *Feeds) nominatimURL() string {
	if f.Cfg != nil && f.Cfg.NominatimURL != "" {
		return f.Cfg.NominatimURL
	}
	return "https://nominatim.openstreetmap.org"
}

func (f *Feeds) osrmURL() string {
	if f.Cfg != nil && f.Cfg.OSRMURL != "" {
		return f.Cfg.OSRMURL
	}
	return "https://router.project-osrm.org"
}
