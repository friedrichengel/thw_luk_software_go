package kraefte

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/upstream"
)

// STEIN-Vorrat: ein gemeinsamer Stand je Ortsverband (BU) fuer alle
// Organisationen, dazu je Organisation die Menge der Ortsverbaende, fuer die
// ihr Schluessel Daten liefert ("berechtigt"). Eine Organisation fragt nur
// dann bei STEIN an, wenn einer ihrer berechtigten Ortsverbaende nicht mehr
// frisch ist - haben zwoelf Organisationen denselben Landesverband im Blick,
// geht pro Frist eine Anfrage statt zwoelf. Niemand erhaelt Daten zu
// Ortsverbaenden, fuer die der eigene Schluessel nie Zugriff hatte.
//
// Alte Daten verfallen nicht: Ist STEIN nicht erreichbar, bleibt der letzte
// Stand (mit Zeitstempel, als veraltet markiert), auch ueber einen Neustart
// hinweg (Datei <DATA_DIR>/stein_store.json). STEIN erlaubt 20 Anfragen pro
// Minute und IP und sperrt die IP danach eine Stunde; der Prozess bleibt
// mit einem eigenen Zaehler darunter und pausiert nach Ablehnung.
const (
	// Frische STEIN-Daten hoechstens alle 120 s holen.
	SteinTTL = 120 * time.Second
	// Im Webhook-Modus meldet STEIN Aenderungen selbst; der volle Abruf ist
	// nur noch Sicherheitsnetz.
	SteinTTLWebhook = 30 * time.Minute
	// Spaetestens nach dieser Zeit fragt eine Organisation wieder selbst an,
	// auch wenn andere ihre Ortsverbaende frisch halten (neue Berechtigungen).
	steinRevalidate = time.Hour
	// Abruf-Zeitbudget pro STEIN-Anfrage
	steinTimeout = 8 * time.Second
	// STEIN erlaubt 20 Anfragen pro Minute und IP, danach ist die IP eine
	// Stunde gesperrt. Dieser Prozess bleibt deutlich darunter.
	steinMaxPerMinute = 10
	// Pausen nach Fehlern (Netz / Ablehnung / Limit)
	steinFailCooldown = 2 * time.Minute
	steinAuthCooldown = 30 * time.Minute
	steinRateCooldown = time.Hour
)

// steinLimiter: Zaehler pro gleitender Minute fuer alle STEIN-Abrufe des Prozesses.
type steinLimiter struct {
	mu    sync.Mutex
	times []time.Time
}

// allow meldet, ob jetzt ein weiterer STEIN-Abruf erlaubt ist, und zaehlt ihn.
func (l *steinLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := now.Add(-time.Minute)
	kept := l.times[:0]
	for _, t := range l.times {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	l.times = kept
	if len(l.times) >= steinMaxPerMinute {
		return false
	}
	l.times = append(l.times, now)
	return true
}

// errSteinLocalLimit: lokales Limit erreicht, Abruf nicht versucht.
var errSteinLocalLimit = errors.New("stein: lokales Abruflimit erreicht, Abruf uebersprungen")

// SteinResult ist das Ergebnis von SteinAssets.
type SteinResult struct {
	OK     bool
	Stand  *string // aeltester Stand der gelieferten Ortsverbaende
	Stale  bool    // mindestens ein Ortsverband aelter als die Frist
	Assets []any
	BUs    map[string]bool // Ortsverbaende, fuer die Daten vorliegen
	Error  *string         // not_configured | fetch_failed | auth_failed
}

func strp(s string) *string { return &s }

func isoStamp(t time.Time) *string {
	return strp(t.UTC().Format("2006-01-02T15:04:05-07:00"))
}

// ---------------------------------------------------------------------------
// Vorrat

type steinBU struct {
	Assets []any     `json:"assets"`
	At     time.Time `json:"at"`
}

type steinOrg struct {
	Entitled  map[string]bool `json:"entitled"`
	LastFetch time.Time       `json:"last_fetch"`
	failUntil time.Time
	lastErr   string
}

type steinStore struct {
	mu       sync.Mutex
	bus      map[string]*steinBU
	orgs     map[int64]*steinOrg
	inflight map[int64]chan struct{}
	path     string // "" = keine Persistenz
}

type steinStoreFile struct {
	BUs  map[string]*steinBU  `json:"bus"`
	Orgs map[string]*steinOrg `json:"orgs"`
}

func newSteinStore(dataDir string) *steinStore {
	s := &steinStore{bus: map[string]*steinBU{}, orgs: map[int64]*steinOrg{}, inflight: map[int64]chan struct{}{}}
	if dataDir != "" {
		s.path = filepath.Join(dataDir, "stein_store.json")
		s.load()
	}
	return s
}

func (s *steinStore) load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var f steinStoreFile
	if json.Unmarshal(b, &f) != nil {
		return
	}
	for id, bu := range f.BUs {
		if bu != nil && bu.Assets != nil {
			s.bus[id] = bu
		}
	}
	for k, o := range f.Orgs {
		id, err := strconv.ParseInt(k, 10, 64)
		if err != nil || o == nil {
			continue
		}
		if o.Entitled == nil {
			o.Entitled = map[string]bool{}
		}
		s.orgs[id] = o
	}
}

// save schreibt den Vorrat atomar (best effort; Aufrufer haelt s.mu).
func (s *steinStore) save() {
	if s.path == "" {
		return
	}
	f := steinStoreFile{BUs: s.bus, Orgs: map[string]*steinOrg{}}
	for id, o := range s.orgs {
		f.Orgs[strconv.FormatInt(id, 10)] = o
	}
	b, err := json.Marshal(f)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.path), 0o750)
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, b, 0o640) == nil {
		_ = os.Rename(tmp, s.path)
	}
}

// ---------------------------------------------------------------------------
// Einheitenmengen

// LiveBuIDs: STEIN-Kennungen aller Einheiten in Live-Regionen (Stammdaten).
func (k *Katalog) LiveBuIDs() []string {
	live := k.liveRegions()
	buids := []string{}
	for _, ov := range k.ovs {
		if bu := mstr(ov, "buid"); bu != "" && live[mstr(ov, "region")] {
			buids = append(buids, bu)
		}
	}
	return buids
}

// AllBuIDs: STEIN-Kennungen aller Einheiten des Landesverbands (ohne
// Nachbar-OVs anderer Landesverbaende).
func (k *Katalog) AllBuIDs() []string {
	buids := []string{}
	for _, ov := range k.ovs {
		if mtrue(ov, "extern") {
			continue
		}
		if bu := mstr(ov, "buid"); bu != "" {
			buids = append(buids, bu)
		}
	}
	return buids
}

// HasOwnKey: hat die Organisation einen eigenen STEIN-Schluessel?
func (k *Katalog) HasOwnKey(orgID int64) bool {
	return k.steinKey(orgID) != ""
}

func (k *Katalog) steinKey(orgID int64) string {
	if k.SteinKey == nil {
		return ""
	}
	return strings.TrimSpace(k.SteinKey(orgID))
}

// BaseBuIDs: die Einheitenmenge, die fuer eine Organisation abgefragt wird:
// alle Einheiten des Landesverbands (STEIN liefert, worauf der Schluessel
// der Organisation Zugriff hat).
func (k *Katalog) BaseBuIDs(orgID int64) []string {
	return k.AllBuIDs()
}

func (k *Katalog) steinTTL(orgID int64) time.Duration {
	if k.SteinMode != nil && k.SteinMode(orgID) == "webhook" {
		return SteinTTLWebhook
	}
	return SteinTTL
}

// ---------------------------------------------------------------------------
// Abruf

// fetchSTEIN holt die Assets der BU-IDs mit einem Schluessel. Nicht-2xx und
// Netzfehler kommen als Fehler zurueck; der Aufrufer setzt die Pause.
func (k *Katalog) fetchSTEIN(key string, buids []string) ([]any, error) {
	if !k.limiter.allow(time.Now()) {
		return nil, errSteinLocalLimit
	}
	ctx, cancel := context.WithTimeout(context.Background(), steinTimeout)
	defer cancel()
	u := k.cfg.SteinBaseURL + "/assets/?buIds=" + url.QueryEscape(strings.Join(buids, ","))
	body, _, err := upstream.GetJSON(ctx, u, map[string]string{"Authorization": "Bearer " + key, "Accept": "application/json"})
	if err != nil {
		return nil, err
	}
	assets := parseAssets(body)
	if assets == nil {
		return nil, upstream.ErrUpstream
	}
	return assets, nil
}

// groupByBU ordnet Assets ihren Ortsverbaenden zu.
func groupByBU(assets []any) map[string][]any {
	out := map[string][]any{}
	for _, a := range assets {
		if m, ok := a.(map[string]any); ok {
			if bu := mstr(m, "buId"); bu != "" {
				out[bu] = append(out[bu], a)
			}
		}
	}
	return out
}

// cooldownFor: Pause nach einem fehlgeschlagenen Abruf.
func cooldownFor(err error) (time.Duration, string) {
	var se *upstream.StatusError
	if errors.As(err, &se) {
		switch {
		case se.Status == 429:
			return steinRateCooldown, "auth_failed"
		case upstream.IsAuthStatus(se.Status):
			return steinAuthCooldown, "auth_failed"
		}
	}
	return steinFailCooldown, "fetch_failed"
}

// refreshOrg holt den vollen Stand einer Organisation (alle Einheiten des
// Landesverbands) genau einmal, auch wenn mehrere Anfragen gleichzeitig
// darauf warten, und aktualisiert Vorrat und Berechtigungen.
func (k *Katalog) refreshOrg(orgID int64, key string) {
	s := k.store
	s.mu.Lock()
	if ch, busy := s.inflight[orgID]; busy {
		s.mu.Unlock()
		<-ch
		return
	}
	ch := make(chan struct{})
	s.inflight[orgID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inflight, orgID)
		s.mu.Unlock()
		close(ch)
	}()

	buids := k.AllBuIDs()
	sort.Strings(buids)
	assets, err := k.fetchSTEIN(key, buids)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.orgs[orgID]
	if o == nil {
		o = &steinOrg{Entitled: map[string]bool{}}
		s.orgs[orgID] = o
	}
	if err != nil {
		cd, code := cooldownFor(err)
		o.failUntil = now.Add(cd)
		o.lastErr = code
		return
	}
	grouped := groupByBU(assets)
	o.Entitled = map[string]bool{}
	for bu, list := range grouped {
		s.bus[bu] = &steinBU{Assets: list, At: now}
		o.Entitled[bu] = true
	}
	o.LastFetch = now
	o.failUntil = time.Time{}
	o.lastErr = ""
	s.save()
}

// needsRefresh: muss die Organisation jetzt selbst bei STEIN anfragen?
// (Aufrufer haelt s.mu.)
func (s *steinStore) needsRefresh(orgID int64, ttl time.Duration, now time.Time) bool {
	o := s.orgs[orgID]
	if o == nil || o.LastFetch.IsZero() {
		return true
	}
	if now.Sub(o.LastFetch) > steinRevalidate {
		return true
	}
	for bu := range o.Entitled {
		e := s.bus[bu]
		if e == nil || now.Sub(e.At) > ttl {
			return true
		}
	}
	return false
}

// SteinAssets liefert die STEIN-Assets aller Einheiten aus Sicht einer
// Organisation (Kraeftekatalog eines Einsatzes).
func (k *Katalog) SteinAssets(orgID int64) SteinResult {
	return k.SteinAssetsFor(orgID, nil)
}

// SteinAssetsFor liefert die Assets der gewuenschten BU-IDs (nil = alle) mit
// dem STEIN-Zugang der Organisation: aus dem gemeinsamen Vorrat, der bei
// Bedarf (nicht mehr frisch) mit einem einzigen Abruf aufgefrischt wird.
func (k *Katalog) SteinAssetsFor(orgID int64, requested []string) SteinResult {
	key := k.steinKey(orgID)
	if key == "" {
		return SteinResult{OK: false, Assets: []any{}, BUs: map[string]bool{}, Error: strp("not_configured")}
	}
	want := map[string]bool{}
	for _, b := range requested {
		b = strings.TrimSpace(b)
		if b != "" && validBuID(b) {
			want[b] = true
		}
	}
	if requested != nil && len(want) == 0 {
		return SteinResult{OK: false, Assets: []any{}, BUs: map[string]bool{}, Error: strp("not_configured")}
	}
	ttl := k.steinTTL(orgID)
	s := k.store
	now := time.Now()
	s.mu.Lock()
	need := s.needsRefresh(orgID, ttl, now)
	paused := false
	if o := s.orgs[orgID]; o != nil && now.Before(o.failUntil) {
		paused = true
	}
	s.mu.Unlock()
	if need && !paused {
		k.refreshOrg(orgID, key)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.orgs[orgID]
	if o == nil || len(o.Entitled) == 0 {
		if o != nil && !o.LastFetch.IsZero() && o.lastErr == "" {
			// Abruf war erfolgreich, aber der Schluessel liefert keine Einheit.
			return SteinResult{OK: true, Stand: isoStamp(o.LastFetch), Assets: []any{}, BUs: map[string]bool{}}
		}
		code := "fetch_failed"
		if o != nil && o.lastErr != "" {
			code = o.lastErr
		}
		return SteinResult{OK: false, Assets: []any{}, BUs: map[string]bool{}, Error: strp(code)}
	}
	out := SteinResult{OK: true, Assets: []any{}, BUs: map[string]bool{}}
	var oldest time.Time
	for bu := range o.Entitled {
		if len(want) > 0 && !want[bu] {
			continue
		}
		e := s.bus[bu]
		if e == nil {
			continue
		}
		out.BUs[bu] = true
		out.Assets = append(out.Assets, e.Assets...)
		if oldest.IsZero() || e.At.Before(oldest) {
			oldest = e.At
		}
		if now.Sub(e.At) > ttl {
			out.Stale = true
		}
	}
	if !oldest.IsZero() {
		out.Stand = isoStamp(oldest)
	} else {
		out.Stand = isoStamp(o.LastFetch)
	}
	if out.Stale && o.lastErr != "" {
		out.Error = strp(o.lastErr)
	}
	return out
}

// ForgetStein verwirft Berechtigungen und Pause einer Organisation (nach
// Schluesselwechsel); die Daten der Ortsverbaende bleiben im Vorrat.
func (k *Katalog) ForgetStein(orgID int64) {
	k.store.mu.Lock()
	delete(k.store.orgs, orgID)
	k.store.save()
	k.store.mu.Unlock()
}

// SteinCheck prueft einen STEIN-Schluessel live (ohne Vorrat) mit derselben
// Einheitenmenge wie der regulaere Abruf (alle Einheiten des Landesverbands)
// und meldet, fuer wie viele Einheiten STEIN Daten geliefert hat.
func (k *Katalog) SteinCheck(ctx context.Context, key string) (string, error) {
	buids := k.AllBuIDs()
	if len(buids) == 0 {
		return "", errors.New("keine Einheiten mit STEIN-Kennung in den Stammdaten")
	}
	if !k.limiter.allow(time.Now()) {
		return "", errors.New("zu viele STEIN-Abrufe in dieser Minute, bitte gleich noch einmal prüfen")
	}
	ctx, cancel := context.WithTimeout(ctx, steinTimeout)
	defer cancel()
	u := k.cfg.SteinBaseURL + "/assets/?buIds=" + url.QueryEscape(strings.Join(buids, ","))
	body, _, status, err := upstream.Do(ctx, upstream.Request{URL: u, Headers: map[string]string{"Authorization": "Bearer " + strings.TrimSpace(key), "Accept": "application/json"}})
	if err != nil {
		if upstream.IsAuthStatus(status) {
			return "", fmt.Errorf("STEIN lehnt den Schlüssel ab (HTTP %d)", status)
		}
		return "", fmt.Errorf("STEIN nicht erreichbar oder Fehler (HTTP %d)", status)
	}
	assets := parseAssets(body)
	if assets == nil {
		return "", errors.New("STEIN antwortet, aber nicht mit einer Asset-Liste")
	}
	withData := groupByBU(assets)
	return fmt.Sprintf("Verbindung OK: %d Assets, Daten für %d von %d angefragten Einheiten (der Schlüssel liefert nur die Einheiten, auf die er Zugriff hat)", len(assets), len(withData), len(buids)), nil
}

// ---------------------------------------------------------------------------
// Webhook

// SteinWebhookTouch merkt geaenderte Ortsverbaende (BU-IDs) vor. Die Liste
// ist prozessweit: melden mehrere Organisationen denselben Ortsverband (jede
// hat ihren eigenen Webhook bei STEIN), wird er trotzdem nur einmal
// nachgeholt. Gemerkt wird, welche Organisation gemeldet hat, damit der
// Nachabruf mit einem Schluessel laeuft, der den Ortsverband sehen darf.
func (k *Katalog) SteinWebhookTouch(orgID int64, buIDs []string) {
	known := map[string]bool{}
	for _, b := range k.AllBuIDs() {
		known[b] = true
	}
	k.hookMu.Lock()
	defer k.hookMu.Unlock()
	added := false
	for _, b := range buIDs {
		b = strings.TrimSpace(b)
		if b == "" || !validBuID(b) || !known[b] {
			continue
		}
		if _, dirty := k.hookDirty[b]; !dirty {
			k.hookDirty[b] = orgID
			added = true
		}
	}
	if !added || k.hookTimer != nil {
		return
	}
	k.hookTimer = time.AfterFunc(k.WebhookDebounce, k.flushWebhook)
}

// flushWebhook holt die vorgemerkten Ortsverbaende nach: gruppiert nach dem
// Schluessel, der sie sehen darf, ein Abruf je Gruppe. Doppelte Meldungen
// desselben Ortsverbands (mehrere Organisationen, mehrere Ereignisse in der
// Sammelzeit) fuehren zu genau einem Abruf.
func (k *Katalog) flushWebhook() {
	k.hookMu.Lock()
	dirty := k.hookDirty
	k.hookDirty = map[string]int64{}
	k.hookTimer = nil
	k.hookMu.Unlock()
	if len(dirty) == 0 {
		return
	}
	s := k.store
	groups := map[int64][]string{} // orgID -> BUs
	s.mu.Lock()
	for bu, reporter := range dirty {
		owner := int64(0)
		// bevorzugt die meldende Organisation, sonst irgendeine berechtigte
		if o := s.orgs[reporter]; o != nil && (o.Entitled[bu] || len(o.Entitled) == 0) && k.steinKey(reporter) != "" {
			owner = reporter
		} else {
			for id, o := range s.orgs {
				if o.Entitled[bu] && k.steinKey(id) != "" {
					owner = id
					break
				}
			}
		}
		if owner == 0 && k.steinKey(reporter) != "" {
			owner = reporter
		}
		if owner != 0 {
			groups[owner] = append(groups[owner], bu)
		}
	}
	s.mu.Unlock()

	retry := map[string]int64{}
	for orgID, bus := range groups {
		sort.Strings(bus)
		assets, err := k.fetchSTEIN(k.steinKey(orgID), bus)
		if err != nil {
			if errors.Is(err, errSteinLocalLimit) {
				for _, b := range bus {
					retry[b] = orgID
				}
				continue
			}
			s.mu.Lock()
			if o := s.orgs[orgID]; o != nil {
				cd, code := cooldownFor(err)
				o.failUntil = time.Now().Add(cd)
				o.lastErr = code
			}
			s.mu.Unlock()
			continue
		}
		grouped := groupByBU(assets)
		s.mu.Lock()
		o := s.orgs[orgID]
		if o == nil {
			o = &steinOrg{Entitled: map[string]bool{}, LastFetch: time.Now()}
			s.orgs[orgID] = o
		}
		for bu, list := range grouped {
			s.bus[bu] = &steinBU{Assets: list, At: time.Now()}
			o.Entitled[bu] = true
		}
		s.save()
		s.mu.Unlock()
	}
	if len(retry) > 0 {
		// Lokales Limit: spaeter noch einmal, nichts verlieren.
		k.hookMu.Lock()
		for b, id := range retry {
			if _, ok := k.hookDirty[b]; !ok {
				k.hookDirty[b] = id
			}
		}
		if k.hookTimer == nil {
			k.hookTimer = time.AfterFunc(30*time.Second, k.flushWebhook)
		}
		k.hookMu.Unlock()
	}
}

func validBuID(s string) bool {
	if len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// parseAssets liest die STEIN-Antwort: Liste oder assoziatives Objekt (Werte).
func parseAssets(raw []byte) []any {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	switch x := v.(type) {
	case []any:
		return x
	case map[string]any:
		keys := make([]string, 0, len(x))
		for kk := range x {
			keys = append(keys, kk)
		}
		sort.Strings(keys)
		out := make([]any, 0, len(x))
		for _, kk := range keys {
			out = append(out, x[kk])
		}
		return out
	}
	return nil
}

// parseTime liest die Zeitformate aus STEIN und den Schichten (strtotime-tolerant).
func parseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

type assetRef struct {
	typ string
	idx int
}

// SteinByKey ordnet STEIN-Assets den Einheiten des Stamms zu:
// kraft_key => Status-Info, dazu "_fahrzeuge" => buid => {total, ready}.
func (k *Katalog) SteinByKey(assets []any) map[string]any {
	type buAssets struct {
		order []string
		lists map[string][]map[string]any
	}
	byBu := map[string]*buAssets{}
	fahrzeuge := map[string]any{}
	fzCount := map[string]*[2]int{}
	for _, raw := range assets {
		a, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		bu := mstr(a, "buId")
		if bu == "" {
			continue
		}
		gid := int(toFloat(a["groupId"]))
		if gid == 1 {
			c := fzCount[bu]
			if c == nil {
				c = &[2]int{}
				fzCount[bu] = c
			}
			c[0]++
			if mstr(a, "status") == "ready" {
				c[1]++
			}
			continue
		}
		if gid != 4 {
			continue
		}
		t := NormTyp(mstr(a, "category"))
		if t.Key == "" {
			continue
		}
		b := byBu[bu]
		if b == nil {
			b = &buAssets{lists: map[string][]map[string]any{}}
			byBu[bu] = b
		}
		if _, ok := b.lists[t.Key]; !ok {
			b.order = append(b.order, t.Key)
		}
		b.lists[t.Key] = append(b.lists[t.Key], a)
	}
	for bu, c := range fzCount {
		fahrzeuge[bu] = map[string]any{"total": c[0], "ready": c[1]}
	}
	out := map[string]any{"_fahrzeuge": fahrzeuge}
	now := time.Now()
	for _, ov := range k.ovs {
		bu := mstr(ov, "buid")
		b := byBu[bu]
		if bu == "" || b == nil || len(b.lists) == 0 {
			continue
		}
		for _, l := range b.lists {
			sort.SliceStable(l, func(i, j int) bool { return mstr(l[i], "name") < mstr(l[j], "name") })
		}
		// Einheiten gleichen Typs: in Stamm-Reihenfolge den (nach Name sortierten) Assets zuordnen.
		// Ohne Asset genau dieses Typs genuegt ein noch freies Asset derselben
		// Familie (FGr O statt FGr O (B) usw.), damit eine abweichende Variante
		// in den Stammdaten die Zuordnung nicht verhindert.
		used := map[string]int{}
		taken := map[assetRef]bool{}
		for _, e := range einheiten(ov) {
			tk := mstr(e, "typ")
			list := b.lists[tk]
			var a map[string]any
			if len(list) > 0 {
				idx := used[tk]
				ti := idx
				if ti >= len(list) {
					ti = len(list) - 1
				}
				a = list[ti]
				used[tk] = idx + 1
				taken[assetRef{tk, ti}] = true
			} else {
				fam := TypFamily(tk)
			search:
				for _, ak := range b.order {
					if ak == tk || TypFamily(ak) != fam {
						continue
					}
					for i, cand := range b.lists[ak] {
						if taken[assetRef{ak, i}] {
							continue
						}
						a = cand
						taken[assetRef{ak, i}] = true
						if used[ak] < i+1 {
							used[ak] = i + 1
						}
						break search
					}
				}
			}
			if a == nil {
				continue
			}
			mod := mstr(a, "lastModified")
			var ageH any
			if t, ok := parseTime(mod); ok {
				ageH = int(math.Round(now.Sub(t).Hours()))
			}
			out[mstr(e, "key")] = map[string]any{
				"status":   mstr(a, "status"),
				"ev":       truthy(a["operationReservation"]),
				"name":     mstr(a, "name"),
				"radio":    mstr(a, "radioName"),
				"comment":  mstr(a, "comment"),
				"modified": mod,
				"age_h":    ageH,
			}
		}
	}
	return out
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		var f float64
		for _, c := range x {
			if c < '0' || c > '9' {
				break
			}
			f = f*10 + float64(c-'0')
		}
		return f
	case bool:
		if x {
			return 1
		}
	}
	return 0
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != "" && x != "0"
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}
