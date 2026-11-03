package org_test

import (
	"path/filepath"
	"testing"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/org"
)

// Kartenmittelpunkt und Nachrichten-Feeds je Organisation: speichern, lesen,
// Standard-Ueberlagerung je Kachel, Freigabe eigener Feed-Hosts.
func TestOrgSettings(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"), luk.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	id, _ := org.Create(d, "OV Test", "", "ov")
	o, _ := org.FindByID(d, id)
	if o.HasMapCenter() || len(o.RSSFeeds) != 0 || (*org.Org)(nil).HasMapCenter() {
		t.Fatalf("frisch: %+v", o)
	}
	// Standard-Feeds fuer nil und fuer eine Organisation ohne eigene Werte
	if def := (*org.Org)(nil).EffectiveFeeds(); len(def) != 4 || def[0].Slot != "4-1" || def[0].Title != "Tagesschau" {
		t.Fatalf("Standard: %+v", def)
	}

	lat, lng := 52.1, 13.2
	if err := org.SetMapCenter(d, id, &lat, &lng, "Stab"); err != nil {
		t.Fatal(err)
	}
	o, _ = org.FindByID(d, id)
	if !o.HasMapCenter() || *o.MapLat != 52.1 || *o.MapLng != 13.2 || o.MapName != "Stab" {
		t.Fatalf("mapcenter: %+v", o)
	}
	org.SetMapCenter(d, id, nil, nil, "nur Name")
	o, _ = org.FindByID(d, id)
	if o.HasMapCenter() || o.MapName != "nur Name" {
		t.Fatalf("zurueck auf Standard: %+v", o)
	}

	// Standard-Feeds: 4-1/4-2 eingebaut, 6-1/6-2 aus den Stammdaten (leer ohne Abschnitt "feeds")
	org.SetDefaultFeeds(nil)
	if def := org.DefaultRSSFeeds(); len(def) != 4 || def[2].Title != "Regional" || len(def[2].URLs) != 0 || len(def[3].URLs) != 0 {
		t.Fatalf("eingebaute Feeds: %+v", def)
	}
	org.SetDefaultFeeds(org.FeedsFromStamm([]any{
		map[string]any{"slot": "6-2", "title": "Regio", "urls": []any{"https://regional.example.org/rss", "ftp://nix"}},
		map[string]any{"slot": "", "title": "ohne Slot"},
	}))
	t.Cleanup(func() { org.SetDefaultFeeds(nil) })
	if def := org.DefaultRSSFeeds(); def[3].Title != "Regio" || len(def[3].URLs) != 1 || def[3].URLs[0] != "https://regional.example.org/rss" || len(def[2].URLs) != 0 {
		t.Fatalf("Stammdaten-Feeds: %+v", def)
	}
	if !org.DefaultFeedHostAllowed("regional.example.org") || !org.DefaultFeedHostAllowed("www.tagesschau.de") || org.DefaultFeedHostAllowed("nix.example.org") {
		t.Fatal("DefaultFeedHostAllowed")
	}

	// Feeds: Kachel 4-1 eigener Titel und Adresse, 6-2 nur Titel, Rest Standard
	err = org.SetRSSFeeds(d, id, []org.RSSFeed{
		{Slot: "4-1", Title: "Lokal", URLs: []string{"https://news.example.org/rss"}},
		{Slot: "4-2"},
		{Slot: "6-2", Title: "MDR SA"},
	})
	if err != nil {
		t.Fatal(err)
	}
	o, _ = org.FindByID(d, id)
	if len(o.RSSFeeds) != 2 {
		t.Fatalf("nur gesetzte Slots speichern: %+v", o.RSSFeeds)
	}
	eff := o.EffectiveFeeds()
	if eff[0].Title != "Lokal" || eff[0].URLs[0] != "https://news.example.org/rss" {
		t.Fatalf("4-1: %+v", eff[0])
	}
	if eff[1].Title != "GDACS" || len(eff[1].URLs) != 1 {
		t.Fatalf("4-2 Standard: %+v", eff[1])
	}
	if eff[3].Title != "MDR SA" || len(eff[3].URLs) != 1 || eff[3].URLs[0] != org.DefaultRSSFeeds()[3].URLs[0] {
		t.Fatalf("6-2 Titel eigen, Adresse Standard: %+v", eff[3])
	}
	if !o.FeedHostAllowed("news.example.org") || !o.FeedHostAllowed("NEWS.example.org") || o.FeedHostAllowed("regional.example.org") || (*org.Org)(nil).FeedHostAllowed("x") {
		t.Fatal("FeedHostAllowed")
	}
	for raw, ok := range map[string]bool{
		"https://a.example/feed.xml": true, "http://a.example/f": true, "ftp://a.example/f": false,
		"https://user:pw@a.example/f": false, "https:///f": false, "a.example/f": false, "": false,
		// interne Ziele (SSRF): Loopback, localhost, private und link-lokale Netze
		"http://127.0.0.1:8080/f": false, "http://localhost/f": false, "http://[::1]/f": false,
		"http://10.1.2.3/f": false, "http://192.168.0.1/f": false, "http://169.254.169.254/latest": false,
		"http://100.64.0.1/f": false, "http://0.0.0.0/f": false,
	} {
		if raw == "http://localhost/f" && !org.ValidFeedURL(raw, true) {
			t.Fatal("RSS_ALLOW_PRIVATE muss localhost erlauben")
		}
		if org.ValidFeedURL(raw, false) != ok {
			t.Fatalf("ValidFeedURL(%q) != %v", raw, ok)
		}
	}
	org.SetRSSFeeds(d, id, nil)
	o, _ = org.FindByID(d, id)
	if len(o.RSSFeeds) != 0 {
		t.Fatal("leer muss Standard sein")
	}
}
