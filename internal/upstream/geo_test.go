package upstream

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/config"
)

// Adresssuche, Rueckwaertssuche und Routing laufen ueber den Server: User-Agent
// mit Kontakt, Parameterpruefung, Cache je Anfrage und Sperre nach 429.
func TestGeo(t *testing.T) {
	var calls int32
	var lastUA, lastPath, lastQuery string
	var status int32 = 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		lastUA, lastPath, lastQuery = r.Header.Get("User-Agent"), r.URL.Path, r.URL.RawQuery
		if st := atomic.LoadInt32(&status); st != 200 {
			w.WriteHeader(int(st))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch {
		case r.URL.Path == "/search":
			w.Write([]byte(`[{"lat":"52.5","lon":"13.4","display_name":"Berlin"}]`))
		case r.URL.Path == "/reverse":
			w.Write([]byte(`{"display_name":"Stresemannstr. 15, Berlin"}`))
		case strings.HasPrefix(r.URL.Path, "/route/v1/driving/"):
			w.Write([]byte(`{"code":"Ok","routes":[{"distance":1200,"duration":300}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	cfg := &config.Config{NominatimURL: srv.URL, OSRMURL: srv.URL, Version: "test", BaseURL: "https://luk.test", MailFrom: "kontakt@example.org"}
	f := &Feeds{Cfg: cfg, Cache: &Cache{}, GeoMinGap: time.Millisecond}
	ctx := context.Background()

	res, err := f.Geocode(ctx, "  Stresemannstr.   15 ", 6, "de")
	if err != nil || !strings.Contains(string(res.Body), "Berlin") {
		t.Fatalf("geocode: %v %s", err, res.Body)
	}
	if lastPath != "/search" || !strings.Contains(lastQuery, "q=Stresemannstr.+15") || !strings.Contains(lastQuery, "countrycodes=de") || !strings.Contains(lastQuery, "limit=6") {
		t.Fatalf("search-URL: %s?%s", lastPath, lastQuery)
	}
	if !strings.HasPrefix(lastUA, "THW-LuK/test (luk.test; kontakt@example.org)") {
		t.Fatalf("User-Agent: %q", lastUA)
	}
	// gleiche Anfrage (andere Schreibweise) kommt aus dem Cache
	if _, err := f.Geocode(ctx, "stresemannstr. 15", 6, "DE"); err != nil || calls != 1 {
		t.Fatalf("cache: %v calls=%d", err, calls)
	}
	// Parameterpruefung
	for _, tc := range []struct {
		q     string
		limit int
		cc    string
	}{{"", 1, ""}, {strings.Repeat("x", GeoQueryMax+1), 1, ""}, {"Berlin", 0, ""}, {"Berlin", 11, ""}, {"Berlin", 1, "deutschland"}} {
		if _, err := f.Geocode(ctx, tc.q, tc.limit, tc.cc); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("geocode %+v: erwartet ErrNotAllowed, got %v", tc, err)
		}
	}

	res, err = f.Reverse(ctx, 52.5074, 13.3813)
	if err != nil || !strings.Contains(string(res.Body), "Stresemannstr") || lastPath != "/reverse" {
		t.Fatalf("reverse: %v %s %s", err, res.Body, lastPath)
	}
	if !strings.Contains(lastQuery, "lat=52.50740") || !strings.Contains(lastQuery, "lon=13.38130") {
		t.Fatalf("reverse-URL: %s", lastQuery)
	}
	if _, err := f.Reverse(ctx, 91, 0); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("reverse lat=91: %v", err)
	}

	res, err = f.Route(ctx, 52.5, 13.4, 52.6, 13.5)
	if err != nil || !strings.Contains(string(res.Body), `"distance":1200`) {
		t.Fatalf("route: %v %s", err, res.Body)
	}
	if lastPath != "/route/v1/driving/13.40000,52.50000;13.50000,52.60000" || lastQuery != "overview=false" {
		t.Fatalf("route-URL: %s?%s", lastPath, lastQuery)
	}
	if _, err := f.Route(ctx, 52.5, 13.4, 0, 181); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("route lon=181: %v", err)
	}

	// 429 von Nominatim sperrt den ganzen Dienst, nicht nur die eine Anfrage
	atomic.StoreInt32(&status, 429)
	before := calls
	var ae *AuthError
	if _, err := f.Geocode(ctx, "Potsdam", 1, ""); !errors.As(err, &ae) || ae.Status != 429 {
		t.Fatalf("429: erwartet AuthError, got %v", err)
	}
	if _, err := f.Geocode(ctx, "Cottbus", 1, ""); !errors.As(err, &ae) || calls != before+1 {
		t.Fatalf("nach 429 nicht gesperrt: %v calls=%d", err, calls)
	}
	// OSRM hat eine eigene Bremse und bleibt nutzbar (aus dem Cache)
	if _, err := f.Route(ctx, 52.5, 13.4, 52.6, 13.5); err != nil {
		t.Fatalf("route nach Nominatim-Sperre: %v", err)
	}
}

// Die Bremse vergibt Zeitschlitze im Mindestabstand und lehnt zu lange
// Warteschlangen ab.
func TestGeoGate(t *testing.T) {
	var g geoGate
	now := time.Unix(1000, 0)
	d, err := g.reserve(now, time.Second, 3*time.Second)
	if err != nil || d != 0 {
		t.Fatalf("erster Schlitz: %v %v", d, err)
	}
	for i := 1; i <= 3; i++ {
		d, err = g.reserve(now, time.Second, 3*time.Second)
		if err != nil || d != time.Duration(i)*time.Second {
			t.Fatalf("Schlitz %d: %v %v", i, d, err)
		}
	}
	if _, err = g.reserve(now, time.Second, 3*time.Second); !errors.Is(err, ErrBusy) {
		t.Fatalf("Warteschlange voll: %v", err)
	}
	// Zeit vergeht: wieder frei
	if d, err = g.reserve(now.Add(10*time.Second), time.Second, 3*time.Second); err != nil || d != 0 {
		t.Fatalf("nach Pause: %v %v", d, err)
	}
	g.block(now.Add(time.Hour), &AuthError{Status: 429})
	var ae *AuthError
	if _, err = g.reserve(now.Add(20*time.Second), time.Second, 3*time.Second); !errors.As(err, &ae) {
		t.Fatalf("Sperre: %v", err)
	}
	if _, err = g.reserve(now.Add(2*time.Hour), time.Second, 3*time.Second); err != nil {
		t.Fatalf("Sperre abgelaufen: %v", err)
	}
	if _, _, ok := ParseLatLon("52.5,13.4"); !ok {
		t.Fatal("ParseLatLon")
	}
	for _, bad := range []string{"", "52.5", "52.5;13.4", "x,y", "95,13"} {
		if _, _, ok := ParseLatLon(bad); ok {
			t.Errorf("ParseLatLon(%q) akzeptiert", bad)
		}
	}
}
