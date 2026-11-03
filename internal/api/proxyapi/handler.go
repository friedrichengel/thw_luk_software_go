// Package proxyapi: Session-geschuetzter Zugang zu den Hilfsmittel-Daten.
//
// Der Browser ruft /api/proxy?type=... auf (nur mit gueltiger Sitzung, kein
// Schluessel im Client). Der Server holt die Daten selbst bei den Diensten
// (internal/upstream) und cacht sie; die frueheren Node-Proxies entfallen.
//
// Unterstuetzte Typen:
//
//	rss        Feed als JSON (+url=..., nur Hosts aus RSS_ALLOWED_HOSTS oder
//	           aus den eigenen Feeds der Organisation des Nutzers)
//	nina       MOWAS-Warnmeldungen (warnung.bund.de)
//	nina-geo   Warn-Geometrie (+id=...)
//	lhp        Hochwasserportal (+kind=alerts|stations, GeoJSON)
//	waldbrand  Waldbrandgefahrenindex-Karte des DWD (PNG)
//	stein      STEIN-Assets der Organisation des Nutzers (+buIds=...)
//	geocode    Adresssuche ueber Nominatim (+q=..., limit=1..10 [1],
//	           countrycodes=de[,at]); Antwort wie Nominatim /search format=json
//	geocode-reverse  Adresse zu einer Koordinate (+lat=..&lon=..), Nominatim /reverse
//	route      Fahrtroute ueber OSRM (+from=lat,lon&to=lat,lon), Antwort wie
//	           OSRM /route/v1/driving (overview=false)
//
// Adresssuche und Routing bremst der Server auf einen Abruf je Sekunde und
// Dienst; ist die Warteschlange zu lang oder hat der Dienst uns abgewiesen,
// kommt 429 {error:"rate_limited"} - der Client wartet dann kurz.
//
// Antworten tragen X-Cache-At (Abrufzeit) und bei STEIN zusaetzlich
// X-Cache-Oldest-At wie der fruehere Proxy, damit die Stand-Anzeige der
// Clients weiterlaeuft.
package proxyapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/auth"
	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
	"github.com/friedrichengel/thw_luk_software_go/internal/kraefte"
	"github.com/friedrichengel/thw_luk_software_go/internal/upstream"
)

func Register(mux *http.ServeMux, app *httpx.App, kat *kraefte.Katalog) {
	h := func(w http.ResponseWriter, r *http.Request) { handle(w, r, app, kat) }
	mux.HandleFunc("/api/proxy", h)
}

func jsonErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]string{"error": msg})
	w.Write(b)
}

func writeResult(w http.ResponseWriter, res upstream.Result) {
	ct := res.ContentType
	lc := strings.ToLower(ct)
	if ct == "" || (!strings.Contains(lc, "json") && !strings.HasPrefix(lc, "image/")) {
		ct = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Cache-At", res.FetchedAt.UTC().Format(time.RFC3339))
	if res.Stale {
		w.Header().Set("X-Cache-Stale", "1")
	}
	w.WriteHeader(200)
	w.Write(res.Body)
}

func handle(w http.ResponseWriter, r *http.Request, app *httpx.App, kat *kraefte.Katalog) {
	s := app.Sess(r)
	if !auth.IsLoggedIn(s) {
		jsonErr(w, 401, "unauthorized")
		return
	}
	// Admin-Konten haben keinen Einsatzzugriff - auch nicht auf die Hilfsmittel.
	if auth.IsAdmin(s) {
		jsonErr(w, 403, "forbidden")
		return
	}
	if r.Method != http.MethodGet {
		jsonErr(w, 405, "method not allowed")
		return
	}
	typ := strings.TrimSpace(httpx.Query(r, "type"))
	feeds := app.Feeds
	var res upstream.Result
	var err error
	switch typ {
	case "rss":
		feed := httpx.Query(r, "url")
		if feed == "" {
			jsonErr(w, 400, "url parameter required")
			return
		}
		// Hosts der eigenen Feeds der Organisation sind zusaetzlich erlaubt
		res, err = feeds.RSS(r.Context(), feed, app.RSSHostAllowedFor(app.UserOrgID(r)))
	case "nina":
		res, err = feeds.Nina(r.Context())
	case "nina-geo":
		id := httpx.Query(r, "id")
		if id == "" {
			jsonErr(w, 400, "id parameter required")
			return
		}
		res, err = feeds.NinaGeo(r.Context(), id)
	case "waldbrand":
		res, err = feeds.Waldbrand(r.Context())
		if err == nil {
			if lm := feeds.WaldbrandLastModified(); lm != "" {
				w.Header().Set("Last-Modified", lm)
			}
		}
	case "geocode":
		limit := 1
		if l := strings.TrimSpace(httpx.Query(r, "limit")); l != "" {
			n, perr := strconv.Atoi(l)
			if perr != nil {
				jsonErr(w, 400, "limit parameter invalid")
				return
			}
			limit = n
		}
		res, err = feeds.Geocode(r.Context(), httpx.Query(r, "q"), limit, httpx.Query(r, "countrycodes"))
	case "geocode-reverse":
		lat, lon, ok := upstream.ParseLatLon(httpx.Query(r, "lat") + "," + httpx.Query(r, "lon"))
		if !ok {
			jsonErr(w, 400, "lat/lon parameters invalid")
			return
		}
		res, err = feeds.Reverse(r.Context(), lat, lon)
	case "route":
		fLat, fLon, okF := upstream.ParseLatLon(httpx.Query(r, "from"))
		tLat, tLon, okT := upstream.ParseLatLon(httpx.Query(r, "to"))
		if !okF || !okT {
			jsonErr(w, 400, "from/to parameters invalid (lat,lon)")
			return
		}
		res, err = feeds.Route(r.Context(), fLat, fLon, tLat, tLon)
	case "lhp":
		kind := httpx.Query(r, "kind")
		if kind != "alerts" && kind != "stations" {
			jsonErr(w, 400, "kind parameter must be alerts or stations")
			return
		}
		res, err = feeds.LHP(r.Context(), kind)
	case "stein":
		buIDs := httpx.Query(r, "buIds")
		if strings.TrimSpace(buIDs) == "" {
			jsonErr(w, 400, "buIds parameter required")
			return
		}
		u := app.Auth.User(s)
		if u == nil {
			jsonErr(w, 401, "unauthorized")
			return
		}
		st := kat.SteinAssetsFor(u.OrgIDValue(), strings.Split(buIDs, ","))
		if !st.OK {
			code, status := "stein_not_configured", 404
			if st.Error != nil {
				switch *st.Error {
				case "fetch_failed":
					code, status = "upstream unreachable", 503
				case "auth_failed":
					code, status = "stein_auth_failed", 502
				}
			}
			jsonErr(w, status, code)
			return
		}
		body, _ := json.Marshal(st.Assets)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if st.Stand != nil {
			w.Header().Set("X-Cache-At", *st.Stand)
			w.Header().Set("X-Cache-Oldest-At", *st.Stand)
			w.Header().Set("X-Cache-Newest-At", *st.Stand)
		}
		if st.Stale {
			w.Header().Set("X-Cache-Stale", "1")
		}
		w.WriteHeader(200)
		w.Write(body)
		return
	default:
		jsonErr(w, 400, "Unknown type")
		return
	}
	if err != nil {
		if errors.Is(err, upstream.ErrNotAllowed) {
			jsonErr(w, 400, "url not allowed")
			return
		}
		if errors.Is(err, upstream.ErrBusy) || errors.Is(err, context.DeadlineExceeded) {
			jsonErr(w, 429, "rate_limited")
			return
		}
		var ae *upstream.AuthError
		if errors.As(err, &ae) {
			app.Log.Printf("upstream %s: Zugang abgelehnt (%d), Pause", typ, ae.Status)
			if ae.Status == 429 {
				jsonErr(w, 429, "rate_limited")
				return
			}
			jsonErr(w, 502, "upstream_auth_failed")
			return
		}
		// Fehlerdetails nur ins Server-Log, nicht an den Client (Info-Disclosure)
		app.Log.Printf("upstream %s: %v", typ, err)
		jsonErr(w, 502, "upstream unreachable")
		return
	}
	writeResult(w, res)
}
