package kraefte

import (
	"strings"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/db"
)

var statusRank = map[string]int{"vor_ort": 4, "anfahrt": 3, "angefordert": 2, "abgerueckt": 1}

type bindRow struct {
	kind        string
	id          string
	kraftKey    string
	label       string
	status      string
	einsatzID   int64
	von         *string
	bis         *string
	parentLabel *string
}

// Bindungen: alle Einheiten mit kraft_key in offenen Einsaetzen. Fuer
// Einsaetze, die der Nutzer nicht sehen darf, werden Titel und UUID
// weggelassen.
//
// Der Status einer Einheit wird wie im Client (luk_core.js unitStatus)
// ermittelt: Hat mindestens eine aktive Schicht einen eigenen Status
// (!= geplant), gilt der staerkste davon (vor Ort > auf Anfahrt >
// angefordert > abgerueckt), sonst der Status der Einheit selbst. Eine
// abgerueckte Einheit bindet nicht - auch nicht ueber ihre Schichten.
func (k *Katalog) Bindungen(q db.Q, uid int64, ownEinsatzID *int64) (map[string][]map[string]any, error) {
	out := map[string][]map[string]any{}
	openRows, err := db.QueryMaps(q, "SELECT id FROM einsaetze WHERE deleted_at IS NULL AND status = 'open'")
	if err != nil {
		return nil, err
	}
	if len(openRows) == 0 {
		return out, nil
	}
	openIDs := make([]any, 0, len(openRows))
	for _, r := range openRows {
		openIDs = append(openIDs, db.Int(r, "id"))
	}
	in := db.Placeholders(len(openIDs))

	nodeRows, err := db.QueryMaps(q, "SELECT id, einsatz_id, kraft_key, bezeichnung, status FROM einsatz_nodes WHERE deleted_at IS NULL AND einsatz_id IN ("+in+")", openIDs...)
	if err != nil {
		return nil, err
	}
	nodes := map[string]map[string]any{}
	for _, r := range nodeRows {
		nodes[db.Str(r, "id")] = r
	}
	mitRows, err := db.QueryMaps(q, "SELECT id, einsatz_id, node_id, kraft_key, bezeichnung, status FROM einsatz_mitglieder WHERE deleted_at IS NULL AND einsatz_id IN ("+in+")", openIDs...)
	if err != nil {
		return nil, err
	}
	mitglieder := map[string]map[string]any{}
	for _, r := range mitRows {
		mitglieder[db.Str(r, "id")] = r
	}
	schichten, err := db.QueryMaps(q, "SELECT id, einsatz_id, node_id, mitglied_id, kraft_key, status, von, bis, aktiv FROM einsatz_schichten WHERE deleted_at IS NULL AND einsatz_id IN ("+in+")", openIDs...)
	if err != nil {
		return nil, err
	}

	// Effektiver Status je Einheit ("n:<id>" / "m:<id>") aus den aktiven Schichten
	derived := map[string]string{}
	for _, s := range schichten {
		if db.Int(s, "aktiv") != 1 {
			continue
		}
		ss := db.Str(s, "status")
		if ss == "" {
			ss = "geplant"
		}
		if ss == "geplant" || statusRank[ss] == 0 {
			continue
		}
		unit := "n:" + db.Str(s, "node_id")
		if mid := db.Str(s, "mitglied_id"); mid != "" {
			unit = "m:" + mid
		}
		if cur, ok := derived[unit]; !ok || statusRank[ss] > statusRank[cur] {
			derived[unit] = ss
		}
	}
	unitStatus := func(unit string, row map[string]any) string {
		if s, ok := derived[unit]; ok {
			return s
		}
		if s := db.Str(row, "status"); s != "" {
			return s
		}
		return "geplant"
	}

	rows := []bindRow{}
	for _, r := range nodeRows {
		id := db.Str(r, "id")
		if db.Str(r, "kraft_key") == "" {
			continue
		}
		status := unitStatus("n:"+id, r)
		if status == "abgerueckt" {
			continue
		}
		rows = append(rows, bindRow{kind: "node", id: id, kraftKey: db.Str(r, "kraft_key"), label: db.Str(r, "bezeichnung"), status: status, einsatzID: db.Int(r, "einsatz_id")})
	}
	for _, r := range mitRows {
		id := db.Str(r, "id")
		if db.Str(r, "kraft_key") == "" {
			continue
		}
		status := unitStatus("m:"+id, r)
		if status == "abgerueckt" {
			continue
		}
		rows = append(rows, bindRow{kind: "mitglied", id: id, kraftKey: db.Str(r, "kraft_key"), label: db.Str(r, "bezeichnung"), status: status, einsatzID: db.Int(r, "einsatz_id")})
	}
	now := time.Now()
	for _, s := range schichten {
		if db.Str(s, "kraft_key") == "" || db.Int(s, "aktiv") != 1 {
			continue
		}
		status := db.Str(s, "status")
		if status == "" {
			status = "geplant"
		}
		if status == "abgerueckt" {
			continue
		}
		if bisStr := db.Str(s, "bis"); bisStr != "" {
			if t, ok := parseTime(bisStr); ok && t.Before(now) {
				continue
			}
		}
		// Einheit, zu der die Schicht gehoert: abgerueckt -> Schicht bindet nicht mehr
		mid := db.Str(s, "mitglied_id")
		nn := nodes[db.Str(s, "node_id")]
		var mm map[string]any
		if mid != "" {
			mm = mitglieder[mid]
		}
		owner := mm
		unit := "m:" + mid
		if owner == nil {
			owner = nn
			unit = "n:" + db.Str(s, "node_id")
		}
		if owner == nil {
			continue
		}
		if unitStatus(unit, owner) == "abgerueckt" {
			continue
		}
		label := ""
		if mm != nil && db.Str(mm, "bezeichnung") != "" {
			label = db.Str(mm, "bezeichnung")
		} else if nn != nil {
			label = db.Str(nn, "bezeichnung")
		}
		var parent *string
		if nn != nil {
			parent = strp(db.Str(nn, "bezeichnung"))
		}
		rows = append(rows, bindRow{kind: "schicht", id: db.Str(s, "id"), kraftKey: db.Str(s, "kraft_key"), label: label, status: status,
			einsatzID: db.Int(s, "einsatz_id"), von: db.StrPtr(s, "von"), bis: db.StrPtr(s, "bis"), parentLabel: parent})
	}
	if len(rows) == 0 {
		return out, nil
	}

	seen := map[int64]bool{}
	einsatzIDs := []any{}
	for _, r := range rows {
		if !seen[r.einsatzID] {
			seen[r.einsatzID] = true
			einsatzIDs = append(einsatzIDs, r.einsatzID)
		}
	}
	in = db.Placeholders(len(einsatzIDs))
	eRows, err := db.QueryMaps(q, "SELECT id, uuid, title, stab_name, ebene, owner_id FROM einsaetze WHERE id IN ("+in+")", einsatzIDs...)
	if err != nil {
		return nil, err
	}
	einsaetze := map[int64]map[string]any{}
	for _, e := range eRows {
		einsaetze[db.Int(e, "id")] = e
	}
	mRows, err := db.QueryMaps(q, `SELECT DISTINCT einsatz_id FROM einsatz_members WHERE user_id = ? AND einsatz_id IN (`+in+`)
		UNION SELECT om.einsatz_id FROM einsatz_org_members om JOIN users ou ON ou.id = ? WHERE om.org_id = ou.org_id AND om.einsatz_id IN (`+in+`)`,
		append(append(append([]any{uid}, einsatzIDs...), uid), einsatzIDs...)...)
	if err != nil {
		return nil, err
	}
	memberOf := map[int64]bool{}
	for _, m := range mRows {
		memberOf[db.Int(m, "einsatz_id")] = true
	}

	for _, r := range rows {
		e := einsaetze[r.einsatzID]
		if e == nil {
			continue
		}
		visible := uid > 0 && (db.Int(e, "owner_id") == uid || memberOf[r.einsatzID])
		var euuid, etitle any
		if visible {
			euuid = db.Str(e, "uuid")
			etitle = db.Str(e, "title")
		}
		out[r.kraftKey] = append(out[r.kraftKey], map[string]any{
			"kind":          r.kind,
			"id":            r.id,
			"label":         r.label,
			"parent_label":  strOrNil(r.parentLabel),
			"status":        r.status,
			"von":           nonEmpty(r.von),
			"bis":           nonEmpty(r.bis),
			"own":           ownEinsatzID != nil && r.einsatzID == *ownEinsatzID,
			"visible":       visible,
			"einsatz_uuid":  euuid,
			"einsatz_title": etitle,
			"stab_name":     db.Str(e, "stab_name"),
			"ebene_label":   ebeneLabel(db.Str(e, "ebene")),
		})
	}
	return out, nil
}

func strOrNil(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func nonEmpty(p *string) any {
	if p == nil || strings.TrimSpace(*p) == "" {
		return nil
	}
	return *p
}
