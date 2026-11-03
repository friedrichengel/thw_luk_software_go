package kraefte

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/stab"
)

// Katalog buendelt Stammdaten, STEIN-Status und Bindungen.
type Katalog struct {
	cfg   *config.Config
	stamm map[string]any
	reg   []map[string]any
	ovs   []map[string]any

	// SteinKey liefert den STEIN-Schluessel einer Organisation ("" = keiner);
	// gesetzt vom Server (internal/org). Nil = kein STEIN-Abruf.
	SteinKey func(orgID int64) string
	// SteinMode liefert den Abrufmodus einer Organisation ("poll" oder
	// "webhook"); nil = immer poll.
	SteinMode func(orgID int64) string
	// WebhookDebounce: Wartezeit, in der Webhook-Ereignisse gesammelt werden,
	// bevor die geaenderten Ortsverbaende in EINEM Abruf nachgeholt werden.
	WebhookDebounce time.Duration
	store           *steinStore
	limiter         steinLimiter

	hookMu    sync.Mutex
	hookDirty map[string]int64 // BU-ID -> meldende Organisation
	hookTimer *time.Timer
}

// Stammdaten-Dateien im eingebetteten public/: die echte Datei liegt nicht im
// Repository (jede Installation pflegt ihren Landesverband selbst) und wird
// beim Bauen eingebettet, wenn sie vorliegt; sonst gilt die Beispieldatei.
const (
	StammFile    = "public/daten/stammdaten.json"
	StammExample = "public/daten/stammdaten.example.json"
)

// ReadStamm liest die Stammdaten: aus path (STAMM_FILE), wenn gesetzt, sonst
// die eingebettete stammdaten.json, sonst die Beispieldatei. source nennt die
// verwendete Quelle (fuer das Startprotokoll).
func ReadStamm(fsys fs.FS, path string) (raw []byte, source string, err error) {
	if path != "" {
		raw, err = os.ReadFile(path)
		return raw, path, err
	}
	for _, name := range []string{StammFile, StammExample} {
		raw, err = fs.ReadFile(fsys, name)
		if err == nil {
			return raw, name, nil
		}
	}
	return nil, "", errors.New("keine Stammdaten gefunden (" + StammFile + ", " + StammExample + ")")
}

// New laedt die Stammdaten (ReadStamm) einmalig.
func New(cfg *config.Config, stammJSON []byte) *Katalog {
	k := &Katalog{cfg: cfg, store: newSteinStore(cfg.DataDir), WebhookDebounce: 8 * time.Second,
		hookDirty: map[string]int64{}}
	var data map[string]any
	if json.Unmarshal(stammJSON, &data) != nil || data == nil {
		data = map[string]any{"version": float64(0), "stand": "", "lv": map[string]any{}, "regionen": []any{}, "ovs": []any{}}
	}
	k.reg = filterMaps(data["regionen"])
	k.ovs = filterMaps(data["ovs"])
	data["regionen"] = k.reg
	data["ovs"] = k.ovs
	k.stamm = data
	return k
}

func filterMaps(v any) []map[string]any {
	out := []map[string]any{}
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

// Stamm liefert die Stammdaten (regionen/ovs sind nie null).
func (k *Katalog) Stamm() map[string]any {
	return k.stamm
}

// Regionen liefert die Regionalstellen.
func (k *Katalog) Regionen() []map[string]any { return k.reg }

// OVs liefert alle Ortsverbaende.
func (k *Katalog) OVs() []map[string]any { return k.ovs }

// Karte: Abschnitt "karte" der Stammdaten (Kartenmittelpunkt des
// Landesverbands); ok=false, wenn er fehlt oder unvollstaendig ist.
func (k *Katalog) Karte() (config.MapCenter, bool) {
	m, _ := k.stamm["karte"].(map[string]any)
	if m == nil {
		return config.MapCenter{}, false
	}
	lat, okLat := floatOrNil(m["lat"]).(float64)
	lng, okLng := floatOrNil(m["lng"]).(float64)
	if !okLat || !okLng {
		return config.MapCenter{}, false
	}
	name := mstr(m, "name")
	if name == "" {
		if lv, _ := k.stamm["lv"].(map[string]any); lv != nil {
			name = mstr(lv, "name")
		}
	}
	return config.MapCenter{Lat: lat, Lng: lng, Name: name}, true
}

// Center: wirksamer Kartenmittelpunkt des Servers: MAP_CENTER_*, wenn die
// Umgebung ihn setzt, sonst "karte" aus den Stammdaten, sonst der eingebaute
// Standard.
func (k *Katalog) Center(cfg *config.Config) config.MapCenter {
	if cfg != nil && cfg.MapCenterSet {
		return cfg.MapCenter()
	}
	if c, ok := k.Karte(); ok {
		return c
	}
	if cfg != nil {
		return cfg.MapCenter()
	}
	return config.DefaultMapCenter
}

// Feeds: Abschnitt "feeds" der Stammdaten (regionale Nachrichten-Kacheln),
// roh fuer org.FeedsFromStamm.
func (k *Katalog) Feeds() any { return k.stamm["feeds"] }

func mstr(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "1"
		}
		return ""
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func mtrue(m map[string]any, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != "" && v != "0"
	}
	return false
}

func einheiten(ov map[string]any) []map[string]any {
	return filterMaps(ov["einheiten"])
}

// OV: OV-Zeile zu einem OV-Schluessel ("103" oder "n-fremdstadt").
func (k *Katalog) OV(ovKey string) map[string]any {
	for _, ov := range k.ovs {
		if mstr(ov, "key") == ovKey {
			return ov
		}
	}
	return nil
}

// Lookup: Einheit + OV zu einem kraft_key.
func (k *Katalog) Lookup(key string) (ov map[string]any, einheit map[string]any, ok bool) {
	p := ParseKey(key)
	if p == nil {
		return nil, nil, false
	}
	ov = k.OV(p.OV)
	if ov == nil {
		return nil, nil, false
	}
	for _, e := range einheiten(ov) {
		if mstr(e, "key") == key {
			return ov, e, true
		}
	}
	return nil, nil, false
}

// KeyLabel: lesbare Bezeichnung fuer Log-Texte: "FGr N Musterstadt".
func (k *Katalog) KeyLabel(key string) string {
	if key == "" {
		return ""
	}
	if ov, e, ok := k.Lookup(key); ok {
		s := mstr(e, "short")
		if nr := mstr(e, "nr"); nr != "" && nr != "0" {
			s += " " + nr
		}
		return s + " " + mstr(ov, "name")
	}
	p := ParseKey(key)
	if p != nil {
		return mstr(TypInfo(p.Typ), "short") + " (" + p.OV + ")"
	}
	return key
}

// liveRegions: Regionalstellen mit STEIN-Live-Daten.
func (k *Katalog) liveRegions() map[string]bool {
	out := map[string]bool{}
	for _, r := range k.reg {
		if mtrue(r, "live") {
			out[mstr(r, "key")] = true
		}
	}
	return out
}

// KatalogFor: Stamm + STEIN + Bindungen aus Sicht eines Einsatzes. Den
// STEIN-Zugang stellt die Organisation des Einsatzes; fehlt sie, die des
// Nutzers (userOrgID).
func (k *Katalog) KatalogFor(q db.Q, c *access.Ctx, userOrgID int64) (map[string]any, error) {
	e := c.Einsatz
	steinAktiv := e.SteinAktiv
	orgID := e.OrgIDValue()
	if orgID == 0 {
		orgID = userOrgID
	}
	var st SteinResult
	if steinAktiv {
		st = k.SteinAssets(orgID)
	} else {
		errDisabled := "disabled"
		st = SteinResult{OK: false, Assets: []any{}, Error: &errDisabled}
	}
	byKey := map[string]any{"_fahrzeuge": map[string]any{}}
	if st.OK {
		byKey = k.SteinByKey(st.Assets)
	}
	fahrzeuge, _ := byKey["_fahrzeuge"].(map[string]any)
	delete(byKey, "_fahrzeuge")
	own := e.ID
	bind, err := k.Bindungen(q, c.UID, &own)
	if err != nil {
		return nil, err
	}
	ovs := []map[string]any{}
	for _, ov := range k.ovs {
		list := []map[string]any{}
		for _, u := range einheiten(ov) {
			key := mstr(u, "key")
			var steinInfo any
			if v, ok := byKey[key]; ok {
				steinInfo = v
			}
			b := bind[key]
			if b == nil {
				b = []map[string]any{}
			}
			list = append(list, map[string]any{
				"key":       key,
				"typ":       mstr(u, "typ"),
				"short":     mstr(u, "short"),
				"label":     mstr(u, "label"),
				"nr":        u["nr"],
				"stein":     steinInfo,
				"bindungen": b,
			})
		}
		bu := mstr(ov, "buid")
		var buid any
		if bu != "" {
			buid = bu
		}
		art := mstr(ov, "art")
		if art == "" {
			art = "ov"
		}
		var fz any
		if fahrzeuge != nil {
			if v, ok := fahrzeuge[bu]; ok {
				fz = v
			}
		}
		ovs = append(ovs, map[string]any{
			"key":       mstr(ov, "key"),
			"name":      mstr(ov, "name"),
			"buid":      buid,
			"art":       art,
			"region":    mstr(ov, "region"),
			"lat":       floatOrNil(ov["lat"]),
			"lng":       floatOrNil(ov["lng"]),
			"landkreis": mstr(ov, "landkreis"),
			"live":      st.OK && st.BUs[bu],
			"fahrzeuge": fz,
			"einheiten": list,
		})
	}
	var errAny any
	if st.Error != nil {
		errAny = *st.Error
	}
	var standAny any
	if st.Stand != nil {
		standAny = *st.Stand
	}
	var lat, lng any
	if e.OrtLat != nil {
		lat = *e.OrtLat
	}
	if e.OrtLng != nil {
		lng = *e.OrtLng
	}
	lv := k.stamm["lv"]
	if lv == nil {
		lv = []any{}
	}
	return map[string]any{
		"stand":       time.Now().UTC().Format("2006-01-02T15:04:05-07:00"),
		"stamm_stand": mstr(k.stamm, "stand"),
		"stein":       map[string]any{"ok": st.OK, "stand": standAny, "stale": st.Stale, "error": errAny, "aktiv": steinAktiv},
		"lv":          lv,
		"regionen":    k.reg,
		"typen":       AllTypInfo(),
		"bereich_key": e.BereichKey,
		"ort":         map[string]any{"name": e.OrtName, "lat": lat, "lng": lng},
		"ovs":         ovs,
	}, nil
}

func floatOrNil(v any) any {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		if f, err := strconv.ParseFloat(x, 64); err == nil {
			return f
		}
	}
	return nil
}

// Bereiche: Auswahlliste fuer die Verwaltung: Regionen und OVs als Bereich.
func (k *Katalog) Bereiche() []map[string]any {
	out := []map[string]any{{"key": "", "label": "Gesamter Landesverband (keine Einschränkung)"}}
	for _, r := range k.reg {
		if mtrue(r, "extern") {
			continue
		}
		out = append(out, map[string]any{"key": mstr(r, "key"), "label": mstr(r, "name")})
	}
	for _, ov := range k.ovs {
		art := mstr(ov, "art")
		if art == "" {
			art = "ov"
		}
		if art != "ov" || mtrue(ov, "extern") {
			continue
		}
		out = append(out, map[string]any{"key": "ov:" + mstr(ov, "key"), "label": "OV " + mstr(ov, "name")})
	}
	return out
}

func (k *Katalog) ValidBereich(key string) bool {
	if key == "" {
		return true
	}
	for _, b := range k.Bereiche() {
		if b["key"] == key {
			return true
		}
	}
	return false
}

// EbeneLabel wird fuer Bindungen gebraucht (Re-Export, damit stab nicht ueberall importiert werden muss).
func ebeneLabel(e string) string { return stab.EbeneLabel(e) }
