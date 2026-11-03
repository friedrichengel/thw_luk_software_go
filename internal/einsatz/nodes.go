package einsatz

import (
	"regexp"
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/input"
	"github.com/friedrichengel/thw_luk_software_go/internal/kraefte"
	"github.com/friedrichengel/thw_luk_software_go/internal/stab"
)

var reHTTP = regexp.MustCompile(`(?i)^https?://`)

// SanitizeNodeFields prueft die erlaubten Felder eines Knotens.
func SanitizeNodeFields(in map[string]any, allowed []string) (Fields, error) {
	out := Fields{}
	for _, def := range NodeFields {
		if !input.Has(in, def.Key) || !contains(allowed, def.Key) {
			continue
		}
		v := input.Str(in[def.Key], def.Max)
		if def.Key == "type" && v != "fuehrung" && v != "einheit" {
			continue
		}
		if def.Key == "extern_link_url" && v != "" && !reHTTP.MatchString(v) {
			return nil, apierr.New("invalid_url", 400)
		}
		if def.Key == "kraft_key" && !kraefte.ValidKey(v) {
			return nil, apierr.New("invalid_kraft_key", 400)
		}
		out = append(out, Field{def.Key, v})
	}
	return out, nil
}

func optParentID(v any) (*string, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok && s == "" {
		return nil, nil
	}
	id, err := input.RequireID(v)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func nullStrPtr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func CreateNode(q db.Q, c *access.Ctx, body map[string]any) (*Node, error) {
	einsatzID := c.Einsatz.ID
	id := input.IDOrNew(body["id"])
	if ex, err := RowByIDIncludingDeleted(q, "einsatz_nodes", id); err != nil {
		return nil, err
	} else if ex != nil {
		return nil, apierr.New("id_exists", 409)
	}
	parentID, err := optParentID(body["parent_id"])
	if err != nil {
		return nil, err
	}
	if parentID != nil {
		parent, err := RowByID(q, "einsatz_nodes", *parentID)
		if err != nil {
			return nil, err
		}
		if parent == nil || db.Int(parent, "einsatz_id") != einsatzID {
			return nil, apierr.New("parent_not_found", 404)
		}
	}
	fields, err := SanitizeNodeFields(body, Keys(NodeFields))
	if err != nil {
		return nil, err
	}
	typ := fields.Str("type")
	if typ == "" {
		typ = "einheit"
	}
	status := input.Str(body["status"], 20)
	if status == "" || !stab.StatusExists(status) {
		status = "geplant"
	}
	// Anforderungs-/Anfahrtsstatus fuehren die Einheiten und Fachberater
	// an einer Fuehrungsstelle - die Stelle selbst (TEL, EAL, ...) nicht.
	if typ == "fuehrung" {
		status = "geplant"
	}
	now := db.Now()
	var out *Node
	err = db.InTx(q, func(q db.Q) error {
		sort, err := nextSortOrder(q, "einsatz_nodes", "parent_id", parentID, einsatzID)
		if err != nil {
			return err
		}
		var statusAt any
		if status != "geplant" {
			statusAt = now
		}
		_, err = q.Exec(`INSERT INTO einsatz_nodes (id, einsatz_id, parent_id, sort_order, type, bezeichnung, funkruf, ov, kraft_key, note, status, status_at,
			extern_link_url, extern_link_label, version, created_at, created_by, updated_at, updated_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
			id, einsatzID, nullStrPtr(parentID), sort, typ,
			fields.Str("bezeichnung"), fields.Str("funkruf"), fields.Str("ov"), fields.Str("kraft_key"), fields.Str("note"),
			status, statusAt, fields.Str("extern_link_url"), fields.Str("extern_link_label"),
			now, db.NullIfZero(c.UID), now, db.NullIfZero(c.UID))
		if err != nil {
			return err
		}
		// Optional direkt hinter einem Geschwister einsortieren
		before := input.Raw(body["before_id"])
		after := input.Raw(body["after_id"])
		if before != "" || after != "" {
			if err := reorderNode(q, einsatzID, id, parentID, before, after); err != nil {
				return err
			}
		}
		row, err := RowByID(q, "einsatz_nodes", id)
		if err != nil {
			return err
		}
		node := NodeRow(row)
		parentLabel := ""
		if parentID != nil {
			p, err := RowByID(q, "einsatz_nodes", *parentID)
			if err != nil {
				return err
			}
			parentLabel = " unter " + NodeLabel(p)
		}
		if _, err := access.Event(q, c, "node", id, "insert", NodeLabel(row)+" angelegt"+parentLabel, nil, node); err != nil {
			return err
		}
		if err := TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = node
		return nil
	})
	return out, err
}

func UpdateNode(q db.Q, c *access.Ctx, id string, version int, in map[string]any, allowed []string) (*Node, error) {
	einsatzID := c.Einsatz.ID
	cur, err := rowBelongs(q, "einsatz_nodes", id, einsatzID)
	if err != nil {
		return nil, err
	}
	fields, err := SanitizeNodeFields(in, allowed)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, apierr.New("no_fields", 400)
	}
	// Typwechsel Fuehrung -> Einheit nur ohne Mitglieder
	if fields.Str("type") == "einheit" && db.Str(cur, "type") == "fuehrung" && fields.Has("type") {
		n, err := db.QueryInt(q, "SELECT COUNT(*) FROM einsatz_mitglieder WHERE node_id = ? AND deleted_at IS NULL", id)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, apierr.New("has_members", 409)
		}
	}
	// Typwechsel Einheit -> Fuehrung: den Anforderungs-/Anfahrtsstatus
	// zuruecksetzen, Fuehrungsstellen fuehren keinen eigenen Status.
	resetStatus := fields.Has("type") && fields.Str("type") == "fuehrung" && db.Str(cur, "type") != "fuehrung" && db.Str(cur, "status") != "geplant"
	var out *Node
	err = db.InTx(q, func(q db.Q) error {
		write := append(Fields{}, fields...)
		if resetStatus {
			write.Set("status", "geplant")
			write.Set("status_at", nil)
		}
		row, err := UpdateVersioned(q, "einsatz_nodes", id, version, write, c.UID)
		if err != nil {
			return err
		}
		changed := []string{}
		before := map[string]any{}
		for _, f := range fields {
			if db.Str(cur, f.K) != input.Raw(f.V) {
				changed = append(changed, FieldLabel(f.K))
				before[f.K] = cur[f.K]
			}
		}
		if resetStatus {
			changed = append(changed, "Status zurückgesetzt")
			before["status"] = cur["status"]
			before["status_at"] = cur["status_at"]
		}
		node := NodeRow(row)
		if len(changed) > 0 {
			if _, err := access.Event(q, c, "node", id, "update", NodeLabel(row)+" geändert: "+strings.Join(changed, ", "), before, node); err != nil {
				return err
			}
			if err := TouchEinsatz(q, einsatzID); err != nil {
				return err
			}
		}
		out = node
		return nil
	})
	return out, err
}

func SetNodeStatus(q db.Q, c *access.Ctx, id string, version int, status string) (*Node, error) {
	if !stab.StatusExists(status) {
		return nil, apierr.New("invalid_status", 400)
	}
	einsatzID := c.Einsatz.ID
	cur, err := rowBelongs(q, "einsatz_nodes", id, einsatzID)
	if err != nil {
		return nil, err
	}
	if db.Str(cur, "type") == "fuehrung" {
		return nil, apierr.New("fuehrung_no_status", 409)
	}
	var out *Node
	err = db.InTx(q, func(q db.Q) error {
		row, err := UpdateVersioned(q, "einsatz_nodes", id, version, Fields{{"status", status}, {"status_at", db.Now()}}, c.UID)
		if err != nil {
			return err
		}
		node := NodeRow(row)
		if _, err := access.Event(q, c, "node", id, "status",
			NodeLabel(row)+": Status "+stab.StatusLabel(db.Str(cur, "status"))+" → "+stab.StatusLabel(status),
			map[string]any{"status": cur["status"], "status_at": cur["status_at"]}, node); err != nil {
			return err
		}
		if err := TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = node
		return nil
	})
	return out, err
}

// MoveNode verschiebt einen Knoten (neuer Parent und/oder Position unter Geschwistern).
func MoveNode(q db.Q, c *access.Ctx, id string, version int, parentRaw any, beforeID, afterID string) (*Node, error) {
	einsatzID := c.Einsatz.ID
	cur, err := rowBelongs(q, "einsatz_nodes", id, einsatzID)
	if err != nil {
		return nil, err
	}
	parentID, err := optParentID(parentRaw)
	if err != nil {
		return nil, err
	}
	if parentID != nil {
		if *parentID == id {
			return nil, apierr.New("cycle", 409)
		}
		parent, err := RowByID(q, "einsatz_nodes", *parentID)
		if err != nil {
			return nil, err
		}
		if parent == nil || db.Int(parent, "einsatz_id") != einsatzID {
			return nil, apierr.New("parent_not_found", 404)
		}
		desc, err := IsDescendant(q, einsatzID, *parentID, id)
		if err != nil {
			return nil, err
		}
		if desc {
			return nil, apierr.New("cycle", 409)
		}
	}
	var out *Node
	err = db.InTx(q, func(q db.Q) error {
		if _, err := UpdateVersioned(q, "einsatz_nodes", id, version, Fields{{"parent_id", nullStrPtr(parentID)}}, c.UID); err != nil {
			return err
		}
		if err := reorderNode(q, einsatzID, id, parentID, beforeID, afterID); err != nil {
			return err
		}
		row, err := RowByID(q, "einsatz_nodes", id)
		if err != nil {
			return err
		}
		node := NodeRow(row)
		target := "auf oberste Ebene"
		if parentID != nil {
			p, err := RowByID(q, "einsatz_nodes", *parentID)
			if err != nil {
				return err
			}
			target = "unter " + NodeLabel(p)
		}
		moved := db.Str(cur, "parent_id") != strPtrVal(parentID)
		summary := NodeLabel(row) + " umsortiert"
		if moved {
			summary = NodeLabel(row) + " verschoben " + target
		}
		if _, err := access.Event(q, c, "node", id, "move", summary,
			map[string]any{"parent_id": cur["parent_id"], "sort_order": db.Int(cur, "sort_order")}, node); err != nil {
			return err
		}
		if err := TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = node
		return nil
	})
	return out, err
}

func strPtrVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// reorderNode sortiert id unter parentID vor beforeID bzw. nach afterID ein (sonst ans Ende).
func reorderNode(q db.Q, einsatzID int64, id string, parentID *string, beforeID, afterID string) error {
	var rows []map[string]any
	var err error
	if parentID == nil {
		rows, err = db.QueryMaps(q, "SELECT id FROM einsatz_nodes WHERE einsatz_id = ? AND parent_id IS NULL AND deleted_at IS NULL ORDER BY sort_order, created_at", einsatzID)
	} else {
		rows, err = db.QueryMaps(q, "SELECT id FROM einsatz_nodes WHERE einsatz_id = ? AND parent_id = ? AND deleted_at IS NULL ORDER BY sort_order, created_at", einsatzID, *parentID)
	}
	if err != nil {
		return err
	}
	ids := []string{}
	for _, r := range rows {
		if x := db.Str(r, "id"); x != id {
			ids = append(ids, x)
		}
	}
	pos := len(ids)
	if i := indexOf(ids, beforeID); beforeID != "" && i >= 0 {
		pos = i
	} else if i := indexOf(ids, afterID); afterID != "" && i >= 0 {
		pos = i + 1
	}
	ids = append(ids[:pos], append([]string{id}, ids[pos:]...)...)
	for i, nid := range ids {
		if _, err := q.Exec("UPDATE einsatz_nodes SET sort_order = ? WHERE id = ?", (i+1)*10, nid); err != nil {
			return err
		}
	}
	return nil
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

// IsDescendant: ist candidate ein Nachfahre von ancestor?
func IsDescendant(q db.Q, einsatzID int64, candidate, ancestor string) (bool, error) {
	rows, err := db.QueryMaps(q, "SELECT id, parent_id FROM einsatz_nodes WHERE einsatz_id = ? AND deleted_at IS NULL", einsatzID)
	if err != nil {
		return false, err
	}
	parents := map[string]*string{}
	for _, r := range rows {
		parents[db.Str(r, "id")] = db.StrPtr(r, "parent_id")
	}
	cur := &candidate
	for guard := 0; cur != nil && guard < 1000; guard++ {
		if *cur == ancestor {
			return true, nil
		}
		cur = parents[*cur]
	}
	return false, nil
}

// Deleted beschreibt eine geloeschte Zeile im Sync-Feed.
type Deleted map[string]any

func DeleteNode(q db.Q, c *access.Ctx, id string, version int) (Deleted, error) {
	einsatzID := c.Einsatz.ID
	cur, err := rowBelongs(q, "einsatz_nodes", id, einsatzID)
	if err != nil {
		return nil, err
	}
	n, err := db.QueryInt(q, "SELECT COUNT(*) FROM einsatz_nodes WHERE parent_id = ? AND deleted_at IS NULL", id)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, apierr.New("has_children", 409)
	}
	var out Deleted
	err = db.InTx(q, func(q db.Q) error {
		if _, err := SoftDelete(q, "einsatz_nodes", id, version, c.UID); err != nil {
			return err
		}
		if err := softDeleteWhere(q, "einsatz_schichten", "node_id = ?", []any{id}, c.UID); err != nil {
			return err
		}
		if err := softDeleteWhere(q, "einsatz_mitglieder", "node_id = ?", []any{id}, c.UID); err != nil {
			return err
		}
		after := Deleted{"id": id, "deleted_at": db.Now()}
		if _, err := access.Event(q, c, "node", id, "delete", NodeLabel(cur)+" gelöscht", NodeRow(cur), after); err != nil {
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
