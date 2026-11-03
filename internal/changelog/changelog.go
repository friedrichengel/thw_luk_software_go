// Package changelog: "Was ist neu" fuer Nutzer. Eintraege aus
// public/daten/changelog.json (im Binary eingebettet), zuletzt bestaetigter
// Stand je Nutzer in users.changelog_seen.
//
// Nach dem Login zeigt public/js/changelog.js alle Eintraege, die neuer sind
// als der bestaetigte Stand, gemeinsam in einem scrollbaren Dialog - also
// auch mehrere Versionen auf einmal, wenn sich jemand laenger nicht
// angemeldet hat. "Verstanden" schreibt die neueste Version zurueck.
package changelog

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/db"
)

type Entry struct {
	Version string   `json:"version"`
	Date    string   `json:"date"`
	Title   string   `json:"title"`
	Show    bool     `json:"show"`
	Items   []string `json:"items"`
}

type Service struct {
	AppVersion string
	entries    []Entry
}

// New liest die Eintraege aus dem JSON (neueste zuerst).
func New(raw []byte, appVersion string) *Service {
	s := &Service{AppVersion: appVersion}
	var data struct {
		Entries []map[string]any `json:"entries"`
	}
	_ = json.Unmarshal(raw, &data)
	for _, e := range data.Entries {
		v, _ := e["version"].(string)
		if v == "" {
			continue
		}
		en := Entry{Version: v, Show: true}
		en.Date, _ = e["date"].(string)
		en.Title, _ = e["title"].(string)
		if show, ok := e["show"]; ok {
			en.Show = show == true
		}
		en.Items = []string{}
		if items, ok := e["items"].([]any); ok {
			for _, it := range items {
				switch x := it.(type) {
				case string:
					en.Items = append(en.Items, x)
				case float64, bool:
					en.Items = append(en.Items, strings.TrimSpace(strconv.Quote(strings.Trim(strings.TrimSpace(jsonStr(x)), "\""))))
				}
			}
		}
		s.entries = append(s.entries, en)
	}
	sort.SliceStable(s.entries, func(i, j int) bool { return CompareVersions(s.entries[i].Version, s.entries[j].Version) > 0 })
	return s
}

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var reVersionPart = regexp.MustCompile(`\d+|[A-Za-z]+`)

// CompareVersions vergleicht Versionsnummern wie PHP version_compare (vereinfacht).
func CompareVersions(a, b string) int {
	pa := reVersionPart.FindAllString(a, -1)
	pb := reVersionPart.FindAllString(b, -1)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y string
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x == y {
			continue
		}
		nx, ex := strconv.Atoi(x)
		ny, ey := strconv.Atoi(y)
		switch {
		case ex == nil && ey == nil:
			if nx < ny {
				return -1
			}
			return 1
		case ex == nil:
			return 1 // Zahl > Text (3.0 > 3.0-rc)
		case ey == nil:
			return -1
		default:
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Entries: alle Eintraege, neueste zuerst.
func (s *Service) Entries() []Entry {
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

func (s *Service) LatestVersion() string {
	if len(s.entries) == 0 {
		return ""
	}
	return s.entries[0].Version
}

// DisplayVersion: APP_VERSION, oder die neueste Changelog-Version, falls die
// Konfiguration nicht nachgezogen wurde.
func (s *Service) DisplayVersion() string {
	latest := s.LatestVersion()
	if latest != "" && (s.AppVersion == "" || CompareVersions(latest, s.AppVersion) > 0) {
		return latest
	}
	return s.AppVersion
}

// EntriesSince: Eintraege, die neuer sind als seen (leer = alle).
func (s *Service) EntriesSince(seen string) []Entry {
	out := []Entry{}
	for _, e := range s.entries {
		if seen == "" || CompareVersions(e.Version, seen) > 0 {
			out = append(out, e)
		}
	}
	return out
}

func SeenVersion(q db.Q, uid int64) string {
	if uid <= 0 {
		return ""
	}
	v, _ := db.QueryStr(q, "SELECT changelog_seen FROM users WHERE id = ? LIMIT 1", uid)
	return v
}

var reVersion = regexp.MustCompile(`^[0-9][0-9A-Za-z.\-]{0,31}$`)

func MarkSeen(q db.Q, uid int64, version string) error {
	if uid <= 0 || !reVersion.MatchString(version) {
		return nil
	}
	_, err := q.Exec("UPDATE users SET changelog_seen = ?, updated_at = datetime('now') WHERE id = ?", version, uid)
	return err
}

// ClientConfig: Konfiguration fuer public/js/changelog.js (window.LUK_CHANGELOG):
// pending = Overlay nach dem Login automatisch oeffnen.
func (s *Service) ClientConfig(loggedIn bool, seen string, mustChangePassword bool, csrf string) map[string]any {
	latest := s.LatestVersion()
	pending := false
	if loggedIn && latest != "" && !mustChangePassword {
		for _, e := range s.EntriesSince(seen) {
			if e.Show {
				pending = true
				break
			}
		}
	}
	if !loggedIn {
		seen = ""
		csrf = ""
	}
	return map[string]any{
		"version":   s.DisplayVersion(),
		"latest":    latest,
		"seen":      seen,
		"pending":   pending,
		"logged_in": loggedIn,
		"csrf":      csrf,
		"api":       "/api/changelog",
	}
}
