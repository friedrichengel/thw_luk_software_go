package einsatz

import (
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/input"
	"github.com/friedrichengel/thw_luk_software_go/internal/kraefte"
	"github.com/friedrichengel/thw_luk_software_go/internal/stab"
)

// SanitizeMitgliedFields prueft die erlaubten Felder eines Mitglieds.
func SanitizeMitgliedFields(in map[string]any, allowed []string) (Fields, error) {
	out := Fields{}
	for _, def := range MitgliedFields {
		if !input.Has(in, def.Key) || !contains(allowed, def.Key) {
			continue
		}
		v := input.Str(in[def.Key], def.Max)
		if def.Key == "kraft_key" && !kraefte.ValidKey(v) {
			return nil, apierr.New("invalid_kraft_key", 400)
		}
		out = append(out, Field{def.Key, v})
	}
	return out, nil
}

func CreateMitglied(q db.Q, c *access.Ctx, body map[string]any) (*Mitglied, error) {
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
	if db.Str(node, "type") != "fuehrung" {
		return nil, apierr.New("not_fuehrung", 409)
	}
	kind := input.Str(body["kind"], 20)
	if kind != "fachberater" && kind != "einheit" {
		return nil, apierr.New("invalid_kind", 400)
	}
	id := input.IDOrNew(body["id"])
	if ex, err := RowByIDIncludingDeleted(q, "einsatz_mitglieder", id); err != nil {
		return nil, err
	} else if ex != nil {
		return nil, apierr.New("id_exists", 409)
	}
	fields, err := SanitizeMitgliedFields(body, Keys(MitgliedFields))
	if err != nil {
		return nil, err
	}
	status := input.Str(body["status"], 20)
	if status == "" || !stab.StatusExists(status) {
		status = "geplant"
	}
	now := db.Now()
	var out *Mitglied
	err = db.InTx(q, func(q db.Q) error {
		sort, err := nextSortOrder(q, "einsatz_mitglieder", "node_id", &nodeID, einsatzID)
		if err != nil {
			return err
		}
		var statusAt any
		if status != "geplant" {
			statusAt = now
		}
		_, err = q.Exec(`INSERT INTO einsatz_mitglieder (id, einsatz_id, node_id, kind, sort_order, funktion, name, bezeichnung, funkruf, telefon, ov, kraft_key, note, status, status_at,
			version, created_at, created_by, updated_at, updated_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
			id, einsatzID, nodeID, kind, sort,
			fields.Str("funktion"), fields.Str("name"), fields.Str("bezeichnung"),
			fields.Str("funkruf"), fields.Str("telefon"), fields.Str("ov"), fields.Str("kraft_key"), fields.Str("note"),
			status, statusAt, now, db.NullIfZero(c.UID), now, db.NullIfZero(c.UID))
		if err != nil {
			return err
		}
		row, err := RowByID(q, "einsatz_mitglieder", id)
		if err != nil {
			return err
		}
		m := MitgliedRow(row)
		if _, err := access.Event(q, c, "mitglied", id, "insert", MitgliedLabel(row)+" angelegt an "+NodeLabel(node), nil, m); err != nil {
			return err
		}
		if err := TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = m
		return nil
	})
	return out, err
}

func UpdateMitglied(q db.Q, c *access.Ctx, id string, version int, in map[string]any, allowed []string) (*Mitglied, error) {
	einsatzID := c.Einsatz.ID
	cur, err := rowBelongs(q, "einsatz_mitglieder", id, einsatzID)
	if err != nil {
		return nil, err
	}
	fields, err := SanitizeMitgliedFields(in, allowed)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, apierr.New("no_fields", 400)
	}
	var out *Mitglied
	err = db.InTx(q, func(q db.Q) error {
		row, err := UpdateVersioned(q, "einsatz_mitglieder", id, version, fields, c.UID)
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
		m := MitgliedRow(row)
		if len(changed) > 0 {
			if _, err := access.Event(q, c, "mitglied", id, "update", MitgliedLabel(row)+" geändert: "+strings.Join(changed, ", "), before, m); err != nil {
				return err
			}
			if err := TouchEinsatz(q, einsatzID); err != nil {
				return err
			}
		}
		out = m
		return nil
	})
	return out, err
}

func SetMitgliedStatus(q db.Q, c *access.Ctx, id string, version int, status string) (*Mitglied, error) {
	if !stab.StatusExists(status) {
		return nil, apierr.New("invalid_status", 400)
	}
	einsatzID := c.Einsatz.ID
	cur, err := rowBelongs(q, "einsatz_mitglieder", id, einsatzID)
	if err != nil {
		return nil, err
	}
	var out *Mitglied
	err = db.InTx(q, func(q db.Q) error {
		row, err := UpdateVersioned(q, "einsatz_mitglieder", id, version, Fields{{"status", status}, {"status_at", db.Now()}}, c.UID)
		if err != nil {
			return err
		}
		m := MitgliedRow(row)
		if _, err := access.Event(q, c, "mitglied", id, "status",
			MitgliedLabel(row)+": Status "+stab.StatusLabel(db.Str(cur, "status"))+" → "+stab.StatusLabel(status),
			map[string]any{"status": cur["status"], "status_at": cur["status_at"]}, m); err != nil {
			return err
		}
		if err := TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = m
		return nil
	})
	return out, err
}

// MoveMitglied haengt ein Mitglied an eine andere Fuehrungsstelle.
func MoveMitglied(q db.Q, c *access.Ctx, id string, version int, targetNodeID string) (*Mitglied, error) {
	einsatzID := c.Einsatz.ID
	cur, err := rowBelongs(q, "einsatz_mitglieder", id, einsatzID)
	if err != nil {
		return nil, err
	}
	target, err := RowByID(q, "einsatz_nodes", targetNodeID)
	if err != nil {
		return nil, err
	}
	if target == nil || db.Int(target, "einsatz_id") != einsatzID {
		return nil, apierr.New("parent_not_found", 404)
	}
	if db.Str(target, "type") != "fuehrung" {
		return nil, apierr.New("not_fuehrung", 409)
	}
	var out *Mitglied
	err = db.InTx(q, func(q db.Q) error {
		sort, err := nextSortOrder(q, "einsatz_mitglieder", "node_id", &targetNodeID, einsatzID)
		if err != nil {
			return err
		}
		row, err := UpdateVersioned(q, "einsatz_mitglieder", id, version, Fields{{"node_id", targetNodeID}, {"sort_order", sort}}, c.UID)
		if err != nil {
			return err
		}
		// Schichten des Mitglieds wandern mit (node_id nachziehen)
		if _, err := q.Exec("UPDATE einsatz_schichten SET node_id = ?, version = version + 1 WHERE mitglied_id = ? AND deleted_at IS NULL", targetNodeID, id); err != nil {
			return err
		}
		m := MitgliedRow(row)
		if _, err := access.Event(q, c, "mitglied", id, "move", MitgliedLabel(row)+" verschoben an "+NodeLabel(target),
			map[string]any{"node_id": cur["node_id"]}, m); err != nil {
			return err
		}
		if err := emitSchichtMoves(q, c, "mitglied_id = ?", []any{id}, "Schicht mit "+MitgliedLabel(row)+" verschoben"); err != nil {
			return err
		}
		if err := TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = m
		return nil
	})
	return out, err
}

func DeleteMitglied(q db.Q, c *access.Ctx, id string, version int) (Deleted, error) {
	einsatzID := c.Einsatz.ID
	cur, err := rowBelongs(q, "einsatz_mitglieder", id, einsatzID)
	if err != nil {
		return nil, err
	}
	var out Deleted
	err = db.InTx(q, func(q db.Q) error {
		if _, err := SoftDelete(q, "einsatz_mitglieder", id, version, c.UID); err != nil {
			return err
		}
		if err := softDeleteWhere(q, "einsatz_schichten", "mitglied_id = ?", []any{id}, c.UID); err != nil {
			return err
		}
		after := Deleted{"id": id, "node_id": db.Str(cur, "node_id"), "deleted_at": db.Now()}
		if _, err := access.Event(q, c, "mitglied", id, "delete", MitgliedLabel(cur)+" gelöscht", MitgliedRow(cur), after); err != nil {
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

// ConvertResult: Ergebnis einer Umwandlung Mitglied <-> Knoten.
type ConvertResult struct {
	Node            *Node     `json:"node,omitempty"`
	MitgliedDeleted Deleted   `json:"mitglied_deleted,omitempty"`
	Mitglied        *Mitglied `json:"mitglied,omitempty"`
	NodeDeleted     Deleted   `json:"node_deleted,omitempty"`
}

// MitgliedToNode: interne Einheit -> eigener Knoten in der Hierarchie (unter parent).
// Schichten werden umgehaengt.
func MitgliedToNode(q db.Q, c *access.Ctx, id string, version int, parentRaw any) (*ConvertResult, error) {
	einsatzID := c.Einsatz.ID
	cur, err := rowBelongs(q, "einsatz_mitglieder", id, einsatzID)
	if err != nil {
		return nil, err
	}
	if db.Str(cur, "kind") != "einheit" {
		return nil, apierr.New("not_einheit", 409)
	}
	parentID, err := optParentID(parentRaw)
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
	var out *ConvertResult
	err = db.InTx(q, func(q db.Q) error {
		if int(db.Int(cur, "version")) != version {
			return apierr.Conflict(MitgliedRow(cur))
		}
		node, err := CreateNode(q, c, map[string]any{
			"parent_id": nullStrPtr(parentID), "type": "einheit",
			"bezeichnung": db.Str(cur, "bezeichnung"), "funkruf": db.Str(cur, "funkruf"), "ov": db.Str(cur, "ov"), "note": db.Str(cur, "note"),
			"kraft_key": db.Str(cur, "kraft_key"),
			"status":    db.Str(cur, "status"),
		})
		if err != nil {
			return err
		}
		// Schichten uebernehmen
		if _, err := q.Exec("UPDATE einsatz_schichten SET node_id = ?, mitglied_id = NULL, version = version + 1 WHERE mitglied_id = ? AND deleted_at IS NULL", node.ID, id); err != nil {
			return err
		}
		nodeRow, err := RowByID(q, "einsatz_nodes", node.ID)
		if err != nil {
			return err
		}
		if err := emitSchichtMoves(q, c, "node_id = ? AND mitglied_id IS NULL", []any{node.ID}, "Schicht in Knoten "+NodeLabel(nodeRow)+" übernommen"); err != nil {
			return err
		}
		if _, err := SoftDelete(q, "einsatz_mitglieder", id, version, c.UID); err != nil {
			return err
		}
		after := Deleted{"id": id, "node_id": db.Str(cur, "node_id"), "deleted_at": db.Now(), "converted_to_node": node.ID}
		if _, err := access.Event(q, c, "mitglied", id, "delete", MitgliedLabel(cur)+" in eigenständigen Knoten umgewandelt", MitgliedRow(cur), after); err != nil {
			return err
		}
		out = &ConvertResult{Node: NodeRow(nodeRow), MitgliedDeleted: after}
		return nil
	})
	return out, err
}

// NodeToMitglied: Knoten (Einheit ohne Kinder/Mitglieder) -> interne Einheit einer Fuehrungsstelle.
func NodeToMitglied(q db.Q, c *access.Ctx, id string, version int, targetNodeID string) (*ConvertResult, error) {
	einsatzID := c.Einsatz.ID
	cur, err := rowBelongs(q, "einsatz_nodes", id, einsatzID)
	if err != nil {
		return nil, err
	}
	target, err := RowByID(q, "einsatz_nodes", targetNodeID)
	if err != nil {
		return nil, err
	}
	if target == nil || db.Int(target, "einsatz_id") != einsatzID {
		return nil, apierr.New("parent_not_found", 404)
	}
	if db.Str(target, "type") != "fuehrung" || targetNodeID == id {
		return nil, apierr.New("not_fuehrung", 409)
	}
	n, err := db.QueryInt(q, "SELECT COUNT(*) FROM einsatz_nodes WHERE parent_id = ? AND deleted_at IS NULL", id)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, apierr.New("has_children", 409)
	}
	n, err = db.QueryInt(q, "SELECT COUNT(*) FROM einsatz_mitglieder WHERE node_id = ? AND deleted_at IS NULL", id)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, apierr.New("has_members", 409)
	}
	var out *ConvertResult
	err = db.InTx(q, func(q db.Q) error {
		if int(db.Int(cur, "version")) != version {
			return apierr.Conflict(NodeRow(cur))
		}
		m, err := CreateMitglied(q, c, map[string]any{
			"node_id": targetNodeID, "kind": "einheit",
			"bezeichnung": db.Str(cur, "bezeichnung"), "funkruf": db.Str(cur, "funkruf"), "ov": db.Str(cur, "ov"), "note": db.Str(cur, "note"),
			"kraft_key": db.Str(cur, "kraft_key"),
			"status":    db.Str(cur, "status"),
		})
		if err != nil {
			return err
		}
		if _, err := q.Exec("UPDATE einsatz_schichten SET node_id = ?, mitglied_id = ?, version = version + 1 WHERE node_id = ? AND mitglied_id IS NULL AND deleted_at IS NULL", targetNodeID, m.ID, id); err != nil {
			return err
		}
		if err := emitSchichtMoves(q, c, "mitglied_id = ?", []any{m.ID}, "Schicht in interne Einheit übernommen"); err != nil {
			return err
		}
		if _, err := SoftDelete(q, "einsatz_nodes", id, version, c.UID); err != nil {
			return err
		}
		after := Deleted{"id": id, "deleted_at": db.Now(), "converted_to_mitglied": m.ID}
		if _, err := access.Event(q, c, "node", id, "delete", NodeLabel(cur)+" als interne Einheit eingegliedert", NodeRow(cur), after); err != nil {
			return err
		}
		mRow, err := RowByID(q, "einsatz_mitglieder", m.ID)
		if err != nil {
			return err
		}
		out = &ConvertResult{Mitglied: MitgliedRow(mRow), NodeDeleted: after}
		return nil
	})
	return out, err
}
