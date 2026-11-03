package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/friedrichengel/thw_luk_software_go/internal/org"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

// Einstellungen je Organisation: Kartenmittelpunkt und Nachrichten-Feeds
// setzt der Org-Admin; Dashboard und Lagekarten-Konfiguration der Konten
// dieser Organisation folgen, fremde Organisationen nicht. Hosts eigener
// Feeds sind nur fuer diese Organisation freigegeben.
func TestOrgSettingsPage(t *testing.T) {
	rss := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		io.WriteString(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Lokal</title><item><title>Meldung</title><link>https://x.example/1</link><pubDate>Mon, 01 Jan 2024 10:00:00 +0000</pubDate></item></channel></rss>`)
	}))
	defer rss.Close()

	srv, d := newTestServer(t)
	base := srv.URL
	oidA, _ := org.Create(d, "OV Alpha", "", "ov")
	oidB, _ := org.Create(d, "OV Beta", "", "ov")
	la, _ := user.Create(d, "leiter_a", "geheim1234", nil, nil, "leiter", false)
	ub, _ := user.Create(d, "user_b", "geheim1234", nil, nil, "user", false)
	user.SetOrg(d, la, &oidA)
	user.SetOrg(d, ub, &oidB)
	user.SetOrgAdmin(d, la, true)

	c := newClient()
	loginAs(t, c, base, "leiter_a", "geheim1234")
	st, body, _ := getBody(t, c, base+"/admin?tab=einstellungen")
	if st != 200 || !strings.Contains(body, "Kartenmittelpunkt") || !strings.Contains(body, "Nachrichten-Feeds") || !strings.Contains(body, "Server (MAP_CENTER_* bzw. Stammdaten)") || !strings.Contains(body, `placeholder="Tagesschau"`) {
		t.Fatalf("einstellungen: %d", st)
	}
	csrf := csrfFrom(t, body)

	// Ungueltiger Mittelpunkt
	_, body = postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"set_map"}, "tab": {"einstellungen"}, "map_lat": {"95"}, "map_lng": {"13"}})
	if !strings.Contains(body, "Mittelpunkt: Breite") {
		t.Fatal("95 Grad Breite angenommen")
	}
	// Eigener Mittelpunkt mit Komma als Dezimaltrenner
	_, body = postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"set_map"}, "tab": {"einstellungen"}, "map_lat": {"52,1"}, "map_lng": {"13.2"}, "map_name": {"Stab Alpha"}})
	if !strings.Contains(body, "Kartenmittelpunkt gespeichert") || !strings.Contains(body, "(eigener Mittelpunkt)") || !strings.Contains(body, `value="52.10000"`) {
		t.Fatalf("set_map: %s", snippet(body, "Kartenmittelpunkt"))
	}
	// Dashboard des Org-Admins: CURRENT_OV und STAB.map_center folgen
	_, body, _ = getBody(t, c, base+"/")
	if !strings.Contains(body, `"lat":52.1`) || !strings.Contains(body, `"lng":13.2`) || !strings.Contains(body, `"name":"Stab Alpha"`) || !strings.Contains(body, "window.LUK_FEEDS") || !strings.Contains(body, `"title":"Tagesschau"`) {
		t.Fatalf("dashboard A: %s", snippet(body, "CURRENT_OV"))
	}
	// Fremde Organisation: Server-Standard bleibt
	cb := newClient()
	loginAs(t, cb, base, "user_b", "geheim1234")
	_, body, _ = getBody(t, cb, base+"/")
	if strings.Contains(body, `"lat":52.1`) || !strings.Contains(body, `"name":"LV Test"`) {
		t.Fatalf("dashboard B: %s", snippet(body, "CURRENT_OV"))
	}

	// Feeds: Kachel 4-1 mit eigenem Mock-Feed, ungueltige Adresse abgelehnt
	_, body = postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"set_feeds"}, "tab": {"einstellungen"}, "feed_urls_4-1": {"ftp://nix"}})
	if !strings.Contains(body, "Feed-Adresse ungültig") {
		t.Fatal("ftp angenommen")
	}
	_, body = postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"set_feeds"}, "tab": {"einstellungen"},
		"feed_title_4-1": {"Lokal"}, "feed_urls_4-1": {rss.URL + "/feed.xml\n\n" + rss.URL + "/zwei.xml"}, "feed_title_6-2": {"MDR SA"}})
	if !strings.Contains(body, "Nachrichten-Feeds gespeichert") || !strings.Contains(body, "eigene Werte") {
		t.Fatalf("set_feeds: %s", snippet(body, "Nachrichten-Feeds"))
	}
	_, body, _ = getBody(t, c, base+"/")
	if !strings.Contains(body, `"title":"Lokal"`) || !strings.Contains(body, rss.URL+"/zwei.xml") || !strings.Contains(body, `{"slot":"6-2","title":"MDR SA","urls":["https://gdacs.org/xml/rss_24h.xml"]}`) {
		t.Fatalf("LUK_FEEDS: %s", snippet(body, "LUK_FEEDS"))
	}
	// Abruf: Host des eigenen Feeds fuer Organisation A erlaubt, fuer B nicht
	feedQ := "/api/proxy?type=rss&url=" + url.QueryEscape(rss.URL+"/feed.xml")
	if st, body, _ = getBody(t, c, base+feedQ); st != 200 || !strings.Contains(body, "Meldung") {
		t.Fatalf("rss A: %d %s", st, body)
	}
	if st, body, _ = getBody(t, cb, base+feedQ); st != 400 || !strings.Contains(body, "url not allowed") {
		t.Fatalf("rss B: %d %s", st, body)
	}
	// Zuruecksetzen
	_, body = postForm(t, c, base+"/admin", url.Values{"csrf": {csrf}, "action": {"reset_feeds"}, "tab": {"einstellungen"}})
	if !strings.Contains(body, "auf den Standard zurückgesetzt") || strings.Contains(body, "eigene Werte") {
		t.Fatal("reset_feeds")
	}
	if st, _, _ = getBody(t, c, base+feedQ); st != 400 {
		t.Fatalf("nach Reset weiter erlaubt: %d", st)
	}
	// Globaler Admin hat den Reiter nicht und darf nicht setzen
	user.Create(d, "admin", "adminpass123", nil, nil, "admin", false)
	ca := newClient()
	loginAs(t, ca, base, "admin", "adminpass123")
	_, body, _ = getBody(t, ca, base+"/admin?tab=einstellungen")
	if strings.Contains(body, "Kartenmittelpunkt") {
		t.Fatal("Reiter Einstellungen beim globalen Admin")
	}
	_, body = postForm(t, ca, base+"/admin", url.Values{"csrf": {csrfFrom(t, body)}, "action": {"set_map"}, "map_lat": {"1"}, "map_lng": {"1"}})
	if !strings.Contains(body, "setzt der Org-Admin") {
		t.Fatal("globaler Admin konnte Mittelpunkt setzen")
	}
}
