// Package divera: Anbindung an Divera 24/7 (app.divera247.com) fuer
// Fahrzeugstatus, Einsaetze (Alarme) und die Personaluebersicht.
//
// Der Zugang gehoert der Organisation (org_credentials: divera_access,
// optional divera_system); das Recht pro Konto (users.divera_access:
// off/read/write) vergibt der Org-Admin. Der Server ruft Divera direkt mit
// dem Schluessel der Organisation auf, cacht die Antworten kurz und kuerzt
// in der Personaluebersicht Vor- und Nachnamen auf Initialen - wie der
// fruehere Node-Proxy. Schluessel erreichen den Browser nie.
package divera

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/org"
	"github.com/friedrichengel/thw_luk_software_go/internal/upstream"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

const (
	allTTL       = 120 * time.Second
	allStale     = 30 * time.Minute
	vehiclesTTL  = 30 * time.Second
	vehicleStale = 15 * time.Minute
	alarmsTTL    = 30 * time.Second
	alarmsStale  = 15 * time.Minute
	timeout      = 20 * time.Second
)

// Source: wirksamer Divera-Zugang eines Kontos.
type Source struct {
	OrgID     int64
	AccessKey string // Fahrzeuge, Einsaetze, Download, Status setzen
	SystemKey string // Personaluebersicht (pull/all); Fallback AccessKey
	Write     bool   // Status setzen erlaubt (Recht "write")
	// HiddenGroups: Divera-Gruppen, die im Dashboard ausgeblendet werden
	// (Sammelgruppen); gepflegt vom Org-Admin.
	HiddenGroups []int64
}

// Resolve bestimmt den Divera-Zugang eines Kontos: Recht read/write und ein
// hinterlegter Zugangsschluessel der Organisation. nil = kein Divera.
func Resolve(q db.Q, u *user.User, secret string) (*Source, error) {
	if u == nil || !u.IsEinsatzUser() || u.DiveraAccess == "off" || u.OrgIDValue() == 0 {
		return nil, nil
	}
	access, err := org.Credential(q, u.OrgIDValue(), org.KindDiveraAccess, secret)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(access) == "" {
		return nil, nil
	}
	system, err := org.Credential(q, u.OrgIDValue(), org.KindDiveraSystem, secret)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(system) == "" {
		system = access
	}
	src := &Source{OrgID: u.OrgIDValue(), AccessKey: strings.TrimSpace(access), SystemKey: strings.TrimSpace(system), Write: u.DiveraAccess == "write"}
	if o, err := org.FindByID(q, u.OrgIDValue()); err == nil && o != nil {
		src.HiddenGroups = o.DiveraHiddenGroups
	}
	return src, nil
}

// Group: eine Divera-Gruppe der Einheit (aus pull/all, data.cluster.group).
type Group struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Sorting int64  `json:"sorting"`
}

// Groups liest die Gruppen der Einheit aus pull/all (gecacht).
func (c *Client) Groups(ctx context.Context, src *Source) ([]Group, error) {
	res, err := c.All(ctx, src)
	if err != nil {
		return nil, err
	}
	var v struct {
		Data struct {
			Cluster struct {
				Group json.RawMessage `json:"group"`
			} `json:"cluster"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &v); err != nil {
		return nil, err
	}
	return parseGroups(v.Data.Cluster.Group), nil
}

// parseGroups: Divera liefert Gruppen als Objekt id -> {name, sorting} oder als Liste.
func parseGroups(raw json.RawMessage) []Group {
	out := []Group{}
	if len(raw) == 0 {
		return out
	}
	type entry struct {
		ID      any    `json:"id"`
		Name    string `json:"name"`
		Sorting any    `json:"sorting"`
	}
	toInt := func(v any) int64 {
		switch x := v.(type) {
		case float64:
			return int64(x)
		case string:
			n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
			return n
		}
		return 0
	}
	var asMap map[string]entry
	if json.Unmarshal(raw, &asMap) == nil {
		for k, e := range asMap {
			if e.Name == "" {
				continue
			}
			id := toInt(e.ID)
			if id == 0 {
				id, _ = strconv.ParseInt(k, 10, 64)
			}
			if id != 0 {
				out = append(out, Group{ID: id, Name: e.Name, Sorting: toInt(e.Sorting)})
			}
		}
	} else {
		var asList []entry
		if json.Unmarshal(raw, &asList) == nil {
			for _, e := range asList {
				if id := toInt(e.ID); id != 0 && e.Name != "" {
					out = append(out, Group{ID: id, Name: e.Name, Sorting: toInt(e.Sorting)})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sorting != out[j].Sorting {
			return out[i].Sorting < out[j].Sorting
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// FilterGroups entfernt ausgeblendete Gruppen aus einer pull/all-Antwort:
// aus data.cluster.group und aus den Gruppenlisten der Personen
// (data.cluster.consumer[*].groups). Bei ungueltigem JSON unveraendert.
func FilterGroups(raw []byte, hidden []int64) []byte {
	if len(hidden) == 0 {
		return raw
	}
	hide := map[string]bool{}
	for _, id := range hidden {
		hide[strconv.FormatInt(id, 10)] = true
	}
	var v map[string]any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	data, _ := v["data"].(map[string]any)
	cluster, _ := data["cluster"].(map[string]any)
	if cluster == nil {
		return raw
	}
	isHidden := func(x any) bool {
		switch n := x.(type) {
		case float64:
			return hide[strconv.FormatInt(int64(n), 10)]
		case string:
			return hide[strings.TrimSpace(n)]
		}
		return false
	}
	switch g := cluster["group"].(type) {
	case map[string]any:
		for k, e := range g {
			id := k
			if m, ok := e.(map[string]any); ok {
				if v, ok := m["id"]; ok && v != nil {
					if n, ok := v.(float64); ok {
						id = strconv.FormatInt(int64(n), 10)
					}
				}
			}
			if hide[id] {
				delete(g, k)
			}
		}
	case []any:
		kept := make([]any, 0, len(g))
		for _, e := range g {
			if m, ok := e.(map[string]any); ok && isHidden(m["id"]) {
				continue
			}
			kept = append(kept, e)
		}
		cluster["group"] = kept
	}
	if consumers, ok := cluster["consumer"].(map[string]any); ok {
		for _, c := range consumers {
			m, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if groups, ok := m["groups"].([]any); ok {
				kept := make([]any, 0, len(groups))
				for _, gid := range groups {
					if !isHidden(gid) {
						kept = append(kept, gid)
					}
				}
				m["groups"] = kept
			}
		}
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

// ClientConfig: Divera-Teil von window.STAB.
func ClientConfig(src *Source) map[string]any {
	if src == nil {
		return map[string]any{"enabled": false, "write": false}
	}
	return map[string]any{"enabled": true, "write": src.Write}
}

// Client spricht die Divera-API an.
type Client struct {
	Cfg   *config.Config
	Cache *upstream.Cache
}

func New(cfg *config.Config) *Client {
	return &Client{Cfg: cfg, Cache: &upstream.Cache{}}
}

// keyHash: Cache-Schluessel ohne den Schluessel selbst.
func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

func (c *Client) get(ctx context.Context, cacheKey string, ttl, stale time.Duration, u string, sanitize bool) (upstream.Result, error) {
	return c.Cache.Get(cacheKey, ttl, stale, func() ([]byte, string, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		body, ctype, err := upstream.GetJSON(ctx, u, nil)
		if err != nil {
			return nil, "", err
		}
		if sanitize {
			body = Sanitize(body)
		}
		return body, ctype, nil
	})
}

// All: Personaluebersicht (pull/all) mit System-Schluessel, Namen gekuerzt.
func (c *Client) All(ctx context.Context, src *Source) (upstream.Result, error) {
	u := c.Cfg.DiveraBaseURL + "/pull/all?accesskey=" + url.QueryEscape(src.SystemKey)
	return c.get(context.WithoutCancel(ctx), "divera:all:"+keyHash(src.SystemKey), allTTL, allStale, u, true)
}

// Vehicles: Fahrzeugstatus (pull/vehicle-status).
func (c *Client) Vehicles(ctx context.Context, src *Source) (upstream.Result, error) {
	u := c.Cfg.DiveraBaseURL + "/pull/vehicle-status?accesskey=" + url.QueryEscape(src.AccessKey)
	return c.get(context.WithoutCancel(ctx), "divera:vehicles:"+keyHash(src.AccessKey), vehiclesTTL, vehicleStale, u, false)
}

// Operations: Einsaetze/Alarme (alarms).
func (c *Client) Operations(ctx context.Context, src *Source) (upstream.Result, error) {
	u := c.Cfg.DiveraBaseURL + "/alarms?accesskey=" + url.QueryEscape(src.AccessKey)
	return c.get(context.WithoutCancel(ctx), "divera:alarms:"+keyHash(src.AccessKey), alarmsTTL, alarmsStale, u, false)
}

var reNumeric = regexp.MustCompile(`^\d{1,12}$`)

// ErrBadID: id-Parameter ungueltig.
var ErrBadID = errors.New("divera: numeric id required")

// Download: Einsatz-PDF (alarms/download/{id}) ohne Cache; liefert Body und Content-Type.
func (c *Client) Download(ctx context.Context, src *Source, id string) ([]byte, string, error) {
	if !reNumeric.MatchString(id) {
		return nil, "", ErrBadID
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	u := c.Cfg.DiveraBaseURL + "/alarms/download/" + id + "?accesskey=" + url.QueryEscape(src.AccessKey)
	body, ctype, _, err := upstream.Do(ctx, upstream.Request{URL: u, Headers: map[string]string{"Accept": "application/pdf, */*"}, MaxBytes: upstream.MaxDownloadBytes})
	if err != nil {
		return nil, "", err
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	return body, ctype, nil
}

// CheckKey prueft einen Schluessel live (ohne Cache): Zugangsschluessel gegen
// vehicle-status, System-Schluessel gegen pull/all. Liefert einen Ergebnistext.
func (c *Client) CheckKey(ctx context.Context, kind, key string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	key = strings.TrimSpace(key)
	var u string
	switch kind {
	case org.KindDiveraSystem:
		u = c.Cfg.DiveraBaseURL + "/pull/all?accesskey=" + url.QueryEscape(key)
	default:
		u = c.Cfg.DiveraBaseURL + "/pull/vehicle-status?accesskey=" + url.QueryEscape(key)
	}
	body, _, status, err := upstream.Do(ctx, upstream.Request{URL: u})
	if err != nil {
		if upstream.IsAuthStatus(status) {
			return "", fmt.Errorf("Divera lehnt den Schlüssel ab (HTTP %d)", status)
		}
		return "", fmt.Errorf("Divera nicht erreichbar oder Fehler (HTTP %d)", status)
	}
	var v struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &v) != nil || !v.Success {
		return "", errors.New("Divera meldet keinen Erfolg (Schlüssel oder Rechte prüfen)")
	}
	if kind == org.KindDiveraSystem {
		var d struct {
			Cluster struct {
				Name string `json:"name"`
			} `json:"cluster"`
		}
		_ = json.Unmarshal(v.Data, &d)
		if d.Cluster.Name != "" {
			return "Verbindung OK: Einheit „" + d.Cluster.Name + "“", nil
		}
		return "Verbindung OK", nil
	}
	var list []any
	if json.Unmarshal(v.Data, &list) == nil {
		return fmt.Sprintf("Verbindung OK: %d Fahrzeuge", len(list)), nil
	}
	return "Verbindung OK", nil
}

// StatusFields: erlaubte Felder beim Status setzen (Whitelist wie bisher).
var StatusFields = []string{"status", "status_id", "status_note", "lat", "lng"}

// SetStatus setzt den Fahrzeugstatus (using-vehicles/set-status/{id}). Die
// Divera-Antwort (Body, Status) wird durchgereicht; Caches fuer Fahrzeuge
// werden verworfen, damit die naechste Abfrage den neuen Stand zeigt.
func (c *Client) SetStatus(ctx context.Context, src *Source, id string, payload map[string]any) ([]byte, int, error) {
	if !reNumeric.MatchString(id) {
		return nil, 0, ErrBadID
	}
	clean := map[string]any{}
	for _, f := range StatusFields {
		if v, ok := payload[f]; ok {
			clean[f] = v
		}
	}
	body, err := json.Marshal(clean)
	if err != nil {
		return nil, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	u := c.Cfg.DiveraBaseURL + "/using-vehicles/set-status/" + id + "?accesskey=" + url.QueryEscape(src.AccessKey)
	resp, _, status, err := upstream.Do(ctx, upstream.Request{Method: "POST", URL: u, Body: body,
		Headers: map[string]string{"Content-Type": "application/json", "Accept": "application/json"}})
	c.Cache.Forget("divera:vehicles:" + keyHash(src.AccessKey))
	var se *upstream.StatusError
	if errors.As(err, &se) {
		// Fachliche Ablehnung durch Divera (z.B. 403): Antwort 1:1 weiterreichen
		return se.Body, se.Status, nil
	}
	if err != nil {
		return nil, 0, err
	}
	return resp, status, nil
}

// ---------------------------------------------------------------------------
// Datensparsamkeit

// abbrev: "Max" -> "M."
func abbrev(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r, _ := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + "."
}

// Sanitize kuerzt in einer pull/all-Antwort firstname/lastname auf Initialen,
// baut stdformat_name daraus neu und maskiert data.user.accesskey. Bei
// ungueltigem JSON bleibt der Body unveraendert.
func Sanitize(raw []byte) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	if m, ok := v.(map[string]any); ok {
		if data, ok := m["data"].(map[string]any); ok {
			if u, ok := data["user"].(map[string]any); ok {
				if _, has := u["accesskey"]; has {
					u["accesskey"] = "123456"
				}
			}
		}
	}
	walk(v)
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

func walk(v any) {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			walk(e)
		}
	case map[string]any:
		first, hasFirst := x["firstname"].(string)
		last, hasLast := x["lastname"].(string)
		if hasFirst {
			x["firstname"] = abbrev(first)
		}
		if hasLast {
			x["lastname"] = abbrev(last)
		}
		if _, ok := x["stdformat_name"].(string); ok {
			f, l := "", ""
			if hasFirst {
				f = abbrev(first)
			}
			if hasLast {
				l = abbrev(last)
			}
			x["stdformat_name"] = strings.TrimSpace(strings.TrimSpace(f + " " + l))
		}
		for k, e := range x {
			if k == "firstname" || k == "lastname" || k == "stdformat_name" {
				continue
			}
			walk(e)
		}
	}
}
