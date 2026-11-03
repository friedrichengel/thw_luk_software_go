package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/org"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

func getBody(t *testing.T, c *http.Client, u string) (int, string, http.Header) {
	t.Helper()
	res, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res.StatusCode, string(b), res.Header
}

func postForm(t *testing.T, c *http.Client, u string, form url.Values) (int, string) {
	t.Helper()
	res, err := c.PostForm(u, form)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res.StatusCode, string(b)
}

func postJSON(t *testing.T, c *http.Client, u string, body map[string]any) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	res, err := c.Post(u, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res.StatusCode, string(b)
}

var reCSRFJSON = regexp.MustCompile(`"csrf":"([0-9a-f]+)"`)

// csrfFrom liest das Token aus einem Formular (name="csrf") oder aus window.STAB.
func csrfFrom(t *testing.T, page string) string {
	t.Helper()
	if m := reCSRF.FindStringSubmatch(page); m != nil {
		return m[1]
	}
	if m := reCSRFJSON.FindStringSubmatch(page); m != nil {
		return m[1]
	}
	t.Fatalf("kein CSRF-Token in Seite")
	return ""
}

// TestOrgDivera: Organisationsmodell von Ende zu Ende - Org-Admin vergibt
// Divera-Rechte und hinterlegt Zugaenge, der Server ruft Divera (hier ein
// Mock) mit dem Schluessel der Organisation auf, kuerzt Namen, der Schluessel
// erreicht den Browser nie. Fremde Organisationen und der globale Admin
// bleiben aussen vor.
func TestOrgDivera(t *testing.T) {
	var lastStatusKey atomic.Value
	var vehicleHits int32
	divMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("accesskey")
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/pull/vehicle-status" {
			atomic.AddInt32(&vehicleHits, 1)
		}
		switch {
		case r.URL.Path == "/pull/all" && key == "SYSTEMKEY-1234567":
			io.WriteString(w, `{"success":true,"data":{"user":{"accesskey":"SYSTEMKEY-1234567","firstname":"Max","lastname":"Muster","stdformat_name":"Max Muster"},"cluster":{"group":{"1":{"name":"Zugtrupp","sorting":1},"2":{"name":"Sammelgruppe","sorting":2}},"consumer":{"1":{"id":1,"firstname":"Erika","lastname":"Musterfrau","stdformat_name":"Erika Musterfrau","groups":[1,2]}}}}}`)
		case r.URL.Path == "/pull/vehicle-status" && key == "ACCESSKEY-1234567":
			io.WriteString(w, `{"success":true,"data":[{"id":1,"fmsstatus":2,"name":"MTW"}]}`)
		case r.URL.Path == "/using-vehicles/set-status/1" && r.Method == "POST":
			lastStatusKey.Store(key)
			b, _ := io.ReadAll(r.Body)
			io.WriteString(w, `{"success":true,"echo":`+string(b)+`}`)
		default:
			w.WriteHeader(403)
			io.WriteString(w, `{"success":false}`)
		}
	}))
	defer divMock.Close()
	var steinStatus, steinBu atomic.Value
	steinStatus.Store("ready")
	steinBu.Store("123")
	var steinHits int32
	steinMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer STEIN-TOKEN-1234" {
			w.WriteHeader(401)
			return
		}
		atomic.AddInt32(&steinHits, 1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `[{"buId":"`+steinBu.Load().(string)+`","groupId":1,"status":"`+steinStatus.Load().(string)+`","name":"GKW"}]`)
	}))
	defer steinMock.Close()
	ninaMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `[{"id":"mow.1"}]`)
	}))
	defer ninaMock.Close()
	tileMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("\x89PNG\r\n\x1a\ntile"))
	}))
	defer tileMock.Close()

	geoMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/search":
			io.WriteString(w, `[{"lat":"52.5","lon":"13.4","display_name":"Stresemannstr. 15, Berlin"}]`)
		case r.URL.Path == "/reverse":
			io.WriteString(w, `{"display_name":"Stresemannstr. 15, Berlin"}`)
		case strings.HasPrefix(r.URL.Path, "/route/v1/driving/"):
			io.WriteString(w, `{"code":"Ok","routes":[{"distance":1200,"duration":300}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer geoMock.Close()

	srv, d := newTestServerWith(t, func(c *config.Config) {
		c.DiveraBaseURL = divMock.URL
		c.SteinBaseURL = steinMock.URL
		c.NinaBaseURL = ninaMock.URL
		c.NominatimURL = geoMock.URL
		c.OSRMURL = geoMock.URL
		c.TileSource = tileMock.URL + "/{z}/{x}/{y}.png"
	})
	base := srv.URL
	// echte STEIN-Kennung einer Live-Einheit aus den Stammdaten, damit der
	// Webhook-Nachabruf die gecachte Live-Menge trifft
	bu := lastServer.Katalog.LiveBuIDs()[0]
	steinBu.Store(bu)

	oidA, _ := org.Create(d, "OV Alpha", "", "ov")
	oidB, _ := org.Create(d, "OV Beta", "", "ov")
	user.Create(d, "admin", "adminpass123", nil, nil, "admin", false)
	la, _ := user.Create(d, "leiter_a", "geheim1234", nil, nil, "leiter", false)
	ua, _ := user.Create(d, "user_a", "geheim1234", nil, nil, "user", false)
	ub, _ := user.Create(d, "user_b", "geheim1234", nil, nil, "user", false)
	user.SetOrg(d, la, &oidA)
	user.SetOrg(d, ua, &oidA)
	user.SetOrg(d, ub, &oidB)
	user.SetOrgAdmin(d, la, true)

	// --- Org-Admin der Organisation A -------------------------------------
	c := newClient()
	loginAs(t, c, base, "leiter_a", "geheim1234")
	st, body, _ := getBody(t, c, base+"/api/divera?action=all")
	if st != 403 || !strings.Contains(body, "divera_disabled") {
		t.Fatalf("ohne Recht: %d %s", st, body)
	}
	st, body, _ = getBody(t, c, base+"/divera")
	if st != 403 {
		t.Fatalf("/divera ohne Recht: %d", st)
	}
	st, body, _ = getBody(t, c, base+"/admin")
	if st != 200 || !strings.Contains(body, "OV Alpha") || strings.Contains(body, "user_b") || strings.Contains(body, "Organisationen <span") {
		t.Fatalf("admin als org-admin: %d (Beta-Nutzer oder Org-Reiter sichtbar?)", st)
	}
	csrf := csrfFrom(t, body)
	// Divera-Recht fuer sich selbst (write) und user_a (read)
	postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"set_divera"}, "uid": {itoa(la)}, "divera": {"write"}})
	postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"set_divera"}, "uid": {itoa(ua)}, "divera": {"read"}})
	// Nutzer einer fremden Organisation: abgelehnt
	_, body = postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"set_divera"}, "uid": {itoa(ub)}, "divera": {"write"}})
	if !strings.Contains(body, "gehört nicht zu deiner Organisation") {
		t.Fatalf("fremder Nutzer nicht abgelehnt")
	}
	if u, _ := user.FindByID(d, ub); u.DiveraAccess != "off" {
		t.Fatal("fremder Nutzer veraendert")
	}
	// Recht ohne Zugang: weiter gesperrt
	st, body, _ = getBody(t, c, base+"/api/divera?action=vehicles")
	if st != 403 || !strings.Contains(body, "divera_disabled") {
		t.Fatalf("ohne Zugang: %d %s", st, body)
	}
	// Zugaenge hinterlegen
	for kind, val := range map[string]string{org.KindDiveraAccess: "ACCESSKEY-1234567", org.KindDiveraSystem: "SYSTEMKEY-1234567", org.KindStein: "STEIN-TOKEN-1234"} {
		st, body = postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"set_credential"}, "kind": {kind}, "value": {val}, "tab": {"zugaenge"}})
		if st != 200 || !strings.Contains(body, "hinterlegt.") {
			t.Fatalf("set_credential %s: %d", kind, st)
		}
	}
	st, body, _ = getBody(t, c, base+"/admin?tab=zugaenge")
	if !strings.Contains(body, "…4567") || strings.Contains(body, "ACCESSKEY-1234567") || strings.Contains(body, "STEIN-TOKEN") {
		t.Fatalf("Zugaenge-Seite zeigt Schluessel oder keinen Hinweis")
	}

	// Divera-Daten: Namen gekuerzt, Schluessel nie im Body
	st, body, hdr := getBody(t, c, base+"/api/divera?action=all")
	if st != 200 || hdr.Get("X-Cache-At") == "" {
		t.Fatalf("all: %d %s", st, body)
	}
	for _, bad := range []string{"SYSTEMKEY", "ACCESSKEY", "Muster", "Erika"} {
		if strings.Contains(body, bad) {
			t.Fatalf("all enthaelt %q: %s", bad, body)
		}
	}
	if !strings.Contains(body, `"stdformat_name":"E. M."`) || !strings.Contains(body, `"accesskey":"123456"`) {
		t.Fatalf("all nicht gekuerzt: %s", body)
	}
	st, body, _ = getBody(t, c, base+"/api/divera?action=vehicles")
	if st != 200 || !strings.Contains(body, `"fmsstatus":2`) {
		t.Fatalf("vehicles: %d %s", st, body)
	}
	st, body = postJSON(t, c, base+"/api/divera?action=set_status&id=1", map[string]any{"csrf": csrf, "status": 6, "status_note": "Test", "bogus": "x"})
	if st != 200 || !strings.Contains(body, `"status_note":"Test"`) || strings.Contains(body, "bogus") {
		t.Fatalf("set_status: %d %s", st, body)
	}
	if k, _ := lastStatusKey.Load().(string); k != "ACCESSKEY-1234567" {
		t.Fatalf("set_status mit falschem Schluessel: %q", k)
	}
	st, body = postJSON(t, c, base+"/api/divera?action=set_status&id=abc", map[string]any{"csrf": csrf})
	if st != 400 || !strings.Contains(body, "invalid_id") {
		t.Fatalf("set_status id: %d %s", st, body)
	}
	// Dashboard-Tab, DiveraMap-Seite, Footer-Eintrag
	st, body, _ = getBody(t, c, base+"/")
	if st != 200 || !strings.Contains(body, `id="content1-0"`) || !strings.Contains(body, "divera_container.js") || !strings.Contains(body, "DiveraMap") {
		t.Fatalf("dashboard ohne Divera-Tab: %d", st)
	}
	st, body, _ = getBody(t, c, base+"/divera")
	if st != 200 || !strings.Contains(body, `id="vehicleList"`) || !strings.Contains(body, `"divera":{"enabled":true,"write":true}`) {
		t.Fatalf("/divera: %d", st)
	}
	// Heimatstandort: erst mit Stammdaten-Bezug der Organisation (OV mit Koordinaten)
	ovKey := ""
	for _, ov := range lastServer.Katalog.OVs() {
		if _, ok := ov["lat"].(float64); ok {
			ovKey, _ = ov["key"].(string)
			break
		}
	}
	d.Exec("UPDATE organisationen SET stamm_key = ? WHERE id = ?", org.StammKeyOV(ovKey), oidA)
	_, body, _ = getBody(t, c, base+"/divera")
	if !strings.Contains(body, `"home":{"lat":`) || !strings.Contains(body, `"name":`) {
		t.Fatalf("/divera ohne Heimatstandort: %s", body[strings.Index(body, `"divera"`):min(len(body), strings.Index(body, `"divera"`)+200)])
	}
	// STEIN ueber den Org-Schluessel (STEINboard-Pfad)
	st, body, hdr = getBody(t, c, base+"/api/proxy?type=stein&buIds="+bu)
	if st != 200 || !strings.Contains(body, `"GKW"`) || hdr.Get("X-Cache-Oldest-At") == "" {
		t.Fatalf("stein: %d %s", st, body)
	}
	// Oeffentliche Feeds direkt (Mock), RSS nur erlaubte Hosts
	st, body, _ = getBody(t, c, base+"/api/proxy?type=nina")
	if st != 200 || !strings.Contains(body, "mow.1") {
		t.Fatalf("nina: %d %s", st, body)
	}
	st, body, _ = getBody(t, c, base+"/api/proxy?type=rss&url="+url.QueryEscape("https://evil.example/feed.xml"))
	if st != 400 || !strings.Contains(body, "url not allowed") {
		t.Fatalf("rss fremder Host: %d %s", st, body)
	}
	// Adresssuche, Rueckwaertssuche und Routing (STEINmap) ueber den Server
	st, body, _ = getBody(t, c, base+"/api/proxy?type=geocode&limit=1&q="+url.QueryEscape("Stresemannstr. 15"))
	if st != 200 || !strings.Contains(body, `"display_name":"Stresemannstr. 15, Berlin"`) {
		t.Fatalf("geocode: %d %s", st, body)
	}
	st, body, _ = getBody(t, c, base+"/api/proxy?type=geocode&q=")
	if st != 400 {
		t.Fatalf("geocode ohne q: %d %s", st, body)
	}
	st, body, _ = getBody(t, c, base+"/api/proxy?type=geocode-reverse&lat=52.5&lon=13.4")
	if st != 200 || !strings.Contains(body, "Stresemannstr") {
		t.Fatalf("geocode-reverse: %d %s", st, body)
	}
	st, body, _ = getBody(t, c, base+"/api/proxy?type=route&from=52.5,13.4&to=52.6,13.5")
	if st != 200 || !strings.Contains(body, `"distance":1200`) {
		t.Fatalf("route: %d %s", st, body)
	}
	st, body, _ = getBody(t, c, base+"/api/proxy?type=route&from=52.5&to=52.6,13.5")
	if st != 400 {
		t.Fatalf("route ohne lon: %d %s", st, body)
	}

	// Eingebauter Kachel-Cache: mit Sitzung (Cookie) erreichbar, Bild kommt aus der Quelle
	st, body, hdr = getBody(t, c, base+"/tiles/4/8/5.png")
	if st != 200 || hdr.Get("Content-Type") != "image/png" || hdr.Get("X-Cache") != "MISS" || !strings.HasPrefix(body, "\x89PNG") {
		t.Fatalf("tiles: %d %v", st, hdr)
	}
	if _, _, hdr = getBody(t, c, base+"/tiles/4/8/5.png"); hdr.Get("X-Cache") != "HIT" {
		t.Fatalf("tiles zweiter Abruf: %s", hdr.Get("X-Cache"))
	}

	// --- Nutzer mit Leserecht: lesen ja, Status setzen nein --------------
	c2 := newClient()
	loginAs(t, c2, base, "user_a", "geheim1234")
	st, _, _ = getBody(t, c2, base+"/api/divera?action=vehicles")
	if st != 200 {
		t.Fatalf("user_a vehicles: %d", st)
	}
	_, page, _ := getBody(t, c2, base+"/")
	if !strings.Contains(page, `"enabled":true`) || !strings.Contains(page, `"write":false`) {
		t.Fatal("user_a: write-Flag falsch")
	}
	st, body = postJSON(t, c2, base+"/api/divera?action=set_status&id=1", map[string]any{"csrf": csrfFrom(t, page), "status": 2})
	if st != 403 || !strings.Contains(body, "divera_forbidden") {
		t.Fatalf("user_a set_status: %d %s", st, body)
	}
	st, _, _ = getBody(t, c2, base+"/admin")
	if st != 403 {
		t.Fatalf("user_a /admin: %d", st)
	}

	// --- Nutzer der Organisation B: nichts davon ---------------------------
	c3 := newClient()
	loginAs(t, c3, base, "user_b", "geheim1234")
	st, body, _ = getBody(t, c3, base+"/api/divera?action=all")
	if st != 403 || !strings.Contains(body, "divera_disabled") {
		t.Fatalf("user_b: %d %s", st, body)
	}
	st, body, _ = getBody(t, c3, base+"/")
	if st != 200 || strings.Contains(body, `id="content1-0"`) || strings.Contains(body, "DiveraMap") {
		t.Fatal("user_b sieht Divera")
	}
	st, body, _ = getBody(t, c3, base+"/api/proxy?type=stein&buIds="+bu)
	if st != 404 || !strings.Contains(body, "stein_not_configured") {
		t.Fatalf("user_b stein: %d %s", st, body)
	}

	// --- Globaler Admin: Organisationen ja, Schluessel nein ---------------
	c4 := newClient()
	loginAs(t, c4, base, "admin", "adminpass123")
	st, body, _ = getBody(t, c4, base+"/admin?tab=orgs")
	if st != 200 || !strings.Contains(body, "OV Alpha") || !strings.Contains(body, "OV Beta") || !strings.Contains(body, "3 / 3") {
		t.Fatalf("admin orgs: %d", st)
	}
	acsrf := csrfFrom(t, body)
	_, body = postForm(t, c4, base+"/admin", url.Values{"csrf": {acsrf}, "action": {"set_credential"}, "kind": {org.KindStein}, "value": {"ADMIN-DARF-NICHT-123"}})
	if !strings.Contains(body, "Zugänge hinterlegt der Org-Admin") {
		t.Fatal("globaler Admin konnte Zugang setzen")
	}
	if v, _ := org.Credential(d, oidA, org.KindStein, "test-secret-0123456789abcdef0123456789"); v != "STEIN-TOKEN-1234" {
		t.Fatalf("Zugang veraendert: %q", v)
	}
	// Reiter "Server": Kachelquelle testen, speichern, Endpunkt freigeben
	st, body, _ = getBody(t, c4, base+"/admin?tab=server")
	if st != 200 || !strings.Contains(body, "Kacheln der Karten") || !strings.Contains(body, `name="tile_source" value=""`) || !strings.Contains(body, "/tiles/{z}/{x}/{y}.png") {
		t.Fatalf("server-tab: %d", st)
	}
	_, body = postForm(t, c4, base+"/admin", url.Values{"csrf": {acsrf}, "action": {"test_tiles"}, "tab": {"server"}, "tile_source": {tileMock.URL + "/{z}/{x}/{y}.png"}})
	if !strings.Contains(body, "Testabruf OK") {
		t.Fatal("Testabruf gegen Mock fehlgeschlagen")
	}
	_, body = postForm(t, c4, base+"/admin", url.Values{"csrf": {acsrf}, "action": {"test_tiles"}, "tab": {"server"}, "tile_source": {"nix"}})
	if !strings.Contains(body, "Testabruf fehlgeschlagen") {
		t.Fatal("ungueltige Quelle nicht abgelehnt")
	}
	_, body = postForm(t, c4, base+"/admin", url.Values{"csrf": {acsrf}, "action": {"set_tiles"}, "tab": {"server"}, "tile_source": {tileMock.URL + "/alt/{z}/{x}/{y}.png"}, "tile_public": {"1"}, "tile_token": {"geheim-tok"}})
	if !strings.Contains(body, "Kachel-Einstellungen gespeichert") || !strings.Contains(body, "/alt/{z}/{x}/{y}.png") {
		t.Fatal("set_tiles")
	}
	// ohne Sitzung: jetzt freigegeben, aber nur mit Token
	if st, _, _ = getBody(t, newClient(), base+"/tiles/4/8/6.png"); st != 403 {
		t.Fatalf("tiles ohne Token: %d", st)
	}
	if st, _, hdr = getBody(t, newClient(), base+"/tiles/4/8/6.png?key=geheim-tok"); st != 200 || hdr.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("tiles mit Token: %d", st)
	}
	postForm(t, c4, base+"/admin", url.Values{"csrf": {acsrf}, "action": {"set_tiles"}, "tab": {"server"}, "tile_source": {""}, "tile_token": {""}})
	if st, _, _ = getBody(t, newClient(), base+"/tiles/4/8/6.png"); st != 401 {
		t.Fatalf("tiles nach Zuruecknahme: %d", st)
	}
	_, body = postForm(t, c4, base+"/admin", url.Values{"csrf": {acsrf}, "action": {"create_org"}, "name": {"RSt Gamma"}, "ebene": {"rst"}, "tab": {"orgs"}})
	if !strings.Contains(body, "Organisation angelegt.") || !strings.Contains(body, "RSt Gamma") {
		t.Fatal("create_org")
	}
	// Uebernahme aus den Stammdaten: nur zwei gewaehlte Eintraege
	_, body = postForm(t, c4, base+"/admin", url.Values{"csrf": {acsrf}, "action": {"seed_orgs"}, "tab": {"orgs"}, "keys": {"rst_nord", org.StammKeyOV("103")}})
	if !strings.Contains(body, "2 Organisationen aus den Stammdaten angelegt") || !strings.Contains(body, "Regionalstelle Nordstadt") || !strings.Contains(body, "OV Südstadt") {
		t.Fatalf("seed_orgs Auswahl fehlgeschlagen")
	}
	if !strings.Contains(body, "Aus den Stammdaten übernehmen") || !strings.Contains(body, `value="ov:101"`) {
		t.Fatal("Stammdaten-Auswahl fehlt auf der Seite")
	}
	_, body = postForm(t, c4, base+"/admin", url.Values{"csrf": {acsrf}, "action": {"seed_orgs"}, "tab": {"orgs"}})
	if !strings.Contains(body, "mindestens einen Eintrag") {
		t.Fatal("leere Auswahl nicht abgelehnt")
	}
	gamma, _ := org.FindByName(d, "RSt Gamma")
	postForm(t, c4, base+"/admin", url.Values{"csrf": {acsrf}, "action": {"set_org"}, "uid": {itoa(ub)}, "org_id": {itoa(gamma.ID)}})
	if u, _ := user.FindByID(d, ub); u.OrgIDValue() != gamma.ID {
		t.Fatal("set_org")
	}
	_, body = postForm(t, c4, base+"/admin", url.Values{"csrf": {acsrf}, "action": {"delete_org"}, "org_id": {itoa(gamma.ID)}, "tab": {"orgs"}})
	if !strings.Contains(body, "noch Konten") {
		t.Fatal("delete_org mit Konten muss abgelehnt werden")
	}
	st, body, _ = getBody(t, c4, base+"/api/divera?action=all")
	if st != 403 || !strings.Contains(body, "forbidden_admin") {
		t.Fatalf("admin divera: %d %s", st, body)
	}

	// --- Divera-Gruppen fuer das Dashboard auswaehlen ----------------------
	_, body, _ = getBody(t, c, base+"/admin?tab=zugaenge")
	if !strings.Contains(body, "Gruppen im Dashboard") || !strings.Contains(body, "Zugtrupp") || !strings.Contains(body, "Sammelgruppe") || !strings.Contains(body, `name="all_ids" value="1,2"`) {
		t.Fatal("Gruppenauswahl fehlt im Reiter Zugaenge")
	}
	_, body = postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"set_divera_groups"}, "tab": {"zugaenge"}, "all_ids": {"1,2"}, "show": {"1"}})
	if !strings.Contains(body, "1 ausgeblendet") {
		t.Fatalf("set_divera_groups: %s", body[:200])
	}
	// Die Antwortseite zeigt die neue Auswahl sofort (kein Neuladen noetig)
	if !strings.Contains(body, `value="2" > Sammelgruppe`) || !strings.Contains(body, `value="1" checked> Zugtrupp`) {
		t.Fatal("Auswahl nach dem Speichern nicht aktualisiert angezeigt")
	}
	st, body, _ = getBody(t, c, base+"/api/divera?action=all")
	if st != 200 || strings.Contains(body, "Sammelgruppe") || !strings.Contains(body, "Zugtrupp") || !strings.Contains(body, `"groups":[1]`) {
		t.Fatalf("ausgeblendete Gruppe noch in der Antwort: %d %s", st, body)
	}
	// wieder einblenden
	postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"set_divera_groups"}, "tab": {"zugaenge"}, "all_ids": {"1,2"}, "show": {"1", "2"}})
	if _, body, _ = getBody(t, c, base+"/api/divera?action=all"); !strings.Contains(body, "Sammelgruppe") {
		t.Fatal("Gruppe nicht wieder eingeblendet")
	}

	// --- Verbindung pruefen (Org-Admin A) ---------------------------------
	for kind, want := range map[string]string{org.KindStein: "Verbindung OK: 1 Assets", org.KindDiveraAccess: "Verbindung OK: 1 Fahrzeuge", org.KindDiveraSystem: "Verbindung OK"} {
		_, body = postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"check_credential"}, "kind": {kind}, "tab": {"zugaenge"}})
		if !strings.Contains(body, want) {
			t.Fatalf("check_credential %s: %q fehlt", kind, want)
		}
	}

	// --- Freigabe an eine Organisation: Einsatz ja, Divera nein -----------
	// user_b gehoert inzwischen zu "RSt Gamma" (set_org oben).
	shareOrg := gamma.ID
	fetchPage := func(cl *http.Client, u string) string { _, b, _ := getBody(t, cl, base+u); return b }
	st, body = postJSON(t, c, base+"/api/einsatz?action=create", map[string]any{"csrf": csrf, "title": "Sturm", "ebene": "ov"})
	if st != 200 {
		t.Fatalf("create: %d %s", st, body)
	}
	var created struct {
		UUID string `json:"uuid"`
	}
	json.Unmarshal([]byte(body), &created)
	eu := created.UUID
	if st, _, _ = getBody(t, c3, base+"/api/einsatz?action=load&einsatz="+eu); st != 403 {
		t.Fatalf("user_b vor Freigabe: %d", st)
	}
	st, body = postJSON(t, c, base+"/api/einsatz?action=org_member_set", map[string]any{"csrf": csrf, "einsatz": eu, "org_id": shareOrg, "roles": []string{"s2"}})
	if st != 200 || !strings.Contains(body, `"org_members":[{"org_id":`) {
		t.Fatalf("org_member_set: %d %s", st, body)
	}
	st, body, _ = getBody(t, c3, base+"/api/einsatz?action=load&einsatz="+eu)
	if st != 200 || !strings.Contains(body, `"roles":["s2"]`) || !strings.Contains(body, `"lagekarte":true`) {
		t.Fatalf("user_b nach Freigabe: %d %s", st, body)
	}
	if body = fetchPage(c3, "/api/einsatz?action=list"); !strings.Contains(body, "Sturm") {
		t.Fatal("user_b: Einsatz fehlt in der Liste")
	}
	if body = fetchPage(c3, "/harke?einsatz="+eu); !strings.Contains(body, "Sturm") {
		t.Fatal("user_b: Harke nicht erreichbar")
	}
	// Divera bleibt fuer user_b gesperrt (Hilfsmittel haengen am eigenen Konto)
	if st, body, _ = getBody(t, c3, base+"/api/divera?action=all"); st != 403 || !strings.Contains(body, "divera_disabled") {
		t.Fatalf("user_b divera trotz Einsatz-Freigabe: %d %s", st, body)
	}
	if body = fetchPage(c3, "/"); strings.Contains(body, `id="content1-0"`) {
		t.Fatal("user_b sieht Divera-Tab")
	}
	// Freigabe zuruecknehmen
	postJSON(t, c, base+"/api/einsatz?action=org_member_set", map[string]any{"csrf": csrf, "einsatz": eu, "org_id": shareOrg, "roles": []string{}})
	if st, _, _ = getBody(t, c3, base+"/api/einsatz?action=load&einsatz="+eu); st != 403 {
		t.Fatalf("user_b nach Entzug: %d", st)
	}

	// --- STEIN-Webhook: Modus, Secrets, Ereignisse, gebuendelter Nachabruf ---
	lastServer.Katalog.WebhookDebounce = 50 * time.Millisecond
	_, body = postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"set_stein_mode"}, "stein_mode": {"webhook"}, "tab": {"zugaenge"}})
	if !strings.Contains(body, "/api/stein/webhook/") || !strings.Contains(body, "Noch kein Webhook-Secret") {
		t.Fatal("Webhook-Modus: URL oder Secret-Hinweis fehlt")
	}
	hookURL := regexp.MustCompile(`http://[^<\s]+/api/stein/webhook/[0-9a-f]{32}`).FindString(body)
	if hookURL == "" {
		t.Fatalf("Webhook-URL nicht gefunden")
	}
	hookPath := hookURL[strings.Index(hookURL, "/api/stein/webhook/"):]
	postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"add_webhook_secret"}, "label": {"OV Alpha"}, "value": {"HOOK-SECRET-ALPHA-1"}, "tab": {"zugaenge"}})
	event := `{"meta":{"eventId":"STEIN-1","environmentName":"prod","timestamp":"2026-10-25T12:00:00+02:00"},"items":[{"type":"asset","action":"update","id":206,"url":"x","parentId":` + bu + `,"parentType":"bu"}]}`
	postHook := func(path, secret, body string) int {
		req, _ := http.NewRequest("POST", base+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if secret != "" {
			req.Header.Set("X-Secret", secret)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if st := postHook("/api/stein/webhook/00000000000000000000000000000000", "HOOK-SECRET-ALPHA-1", event); st != 404 {
		t.Fatalf("unbekannter Token: %d", st)
	}
	if st := postHook(hookPath, "falsch", event); st != 403 {
		t.Fatalf("falsches Secret: %d", st)
	}
	if st := postHook(hookPath, "", event); st != 403 {
		t.Fatalf("ohne Secret: %d", st)
	}
	// Live-Menge einmal im Cache, dann aendert sich der Status in STEIN
	getBody(t, c, base+"/api/proxy?type=stein&buIds="+bu)
	hits := atomic.LoadInt32(&steinHits)
	steinStatus.Store("notready")
	if st := postHook(hookPath, "HOOK-SECRET-ALPHA-1", event); st != 200 {
		t.Fatalf("gueltiges Ereignis: %d", st)
	}
	if st := postHook(hookPath, "HOOK-SECRET-ALPHA-1", event); st != 200 {
		t.Fatalf("Duplikat: %d", st)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, body, _ = getBody(t, c, base+"/api/proxy?type=stein&buIds="+bu)
		if strings.Contains(body, `"notready"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Webhook-Nachabruf hat den Cache nicht aktualisiert: %s", body)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n := atomic.LoadInt32(&steinHits) - hits; n != 1 {
		t.Fatalf("Nachabruf: %d STEIN-Anfragen, erwartet 1 (gebuendelt, Duplikat ignoriert)", n)
	}
	_, body, _ = getBody(t, c, base+"/admin?tab=zugaenge")
	if !strings.Contains(body, "STEIN-1") || !strings.Contains(body, "OV Alpha") || !strings.Contains(body, "<td>asset</td>") || strings.Contains(body, "HOOK-SECRET") {
		t.Fatal("Ereignisliste oder Secret-Anzeige falsch")
	}
	postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"set_stein_mode"}, "stein_mode": {"poll"}, "tab": {"zugaenge"}})

	// --- Falscher Schluessel: Ablehnung einmal, danach Pause --------------
	hitsBefore := atomic.LoadInt32(&vehicleHits)
	postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"set_credential"}, "kind": {org.KindDiveraAccess}, "value": {"WRONGKEY-12345678"}, "tab": {"zugaenge"}})
	for i := 0; i < 3; i++ {
		st, body, _ = getBody(t, c, base+"/api/divera?action=vehicles")
		if st != 502 || !strings.Contains(body, "divera_auth_failed") {
			t.Fatalf("falscher Schluessel #%d: %d %s", i, st, body)
		}
	}
	if hits := atomic.LoadInt32(&vehicleHits) - hitsBefore; hits != 1 {
		t.Fatalf("abgelehnter Schluessel wurde %d-mal probiert, erwartet 1", hits)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
