// Package access: Zugriffsaufloesung fuer einen Einsatz - wer darf was?
//
// Ein Zugriffskontext (Ctx) wird pro Request einmal aufgebaut und an alle
// Repository-Funktionen weitergereicht:
//
//	Einsatz        Zeile aus einsaetze
//	UID            Nutzer-ID (0 bei Freigabelink)
//	UserLabel      Anzeigename fuer Log-Eintraege
//	IsOwner        Ersteller des Einsatzes
//	Roles          Stabsrollen des Nutzers in diesem Einsatz
//	Caps           Capabilities aus den Rollen (stab.CapsFor)
//	IsShare        Zugriff ueber Freigabelink/Verknuepfung (immer nur lesen)
//	ShareLinkID    ID des Freigabelinks
//	CanManage      Mitglieder, Freigabelinks, Abschluss, Loeschen, Meta
//	CanLog         Log einsehen (Leiter Stab oder Eigentuemer)
//	IsOpen         Einsatz offen (geschlossen = komplett schreibgeschuetzt)
//
// Admin-Konten haben grundsaetzlich keinen Zugriff. Ausserdem hier: das
// Ereignis-Log (einsatz_events) und das Zugriffslog (einsatz_access_log).
package access

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"unicode/utf8"

	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/stab"
	"github.com/friedrichengel/thw_luk_software_go/internal/uuid"
)

// Einsatz ist eine Zeile aus einsaetze.
type Einsatz struct {
	ID         int64
	UUID       string
	Title      string
	Ebene      string
	StabName   string
	Note       string
	OwnerID    int64
	Status     string
	VorwarnMin int
	BereichKey string
	OrtName    string
	OrtLat     *float64
	OrtLng     *float64
	SteinAktiv bool
	OrgID      *int64
	Version    int
	CreatedAt  string
	UpdatedAt  string
	ClosedAt   *string
	ClosedBy   *int64
	DeletedAt  *string
	DeletedBy  *int64
}

// OrgIDValue: Organisation des Einsatzes oder 0.
func (e *Einsatz) OrgIDValue() int64 {
	if e == nil || e.OrgID == nil {
		return 0
	}
	return *e.OrgID
}

// EinsatzFromMap wandelt eine Datenbankzeile um.
func EinsatzFromMap(m map[string]any) *Einsatz {
	if m == nil {
		return nil
	}
	vw := 180
	if !db.IsNull(m, "vorwarn_min") {
		vw = int(db.Int(m, "vorwarn_min"))
	}
	sa := true
	if !db.IsNull(m, "stein_aktiv") {
		sa = db.Int(m, "stein_aktiv") == 1
	}
	return &Einsatz{
		ID:         db.Int(m, "id"),
		UUID:       db.Str(m, "uuid"),
		Title:      db.Str(m, "title"),
		Ebene:      db.Str(m, "ebene"),
		StabName:   db.Str(m, "stab_name"),
		Note:       db.Str(m, "note"),
		OwnerID:    db.Int(m, "owner_id"),
		Status:     db.Str(m, "status"),
		VorwarnMin: vw,
		BereichKey: db.Str(m, "bereich_key"),
		OrtName:    db.Str(m, "ort_name"),
		OrtLat:     db.FloatPtr(m, "ort_lat"),
		OrtLng:     db.FloatPtr(m, "ort_lng"),
		SteinAktiv: sa,
		OrgID:      db.IntPtr(m, "org_id"),
		Version:    int(db.Int(m, "version")),
		CreatedAt:  db.Str(m, "created_at"),
		UpdatedAt:  db.Str(m, "updated_at"),
		ClosedAt:   db.StrPtr(m, "closed_at"),
		ClosedBy:   db.IntPtr(m, "closed_by"),
		DeletedAt:  db.StrPtr(m, "deleted_at"),
		DeletedBy:  db.IntPtr(m, "deleted_by"),
	}
}

// Principal beschreibt den angemeldeten Nutzer (aus der Sitzung).
type Principal struct {
	UID   int64
	Role  string
	Label string
}

func (p Principal) IsEinsatzUser() bool { return p.UID > 0 && (p.Role == "user" || p.Role == "leiter") }
func (p Principal) IsAdmin() bool       { return p.Role == "admin" }
func (p Principal) IsLeiter() bool      { return p.Role == "leiter" }

type ViaEinsatz struct {
	UUID  string `json:"uuid"`
	Title string `json:"title"`
}

type Ctx struct {
	Einsatz     *Einsatz
	UID         int64
	UserLabel   string
	IsOwner     bool
	Roles       []string
	Caps        []string
	IsShare     bool
	ShareLinkID *int64
	ViaEinsatz  *ViaEinsatz
	CanManage   bool
	CanLog      bool
	IsOpen      bool
}

func (c *Ctx) EinsatzID() int64 { return c.Einsatz.ID }

// Resolve liefert den Zugriffskontext oder nil, wenn der Einsatz nicht
// existiert oder der aktuelle Besucher nichts darin zu suchen hat.
func Resolve(q db.Q, p Principal, uuidStr string) (*Ctx, error) {
	if !uuid.Valid(uuidStr) {
		return nil, nil
	}
	m, err := db.QueryMap(q, "SELECT * FROM einsaetze WHERE uuid = ? AND deleted_at IS NULL LIMIT 1", uuidStr)
	if err != nil || m == nil {
		return nil, err
	}
	return buildContext(q, p, EinsatzFromMap(m))
}

func ResolveByID(q db.Q, p Principal, id int64) (*Ctx, error) {
	m, err := db.QueryMap(q, "SELECT * FROM einsaetze WHERE id = ? AND deleted_at IS NULL LIMIT 1", id)
	if err != nil || m == nil {
		return nil, err
	}
	return buildContext(q, p, EinsatzFromMap(m))
}

func buildContext(q db.Q, p Principal, e *Einsatz) (*Ctx, error) {
	ctx := &Ctx{
		Einsatz: e,
		UID:     p.UID,
		Roles:   []string{},
		Caps:    []string{},
		IsOpen:  e.Status == "open",
	}
	if !p.IsEinsatzUser() {
		// Ohne Login (oder als Admin) gibt es keinen Zugriff - auch nicht ueber Freigabelinks.
		return nil, nil
	}
	ctx.UserLabel = p.Label
	ctx.IsOwner = e.OwnerID == p.UID
	roles, err := RolesFor(q, e.ID, p.UID)
	if err != nil {
		return nil, err
	}
	ctx.Roles = roles
	if ctx.IsOwner || len(roles) > 0 {
		ctx.Caps = stab.CapsFor(roles)
		ctx.CanManage = ctx.IsOwner
		ctx.CanLog = ctx.IsOwner || stab.HasCap(ctx.Caps, stab.CapLog)
		return ctx, nil
	}
	// Weder Eigentuemer noch Mitglied: Lesezugriff ueber eine Verknuepfung
	// eines eigenen Einsatzes (dort eingetragener Freigabelink, vom
	// Ersteller nicht entzogen)?
	acc, err := linkAccessFor(q, e.ID, p.UID)
	if err != nil {
		return nil, err
	}
	if acc != nil {
		ctx.IsShare = true
		id := acc.shareLinkID
		ctx.ShareLinkID = &id
		ctx.ViaEinsatz = &acc.via
		return ctx, nil
	}
	return nil, nil
}

type linkAccess struct {
	shareLinkID int64
	via         ViaEinsatz
}

// linkAccessFor: Verknuepfung, ueber die uid den Einsatz A lesen darf
// (Mitglied oder Eigentuemer eines Einsatzes B mit freigegebener, gueltiger
// Verknuepfung). Entspricht EinsatzLinkRepo::accessFor.
func linkAccessFor(q db.Q, aID, uid int64) (*linkAccess, error) {
	if uid <= 0 {
		return nil, nil
	}
	row, err := db.QueryMap(q, `SELECT k.share_link_id, b.uuid AS b_uuid, b.title AS b_title
		FROM einsatz_links k
		JOIN einsatz_share_links l ON l.id = k.share_link_id
		JOIN einsaetze a ON a.id = l.einsatz_id
		JOIN einsaetze b ON b.id = k.einsatz_id
		WHERE l.einsatz_id = ? AND k.status = 'approved' AND k.removed_at IS NULL
		AND l.revoked_at IS NULL AND (l.expires_at IS NULL OR l.expires_at > ?)
		AND a.deleted_at IS NULL AND b.deleted_at IS NULL
		AND (b.owner_id = ? OR `+MemberCond("b.id", "?", "?")+`)
		ORDER BY k.id LIMIT 1`, aID, db.Now(), uid, uid, uid)
	if err != nil || row == nil {
		return nil, err
	}
	return &linkAccess{shareLinkID: db.Int(row, "share_link_id"), via: ViaEinsatz{UUID: db.Str(row, "b_uuid"), Title: db.Str(row, "b_title")}}, nil
}

// MemberCond: SQL-Bedingung "Nutzer ist Mitglied des Einsatzes" - persoenlich
// (einsatz_members) oder ueber seine Organisation (einsatz_org_members).
// einsatzExpr ist der Spaltenausdruck der Einsatz-ID, uidExpr der Platzhalter
// fuer die Nutzer-ID; der Aufrufer liefert die Nutzer-ID ZWEIMAL als Argument.
func MemberCond(einsatzExpr, uidExpr1, uidExpr2 string) string {
	return "(EXISTS (SELECT 1 FROM einsatz_members m WHERE m.einsatz_id = " + einsatzExpr + " AND m.user_id = " + uidExpr1 + ")" +
		" OR EXISTS (SELECT 1 FROM einsatz_org_members om JOIN users ou ON ou.id = " + uidExpr2 + " WHERE om.einsatz_id = " + einsatzExpr + " AND om.org_id = ou.org_id))"
}

// RolesFor liefert die Stabsrollen eines Nutzers in einem Einsatz: eigene
// Rollen vereinigt mit den Rollen seiner Organisation (Org-Freigabe).
func RolesFor(q db.Q, einsatzID, uid int64) ([]string, error) {
	rows, err := db.QueryMaps(q, `SELECT role FROM einsatz_members WHERE einsatz_id = ? AND user_id = ?
		UNION SELECT om.role AS role FROM einsatz_org_members om JOIN users ou ON ou.id = ? WHERE om.einsatz_id = ? AND om.org_id = ou.org_id
		ORDER BY role`, einsatzID, uid, uid, einsatzID)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, r := range rows {
		if role := db.Str(r, "role"); stab.Exists(role) {
			out = append(out, role)
		}
	}
	return out, nil
}

// Can prueft eine Capability (nie bei Freigabe oder geschlossenem Einsatz).
func (c *Ctx) Can(cap string) bool {
	if c.IsShare || !c.IsOpen {
		return false
	}
	return stab.HasCap(c.Caps, cap)
}

// CanStatus: darf Status (angefordert/anfahrt/vor Ort) setzen: Harke- oder Kraefte-Recht.
func (c *Ctx) CanStatus() bool {
	return c.Can(stab.CapHarke) || c.Can(stab.CapKraefte)
}

// LogAccess schreibt einen Zugriff ins Zugriffsprotokoll (Seitenaufrufe und
// Erstladen der API-Daten, nicht jeder Sync-Poll).
func LogAccess(q db.Q, c *Ctx, ipHash, page string) {
	var share any
	if c.ShareLinkID != nil {
		share = *c.ShareLinkID
	}
	_, err := q.Exec(`INSERT INTO einsatz_access_log (einsatz_id, ts, user_id, user_label, share_link_id, ip_hash, page) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.Einsatz.ID, db.Now(), db.NullIfZero(c.UID), c.UserLabel, share, ipHash, Truncate(page, 80))
	if err != nil {
		log.Printf("access: logAccess: %v", err)
		return
	}
	if c.ShareLinkID != nil {
		_, _ = q.Exec("UPDATE einsatz_share_links SET last_used_at = ?, use_count = use_count + 1 WHERE id = ?", db.Now(), *c.ShareLinkID)
	}
}

// Event schreibt ein Ereignis ins Aenderungslog und liefert die neue seq.
func Event(q db.Q, c *Ctx, entity, entityID, action, summary string, before, after any) (int64, error) {
	return EventFor(q, c, c.Einsatz.ID, entity, entityID, action, summary, before, after)
}

// EventFor schreibt ein Ereignis in einen (anderen) Einsatz mit dem Nutzer von c.
func EventFor(q db.Q, c *Ctx, einsatzID int64, entity, entityID, action, summary string, before, after any) (int64, error) {
	res, err := q.Exec(`INSERT INTO einsatz_events (einsatz_id, ts, user_id, user_label, entity, entity_id, action, summary, before_json, after_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		einsatzID, db.Now(), db.NullIfZero(c.UID), c.UserLabel, entity, entityID, action, Truncate(summary, 400), JSONOrNull(before), JSONOrNull(after))
	if err != nil {
		return 0, err
	}
	return db.LastID(res), nil
}

// JSONOrNull kodiert einen Wert als JSON-Text (nil bleibt NULL).
func JSONOrNull(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}

// HashToken: HMAC eines Freigabelink-Tokens mit APP_SECRET.
func HashToken(secret, token string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(token))
	return hex.EncodeToString(m.Sum(nil))
}

// Truncate kuerzt auf n Zeichen (Runen, wie mb_substr).
func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// ClientContext: kompakte Zusammenfassung des Kontexts fuer das Frontend.
func ClientContext(c *Ctx) map[string]any {
	e := c.Einsatz
	var closedAt any
	if e.ClosedAt != nil {
		closedAt = *e.ClosedAt
	}
	var lat, lng any
	if e.OrtLat != nil {
		lat = *e.OrtLat
	}
	if e.OrtLng != nil {
		lng = *e.OrtLng
	}
	var via any
	if c.ViaEinsatz != nil {
		via = c.ViaEinsatz
	}
	return map[string]any{
		"uuid":        e.UUID,
		"title":       e.Title,
		"ebene":       e.Ebene,
		"ebene_label": stab.EbeneLabel(e.Ebene),
		"stab_name":   e.StabName,
		"note":        e.Note,
		"version":     e.Version,
		"status":      e.Status,
		"vorwarn_min": e.VorwarnMin,
		"bereich_key": e.BereichKey,
		"ort_name":    e.OrtName,
		"ort_lat":     lat,
		"ort_lng":     lng,
		"stein_aktiv": e.SteinAktiv,
		"org_id":      e.OrgID,
		"owner_id":    e.OwnerID,
		"created_at":  e.CreatedAt,
		"closed_at":   closedAt,
		"is_open":     c.IsOpen,
		"uid":         c.UID,
		"user_label":  c.UserLabel,
		"is_owner":    c.IsOwner,
		"is_share":    c.IsShare,
		"via_einsatz": via,
		"roles":       c.Roles,
		"caps":        c.Caps,
		"can": map[string]any{
			"harke":          c.Can(stab.CapHarke),
			"kraefte":        c.Can(stab.CapKraefte),
			"lagekarte":      c.Can(stab.CapLagekarte),
			"schadenskonten": c.Can(stab.CapSchadenskonten),
			"status":         c.CanStatus(),
			"manage":         c.CanManage && !c.IsShare,
			"log":            c.CanLog && !c.IsShare,
		},
	}
}
