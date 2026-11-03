package einsatz

import (
	"regexp"
	"sort"
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/input"
	"github.com/friedrichengel/thw_luk_software_go/internal/kraefte"
	"github.com/friedrichengel/thw_luk_software_go/internal/stab"
)

// ReStaerke prueft das Staerke-Format "F/U/M" (fuehrende Zahlen, Rest frei).
var ReStaerke = regexp.MustCompile(`^\s*\d+\s*/\s*\d+\s*/\s*\d+`)

// SanitizeSchichtFields prueft die Felder einer Schicht.
func SanitizeSchichtFields(in map[string]any) (Fields, error) {
	out := Fields{}
	for _, def := range SchichtFields {
		if !input.Has(in, def.Key) {
			continue
		}
		v := input.Str(in[def.Key], def.Max)
		if def.Key == "kraft_key" && !kraefte.ValidKey(v) {
			return nil, apierr.New("invalid_kraft_key", 400)
		}
		out = append(out, Field{def.Key, v})
	}
	if input.Has(in, "aktiv") {
		a := 0
		if input.Truthy(in["aktiv"]) {
			a = 1
		}
		out = append(out, Field{"aktiv", a})
	}
	if st := out.Str("staerke"); out.Has("staerke") && st != "" && !ReStaerke.MatchString(st) {
		return nil, apierr.New("invalid_staerke", 400)
	}
	return out, nil
}

// schichtTarget liefert Mitglied (wenn gesetzt) sonst Knoten der Schicht, auch geloescht.
func schichtTarget(q db.Q, s map[string]any) (map[string]any, error) {
	if mid := db.Str(s, "mitglied_id"); mid != "" {
		return RowByIDIncludingDeleted(q, "einsatz_mitglieder", mid)
	}
	return RowByIDIncludingDeleted(q, "einsatz_nodes", db.Str(s, "node_id"))
}

func CreateSchicht(q db.Q, c *access.Ctx, body map[string]any) (*Schicht, error) {
	einsatzID := c.Einsatz.ID
	nodeID, err := input.RequireID(body["node_id"])
	if err != nil {
		return nil, err
	}
	node, err := RowByID(q, "einsatz_nodes", nodeID)
	if err != nil {
		return nil, err
	}
	if node == nil || db.Int(node, "einsatz_id") != einsatzID {
		return nil, apierr.New("parent_not_found", 404)
	}
	var mitgliedID *string
	if !input.Empty(body["mitglied_id"]) {
		mid, err := input.RequireID(body["mitglied_id"])
		if err != nil {
			return nil, err
		}
		m, err := RowByID(q, "einsatz_mitglieder", mid)
		if err != nil {
			return nil, err
		}
		if m == nil || db.Str(m, "node_id") != nodeID {
			return nil, apierr.New("parent_not_found", 404)
		}
		mitgliedID = &mid
	} else if db.Str(node, "type") == "fuehrung" {
		return nil, apierr.New("fuehrung_no_schicht", 409)
	}
	id := input.IDOrNew(body["id"])
	if ex, err := RowByIDIncludingDeleted(q, "einsatz_schichten", id); err != nil {
		return nil, err
	} else if ex != nil {
		return nil, apierr.New("id_exists", 409)
	}
	f, err := SanitizeSchichtFields(body)
	if err != nil {
		return nil, err
	}
	if f.Str("von") == "" || f.Str("bis") == "" {
		return nil, apierr.New("missing_time", 400)
	}
	status := input.Raw(body["status"])
	if status == "" {
		status = "geplant"
	}
	if !stab.StatusExists(status) {
		return nil, apierr.New("invalid_status", 400)
	}
	now := db.Now()
	var out *Schicht
	err = db.InTx(q, func(q db.Q) error {
		aktiv := any(1)
		if v, ok := f.Get("aktiv"); ok {
			aktiv = v
		}
		var statusAt any
		if status != "geplant" {
			statusAt = now
		}
		_, err := q.Exec(`INSERT INTO einsatz_schichten (id, einsatz_id, node_id, mitglied_id, von, bis, staerke, name, telefon, funkruf, ov, kraft_key, note, aktiv,
			status, status_at, version, created_at, created_by, updated_at, updated_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
			id, einsatzID, nodeID, nullStrPtr(mitgliedID), f.Str("von"), f.Str("bis"), f.Str("staerke"), f.Str("name"), f.Str("telefon"),
			f.Str("funkruf"), f.Str("ov"), f.Str("kraft_key"), f.Str("note"), aktiv,
			status, statusAt, now, db.NullIfZero(c.UID), now, db.NullIfZero(c.UID))
		if err != nil {
			return err
		}
		row, err := RowByID(q, "einsatz_schichten", id)
		if err != nil {
			return err
		}
		target, err := schichtTarget(q, row)
		if err != nil {
			return err
		}
		s := SchichtRow(row)
		if _, err := access.Event(q, c, "schicht", id, "insert", SchichtLabel(row, target)+" angelegt", nil, s); err != nil {
			return err
		}
		if err := TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = s
		return nil
	})
	return out, err
}

func UpdateSchicht(q db.Q, c *access.Ctx, id string, version int, in map[string]any) (*Schicht, error) {
	einsatzID := c.Einsatz.ID
	cur, err := rowBelongs(q, "einsatz_schichten", id, einsatzID)
	if err != nil {
		return nil, err
	}
	fields, err := SanitizeSchichtFields(in)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, apierr.New("no_fields", 400)
	}
	var out *Schicht
	err = db.InTx(q, func(q db.Q) error {
		row, err := UpdateVersioned(q, "einsatz_schichten", id, version, fields, c.UID)
		if err != nil {
			return err
		}
		changed := []string{}
		before := map[string]any{}
		for _, f := range fields {
			if db.Str(cur, f.K) != input.Raw(f.V) {
				changed = append(changed, FieldLabel(f.K))
				if f.K == "aktiv" {
					before[f.K] = db.Int(cur, f.K) == 1
				} else {
					before[f.K] = cur[f.K]
				}
			}
		}
		s := SchichtRow(row)
		if len(changed) > 0 {
			target, err := schichtTarget(q, row)
			if err != nil {
				return err
			}
			if _, err := access.Event(q, c, "schicht", id, "update", SchichtLabel(row, target)+" geändert: "+strings.Join(changed, ", "), before, s); err != nil {
				return err
			}
			if err := TouchEinsatz(q, einsatzID); err != nil {
				return err
			}
		}
		out = s
		return nil
	})
	return out, err
}

// SetSchichtStatus: Status einer Schicht setzen (angefordert / auf Anfahrt / vor Ort / abgerueckt).
func SetSchichtStatus(q db.Q, c *access.Ctx, id string, version int, status string) (*Schicht, error) {
	if !stab.StatusExists(status) {
		return nil, apierr.New("invalid_status", 400)
	}
	einsatzID := c.Einsatz.ID
	cur, err := rowBelongs(q, "einsatz_schichten", id, einsatzID)
	if err != nil {
		return nil, err
	}
	var out *Schicht
	err = db.InTx(q, func(q db.Q) error {
		row, err := UpdateVersioned(q, "einsatz_schichten", id, version, Fields{{"status", status}, {"status_at", db.Now()}}, c.UID)
		if err != nil {
			return err
		}
		s := SchichtRow(row)
		curStatus := db.Str(cur, "status")
		if curStatus == "" {
			curStatus = "geplant"
		}
		target, err := schichtTarget(q, row)
		if err != nil {
			return err
		}
		if _, err := access.Event(q, c, "schicht", id, "status",
			SchichtLabel(row, target)+": Status "+stab.StatusLabel(curStatus)+" → "+stab.StatusLabel(status),
			map[string]any{"status": curStatus, "status_at": cur["status_at"]}, s); err != nil {
			return err
		}
		if err := TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = s
		return nil
	})
	return out, err
}

func DeleteSchicht(q db.Q, c *access.Ctx, id string, version int) (Deleted, error) {
	einsatzID := c.Einsatz.ID
	cur, err := rowBelongs(q, "einsatz_schichten", id, einsatzID)
	if err != nil {
		return nil, err
	}
	var out Deleted
	err = db.InTx(q, func(q db.Q) error {
		if _, err := SoftDelete(q, "einsatz_schichten", id, version, c.UID); err != nil {
			return err
		}
		after := Deleted{"id": id, "node_id": db.Str(cur, "node_id"), "mitglied_id": cur["mitglied_id"], "deleted_at": db.Now()}
		target, err := schichtTarget(q, cur)
		if err != nil {
			return err
		}
		if _, err := access.Event(q, c, "schicht", id, "delete", SchichtLabel(cur, target)+" gelöscht", SchichtRow(cur), after); err != nil {
			return err
		}
		if err := TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = after
		return nil
	})
	return out, err
}

// emitSchichtMoves: nach einem Umhaengen von Schichten je Schicht ein Update-Ereignis senden.
func emitSchichtMoves(q db.Q, c *access.Ctx, where string, params []any, summary string) error {
	rows, err := db.QueryMaps(q, "SELECT * FROM einsatz_schichten WHERE deleted_at IS NULL AND "+where, params...)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := access.Event(q, c, "schicht", db.Str(r, "id"), "update", summary, nil, SchichtRow(r)); err != nil {
			return err
		}
	}
	return nil
}

// ------------------------------------------------------------------
// Auf Anfahrt
// ------------------------------------------------------------------

type AnfahrtItem struct {
	Kind        string  `json:"kind"`
	ID          string  `json:"id"`
	Version     int     `json:"version"`
	Type        string  `json:"type"`
	Bezeichnung string  `json:"bezeichnung"`
	Funkruf     string  `json:"funkruf"`
	OV          string  `json:"ov"`
	Note        string  `json:"note"`
	Status      string  `json:"status"`
	StatusAt    *string `json:"status_at"`
	Path        string  `json:"path"`
}

type AnfahrtResult struct {
	Items []AnfahrtItem `json:"items"`
	Seq   int64         `json:"seq"`
}

// Anfahrt: alle Einheiten/Mitglieder mit Status angefordert oder auf Anfahrt.
func Anfahrt(q db.Q, einsatzID int64) (*AnfahrtResult, error) {
	tree, err := TreeOf(q, einsatzID, false)
	if err != nil {
		return nil, err
	}
	byID := map[string]*Node{}
	for _, n := range tree.Nodes {
		byID[n.ID] = n
	}
	path := func(id *string) string {
		parts := []string{}
		for guard := 0; id != nil && guard < 100; guard++ {
			n, ok := byID[*id]
			if !ok {
				break
			}
			b := n.Bezeichnung
			if b == "" {
				b = "(ohne Bezeichnung)"
			}
			parts = append([]string{b}, parts...)
			id = n.ParentID
		}
		return strings.Join(parts, " › ")
	}
	items := []AnfahrtItem{}
	for _, n := range tree.Nodes {
		// Fuehrungsstellen fahren nicht an - nur Einheiten und Fachberater.
		if n.Type == "fuehrung" || !stab.IsAnfahrt(n.Status) {
			continue
		}
		items = append(items, AnfahrtItem{Kind: "node", ID: n.ID, Version: n.Version, Type: n.Type,
			Bezeichnung: n.Bezeichnung, Funkruf: n.Funkruf, OV: n.OV, Note: n.Note, Status: n.Status, StatusAt: n.StatusAt, Path: path(n.ParentID)})
	}
	for _, m := range tree.Mitglieder {
		if !stab.IsAnfahrt(m.Status) {
			continue
		}
		bez := m.Bezeichnung
		if m.Kind == "fachberater" {
			bez = strings.TrimSpace(m.Funktion + " " + m.Name)
		}
		nid := m.NodeID
		items = append(items, AnfahrtItem{Kind: "mitglied", ID: m.ID, Version: m.Version, Type: m.Kind,
			Bezeichnung: bez, Funkruf: m.Funkruf, OV: m.OV, Note: m.Note, Status: m.Status, StatusAt: m.StatusAt, Path: path(&nid)})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return strPtrVal(items[i].StatusAt) < strPtrVal(items[j].StatusAt)
	})
	return &AnfahrtResult{Items: items, Seq: tree.Seq}, nil
}
