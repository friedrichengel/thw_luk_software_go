// Package schadenskonten: Schadenskonten / Einsatzauftraege. Einheiten aus
// der Harke werden nur referenziert (schadenskonten_refs), Fremdeinheiten
// mit eigener Staerke liegen in schadenskonten_fremd.
package schadenskonten

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/einsatz"
	"github.com/friedrichengel/thw_luk_software_go/internal/input"
	"github.com/friedrichengel/thw_luk_software_go/internal/uuid"
)

const MaxTzPerKind = 16

var FremdKinds = []string{"fw", "polizei", "rd", "sanitaet", "bw", "sonstige"}

var FremdKindLabels = map[string]string{
	"fw": "Feuerwehr", "polizei": "Polizei", "rd": "Rettungsdienst",
	"sanitaet": "Sanitätsdienst", "bw": "Bundeswehr", "sonstige": "Sonstige",
}

var tzKinds = []string{"gefahren", "schaeden", "massnahmen"}

var reTzFile = regexp.MustCompile(`^([\p{L}\p{N}_\-]+/)?[\p{L}\p{N}_\-. ()]+\.svg$`)

func init() {
	einsatz.RegisterRow("schadenskonten", func(m map[string]any) any { return Row(m) })
	einsatz.RegisterRow("schadenskonten_fremd", func(m map[string]any) any { return FremdRow(m) })
}

type Ref struct {
	SchadenskontoID string `json:"schadenskonto_id"`
	RefKind         string `json:"ref_kind"`
	RefID           string `json:"ref_id"`
}

type Fremd struct {
	ID              string  `json:"id"`
	SchadenskontoID string  `json:"schadenskonto_id"`
	Kind            string  `json:"kind"`
	Bezeichnung     string  `json:"bezeichnung"`
	Funkruf         string  `json:"funkruf"`
	OV              string  `json:"ov"`
	Staerke         string  `json:"staerke"`
	Note            string  `json:"note"`
	SortOrder       int     `json:"sort_order"`
	Version         int     `json:"version"`
	DeletedAt       *string `json:"deleted_at"`
}

type Item struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	TaktischeZeichen any      `json:"taktische_zeichen"`
	SortOrder        int      `json:"sort_order"`
	Version          int      `json:"version"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	DeletedAt        *string  `json:"deleted_at"`
	Refs             []Ref    `json:"refs"`
	Fremd            []*Fremd `json:"fremd"`
}

func emptyTz() map[string]any {
	return map[string]any{"gefahren": []any{}, "schaeden": []any{}, "massnahmen": []any{}}
}

func decodeTz(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil || v == nil {
		return emptyTz()
	}
	if m, ok := v.(map[string]any); ok && len(m) == 0 {
		return emptyTz()
	}
	if l, ok := v.([]any); ok && len(l) == 0 {
		return emptyTz()
	}
	return v
}

func Row(m map[string]any) *Item {
	return &Item{
		ID:               db.Str(m, "id"),
		Title:            db.Str(m, "title"),
		Description:      db.Str(m, "description"),
		TaktischeZeichen: decodeTz(db.Str(m, "taktische_zeichen")),
		SortOrder:        int(db.Int(m, "sort_order")),
		Version:          int(db.Int(m, "version")),
		CreatedAt:        db.Str(m, "created_at"),
		UpdatedAt:        db.Str(m, "updated_at"),
		DeletedAt:        db.StrPtr(m, "deleted_at"),
		Refs:             []Ref{},
		Fremd:            []*Fremd{},
	}
}

func FremdRow(m map[string]any) *Fremd {
	return &Fremd{
		ID:              db.Str(m, "id"),
		SchadenskontoID: db.Str(m, "schadenskonto_id"),
		Kind:            db.Str(m, "kind"),
		Bezeichnung:     db.Str(m, "bezeichnung"),
		Funkruf:         db.Str(m, "funkruf"),
		OV:              db.Str(m, "ov"),
		Staerke:         db.Str(m, "staerke"),
		Note:            db.Str(m, "note"),
		SortOrder:       int(db.Int(m, "sort_order")),
		Version:         int(db.Int(m, "version")),
		DeletedAt:       db.StrPtr(m, "deleted_at"),
	}
}

func RefRow(m map[string]any) Ref {
	return Ref{SchadenskontoID: db.Str(m, "schadenskonto_id"), RefKind: db.Str(m, "ref_kind"), RefID: db.Str(m, "ref_id")}
}

// List: alle Schadenskonten eines Einsatzes mit Refs und Fremdeinheiten.
func List(q db.Q, einsatzID int64, includeDeleted bool) ([]*Item, error) {
	del := " AND deleted_at IS NULL"
	if includeDeleted {
		del = ""
	}
	rows, err := db.QueryMaps(q, "SELECT * FROM schadenskonten WHERE einsatz_id = ?"+del+" ORDER BY sort_order, created_at", einsatzID)
	if err != nil {
		return nil, err
	}
	items := []*Item{}
	byID := map[string]*Item{}
	for _, r := range rows {
		it := Row(r)
		items = append(items, it)
		byID[it.ID] = it
	}
	refs, err := db.QueryMaps(q, "SELECT r.* FROM schadenskonten_refs r WHERE r.einsatz_id = ? ORDER BY r.added_at", einsatzID)
	if err != nil {
		return nil, err
	}
	for _, r := range refs {
		if it, ok := byID[db.Str(r, "schadenskonto_id")]; ok {
			it.Refs = append(it.Refs, RefRow(r))
		}
	}
	fremd, err := db.QueryMaps(q, "SELECT * FROM schadenskonten_fremd WHERE einsatz_id = ?"+del+" ORDER BY sort_order, created_at", einsatzID)
	if err != nil {
		return nil, err
	}
	for _, r := range fremd {
		if it, ok := byID[db.Str(r, "schadenskonto_id")]; ok {
			it.Fremd = append(it.Fremd, FremdRow(r))
		}
	}
	return items, nil
}

func AllRefs(q db.Q, einsatzID int64) ([]Ref, error) {
	rows, err := db.QueryMaps(q, "SELECT * FROM schadenskonten_refs WHERE einsatz_id = ?", einsatzID)
	if err != nil {
		return nil, err
	}
	out := []Ref{}
	for _, r := range rows {
		out = append(out, RefRow(r))
	}
	return out, nil
}

func sk(q db.Q, einsatzID int64, id string) (map[string]any, error) {
	if !uuid.ValidClientID(id) {
		return nil, nil
	}
	return db.QueryMap(q, "SELECT * FROM schadenskonten WHERE id = ? AND einsatz_id = ? AND deleted_at IS NULL LIMIT 1", id, einsatzID)
}

func Label(m map[string]any) string {
	t := strings.TrimSpace(db.Str(m, "title"))
	if t == "" {
		t = "ohne Titel"
	}
	return "Schadenskonto „" + t + "\""
}

// SanitizeTz prueft die taktischen Zeichen je Kategorie (Datei oder Freitext).
func SanitizeTz(in any) map[string]any {
	out := emptyTz()
	m := input.Map(in)
	if m == nil {
		return out
	}
	for _, kind := range tzKinds {
		entries := input.List(m[kind])
		if entries == nil {
			continue
		}
		list := []any{}
		for _, e := range entries {
			if len(list) >= MaxTzPerKind {
				break
			}
			em := input.Map(e)
			if em == nil {
				continue
			}
			file := input.Raw(em["file"])
			label := input.Str(em["label"], 160)
			custom := !input.Empty(em["custom"]) || file == ""
			if custom {
				if label == "" {
					continue
				}
				list = append(list, map[string]any{"file": "", "label": label, "custom": true})
				continue
			}
			if strings.Contains(file, "..") || strings.Contains(file, "\\") {
				continue
			}
			if !reTzFile.MatchString(file) {
				continue
			}
			list = append(list, map[string]any{"file": input.Trunc(file, 200), "label": label})
		}
		out[kind] = list
	}
	return out
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func Create(q db.Q, c *access.Ctx, body map[string]any) (*Item, error) {
	einsatzID := c.Einsatz.ID
	id := input.IDOrNew(body["id"])
	if ex, err := einsatz.RowByIDIncludingDeleted(q, "schadenskonten", id); err != nil {
		return nil, err
	} else if ex != nil {
		return nil, apierr.New("id_exists", 409)
	}
	title := input.Str(body["title"], 200)
	if title == "" {
		title = "Neues Schadenskonto"
	}
	desc := input.StrRaw(body["description"], 5000)
	tz := SanitizeTz(body["taktische_zeichen"])
	now := db.Now()
	var out *Item
	err := db.InTx(q, func(q db.Q) error {
		n, err := db.QueryInt(q, "SELECT COALESCE(MAX(sort_order), 0) FROM schadenskonten WHERE einsatz_id = ? AND deleted_at IS NULL", einsatzID)
		if err != nil {
			return err
		}
		if _, err := q.Exec(`INSERT INTO schadenskonten (id, einsatz_id, title, description, taktische_zeichen, sort_order, version, created_at, created_by, updated_at, updated_by)
			VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
			id, einsatzID, title, desc, toJSON(tz), int(n)+10, now, db.NullIfZero(c.UID), now, db.NullIfZero(c.UID)); err != nil {
			return err
		}
		row, err := einsatz.RowByID(q, "schadenskonten", id)
		if err != nil {
			return err
		}
		it := Row(row)
		if _, err := access.Event(q, c, "schadenskonto", id, "insert", Label(row)+" angelegt", nil, it); err != nil {
			return err
		}
		if err := einsatz.TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = it
		return nil
	})
	return out, err
}

func Update(q db.Q, c *access.Ctx, id string, version int, in map[string]any) (*Item, error) {
	einsatzID := c.Einsatz.ID
	cur, err := sk(q, einsatzID, id)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, apierr.New("not_found", 404)
	}
	fields := einsatz.Fields{}
	if input.Has(in, "title") {
		t := input.Str(in["title"], 200)
		if t == "" {
			t = "Schadenskonto"
		}
		fields = append(fields, einsatz.Field{K: "title", V: t})
	}
	if input.Has(in, "description") {
		fields = append(fields, einsatz.Field{K: "description", V: input.StrRaw(in["description"], 5000)})
	}
	if input.Has(in, "taktische_zeichen") {
		fields = append(fields, einsatz.Field{K: "taktische_zeichen", V: toJSON(SanitizeTz(in["taktische_zeichen"]))})
	}
	if len(fields) == 0 {
		return nil, apierr.New("no_fields", 400)
	}
	var out *Item
	err = db.InTx(q, func(q db.Q) error {
		row, err := einsatz.UpdateVersioned(q, "schadenskonten", id, version, fields, c.UID)
		if err != nil {
			return err
		}
		it := Row(row)
		changed := []string{}
		before := map[string]any{}
		for _, f := range fields {
			if db.Str(cur, f.K) != input.Raw(f.V) {
				changed = append(changed, einsatz.FieldLabel(f.K))
				if f.K == "taktische_zeichen" {
					var v any
					_ = json.Unmarshal([]byte(db.Str(cur, f.K)), &v)
					before[f.K] = v
				} else {
					before[f.K] = cur[f.K]
				}
			}
		}
		if len(changed) > 0 {
			if _, err := access.Event(q, c, "schadenskonto", id, "update", Label(row)+" geändert: "+strings.Join(changed, ", "), before, it); err != nil {
				return err
			}
			if err := einsatz.TouchEinsatz(q, einsatzID); err != nil {
				return err
			}
		}
		out = it
		return nil
	})
	return out, err
}

func Delete(q db.Q, c *access.Ctx, id string, version int) (map[string]any, error) {
	einsatzID := c.Einsatz.ID
	cur, err := sk(q, einsatzID, id)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, apierr.New("not_found", 404)
	}
	var out map[string]any
	err = db.InTx(q, func(q db.Q) error {
		if _, err := einsatz.SoftDelete(q, "schadenskonten", id, version, c.UID); err != nil {
			return err
		}
		now := db.Now()
		if _, err := q.Exec("UPDATE schadenskonten_fremd SET deleted_at = ?, deleted_by = ?, version = version + 1 WHERE schadenskonto_id = ? AND deleted_at IS NULL", now, db.NullIfZero(c.UID), id); err != nil {
			return err
		}
		if _, err := q.Exec("DELETE FROM schadenskonten_refs WHERE schadenskonto_id = ?", id); err != nil {
			return err
		}
		after := map[string]any{"id": id, "deleted_at": now}
		if _, err := access.Event(q, c, "schadenskonto", id, "delete", Label(cur)+" gelöscht", Row(cur), after); err != nil {
			return err
		}
		if err := einsatz.TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = after
		return nil
	})
	return out, err
}

func Reorder(q db.Q, c *access.Ctx, order []any) error {
	einsatzID := c.Einsatz.ID
	ids := []string{}
	for _, o := range order {
		if s, ok := o.(string); ok && uuid.ValidClientID(s) {
			ids = append(ids, s)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return db.InTx(q, func(q db.Q) error {
		for i, id := range ids {
			if _, err := q.Exec("UPDATE schadenskonten SET sort_order = ? WHERE id = ? AND einsatz_id = ?", (i+1)*10, id, einsatzID); err != nil {
				return err
			}
		}
		_, err := access.Event(q, c, "schadenskonto", "", "reorder", "Reihenfolge der Schadenskonten geändert", nil, map[string]any{"order": ids})
		return err
	})
}

// ------------------------------------------------------------------
// Refs (Einheiten aus der Harke)
// ------------------------------------------------------------------

func refTargetLabel(q db.Q, kind, id string) string {
	if kind == "node" {
		row, _ := einsatz.RowByIDIncludingDeleted(q, "einsatz_nodes", id)
		return einsatz.NodeLabel(row)
	}
	row, _ := einsatz.RowByIDIncludingDeleted(q, "einsatz_mitglieder", id)
	return einsatz.MitgliedLabel(row)
}

// RefAdd fuegt eine Einheit hinzu. Eine Einheit darf nur einem Schadenskonto
// zugeordnet sein: ohne force -> 409 ref_assigned mit Angabe des bisherigen
// Kontos; mit force wird sie dort entfernt.
func RefAdd(q db.Q, c *access.Ctx, skID, kind, refID string, force bool) (map[string]any, error) {
	einsatzID := c.Einsatz.ID
	skRow, err := sk(q, einsatzID, skID)
	if err != nil {
		return nil, err
	}
	if skRow == nil {
		return nil, apierr.New("not_found", 404)
	}
	if (kind != "node" && kind != "mitglied") || !uuid.ValidClientID(refID) {
		return nil, apierr.New("invalid_ref", 400)
	}
	table := "einsatz_mitglieder"
	if kind == "node" {
		table = "einsatz_nodes"
	}
	target, err := einsatz.RowByID(q, table, refID)
	if err != nil {
		return nil, err
	}
	if target == nil || db.Int(target, "einsatz_id") != einsatzID {
		return nil, apierr.New("ref_not_found", 404)
	}
	var out map[string]any
	err = db.InTx(q, func(q db.Q) error {
		existing, err := db.QueryMaps(q, `SELECT r.schadenskonto_id, s.title FROM schadenskonten_refs r JOIN schadenskonten s ON s.id = r.schadenskonto_id
			WHERE r.einsatz_id = ? AND r.ref_kind = ? AND r.ref_id = ? AND s.deleted_at IS NULL`, einsatzID, kind, refID)
		if err != nil {
			return err
		}
		removedFrom := []string{}
		for _, e := range existing {
			eid := db.Str(e, "schadenskonto_id")
			if eid == skID {
				out = map[string]any{"noop": true, "ref": Ref{SchadenskontoID: skID, RefKind: kind, RefID: refID}}
				return nil
			}
			if !force {
				return apierr.WithExtra("ref_assigned", 409, map[string]any{"schadenskonto_id": eid, "title": db.Str(e, "title")})
			}
			if _, err := q.Exec("DELETE FROM schadenskonten_refs WHERE schadenskonto_id = ? AND ref_kind = ? AND ref_id = ?", eid, kind, refID); err != nil {
				return err
			}
			removedFrom = append(removedFrom, eid)
			if _, err := access.Event(q, c, "sk_ref", eid+":"+refID, "delete",
				refTargetLabel(q, kind, refID)+" aus Schadenskonto „"+db.Str(e, "title")+"\" entfernt (verschoben)",
				map[string]any{"schadenskonto_id": eid, "ref_kind": kind, "ref_id": refID},
				map[string]any{"schadenskonto_id": eid, "ref_kind": kind, "ref_id": refID, "removed": true}); err != nil {
				return err
			}
		}
		if _, err := q.Exec("INSERT INTO schadenskonten_refs (schadenskonto_id, einsatz_id, ref_kind, ref_id, added_by, added_at) VALUES (?, ?, ?, ?, ?, ?)",
			skID, einsatzID, kind, refID, db.NullIfZero(c.UID), db.Now()); err != nil {
			return err
		}
		ref := Ref{SchadenskontoID: skID, RefKind: kind, RefID: refID}
		if _, err := access.Event(q, c, "sk_ref", skID+":"+refID, "insert",
			refTargetLabel(q, kind, refID)+" zu "+Label(skRow)+" zugeordnet", nil, ref); err != nil {
			return err
		}
		if err := einsatz.TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = map[string]any{"ref": ref, "removed_from": removedFrom}
		return nil
	})
	return out, err
}

func RefRemove(q db.Q, c *access.Ctx, skID, kind, refID string) (map[string]any, error) {
	einsatzID := c.Einsatz.ID
	skRow, err := sk(q, einsatzID, skID)
	if err != nil {
		return nil, err
	}
	if skRow == nil {
		return nil, apierr.New("not_found", 404)
	}
	if (kind != "node" && kind != "mitglied") || !uuid.ValidClientID(refID) {
		return nil, apierr.New("invalid_ref", 400)
	}
	var out map[string]any
	err = db.InTx(q, func(q db.Q) error {
		res, err := q.Exec("DELETE FROM schadenskonten_refs WHERE schadenskonto_id = ? AND ref_kind = ? AND ref_id = ?", skID, kind, refID)
		if err != nil {
			return err
		}
		ref := map[string]any{"schadenskonto_id": skID, "ref_kind": kind, "ref_id": refID, "removed": true}
		if db.Affected(res) > 0 {
			if _, err := access.Event(q, c, "sk_ref", skID+":"+refID, "delete",
				refTargetLabel(q, kind, refID)+" aus "+Label(skRow)+" entfernt",
				map[string]any{"schadenskonto_id": skID, "ref_kind": kind, "ref_id": refID}, ref); err != nil {
				return err
			}
			if err := einsatz.TouchEinsatz(q, einsatzID); err != nil {
				return err
			}
		}
		out = ref
		return nil
	})
	return out, err
}

// ------------------------------------------------------------------
// Fremdeinheiten
// ------------------------------------------------------------------

var fremdTextFields = []struct {
	Key string
	Max int
}{{"bezeichnung", 160}, {"funkruf", 120}, {"ov", 120}, {"staerke", 40}}

func SanitizeFremd(in map[string]any) (einsatz.Fields, error) {
	out := einsatz.Fields{}
	if input.Has(in, "kind") {
		k := input.Raw(in["kind"])
		if !containsStr(FremdKinds, k) {
			k = "sonstige"
		}
		out = append(out, einsatz.Field{K: "kind", V: k})
	}
	for _, f := range fremdTextFields {
		if input.Has(in, f.Key) {
			out = append(out, einsatz.Field{K: f.Key, V: input.Str(in[f.Key], f.Max)})
		}
	}
	if input.Has(in, "note") {
		out = append(out, einsatz.Field{K: "note", V: input.StrRaw(in["note"], 500)})
	}
	if st := out.Str("staerke"); out.Has("staerke") && st != "" && !einsatz.ReStaerke.MatchString(st) {
		return nil, apierr.New("invalid_staerke", 400)
	}
	return out, nil
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func FremdLabel(m map[string]any) string {
	b := strings.TrimSpace(db.Str(m, "bezeichnung"))
	if b == "" {
		if l, ok := FremdKindLabels[db.Str(m, "kind")]; ok {
			b = l
		} else {
			b = "ohne Bezeichnung"
		}
	}
	return "Fremdeinheit „" + b + "\""
}

func FremdCreate(q db.Q, c *access.Ctx, skID string, body map[string]any) (*Fremd, error) {
	einsatzID := c.Einsatz.ID
	skRow, err := sk(q, einsatzID, skID)
	if err != nil {
		return nil, err
	}
	if skRow == nil {
		return nil, apierr.New("not_found", 404)
	}
	id := input.IDOrNew(body["id"])
	if ex, err := einsatz.RowByIDIncludingDeleted(q, "schadenskonten_fremd", id); err != nil {
		return nil, err
	} else if ex != nil {
		return nil, apierr.New("id_exists", 409)
	}
	f, err := SanitizeFremd(body)
	if err != nil {
		return nil, err
	}
	now := db.Now()
	var out *Fremd
	err = db.InTx(q, func(q db.Q) error {
		n, err := db.QueryInt(q, "SELECT COALESCE(MAX(sort_order), 0) FROM schadenskonten_fremd WHERE schadenskonto_id = ? AND deleted_at IS NULL", skID)
		if err != nil {
			return err
		}
		kind := f.Str("kind")
		if kind == "" {
			kind = "sonstige"
		}
		if _, err := q.Exec(`INSERT INTO schadenskonten_fremd (id, schadenskonto_id, einsatz_id, kind, bezeichnung, funkruf, ov, staerke, note, sort_order, version, created_at, created_by, updated_at, updated_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
			id, skID, einsatzID, kind, f.Str("bezeichnung"), f.Str("funkruf"), f.Str("ov"), f.Str("staerke"), f.Str("note"), int(n)+10,
			now, db.NullIfZero(c.UID), now, db.NullIfZero(c.UID)); err != nil {
			return err
		}
		row, err := einsatz.RowByID(q, "schadenskonten_fremd", id)
		if err != nil {
			return err
		}
		fr := FremdRow(row)
		if _, err := access.Event(q, c, "sk_fremd", id, "insert", FremdLabel(row)+" zu "+Label(skRow)+" hinzugefügt", nil, fr); err != nil {
			return err
		}
		if err := einsatz.TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = fr
		return nil
	})
	return out, err
}

func FremdUpdate(q db.Q, c *access.Ctx, id string, version int, in map[string]any) (*Fremd, error) {
	einsatzID := c.Einsatz.ID
	cur, err := einsatz.RowByID(q, "schadenskonten_fremd", id)
	if err != nil {
		return nil, err
	}
	if cur == nil || db.Int(cur, "einsatz_id") != einsatzID {
		return nil, apierr.New("not_found", 404)
	}
	fields, err := SanitizeFremd(in)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, apierr.New("no_fields", 400)
	}
	var out *Fremd
	err = db.InTx(q, func(q db.Q) error {
		row, err := einsatz.UpdateVersioned(q, "schadenskonten_fremd", id, version, fields, c.UID)
		if err != nil {
			return err
		}
		fr := FremdRow(row)
		changed := []string{}
		before := map[string]any{}
		for _, f := range fields {
			if db.Str(cur, f.K) != input.Raw(f.V) {
				changed = append(changed, einsatz.FieldLabel(f.K))
				before[f.K] = cur[f.K]
			}
		}
		if len(changed) > 0 {
			if _, err := access.Event(q, c, "sk_fremd", id, "update", FremdLabel(row)+" geändert: "+strings.Join(changed, ", "), before, fr); err != nil {
				return err
			}
			if err := einsatz.TouchEinsatz(q, einsatzID); err != nil {
				return err
			}
		}
		out = fr
		return nil
	})
	return out, err
}

func FremdDelete(q db.Q, c *access.Ctx, id string, version int) (map[string]any, error) {
	einsatzID := c.Einsatz.ID
	cur, err := einsatz.RowByID(q, "schadenskonten_fremd", id)
	if err != nil {
		return nil, err
	}
	if cur == nil || db.Int(cur, "einsatz_id") != einsatzID {
		return nil, apierr.New("not_found", 404)
	}
	var out map[string]any
	err = db.InTx(q, func(q db.Q) error {
		if _, err := einsatz.SoftDelete(q, "schadenskonten_fremd", id, version, c.UID); err != nil {
			return err
		}
		after := map[string]any{"id": id, "schadenskonto_id": db.Str(cur, "schadenskonto_id"), "deleted_at": db.Now()}
		if _, err := access.Event(q, c, "sk_fremd", id, "delete", FremdLabel(cur)+" entfernt", FremdRow(cur), after); err != nil {
			return err
		}
		if err := einsatz.TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = after
		return nil
	})
	return out, err
}
