package einsatz

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/org"
	"github.com/friedrichengel/thw_luk_software_go/internal/stab"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
	"github.com/friedrichengel/thw_luk_software_go/internal/uuid"
)

// ------------------------------------------------------------------
// Einsatz
// ------------------------------------------------------------------

func EinsatzByUUID(q db.Q, u string) (*access.Einsatz, error) {
	if !uuid.Valid(u) {
		return nil, nil
	}
	m, err := db.QueryMap(q, "SELECT * FROM einsaetze WHERE uuid = ? AND deleted_at IS NULL LIMIT 1", u)
	if err != nil || m == nil {
		return nil, err
	}
	return access.EinsatzFromMap(m), nil
}

// ListItem ist ein Eintrag der Einsatzliste eines Nutzers.
type ListItem struct {
	UUID        string   `json:"uuid"`
	Title       string   `json:"title"`
	Ebene       string   `json:"ebene"`
	EbeneLabel  string   `json:"ebene_label"`
	StabName    string   `json:"stab_name"`
	Note        string   `json:"note"`
	Status      string   `json:"status"`
	VorwarnMin  int      `json:"vorwarn_min"`
	OwnerID     int64    `json:"owner_id"`
	OwnerLabel  string   `json:"owner_label"`
	IsOwner     bool     `json:"is_owner"`
	Roles       []string `json:"roles"`
	RoleLabels  []string `json:"role_labels"`
	Caps        []string `json:"caps"`
	MemberCount int      `json:"member_count"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	ClosedAt    *string  `json:"closed_at"`
}

// ListForUser: Einsaetze, die der Nutzer sehen darf (Eigentuemer oder Mitglied).
func ListForUser(q db.Q, uid int64) ([]ListItem, error) {
	rows, err := db.QueryMaps(q, `SELECT e.*, u.username AS owner_username, u.display_name AS owner_display,
		(SELECT COUNT(DISTINCT m.user_id) FROM einsatz_members m WHERE m.einsatz_id = e.id) AS member_count
		FROM einsaetze e LEFT JOIN users u ON u.id = e.owner_id
		WHERE e.deleted_at IS NULL AND (e.owner_id = ? OR `+access.MemberCond("e.id", "?", "?")+`)
		ORDER BY (e.status = 'open') DESC, e.updated_at DESC`, uid, uid, uid)
	if err != nil {
		return nil, err
	}
	out := []ListItem{}
	for _, r := range rows {
		e := access.EinsatzFromMap(r)
		roles, err := access.RolesFor(q, e.ID, uid)
		if err != nil {
			return nil, err
		}
		ownerLabel := strings.TrimSpace(db.Str(r, "owner_display"))
		if ownerLabel == "" {
			ownerLabel = db.Str(r, "owner_username")
			if ownerLabel == "" {
				ownerLabel = "?"
			}
		}
		labels := []string{}
		for _, x := range roles {
			labels = append(labels, stab.Short(x))
		}
		out = append(out, ListItem{
			UUID: e.UUID, Title: e.Title, Ebene: e.Ebene, EbeneLabel: stab.EbeneLabel(e.Ebene),
			StabName: e.StabName, Note: e.Note, Status: e.Status, VorwarnMin: e.VorwarnMin,
			OwnerID: e.OwnerID, OwnerLabel: ownerLabel, IsOwner: e.OwnerID == uid,
			Roles: roles, RoleLabels: labels, Caps: stab.CapsFor(roles),
			MemberCount: int(db.Int(r, "member_count")), CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt, ClosedAt: e.ClosedAt,
		})
	}
	return out, nil
}

// CreateEinsatz legt einen Einsatz an; der Ersteller bekommt Leiter Stab.
func CreateEinsatz(q db.Q, p access.Principal, title, ebene, stabName, note string) (string, error) {
	u := uuid.V4()
	now := db.Now()
	err := db.InTx(q, func(q db.Q) error {
		// Die Organisation des Einsatzes ist die des Erstellers (bestimmt den STEIN-Zugang).
		res, err := q.Exec(`INSERT INTO einsaetze (uuid, title, ebene, stab_name, note, owner_id, org_id, status, version, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, (SELECT org_id FROM users WHERE id = ?), 'open', 1, ?, ?)`, u, title, ebene, stabName, note, p.UID, p.UID, now, now)
		if err != nil {
			return err
		}
		id := db.LastID(res)
		if _, err := q.Exec("INSERT INTO einsatz_members (einsatz_id, user_id, role, added_by, added_at) VALUES (?, ?, ?, ?, ?)", id, p.UID, "ltr_stab", p.UID, now); err != nil {
			return err
		}
		ctx, err := access.ResolveByID(q, p, id)
		if err != nil {
			return err
		}
		if ctx != nil {
			if _, err := access.Event(q, ctx, "einsatz", u, "insert",
				"Einsatz „"+title+"\" angelegt ("+stab.EbeneLabel(ebene)+")",
				nil, map[string]any{"title": title, "ebene": ebene, "stab_name": stabName}); err != nil {
				return err
			}
			if _, err := access.Event(q, ctx, "member", fmt.Sprint(p.UID), "update",
				"Rolle „Leiter Stab\" an "+p.Label+" vergeben (Ersteller)", []any{}, []string{"ltr_stab"}); err != nil {
				return err
			}
		}
		return nil
	})
	return u, err
}

func TouchEinsatz(q db.Q, einsatzID int64) error {
	_, err := q.Exec("UPDATE einsaetze SET updated_at = ? WHERE id = ?", db.Now(), einsatzID)
	return err
}

// ------------------------------------------------------------------
// Mitglieder (Stabsrollen)
// ------------------------------------------------------------------

type Member struct {
	UserID      int64    `json:"user_id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Label       string   `json:"label"`
	AccountRole string   `json:"account_role"`
	Roles       []string `json:"roles"`
}

func Members(q db.Q, einsatzID int64) ([]Member, error) {
	rows, err := db.QueryMaps(q, `SELECT m.user_id, m.role, u.username, u.display_name, u.role AS account_role
		FROM einsatz_members m JOIN users u ON u.id = m.user_id
		WHERE m.einsatz_id = ? ORDER BY LOWER(COALESCE(u.display_name, u.username)), m.role`, einsatzID)
	if err != nil {
		return nil, err
	}
	out := []Member{}
	idx := map[int64]int{}
	for _, r := range rows {
		id := db.Int(r, "user_id")
		i, ok := idx[id]
		if !ok {
			dn := db.Str(r, "display_name")
			label := strings.TrimSpace(dn)
			if label == "" {
				label = db.Str(r, "username")
			}
			out = append(out, Member{UserID: id, Username: db.Str(r, "username"), DisplayName: dn, Label: label, AccountRole: db.Str(r, "account_role"), Roles: []string{}})
			i = len(out) - 1
			idx[id] = i
		}
		if role := db.Str(r, "role"); stab.Exists(role) {
			out[i].Roles = append(out[i].Roles, role)
		}
	}
	return out, nil
}

// OrgMember: Freigabe an eine ganze Organisation (alle ihre Einsatz-Konten
// erhalten diese Stabsrollen). Divera und andere Hilfsmittel sind davon
// nicht betroffen, sie haengen am eigenen Konto und dessen Organisation.
type OrgMember struct {
	OrgID int64    `json:"org_id"`
	Name  string   `json:"name"`
	Label string   `json:"label"`
	Ebene string   `json:"ebene"`
	Roles []string `json:"roles"`
}

// OrgMembers liefert die an Organisationen freigegebenen Rollen eines Einsatzes.
func OrgMembers(q db.Q, einsatzID int64) ([]OrgMember, error) {
	rows, err := db.QueryMaps(q, `SELECT om.org_id, om.role, o.name, o.kurz, o.ebene
		FROM einsatz_org_members om JOIN organisationen o ON o.id = om.org_id
		WHERE om.einsatz_id = ? ORDER BY LOWER(o.name), om.role`, einsatzID)
	if err != nil {
		return nil, err
	}
	out := []OrgMember{}
	idx := map[int64]int{}
	for _, r := range rows {
		id := db.Int(r, "org_id")
		i, ok := idx[id]
		if !ok {
			label := strings.TrimSpace(db.Str(r, "kurz"))
			if label == "" {
				label = db.Str(r, "name")
			}
			out = append(out, OrgMember{OrgID: id, Name: db.Str(r, "name"), Label: label, Ebene: db.Str(r, "ebene"), Roles: []string{}})
			i = len(out) - 1
			idx[id] = i
		}
		if role := db.Str(r, "role"); stab.Exists(role) {
			out[i].Roles = append(out[i].Roles, role)
		}
	}
	return out, nil
}

// SetOrgMemberRoles setzt die Rollen einer Organisation im Einsatz (leer = Freigabe entfernen).
func SetOrgMemberRoles(q db.Q, c *access.Ctx, orgID int64, rolesIn any) (*OrgMember, error) {
	einsatzID := c.Einsatz.ID
	o, err := org.FindByID(q, orgID)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, apierr.New("org_not_found", 404)
	}
	roles := stab.NormalizeRoles(rolesIn)
	var out *OrgMember
	err = db.InTx(q, func(q db.Q) error {
		beforeRows, err := db.QueryMaps(q, "SELECT role FROM einsatz_org_members WHERE einsatz_id = ? AND org_id = ? ORDER BY role", einsatzID, orgID)
		if err != nil {
			return err
		}
		before := []string{}
		for _, r := range beforeRows {
			before = append(before, db.Str(r, "role"))
		}
		if _, err := q.Exec("DELETE FROM einsatz_org_members WHERE einsatz_id = ? AND org_id = ?", einsatzID, orgID); err != nil {
			return err
		}
		for _, r := range roles {
			if _, err := q.Exec("INSERT INTO einsatz_org_members (einsatz_id, org_id, role, added_by, added_at) VALUES (?, ?, ?, ?, ?)", einsatzID, orgID, r, db.NullIfZero(c.UID), db.Now()); err != nil {
				return err
			}
		}
		label := o.Name
		var summary, action string
		if len(roles) > 0 {
			summary = "Rollen der Organisation „" + label + "\": " + fmtRoles(before) + " → " + fmtRoles(roles)
			action = "update"
		} else {
			summary = "Freigabe für Organisation „" + label + "\" entfernt (vorher: " + fmtRoles(before) + ")"
			action = "delete"
		}
		if _, err := access.Event(q, c, "org_member", fmt.Sprint(orgID), action, summary,
			map[string]any{"roles": before, "label": label}, map[string]any{"roles": roles, "label": label}); err != nil {
			return err
		}
		if err := TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = &OrgMember{OrgID: orgID, Name: o.Name, Label: o.Label(), Ebene: o.Ebene, Roles: roles}
		return nil
	})
	return out, err
}

type MemberResult struct {
	UserID int64    `json:"user_id"`
	Roles  []string `json:"roles"`
	Label  string   `json:"label"`
}

func fmtRoles(rs []string) string {
	if len(rs) == 0 {
		return "keine"
	}
	parts := []string{}
	for _, r := range rs {
		parts = append(parts, stab.Short(r))
	}
	return strings.Join(parts, ", ")
}

// SetMemberRoles setzt die Rollen eines Nutzers (leere Liste = aus dem Einsatz entfernen).
func SetMemberRoles(q db.Q, c *access.Ctx, userID int64, rolesIn any) (*MemberResult, error) {
	einsatzID := c.Einsatz.ID
	u, err := user.FindByID(q, userID)
	if err != nil {
		return nil, err
	}
	if u == nil || !u.IsEinsatzUser() {
		return nil, apierr.New("user_not_found", 404)
	}
	roles := stab.NormalizeRoles(rolesIn)
	var out *MemberResult
	err = db.InTx(q, func(q db.Q) error {
		before, err := access.RolesFor(q, einsatzID, userID)
		if err != nil {
			return err
		}
		if _, err := q.Exec("DELETE FROM einsatz_members WHERE einsatz_id = ? AND user_id = ?", einsatzID, userID); err != nil {
			return err
		}
		for _, r := range roles {
			if _, err := q.Exec("INSERT INTO einsatz_members (einsatz_id, user_id, role, added_by, added_at) VALUES (?, ?, ?, ?, ?)", einsatzID, userID, r, c.UID, db.Now()); err != nil {
				return err
			}
		}
		label := user.Label(u)
		var summary, action string
		if len(roles) > 0 {
			summary = "Rollen von " + label + ": " + fmtRoles(before) + " → " + fmtRoles(roles)
			action = "update"
		} else {
			summary = label + " aus dem Einsatz entfernt (vorher: " + fmtRoles(before) + ")"
			action = "delete"
		}
		if _, err := access.Event(q, c, "member", fmt.Sprint(userID), action, summary,
			map[string]any{"roles": before, "label": label}, map[string]any{"roles": roles, "label": label}); err != nil {
			return err
		}
		if err := TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = &MemberResult{UserID: userID, Roles: roles, Label: label}
		return nil
	})
	return out, err
}

// ------------------------------------------------------------------
// Tree lesen
// ------------------------------------------------------------------

type Tree struct {
	Nodes      []*Node     `json:"nodes"`
	Mitglieder []*Mitglied `json:"mitglieder"`
	Schichten  []*Schicht  `json:"schichten"`
	Seq        int64       `json:"seq"`
}

// TreeOf: vollstaendiger Tree (nur nicht geloeschte Zeilen) + aktuelle seq.
func TreeOf(q db.Q, einsatzID int64, includeDeleted bool) (*Tree, error) {
	del := " AND deleted_at IS NULL"
	if includeDeleted {
		del = ""
	}
	t := &Tree{Nodes: []*Node{}, Mitglieder: []*Mitglied{}, Schichten: []*Schicht{}}
	rows, err := db.QueryMaps(q, "SELECT * FROM einsatz_nodes WHERE einsatz_id = ?"+del+" ORDER BY sort_order, created_at", einsatzID)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		t.Nodes = append(t.Nodes, NodeRow(r))
	}
	rows, err = db.QueryMaps(q, "SELECT * FROM einsatz_mitglieder WHERE einsatz_id = ?"+del+" ORDER BY sort_order, created_at", einsatzID)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		t.Mitglieder = append(t.Mitglieder, MitgliedRow(r))
	}
	rows, err = db.QueryMaps(q, "SELECT * FROM einsatz_schichten WHERE einsatz_id = ?"+del+" ORDER BY von, created_at", einsatzID)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		t.Schichten = append(t.Schichten, SchichtRow(r))
	}
	t.Seq, err = CurrentSeq(q, einsatzID)
	return t, err
}

func CurrentSeq(q db.Q, einsatzID int64) (int64, error) {
	return db.QueryInt(q, "SELECT COALESCE(MAX(seq), 0) FROM einsatz_events WHERE einsatz_id = ?", einsatzID)
}

// Event ist ein Eintrag des Aenderungslogs (Sync-Feed und Log-Seite).
type Event struct {
	Seq       int64  `json:"seq"`
	Ts        string `json:"ts"`
	UserID    *int64 `json:"user_id"`
	UserLabel string `json:"user_label"`
	Entity    string `json:"entity"`
	EntityID  string `json:"entity_id"`
	Action    string `json:"action"`
	Summary   string `json:"summary"`
	Before    any    `json:"before,omitempty"`
	After     any    `json:"after"`
	Imported  *bool  `json:"imported,omitempty"`
}

func decodeJSON(s *string) any {
	if s == nil {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(*s), &v); err != nil {
		return nil
	}
	return v
}

// EventsSince: Ereignisse seit seq (fuer Live-Sync).
func EventsSince(q db.Q, einsatzID, since int64, limit int) ([]Event, error) {
	rows, err := db.QueryMaps(q, fmt.Sprintf(`SELECT seq, ts, user_id, user_label, entity, entity_id, action, summary, after_json
		FROM einsatz_events WHERE einsatz_id = ? AND seq > ? ORDER BY seq ASC LIMIT %d`, limit), einsatzID, since)
	if err != nil {
		return nil, err
	}
	out := []Event{}
	for _, r := range rows {
		out = append(out, Event{
			Seq: db.Int(r, "seq"), Ts: db.Str(r, "ts"), UserID: db.IntPtr(r, "user_id"), UserLabel: db.Str(r, "user_label"),
			Entity: db.Str(r, "entity"), EntityID: db.Str(r, "entity_id"), Action: db.Str(r, "action"), Summary: db.Str(r, "summary"),
			After: decodeJSON(db.StrPtr(r, "after_json")),
		})
	}
	return out, nil
}

// LogEvent ist ein vollstaendiger Log-Eintrag (mit before/after).
type LogEvent struct {
	Seq       int64  `json:"seq"`
	Ts        string `json:"ts"`
	UserID    *int64 `json:"user_id"`
	UserLabel string `json:"user_label"`
	Entity    string `json:"entity"`
	EntityID  string `json:"entity_id"`
	Action    string `json:"action"`
	Summary   string `json:"summary"`
	Before    any    `json:"before"`
	After     any    `json:"after"`
	Imported  bool   `json:"imported"`
}

// Log: vollstaendiges Log (neueste zuerst) fuer die Log-Seite und den Export.
func Log(q db.Q, einsatzID int64, limit, offset int) ([]LogEvent, error) {
	rows, err := db.QueryMaps(q, fmt.Sprintf("SELECT * FROM einsatz_events WHERE einsatz_id = ? ORDER BY seq DESC LIMIT %d OFFSET %d", limit, offset), einsatzID)
	if err != nil {
		return nil, err
	}
	out := []LogEvent{}
	for _, r := range rows {
		out = append(out, LogEvent{
			Seq: db.Int(r, "seq"), Ts: db.Str(r, "ts"), UserID: db.IntPtr(r, "user_id"), UserLabel: db.Str(r, "user_label"),
			Entity: db.Str(r, "entity"), EntityID: db.Str(r, "entity_id"), Action: db.Str(r, "action"), Summary: db.Str(r, "summary"),
			Before: decodeJSON(db.StrPtr(r, "before_json")), After: decodeJSON(db.StrPtr(r, "after_json")),
			Imported: db.Int(r, "imported") == 1,
		})
	}
	return out, nil
}

type AccessEntry struct {
	ID          int64  `json:"id"`
	Ts          string `json:"ts"`
	UserID      *int64 `json:"user_id"`
	UserLabel   string `json:"user_label"`
	ShareLinkID *int64 `json:"share_link_id"`
	ShareLabel  string `json:"share_label"`
	IPHash      string `json:"ip_hash"`
	Page        string `json:"page"`
}

func AccessLog(q db.Q, einsatzID int64, limit int) ([]AccessEntry, error) {
	rows, err := db.QueryMaps(q, fmt.Sprintf(`SELECT a.*, l.label AS share_label FROM einsatz_access_log a
		LEFT JOIN einsatz_share_links l ON l.id = a.share_link_id
		WHERE a.einsatz_id = ? ORDER BY a.id DESC LIMIT %d`, limit), einsatzID)
	if err != nil {
		return nil, err
	}
	out := []AccessEntry{}
	for _, r := range rows {
		h := db.Str(r, "ip_hash")
		if len(h) > 12 {
			h = h[:12]
		}
		out = append(out, AccessEntry{
			ID: db.Int(r, "id"), Ts: db.Str(r, "ts"), UserID: db.IntPtr(r, "user_id"), UserLabel: db.Str(r, "user_label"),
			ShareLinkID: db.IntPtr(r, "share_link_id"), ShareLabel: db.Str(r, "share_label"), IPHash: h, Page: db.Str(r, "page"),
		})
	}
	return out, nil
}

// ------------------------------------------------------------------
// Versioniertes Update (generisch)
// ------------------------------------------------------------------

// RowNormalizer wandelt eine rohe Zeile einer Tabelle in ihre API-Form.
type RowNormalizer func(map[string]any) any

var normalizers = map[string]RowNormalizer{
	"einsatz_nodes":      func(m map[string]any) any { return NodeRow(m) },
	"einsatz_mitglieder": func(m map[string]any) any { return MitgliedRow(m) },
	"einsatz_schichten":  func(m map[string]any) any { return SchichtRow(m) },
}

// RegisterRow meldet die API-Form einer weiteren Tabelle an (Lagekarte, Schadenskonten).
func RegisterRow(table string, fn RowNormalizer) {
	normalizers[table] = fn
}

func NormalizeRow(table string, m map[string]any) any {
	if fn, ok := normalizers[table]; ok {
		return fn(m)
	}
	return m
}

// RowByID liefert eine nicht geloeschte Zeile oder nil.
func RowByID(q db.Q, table, id string) (map[string]any, error) {
	m, err := db.QueryMap(q, "SELECT * FROM "+table+" WHERE id = ? LIMIT 1", id)
	if err != nil || m == nil {
		return nil, err
	}
	if !db.IsNull(m, "deleted_at") {
		return nil, nil
	}
	return m, nil
}

func RowByIDIncludingDeleted(q db.Q, table, id string) (map[string]any, error) {
	return db.QueryMap(q, "SELECT * FROM "+table+" WHERE id = ? LIMIT 1", id)
}

// UpdateVersioned: UPDATE table SET ... WHERE id=? AND version=? AND deleted_at IS NULL.
// Wirft 409 conflict mit aktueller Zeile, wenn die Version nicht passt.
// Liefert die neue Zeile (roh).
func UpdateVersioned(q db.Q, table, id string, version int, fields Fields, uid int64) (map[string]any, error) {
	cur, err := RowByID(q, table, id)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, apierr.New("not_found", 404)
	}
	if int(db.Int(cur, "version")) != version {
		return nil, apierr.Conflict(NormalizeRow(table, cur))
	}
	set := []string{}
	params := []any{}
	for _, f := range fields {
		set = append(set, f.K+" = ?")
		params = append(params, f.V)
	}
	set = append(set, "version = version + 1", "updated_at = ?", "updated_by = ?")
	params = append(params, db.Now(), db.NullIfZero(uid), id, version)
	res, err := q.Exec("UPDATE "+table+" SET "+strings.Join(set, ", ")+" WHERE id = ? AND version = ? AND deleted_at IS NULL", params...)
	if err != nil {
		return nil, err
	}
	if db.Affected(res) == 0 {
		cur, err = RowByID(q, table, id)
		if err != nil {
			return nil, err
		}
		if cur == nil {
			return nil, apierr.New("not_found", 404)
		}
		return nil, apierr.Conflict(NormalizeRow(table, cur))
	}
	row, err := RowByID(q, table, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return map[string]any{}, nil
	}
	return row, nil
}

// SoftDelete setzt deleted_at mit Versionspruefung; liefert die alte Zeile.
func SoftDelete(q db.Q, table, id string, version int, uid int64) (map[string]any, error) {
	cur, err := RowByID(q, table, id)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, apierr.New("not_found", 404)
	}
	if int(db.Int(cur, "version")) != version {
		return nil, apierr.Conflict(NormalizeRow(table, cur))
	}
	now := db.Now()
	res, err := q.Exec("UPDATE "+table+" SET deleted_at = ?, deleted_by = ?, version = version + 1, updated_at = ?, updated_by = ? WHERE id = ? AND version = ? AND deleted_at IS NULL",
		now, db.NullIfZero(uid), now, db.NullIfZero(uid), id, version)
	if err != nil {
		return nil, err
	}
	if db.Affected(res) == 0 {
		return nil, apierr.Conflict(NormalizeRow(table, cur))
	}
	return cur, nil
}

// softDeleteWhere: Soft-Delete ohne Versionspruefung (Kaskaden).
func softDeleteWhere(q db.Q, table, where string, params []any, uid int64) error {
	now := db.Now()
	args := append([]any{now, db.NullIfZero(uid), now}, params...)
	_, err := q.Exec("UPDATE "+table+" SET deleted_at = ?, deleted_by = ?, version = version + 1, updated_at = ? WHERE deleted_at IS NULL AND "+where, args...)
	return err
}

func nextSortOrder(q db.Q, table, whereCol string, parentID *string, einsatzID int64) (int, error) {
	var n int64
	var err error
	if parentID == nil {
		n, err = db.QueryInt(q, "SELECT COALESCE(MAX(sort_order), 0) FROM "+table+" WHERE einsatz_id = ? AND "+whereCol+" IS NULL AND deleted_at IS NULL", einsatzID)
	} else {
		n, err = db.QueryInt(q, "SELECT COALESCE(MAX(sort_order), 0) FROM "+table+" WHERE einsatz_id = ? AND "+whereCol+" = ? AND deleted_at IS NULL", einsatzID, *parentID)
	}
	return int(n) + 10, err
}

// rowBelongs prueft Existenz und Einsatzzugehoerigkeit.
func rowBelongs(q db.Q, table, id string, einsatzID int64) (map[string]any, error) {
	cur, err := RowByID(q, table, id)
	if err != nil {
		return nil, err
	}
	if cur == nil || db.Int(cur, "einsatz_id") != einsatzID {
		return nil, apierr.New("not_found", 404)
	}
	return cur, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
