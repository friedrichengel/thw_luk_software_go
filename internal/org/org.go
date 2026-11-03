// Package org: Organisationen (Ortsverband, Regionalstelle, Landesverband,
// Leitung) und ihre Zugaenge zu Fremdsystemen.
//
// Jedes Einsatz-Konto gehoert zu genau einer Organisation. Org-Admins (Flag
// users.org_admin auf einem normalen Einsatz-Konto) verwalten die Nutzer,
// Rechte und Zugaenge ihrer Organisation; der globale Admin legt
// Organisationen an und ordnet Konten zu, sieht die Schluessel aber nicht.
//
// Zugaenge (org_credentials) sind mit einem aus APP_SECRET abgeleiteten
// Schluessel AES-GCM-verschluesselt. Der Klartext verlaesst den Server nie;
// die Oberflaeche zeigt nur "hinterlegt am ... von ..." und die letzten
// Zeichen (hint).
package org

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/stab"
)

// Org ist eine Zeile aus organisationen.
type Org struct {
	ID        int64
	Name      string
	Kurz      string
	Ebene     string
	SteinMode string // poll | webhook
	StammKey  string // Bezug zu stammdaten.json ("lv:<key>", "rst_...", "ov:<key>"), leer = frei angelegt
	// DiveraHiddenGroups: Divera-Gruppen-IDs, die im Dashboard ausgeblendet werden
	DiveraHiddenGroups []int64
	// Eigener Kartenmittelpunkt (nil = Standort aus den Stammdaten, sonst Server)
	MapLat, MapLng *float64
	MapName        string
	// Eigene Nachrichten-Feeds der Dashboard-Kacheln (leer = Standard)
	RSSFeeds  []RSSFeed
	CreatedAt string
	UpdatedAt string
}

const (
	SteinModePoll    = "poll"
	SteinModeWebhook = "webhook"
)

// NormalizeSteinMode liefert einen gueltigen STEIN-Modus (Standard poll).
func NormalizeSteinMode(m string) string {
	if m == SteinModeWebhook {
		return m
	}
	return SteinModePoll
}

// SetSteinMode setzt den STEIN-Abrufmodus einer Organisation.
func SetSteinMode(q db.Q, id int64, mode string) error {
	_, err := q.Exec("UPDATE organisationen SET stein_mode = ?, updated_at = datetime('now') WHERE id = ?", NormalizeSteinMode(mode), id)
	return err
}

// SteinModeOf liest den STEIN-Modus einer Organisation (poll, wenn unbekannt).
func SteinModeOf(q db.Q, id int64) string {
	if id <= 0 {
		return SteinModePoll
	}
	m, err := db.QueryStr(q, "SELECT stein_mode FROM organisationen WHERE id = ?", id)
	if err != nil {
		return SteinModePoll
	}
	return NormalizeSteinMode(m)
}

// EbeneLabel: Anzeigetext der Stabsebene.
func (o *Org) EbeneLabel() string { return stab.EbeneLabel(o.Ebene) }

// Label: Kurzname, sonst Name.
func (o *Org) Label() string {
	if o == nil {
		return ""
	}
	if o.Kurz != "" {
		return o.Kurz
	}
	return o.Name
}

func fromMap(m map[string]any) *Org {
	if m == nil {
		return nil
	}
	return &Org{
		ID:                 db.Int(m, "id"),
		Name:               db.Str(m, "name"),
		Kurz:               db.Str(m, "kurz"),
		Ebene:              db.Str(m, "ebene"),
		SteinMode:          NormalizeSteinMode(db.Str(m, "stein_mode")),
		StammKey:           db.Str(m, "stamm_key"),
		DiveraHiddenGroups: parseIDs(db.Str(m, "divera_hidden_groups")),
		MapLat:             db.FloatPtr(m, "map_lat"),
		MapLng:             db.FloatPtr(m, "map_lng"),
		MapName:            db.Str(m, "map_name"),
		RSSFeeds:           parseFeeds(db.Str(m, "rss_feeds")),
		CreatedAt:          db.Str(m, "created_at"),
		UpdatedAt:          db.Str(m, "updated_at"),
	}
}

func parseIDs(s string) []int64 {
	out := []int64{}
	if strings.TrimSpace(s) == "" {
		return out
	}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// SetDiveraHiddenGroups speichert die im Dashboard ausgeblendeten Divera-Gruppen.
func SetDiveraHiddenGroups(q db.Q, id int64, ids []int64) error {
	if ids == nil {
		ids = []int64{}
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	_, err = q.Exec("UPDATE organisationen SET divera_hidden_groups = ?, updated_at = datetime('now') WHERE id = ?", string(b), id)
	return err
}

// NormalizeEbene liefert eine gueltige Stabsebene (Standard ov).
func NormalizeEbene(e string) string {
	if stab.EbeneExists(e) {
		return e
	}
	return "ov"
}

func All(q db.Q) ([]*Org, error) {
	rows, err := db.QueryMaps(q, "SELECT * FROM organisationen ORDER BY LOWER(name) ASC")
	if err != nil {
		return nil, err
	}
	out := make([]*Org, 0, len(rows))
	for _, m := range rows {
		out = append(out, fromMap(m))
	}
	return out, nil
}

func FindByID(q db.Q, id int64) (*Org, error) {
	if id <= 0 {
		return nil, nil
	}
	m, err := db.QueryMap(q, "SELECT * FROM organisationen WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return fromMap(m), nil
}

func FindByName(q db.Q, name string) (*Org, error) {
	m, err := db.QueryMap(q, "SELECT * FROM organisationen WHERE name = ? LIMIT 1", strings.TrimSpace(name))
	if err != nil {
		return nil, err
	}
	return fromMap(m), nil
}

func Count(q db.Q) (int64, error) {
	return db.QueryInt(q, "SELECT COUNT(*) FROM organisationen")
}

func Create(q db.Q, name, kurz, ebene string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("org: name fehlt")
	}
	res, err := q.Exec("INSERT INTO organisationen (name, kurz, ebene, created_at, updated_at) VALUES (?, ?, ?, datetime('now'), datetime('now'))",
		name, strings.TrimSpace(kurz), NormalizeEbene(ebene))
	if err != nil {
		return 0, err
	}
	return db.LastID(res), nil
}

func Update(q db.Q, id int64, name, kurz, ebene string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("org: name fehlt")
	}
	_, err := q.Exec("UPDATE organisationen SET name = ?, kurz = ?, ebene = ?, updated_at = datetime('now') WHERE id = ?",
		name, strings.TrimSpace(kurz), NormalizeEbene(ebene), id)
	return err
}

// Delete entfernt eine Organisation; Konten und Einsaetze verlieren ihre
// Zuordnung (ON DELETE SET NULL), Zugaenge werden geloescht (CASCADE).
func Delete(q db.Q, id int64) error {
	_, err := q.Exec("DELETE FROM organisationen WHERE id = ?", id)
	return err
}

func UserCount(q db.Q, id int64) (int64, error) {
	return db.QueryInt(q, "SELECT COUNT(*) FROM users WHERE org_id = ?", id)
}

func EinsatzCount(q db.Q, id int64) (int64, error) {
	return db.QueryInt(q, "SELECT COUNT(*) FROM einsaetze WHERE org_id = ? AND deleted_at IS NULL", id)
}

// EnsureDefault bereitet eine bestehende Installation auf das
// Organisationsmodell vor: Gibt es noch keine Organisation, aber bereits
// Einsatz-Konten, wird eine Standard-Organisation (defaultName) angelegt und
// alle Einsatz-Konten ohne Zuordnung bekommen sie. Unabhaengig davon erben
// Einsaetze ohne Organisation die ihres Erstellers. Idempotent, laeuft bei
// jedem Start.
func EnsureDefault(q db.Q, defaultName string) error {
	n, err := Count(q)
	if err != nil {
		return err
	}
	if n == 0 {
		users, err := db.QueryInt(q, "SELECT COUNT(*) FROM users WHERE role IN ('user','leiter')")
		if err != nil {
			return err
		}
		if users > 0 {
			name := strings.TrimSpace(defaultName)
			if name == "" {
				name = "Standard"
			}
			id, err := Create(q, name, "", "lv")
			if err != nil {
				return err
			}
			if _, err := q.Exec("UPDATE users SET org_id = ? WHERE role IN ('user','leiter') AND org_id IS NULL", id); err != nil {
				return err
			}
		}
	}
	_, err = q.Exec("UPDATE einsaetze SET org_id = (SELECT u.org_id FROM users u WHERE u.id = einsaetze.owner_id) WHERE org_id IS NULL")
	return err
}

// SeedResult: Ergebnis der Uebernahme aus den Stammdaten.
type SeedResult struct {
	Created  int
	Existing int
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

// StammKeyLV/StammKeyOV bauen die stamm_key-Werte fuer LV und OV; Regional-
// stellen verwenden ihren Stammdaten-Key (z.B. rst_nord) direkt.
func StammKeyLV(key string) string { return "lv:" + key }
func StammKeyOV(key string) string { return "ov:" + key }

// SeedFromStamm legt Landesverband, Regionalstellen und Ortsverbaende aus den
// Stammdaten (stammdaten.json) als Organisationen an, soweit sie fehlen.
// only beschraenkt die Auswahl auf diese stamm_keys (nil = alle).
// Zuordnung ueber stamm_key, ersatzweise ueber den Namen; eine einzelne,
// frei angelegte LV-Organisation (Standard-Organisation) wird als der LV
// erkannt und bekommt den Stammdaten-Bezug. Nachbar-OVs anderer
// Landesverbaende (extern) werden ausgelassen.
func SeedFromStamm(q db.Q, lv map[string]any, regionen, ovs []map[string]any, only map[string]bool) (SeedResult, error) {
	var res SeedResult
	existing, err := All(q)
	if err != nil {
		return res, err
	}
	byKey := map[string]*Org{}
	byName := map[string]*Org{}
	var soleLV *Org
	lvCount := 0
	for _, o := range existing {
		if o.StammKey != "" {
			byKey[o.StammKey] = o
		}
		byName[strings.ToLower(o.Name)] = o
		if o.Ebene == "lv" {
			lvCount++
			soleLV = o
		}
	}
	ensure := func(key, name, kurz, ebene string) error {
		if key == "" || name == "" {
			return nil
		}
		if only != nil && !only[key] {
			return nil
		}
		if _, ok := byKey[key]; ok {
			res.Existing++
			return nil
		}
		if o, ok := byName[strings.ToLower(name)]; ok {
			res.Existing++
			byKey[key] = o
			_, err := q.Exec("UPDATE organisationen SET stamm_key = ? WHERE id = ? AND stamm_key = ''", key, o.ID)
			return err
		}
		if ebene == "lv" && lvCount == 1 && soleLV != nil && soleLV.StammKey == "" {
			res.Existing++
			byKey[key] = soleLV
			_, err := q.Exec("UPDATE organisationen SET stamm_key = ? WHERE id = ?", key, soleLV.ID)
			return err
		}
		id, err := Create(q, name, kurz, ebene)
		if err != nil {
			return err
		}
		if _, err := q.Exec("UPDATE organisationen SET stamm_key = ? WHERE id = ?", key, id); err != nil {
			return err
		}
		o := &Org{ID: id, Name: name, Kurz: kurz, Ebene: ebene, StammKey: key}
		byKey[key] = o
		byName[strings.ToLower(name)] = o
		res.Created++
		return nil
	}
	if lv != nil {
		if err := ensure(StammKeyLV(str(lv, "key")), str(lv, "name"), str(lv, "kurz"), "lv"); err != nil {
			return res, err
		}
	}
	for _, r := range regionen {
		if ext, _ := r["extern"].(bool); ext {
			continue
		}
		if err := ensure(str(r, "key"), str(r, "name"), str(r, "kurz"), "rst"); err != nil {
			return res, err
		}
	}
	for _, o := range ovs {
		if ext, _ := o["extern"].(bool); ext {
			continue
		}
		name := str(o, "name")
		if name == "" {
			continue
		}
		if err := ensure(StammKeyOV(str(o, "key")), "OV "+name, "", "ov"); err != nil {
			return res, err
		}
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// Zugaenge

const (
	KindStein        = "stein"
	KindDiveraAccess = "divera_access"
	KindDiveraSystem = "divera_system"
)

// KindInfo beschreibt eine Zugangsart fuer die Oberflaeche.
type KindInfo struct {
	Key   string
	Label string
	Hint  string
}

// Kinds in Anzeige-Reihenfolge.
var Kinds = []KindInfo{
	{KindStein, "STEIN API-Schlüssel", "Bearer-Token aus STEIN (Verwaltung, Schnittstellen). Liefert den Live-Status der Einheiten für Kräftekatalog, STEINboard und STEINmap."},
	{KindDiveraAccess, "Divera Zugangsschlüssel (Access-Key)", "Access-Key eines Divera-Nutzers der Einheit. Liest Fahrzeugstatus und Einsätze; mit Schreibrecht in Divera auch Status setzen."},
	{KindDiveraSystem, "Divera System-Schlüssel (optional)", "System-Access-Key der Einheit für die Personalübersicht (pull/all). Fehlt er, wird der Zugangsschlüssel verwendet."},
}

// KindExists prueft eine Zugangsart.
func KindExists(kind string) bool {
	for _, k := range Kinds {
		if k.Key == kind {
			return true
		}
	}
	return false
}

// KindLabel: Anzeigetext einer Zugangsart.
func KindLabel(kind string) string {
	for _, k := range Kinds {
		if k.Key == kind {
			return k.Label
		}
	}
	return kind
}

// ---------------------------------------------------------------------------
// STEIN-Webhook-Secrets (org_webhook_secrets): STEIN vergibt das X-Secret je
// Ortsverband, deshalb eine Zeile pro Ortsverband mit Bezeichnung (label).

// SetWebhookSecret hinterlegt oder ersetzt das Secret eines Ortsverbands.
func SetWebhookSecret(q db.Q, orgID int64, label, plain string, setBy int64, secret string) error {
	label = strings.TrimSpace(label)
	plain = strings.TrimSpace(plain)
	if label == "" {
		return errors.New("org: bezeichnung fehlt")
	}
	if plain == "" {
		return errors.New("org: leeres secret")
	}
	enc, err := Encrypt(secret, plain)
	if err != nil {
		return err
	}
	_, err = q.Exec(`INSERT INTO org_webhook_secrets (org_id, label, value_enc, hint, set_by, set_at) VALUES (?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(org_id, label) DO UPDATE SET value_enc = excluded.value_enc, hint = excluded.hint, set_by = excluded.set_by, set_at = excluded.set_at`,
		orgID, label, enc, hintOf(plain), db.NullIfZero(setBy))
	return err
}

// DeleteWebhookSecret entfernt das Secret eines Ortsverbands.
func DeleteWebhookSecret(q db.Q, orgID int64, label string) error {
	_, err := q.Exec("DELETE FROM org_webhook_secrets WHERE org_id = ? AND label = ?", orgID, strings.TrimSpace(label))
	return err
}

// WebhookSecrets liefert die hinterlegten Webhook-Secrets einer Organisation
// (Klartext, fuer die Pruefung des X-Secret-Headers) mit ihrer Bezeichnung.
func WebhookSecrets(q db.Q, orgID int64, secret string) (map[string]string, error) {
	rows, err := db.QueryMaps(q, "SELECT label, value_enc FROM org_webhook_secrets WHERE org_id = ?", orgID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, r := range rows {
		plain, err := Decrypt(secret, db.Str(r, "value_enc"))
		if err != nil {
			continue
		}
		out[db.Str(r, "label")] = plain
	}
	return out, nil
}

// WebhookSecretInfos: Anzeige-Zustand der Webhook-Secrets (ohne Werte);
// Kind und Label tragen beide die Bezeichnung des Ortsverbands.
func WebhookSecretInfos(q db.Q, orgID int64) ([]CredentialInfo, error) {
	rows, err := db.QueryMaps(q, `SELECT c.label, c.hint, c.set_at, u.username, u.display_name
		FROM org_webhook_secrets c LEFT JOIN users u ON u.id = c.set_by WHERE c.org_id = ? ORDER BY c.label`, orgID)
	if err != nil {
		return nil, err
	}
	out := []CredentialInfo{}
	for _, r := range rows {
		label := db.Str(r, "label")
		info := CredentialInfo{Kind: label, Label: label, Set: true, Hint: db.Str(r, "hint"), SetAt: db.Str(r, "set_at")}
		info.SetByLabel = strings.TrimSpace(db.Str(r, "display_name"))
		if info.SetByLabel == "" {
			info.SetByLabel = db.Str(r, "username")
		}
		out = append(out, info)
	}
	return out, nil
}

// WebhookToken: oeffentlicher Pfadbestandteil der Webhook-URL einer
// Organisation (HMAC aus APP_SECRET, verraet die ID nicht).
func WebhookToken(secret string, orgID int64) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("stein-webhook:" + strconv.FormatInt(orgID, 10)))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// FindByWebhookToken: Organisation zu einem Webhook-Token (oder nil).
func FindByWebhookToken(q db.Q, secret, token string) (*Org, error) {
	if len(token) != 32 {
		return nil, nil
	}
	orgs, err := All(q)
	if err != nil {
		return nil, err
	}
	for _, o := range orgs {
		if hmac.Equal([]byte(WebhookToken(secret, o.ID)), []byte(token)) {
			return o, nil
		}
	}
	return nil, nil
}

// WebhookEvent: eine protokollierte Webhook-Zeile.
type WebhookEvent struct {
	ReceivedAt  string
	EventID     string
	Environment string
	ItemType    string
	Action      string
	ItemID      string
	ParentID    string
	SecretLabel string
}

// LogWebhookEvent protokolliert ein Ereignis und raeumt Eintraege aelter als 7 Tage ab.
func LogWebhookEvent(q db.Q, orgID int64, e WebhookEvent) error {
	if _, err := q.Exec(`INSERT INTO stein_webhook_events (org_id, received_at, event_id, environment, item_type, action, item_id, parent_id, secret_label)
		VALUES (?, datetime('now'), ?, ?, ?, ?, ?, ?, ?)`, orgID, e.EventID, e.Environment, e.ItemType, e.Action, e.ItemID, e.ParentID, e.SecretLabel); err != nil {
		return err
	}
	_, err := q.Exec("DELETE FROM stein_webhook_events WHERE org_id = ? AND received_at < datetime('now', '-7 days')", orgID)
	return err
}

// WebhookEvents: die letzten n Ereignisse einer Organisation.
func WebhookEvents(q db.Q, orgID int64, n int) ([]WebhookEvent, error) {
	rows, err := db.QueryMaps(q, "SELECT * FROM stein_webhook_events WHERE org_id = ? ORDER BY id DESC LIMIT ?", orgID, n)
	if err != nil {
		return nil, err
	}
	out := []WebhookEvent{}
	for _, r := range rows {
		out = append(out, WebhookEvent{ReceivedAt: db.Str(r, "received_at"), EventID: db.Str(r, "event_id"), Environment: db.Str(r, "environment"),
			ItemType: db.Str(r, "item_type"), Action: db.Str(r, "action"), ItemID: db.Str(r, "item_id"), ParentID: db.Str(r, "parent_id"), SecretLabel: db.Str(r, "secret_label")})
	}
	return out, nil
}

// WebhookStats: Anzahl der Ereignisse der letzten Stunde und Zeitpunkt des letzten.
func WebhookStats(q db.Q, orgID int64) (lastHour int64, last string) {
	lastHour, _ = db.QueryInt(q, "SELECT COUNT(*) FROM stein_webhook_events WHERE org_id = ? AND received_at >= datetime('now', '-1 hour')", orgID)
	last, _ = db.QueryStr(q, "SELECT COALESCE(MAX(received_at), '') FROM stein_webhook_events WHERE org_id = ?", orgID)
	return
}

func deriveKey(secret string) []byte {
	sum := sha256.Sum256([]byte("luk-org-credentials:" + secret))
	return sum[:]
}

// Encrypt verschluesselt einen Klartext (AES-256-GCM, Nonce vorangestellt, Base64).
func Encrypt(secret, plain string) (string, error) {
	block, err := aes.NewCipher(deriveKey(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(append(nonce, ct...)), nil
}

// Decrypt macht Encrypt rueckgaengig.
func Decrypt(secret, enc string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(deriveKey(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("org: ciphertext zu kurz")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// hintOf: letzte vier Zeichen zur Wiedererkennung.
func hintOf(plain string) string {
	r := []rune(plain)
	if len(r) <= 4 {
		return ""
	}
	return "…" + string(r[len(r)-4:])
}

// SetCredential hinterlegt oder ersetzt einen Zugang.
func SetCredential(q db.Q, orgID int64, kind, plain string, setBy int64, secret string) error {
	plain = strings.TrimSpace(plain)
	if !KindExists(kind) {
		return errors.New("org: unbekannte Zugangsart")
	}
	if plain == "" {
		return errors.New("org: leerer Schluessel")
	}
	enc, err := Encrypt(secret, plain)
	if err != nil {
		return err
	}
	_, err = q.Exec(`INSERT INTO org_credentials (org_id, kind, value_enc, hint, set_by, set_at) VALUES (?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(org_id, kind) DO UPDATE SET value_enc = excluded.value_enc, hint = excluded.hint, set_by = excluded.set_by, set_at = excluded.set_at`,
		orgID, kind, enc, hintOf(plain), db.NullIfZero(setBy))
	return err
}

func DeleteCredential(q db.Q, orgID int64, kind string) error {
	_, err := q.Exec("DELETE FROM org_credentials WHERE org_id = ? AND kind = ?", orgID, kind)
	return err
}

// Credential liefert den Klartext eines Zugangs oder "" (nicht hinterlegt).
func Credential(q db.Q, orgID int64, kind, secret string) (string, error) {
	if orgID <= 0 {
		return "", nil
	}
	enc, err := db.QueryStr(q, "SELECT value_enc FROM org_credentials WHERE org_id = ? AND kind = ?", orgID, kind)
	if err != nil || enc == "" {
		return "", err
	}
	return Decrypt(secret, enc)
}

// HasCredential: ist ein Zugang hinterlegt (ohne ihn zu entschluesseln)?
func HasCredential(q db.Q, orgID int64, kind string) bool {
	if orgID <= 0 {
		return false
	}
	n, err := db.QueryInt(q, "SELECT COUNT(*) FROM org_credentials WHERE org_id = ? AND kind = ?", orgID, kind)
	return err == nil && n > 0
}

// CredentialInfo: Anzeige-Zustand eines Zugangs (nie der Wert selbst).
type CredentialInfo struct {
	Kind       string
	Label      string
	Help       string
	Set        bool
	Hint       string
	SetAt      string
	SetByLabel string
}

// CredentialInfos liefert fuer jede Zugangsart den Zustand einer Organisation.
func CredentialInfos(q db.Q, orgID int64) ([]CredentialInfo, error) {
	rows, err := db.QueryMaps(q, `SELECT c.kind, c.hint, c.set_at, u.username, u.display_name
		FROM org_credentials c LEFT JOIN users u ON u.id = c.set_by WHERE c.org_id = ?`, orgID)
	if err != nil {
		return nil, err
	}
	byKind := map[string]map[string]any{}
	for _, r := range rows {
		byKind[db.Str(r, "kind")] = r
	}
	out := make([]CredentialInfo, 0, len(Kinds))
	for _, k := range Kinds {
		info := CredentialInfo{Kind: k.Key, Label: k.Label, Help: k.Hint}
		if r, ok := byKind[k.Key]; ok {
			info.Set = true
			info.Hint = db.Str(r, "hint")
			info.SetAt = db.Str(r, "set_at")
			info.SetByLabel = strings.TrimSpace(db.Str(r, "display_name"))
			if info.SetByLabel == "" {
				info.SetByLabel = db.Str(r, "username")
			}
		}
		out = append(out, info)
	}
	return out, nil
}
