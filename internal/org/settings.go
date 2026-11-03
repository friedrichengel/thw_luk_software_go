package org

import (
	"encoding/json"
	"net/url"
	"strings"
	"sync"

	"github.com/friedrichengel/thw_luk_software_go/internal/db"
)

// Einstellungen je Organisation (Org-Admin, Reiter "Einstellungen"):
// Kartenmittelpunkt der Dashboard-Karten und neuer Lagekarten sowie die
// Nachrichten-Feeds der vier Dashboard-Kacheln. Beides hat einen Standard
// (Standort aus den Stammdaten bzw. MAP_CENTER_* des Servers; die festen
// Feeds), den die Organisation ueberschreiben kann.

// RSSFeed: eine Nachrichten-Kachel des Dashboards.
type RSSFeed struct {
	Slot  string   `json:"slot"`  // Kachel: 4-1, 4-2 (Box oben), 6-1, 6-2 (Box unten)
	Title string   `json:"title"` // Beschriftung des Reiters
	URLs  []string `json:"urls"`  // Feed-Adressen (mehrere werden zusammengefuehrt)
}

// FeedSlots: die Kacheln in Anzeige-Reihenfolge.
var FeedSlots = []string{"4-1", "4-2", "6-1", "6-2"}

// Eingebaute Feeds: ueberregional (Tagesschau, GDACS). Die regionalen
// Kacheln 6-1 und 6-2 kommen aus dem Abschnitt "feeds" der Stammdaten
// (SetDefaultFeeds beim Start); ohne ihn bleiben sie leer.
func builtinFeeds() []RSSFeed {
	return []RSSFeed{
		{Slot: "4-1", Title: "Tagesschau", URLs: []string{"https://www.tagesschau.de/infoservices/alle-meldungen-100~rss2.xml"}},
		{Slot: "4-2", Title: "GDACS", URLs: []string{"https://gdacs.org/xml/rss_24h.xml"}},
		{Slot: "6-1", Title: "Regional", URLs: []string{}},
		{Slot: "6-2", Title: "Regional", URLs: []string{}},
	}
}

var (
	stammFeedsMu sync.RWMutex
	stammFeeds   []RSSFeed
)

// SetDefaultFeeds legt die Feeds aus den Stammdaten als Standard fest: je
// Slot ersetzen Titel und Adressen den eingebauten Wert. Einmal beim Start.
func SetDefaultFeeds(feeds []RSSFeed) {
	stammFeedsMu.Lock()
	defer stammFeedsMu.Unlock()
	stammFeeds = nil
	for _, f := range feeds {
		if strings.TrimSpace(f.Slot) == "" {
			continue
		}
		urls := []string{}
		for _, u := range f.URLs {
			if ValidFeedURL(u) {
				urls = append(urls, strings.TrimSpace(u))
			}
		}
		stammFeeds = append(stammFeeds, RSSFeed{Slot: strings.TrimSpace(f.Slot), Title: strings.TrimSpace(f.Title), URLs: urls})
	}
}

// FeedsFromStamm liest den Abschnitt "feeds" der Stammdaten (beliebige
// JSON-Form) als Feed-Liste; ungueltig oder leer = nil.
func FeedsFromStamm(v any) []RSSFeed {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return parseFeeds(string(b))
}

// DefaultRSSFeeds: die Standard-Feeds jeder Organisation (eingebaut,
// ueberlagert durch die Stammdaten).
func DefaultRSSFeeds() []RSSFeed {
	def := builtinFeeds()
	stammFeedsMu.RLock()
	defer stammFeedsMu.RUnlock()
	for _, f := range stammFeeds {
		for i := range def {
			if def[i].Slot == f.Slot {
				if f.Title != "" {
					def[i].Title = f.Title
				}
				def[i].URLs = append([]string{}, f.URLs...)
			}
		}
	}
	return def
}

// DefaultFeedHostAllowed: Host eines Standard-Feeds (eingebaut oder aus den
// Stammdaten); der RSS-Abruf laesst ihn zusaetzlich zu RSS_ALLOWED_HOSTS zu.
func DefaultFeedHostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, f := range DefaultRSSFeeds() {
		for _, raw := range f.URLs {
			if u, err := url.Parse(raw); err == nil && strings.ToLower(u.Hostname()) == host {
				return true
			}
		}
	}
	return false
}

func parseFeeds(s string) []RSSFeed {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []RSSFeed
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// ValidFeedURL: http(s), mit Host, ohne Zugangsdaten, nicht ueberlang.
func ValidFeedURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 500 || strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

// SetRSSFeeds speichert eigene Feeds (nur Slots mit Titel oder Adressen);
// leer = Standard.
func SetRSSFeeds(q db.Q, id int64, feeds []RSSFeed) error {
	var keep []RSSFeed
	for _, f := range feeds {
		if strings.TrimSpace(f.Title) == "" && len(f.URLs) == 0 {
			continue
		}
		if f.URLs == nil {
			f.URLs = []string{}
		}
		keep = append(keep, f)
	}
	val := ""
	if len(keep) > 0 {
		b, err := json.Marshal(keep)
		if err != nil {
			return err
		}
		val = string(b)
	}
	_, err := q.Exec("UPDATE organisationen SET rss_feeds = ?, updated_at = datetime('now') WHERE id = ?", val, id)
	return err
}

// EffectiveFeeds liefert je Kachel den wirksamen Feed: eigener Titel und
// eigene Adressen, wo gesetzt, sonst der Standard. o darf nil sein.
func (o *Org) EffectiveFeeds() []RSSFeed {
	def := DefaultRSSFeeds()
	if o == nil {
		return def
	}
	own := map[string]RSSFeed{}
	for _, f := range o.RSSFeeds {
		own[f.Slot] = f
	}
	for i, f := range def {
		c, ok := own[f.Slot]
		if !ok {
			continue
		}
		if strings.TrimSpace(c.Title) != "" {
			def[i].Title = c.Title
		}
		if len(c.URLs) > 0 {
			def[i].URLs = c.URLs
		}
	}
	return def
}

// FeedHostAllowed: Host einer eigenen Feed-Adresse dieser Organisation (der
// RSS-Abruf laesst ihn zusaetzlich zu RSS_ALLOWED_HOSTS zu).
func (o *Org) FeedHostAllowed(host string) bool {
	if o == nil {
		return false
	}
	host = strings.ToLower(strings.TrimSpace(host))
	for _, f := range o.RSSFeeds {
		for _, raw := range f.URLs {
			if u, err := url.Parse(raw); err == nil && strings.ToLower(u.Hostname()) == host {
				return true
			}
		}
	}
	return false
}

// SetMapCenter speichert den eigenen Kartenmittelpunkt; lat/lng nil =
// Standard (Standort aus den Stammdaten, sonst Server). Der Name gilt auch
// allein (z.B. Bezeichnung des Standorts).
func SetMapCenter(q db.Q, id int64, lat, lng *float64, name string) error {
	var la, ln any
	if lat != nil && lng != nil {
		la, ln = *lat, *lng
	}
	_, err := q.Exec("UPDATE organisationen SET map_lat = ?, map_lng = ?, map_name = ?, updated_at = datetime('now') WHERE id = ?",
		la, ln, strings.TrimSpace(name), id)
	return err
}

// HasMapCenter: eigener Mittelpunkt gesetzt.
func (o *Org) HasMapCenter() bool {
	return o != nil && o.MapLat != nil && o.MapLng != nil
}
