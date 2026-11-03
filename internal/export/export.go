// Package export: vollstaendiger Export/Import eines Einsatzes als JSON
// (maschinenlesbar) sowie Snapshots (z.B. beim Abschluss).
//
// Format: { format: "luk-einsatz", version: 1, exported_at, exported_by,
//
//	einsatz, members, nodes, mitglieder, schichten, schadenskonten,
//	lagekarten, abschnitte, features, events, access_log, share_links }
//
// Geloeschte Zeilen sind enthalten (deleted_at gesetzt), damit das Log
// nach einem Import weiterhin auf alle Objekte verweisen kann.
package export

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/einsatz"
	"github.com/friedrichengel/thw_luk_software_go/internal/input"
	"github.com/friedrichengel/thw_luk_software_go/internal/kraefte"
	"github.com/friedrichengel/thw_luk_software_go/internal/lagekarte"
	"github.com/friedrichengel/thw_luk_software_go/internal/schadenskonten"
	"github.com/friedrichengel/thw_luk_software_go/internal/stab"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
	"github.com/friedrichengel/thw_luk_software_go/internal/uuid"
)

const (
	Format  = "luk-einsatz"
	Version = 1
)

func Export(q db.Q, c *access.Ctx, appVersion string) (map[string]any, error) {
	e := c.Einsatz
	einsatzID := e.ID
	owner, err := user.FindByID(q, e.OwnerID)
	if err != nil {
		return nil, err
	}
	tree, err := einsatz.TreeOf(q, einsatzID, true)
	if err != nil {
		return nil, err
	}
	members := []map[string]any{}
	ms, err := einsatz.Members(q, einsatzID)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		members = append(members, map[string]any{"username": m.Username, "display_name": m.DisplayName, "roles": m.Roles})
	}
	logEvents, err := einsatz.Log(q, einsatzID, 100000, 0)
	if err != nil {
		return nil, err
	}
	events := make([]einsatz.LogEvent, 0, len(logEvents))
	for i := len(logEvents) - 1; i >= 0; i-- {
		events = append(events, logEvents[i])
	}
	acc, err := einsatz.AccessLog(q, einsatzID, 100000)
	if err != nil {
		return nil, err
	}
	accessLog := make([]einsatz.AccessEntry, 0, len(acc))
	for i := len(acc) - 1; i >= 0; i-- {
		accessLog = append(accessLog, acc[i])
	}
	links, err := einsatz.ShareLinks(q, einsatzID)
	if err != nil {
		return nil, err
	}
	shareLinks := []map[string]any{}
	for _, l := range links {
		shareLinks = append(shareLinks, map[string]any{
			"label": l.Label, "created_by": l.CreatedBy, "created_at": l.CreatedAt,
			"expires_at": l.ExpiresAt, "revoked_at": l.RevokedAt, "use_count": l.UseCount,
		})
	}
	sks, err := schadenskonten.List(q, einsatzID, true)
	if err != nil {
		return nil, err
	}
	karten, err := lagekarte.List(q, einsatzID, true)
	if err != nil {
		return nil, err
	}
	abschnitte, err := lagekarte.AllAbschnitte(q, einsatzID, true)
	if err != nil {
		return nil, err
	}
	features, err := lagekarte.AllFeatures(q, einsatzID, true)
	if err != nil {
		return nil, err
	}
	var lat, lng any
	if e.OrtLat != nil {
		lat = *e.OrtLat
	}
	if e.OrtLng != nil {
		lng = *e.OrtLng
	}
	var closedAt any
	if e.ClosedAt != nil {
		closedAt = *e.ClosedAt
	}
	return map[string]any{
		"format":      Format,
		"version":     Version,
		"exported_at": db.Now(),
		"exported_by": c.UserLabel,
		"app_version": appVersion,
		"einsatz": map[string]any{
			"uuid":        e.UUID,
			"title":       e.Title,
			"ebene":       e.Ebene,
			"stab_name":   e.StabName,
			"note":        e.Note,
			"vorwarn_min": e.VorwarnMin,
			"bereich_key": e.BereichKey,
			"ort_name":    e.OrtName,
			"ort_lat":     lat,
			"ort_lng":     lng,
			"stein_aktiv": e.SteinAktiv,
			"status":      e.Status,
			"owner_label": user.Label(owner),
			"created_at":  e.CreatedAt,
			"updated_at":  e.UpdatedAt,
			"closed_at":   closedAt,
		},
		"members":        members,
		"nodes":          tree.Nodes,
		"mitglieder":     tree.Mitglieder,
		"schichten":      tree.Schichten,
		"schadenskonten": sks,
		"lagekarten":     karten,
		"abschnitte":     abschnitte,
		"features":       features,
		"events":         events,
		"access_log":     accessLog,
		"share_links":    shareLinks,
		"seq":            tree.Seq,
	}, nil
}

func Snapshot(q db.Q, c *access.Ctx, kind, appVersion string) (int64, error) {
	data, err := Export(q, c, appVersion)
	if err != nil {
		return 0, err
	}
	js, err := json.Marshal(data)
	if err != nil {
		return 0, err
	}
	var id int64
	err = db.InTx(q, func(q db.Q) error {
		res, err := q.Exec("INSERT INTO einsatz_snapshots (einsatz_id, kind, created_at, created_by, export_json) VALUES (?, ?, ?, ?, ?)",
			c.Einsatz.ID, kind, db.Now(), db.NullIfZero(c.UID), string(js))
		if err != nil {
			return err
		}
		id = db.LastID(res)
		_, err = access.Event(q, c, "snapshot", fmt.Sprint(id), "insert", "Snapshot erstellt ("+kind+")", nil, map[string]any{"id": id, "kind": kind})
		return err
	})
	return id, err
}

type SnapshotInfo struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	CreatedAt string `json:"created_at"`
	Size      int64  `json:"size"`
	CreatedBy string `json:"created_by"`
}

func Snapshots(q db.Q, einsatzID int64) ([]SnapshotInfo, error) {
	rows, err := db.QueryMaps(q, `SELECT s.id, s.kind, s.created_at, LENGTH(s.export_json) AS size, u.username, u.display_name
		FROM einsatz_snapshots s LEFT JOIN users u ON u.id = s.created_by WHERE s.einsatz_id = ? ORDER BY s.id DESC`, einsatzID)
	if err != nil {
		return nil, err
	}
	out := []SnapshotInfo{}
	for _, r := range rows {
		cb := strings.TrimSpace(db.Str(r, "display_name"))
		if cb == "" {
			cb = db.Str(r, "username")
		}
		out = append(out, SnapshotInfo{ID: db.Int(r, "id"), Kind: db.Str(r, "kind"), CreatedAt: db.Str(r, "created_at"), Size: db.Int(r, "size"), CreatedBy: cb})
	}
	return out, nil
}

// SnapshotJSON liefert den gespeicherten Export (ok=false, wenn unbekannt).
func SnapshotJSON(q db.Q, einsatzID, id int64) (string, bool, error) {
	row, err := db.QueryMap(q, "SELECT export_json FROM einsatz_snapshots WHERE id = ? AND einsatz_id = ? LIMIT 1", id, einsatzID)
	if err != nil || row == nil {
		return "", false, err
	}
	return db.Str(row, "export_json"), true, nil
}

// ------------------------------------------------------------------
// Import
// ------------------------------------------------------------------

type ImportResult struct {
	UUID     string   `json:"uuid"`
	Warnings []string `json:"warnings"`
}

// strOr: (string)($m[k] ?? def)
func strOr(m map[string]any, k string, def string) string {
	if v, ok := m[k]; ok && v != nil {
		return input.Raw(v)
	}
	return def
}

// nullable: $m[k] ?? null
func nullable(m map[string]any, k string) any {
	if v, ok := m[k]; ok && v != nil {
		return input.Raw(v)
	}
	return nil
}

func jsonOrNull(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}

type idMap map[string]map[string]string

func (m idMap) get(kind, old string) (string, bool) {
	v, ok := m[kind][old]
	return v, ok
}

func (m idMap) newID(kind string, old any) string {
	o := input.Raw(old)
	if o == "" {
		o = uuid.V4()
	}
	if v, ok := m[kind][o]; ok {
		return v
	}
	m[kind][o] = uuid.V4()
	return m[kind][o]
}

// Import legt aus einer Export-Datei einen NEUEN Einsatz an (Eigentuemer =
// Importierender). Alle IDs werden neu vergeben, Verweise umgeschrieben.
// Mitglieder werden ueber den Benutzernamen auf lokale Konten gemappt.
func Import(q db.Q, p access.Principal, data map[string]any, validBereich func(string) bool) (*ImportResult, error) {
	if input.Raw(data["format"]) != Format {
		return nil, apierr.New("invalid_format", 400)
	}
	if input.Int(data["version"], 0) != Version {
		return nil, apierr.New("unsupported_version", 400)
	}
	src := input.Map(data["einsatz"])
	if src == nil {
		src = map[string]any{}
	}
	title := input.Str(src["title"], 200)
	if title == "" {
		title = "Importierter Einsatz"
	}
	ebene := strOr(src, "ebene", "ov")
	if !stab.EbeneExists(ebene) {
		ebene = "ov"
	}
	stabName := input.Str(src["stab_name"], 160)
	note := input.StrRaw(src["note"], 2000)
	vorwarn := einsatz.VorwarnDefaultMin
	if v, ok := src["vorwarn_min"]; ok && v != nil {
		vorwarn = input.Int(v, 0)
	}
	if vorwarn < 0 || vorwarn > einsatz.VorwarnMaxMin {
		vorwarn = einsatz.VorwarnDefaultMin
	}
	bereich := input.Str(src["bereich_key"], 80)
	if validBereich != nil && !validBereich(bereich) {
		bereich = ""
	}
	ortName := input.Str(src["ort_name"], 160)
	var ortLat, ortLng any
	if input.IsNumeric(src["ort_lat"]) && input.IsNumeric(src["ort_lng"]) {
		ortLat = input.Float(src["ort_lat"])
		ortLng = input.Float(src["ort_lng"])
	}
	steinAktiv := 1
	if v, ok := src["stein_aktiv"]; ok {
		if input.Empty(v) {
			steinAktiv = 0
		}
	}
	warnings := []string{}
	uid := p.UID
	var result *ImportResult

	err := db.InTx(q, func(q db.Q) error {
		u := uuid.V4()
		now := db.Now()
		res, err := q.Exec(`INSERT INTO einsaetze (uuid, title, ebene, stab_name, note, vorwarn_min, bereich_key, ort_name, ort_lat, ort_lng, stein_aktiv, owner_id, status, version, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', 1, ?, ?)`,
			u, title, ebene, stabName, note, vorwarn, bereich, ortName, ortLat, ortLng, steinAktiv, uid, strOr(src, "created_at", now), now)
		if err != nil {
			return err
		}
		einsatzID := db.LastID(res)

		// Mitglieder: ueber Benutzername mappen
		for _, mv := range input.List(data["members"]) {
			m := input.Map(mv)
			if m == nil {
				continue
			}
			mu, err := user.FindByUsername(q, input.Raw(m["username"]))
			if err != nil {
				return err
			}
			if mu == nil || !mu.IsEinsatzUser() {
				name := input.Raw(m["display_name"])
				if name == "" {
					name = strOr(m, "username", "?")
				}
				warnings = append(warnings, "Mitglied „"+name+"\" nicht gefunden - Rollen nicht übernommen.")
				continue
			}
			for _, r := range stab.NormalizeRoles(m["roles"]) {
				if _, err := q.Exec("INSERT OR IGNORE INTO einsatz_members (einsatz_id, user_id, role, added_by, added_at) VALUES (?, ?, ?, ?, ?)", einsatzID, mu.ID, r, uid, now); err != nil {
					return err
				}
			}
		}
		// Importierender ist auf jeden Fall Leiter Stab
		if _, err := q.Exec("INSERT OR IGNORE INTO einsatz_members (einsatz_id, user_id, role, added_by, added_at) VALUES (?, ?, ?, ?, ?)", einsatzID, uid, "ltr_stab", uid, now); err != nil {
			return err
		}

		// ID-Mapping
		ids := idMap{"node": {}, "mitglied": {}, "schicht": {}, "sk": {}, "fremd": {}, "karte": {}, "feature": {}}
		kraftKey := func(v any) string {
			k := input.Str(v, kraefte.KraftKeyMax)
			if kraefte.ValidKey(k) {
				return k
			}
			return ""
		}

		// Knoten (Eltern koennen spaeter in der Liste stehen -> zwei Durchgaenge)
		nodes := []map[string]any{}
		for _, nv := range input.List(data["nodes"]) {
			if n := input.Map(nv); n != nil {
				if input.Raw(n["id"]) == "" {
					n["id"] = uuid.V4()
				}
				ids.newID("node", n["id"])
				nodes = append(nodes, n)
			}
		}
		for _, n := range nodes {
			var pid any
			if mapped, ok := ids.get("node", input.Raw(n["parent_id"])); ok && n["parent_id"] != nil {
				pid = mapped
			}
			status := strOr(n, "status", "geplant")
			if !stab.StatusExists(status) {
				status = "geplant"
			}
			typ := "einheit"
			if input.Raw(n["type"]) == "fuehrung" {
				typ = "fuehrung"
			}
			statusAt := nullable(n, "status_at")
			// Fuehrungsstellen fuehren keinen Anfahrtsstatus (Altbestaende
			// aus aelteren Exporten koennen noch einen mitbringen).
			if typ == "fuehrung" {
				status = "geplant"
				statusAt = nil
			}
			nid, _ := ids.get("node", input.Raw(n["id"]))
			if _, err := q.Exec(`INSERT INTO einsatz_nodes (id, einsatz_id, parent_id, sort_order, type, bezeichnung, funkruf, ov, kraft_key, note, status, status_at, extern_link_url, extern_link_label,
				version, created_at, created_by, updated_at, updated_by, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
				nid, einsatzID, pid, input.Int(n["sort_order"], 0), typ,
				input.Str(n["bezeichnung"], 160), input.Str(n["funkruf"], 120), input.Str(n["ov"], 120), kraftKey(n["kraft_key"]), input.StrRaw(n["note"], 500),
				status, statusAt,
				input.Str(n["extern_link_url"], 500), input.Str(n["extern_link_label"], 160),
				now, uid, strOr(n, "updated_at", now), uid, nullable(n, "deleted_at")); err != nil {
				return err
			}
		}

		for _, mv := range input.List(data["mitglieder"]) {
			m := input.Map(mv)
			if m == nil {
				continue
			}
			nodeID, ok := ids.get("node", input.Raw(m["node_id"]))
			if !ok {
				continue
			}
			status := strOr(m, "status", "geplant")
			if !stab.StatusExists(status) {
				status = "geplant"
			}
			kind := "einheit"
			if input.Raw(m["kind"]) == "fachberater" {
				kind = "fachberater"
			}
			if _, err := q.Exec(`INSERT INTO einsatz_mitglieder (id, einsatz_id, node_id, kind, sort_order, funktion, name, bezeichnung, funkruf, telefon, ov, kraft_key, note, status, status_at,
				version, created_at, created_by, updated_at, updated_by, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
				ids.newID("mitglied", m["id"]), einsatzID, nodeID, kind, input.Int(m["sort_order"], 0),
				input.Str(m["funktion"], 120), input.Str(m["name"], 160), input.Str(m["bezeichnung"], 160),
				input.Str(m["funkruf"], 120), input.Str(m["telefon"], 60), input.Str(m["ov"], 120), kraftKey(m["kraft_key"]), input.StrRaw(m["note"], 500),
				status, nullable(m, "status_at"),
				now, uid, strOr(m, "updated_at", now), uid, nullable(m, "deleted_at")); err != nil {
				return err
			}
		}

		for _, sv := range input.List(data["schichten"]) {
			s := input.Map(sv)
			if s == nil {
				continue
			}
			nodeID, ok := ids.get("node", input.Raw(s["node_id"]))
			if !ok {
				continue
			}
			var mid any
			if !input.Empty(s["mitglied_id"]) {
				if mapped, ok := ids.get("mitglied", input.Raw(s["mitglied_id"])); ok {
					mid = mapped
				}
			}
			sStatus := "geplant"
			if st := input.Raw(s["status"]); stab.StatusExists(st) {
				sStatus = st
			}
			aktiv := 0
			if !input.Empty(s["aktiv"]) {
				aktiv = 1
			}
			if _, err := q.Exec(`INSERT INTO einsatz_schichten (id, einsatz_id, node_id, mitglied_id, von, bis, staerke, name, telefon, funkruf, ov, kraft_key, note, aktiv,
				status, status_at, version, created_at, created_by, updated_at, updated_by, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
				ids.newID("schicht", s["id"]), einsatzID, nodeID, mid,
				input.Str(s["von"], 32), input.Str(s["bis"], 32), input.Str(s["staerke"], 40),
				input.Str(s["name"], 160), input.Str(s["telefon"], 60), input.Str(s["funkruf"], 120), input.Str(s["ov"], 120), kraftKey(s["kraft_key"]),
				input.StrRaw(s["note"], 200), aktiv,
				sStatus, nullable(s, "status_at"),
				now, uid, strOr(s, "updated_at", now), uid, nullable(s, "deleted_at")); err != nil {
				return err
			}
		}

		// Schadenskonten
		for _, skv := range input.List(data["schadenskonten"]) {
			sk := input.Map(skv)
			if sk == nil {
				continue
			}
			sid := ids.newID("sk", sk["id"])
			tzJSON, _ := json.Marshal(schadenskonten.SanitizeTz(sk["taktische_zeichen"]))
			if _, err := q.Exec(`INSERT INTO schadenskonten (id, einsatz_id, title, description, taktische_zeichen, sort_order, version, created_at, created_by, updated_at, updated_by, deleted_at)
				VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
				sid, einsatzID, input.Str(sk["title"], 200), input.StrRaw(sk["description"], 5000), string(tzJSON),
				input.Int(sk["sort_order"], 0), now, uid, strOr(sk, "updated_at", now), uid, nullable(sk, "deleted_at")); err != nil {
				return err
			}
			for _, rv := range input.List(sk["refs"]) {
				r := input.Map(rv)
				if r == nil {
					continue
				}
				kind := input.Raw(r["ref_kind"])
				rid := input.Raw(r["ref_id"])
				var mapped string
				var ok bool
				if kind == "node" {
					mapped, ok = ids.get("node", rid)
				} else if kind == "mitglied" {
					mapped, ok = ids.get("mitglied", rid)
				}
				if ok {
					if _, err := q.Exec("INSERT OR IGNORE INTO schadenskonten_refs (schadenskonto_id, einsatz_id, ref_kind, ref_id, added_by, added_at) VALUES (?, ?, ?, ?, ?, ?)",
						sid, einsatzID, kind, mapped, uid, now); err != nil {
						return err
					}
				}
			}
			for _, fv := range input.List(sk["fremd"]) {
				f := input.Map(fv)
				if f == nil {
					continue
				}
				ff, err := schadenskonten.SanitizeFremd(f)
				if err != nil {
					return err
				}
				kind := ff.Str("kind")
				if kind == "" {
					kind = "sonstige"
				}
				if _, err := q.Exec(`INSERT INTO schadenskonten_fremd (id, schadenskonto_id, einsatz_id, kind, bezeichnung, funkruf, ov, staerke, note, sort_order, version, created_at, created_by, updated_at, updated_by, deleted_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
					ids.newID("fremd", f["id"]), sid, einsatzID, kind, ff.Str("bezeichnung"), ff.Str("funkruf"),
					ff.Str("ov"), ff.Str("staerke"), ff.Str("note"), input.Int(f["sort_order"], 0),
					now, uid, strOr(f, "updated_at", now), uid, nullable(f, "deleted_at")); err != nil {
					return err
				}
			}
		}

		// Lagekarten
		for _, kv := range input.List(data["lagekarten"]) {
			k := input.Map(kv)
			if k == nil {
				continue
			}
			var lat, lng, zoom any
			if input.IsNumeric(k["center_lat"]) {
				lat = input.Float(k["center_lat"])
			}
			if input.IsNumeric(k["center_lng"]) {
				lng = input.Float(k["center_lng"])
			}
			if v, ok := k["zoom"]; ok && v != nil {
				zoom = input.Int(v, 11)
			}
			basemap := "osm"
			if b := input.Raw(k["basemap"]); b == "osm" || b == "sat" {
				basemap = b
			}
			title := input.Str(strOr(k, "title", "Lagekarte"), 200)
			if _, err := q.Exec(`INSERT INTO lagekarten (id, einsatz_id, title, center_lat, center_lng, zoom, basemap, sort_order, version, created_at, created_by, updated_at, updated_by, deleted_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
				ids.newID("karte", k["id"]), einsatzID, title, lat, lng, zoom, basemap,
				input.Int(k["sort_order"], 0), now, uid, strOr(k, "updated_at", now), uid, nullable(k, "deleted_at")); err != nil {
				return err
			}
		}
		for _, av := range input.List(data["abschnitte"]) {
			a := input.Map(av)
			if a == nil {
				continue
			}
			kid, ok := ids.get("karte", input.Raw(a["lagekarte_id"]))
			if !ok {
				continue
			}
			if _, err := q.Exec(`INSERT OR IGNORE INTO lagekarte_abschnitte (lagekarte_id, nr, einsatz_id, name, version, created_at, created_by, updated_at, updated_by, deleted_at) VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
				kid, input.Int(a["nr"], 0), einsatzID, input.Str(a["name"], 80), now, uid, now, uid, nullable(a, "deleted_at")); err != nil {
				return err
			}
		}
		for _, fv := range input.List(data["features"]) {
			f := input.Map(fv)
			if f == nil {
				continue
			}
			kid, ok := ids.get("karte", input.Raw(f["lagekarte_id"]))
			if !ok {
				continue
			}
			kind := input.Raw(f["kind"])
			if !containsStr(lagekarte.Kinds, kind) {
				continue
			}
			geom, err := lagekarte.SanitizeGeometry(kind, f["geometry"])
			var props map[string]any
			if err == nil {
				props, err = lagekarte.SanitizeProps(kind, f["props"])
			}
			if err != nil {
				var ae *apierr.Error
				if errors.As(err, &ae) {
					warnings = append(warnings, "Kartenobjekt übersprungen ("+ae.Code+").")
					continue
				}
				return err
			}
			rk := input.Raw(f["ref_kind"])
			rid := input.Raw(f["ref_id"])
			var mappedRef any
			var refKind any
			if rk == "node" {
				if m, ok := ids.get("node", rid); ok {
					mappedRef, refKind = m, rk
				}
			} else if rk == "mitglied" {
				if m, ok := ids.get("mitglied", rid); ok {
					mappedRef, refKind = m, rk
				}
			}
			geomJSON, _ := json.Marshal(geom)
			propsJSON, _ := json.Marshal(props)
			abJSON, _ := json.Marshal(lagekarte.SanitizeAbschnitte(f["abschnitte"]))
			if _, err := q.Exec(`INSERT INTO lagekarte_features (id, lagekarte_id, einsatz_id, kind, geometry, props, abschnitte, ref_kind, ref_id, version, created_at, created_by, updated_at, updated_by, deleted_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
				ids.newID("feature", f["id"]), kid, einsatzID, kind, string(geomJSON), string(propsJSON), string(abJSON),
				refKind, mappedRef, now, uid, strOr(f, "updated_at", now), uid, nullable(f, "deleted_at")); err != nil {
				return err
			}
		}

		// Ereignisse uebernehmen (als importierte Historie, entity_id umgeschrieben)
		remapEntity := func(entity, id string) string {
			mapOr := func(kind, old string) string {
				if v, ok := ids.get(kind, old); ok {
					return v
				}
				return old
			}
			switch entity {
			case "node":
				return mapOr("node", id)
			case "mitglied":
				return mapOr("mitglied", id)
			case "schicht":
				return mapOr("schicht", id)
			case "schadenskonto":
				return mapOr("sk", id)
			case "sk_fremd":
				return mapOr("fremd", id)
			case "lagekarte":
				return mapOr("karte", id)
			case "feature":
				return mapOr("feature", id)
			case "abschnitt":
				p0, p1, _ := strings.Cut(id, ":")
				return mapOr("karte", p0) + ":" + p1
			case "sk_ref":
				p0, p1, _ := strings.Cut(id, ":")
				ref := p1
				if v, ok := ids.get("node", ref); ok {
					ref = v
				} else if v, ok := ids.get("mitglied", ref); ok {
					ref = v
				}
				return mapOr("sk", p0) + ":" + ref
			}
			return id
		}
		count := 0
		for _, ev := range input.List(data["events"]) {
			e := input.Map(ev)
			if e == nil {
				continue
			}
			entity := input.Str(e["entity"], 40)
			if _, err := q.Exec(`INSERT INTO einsatz_events (einsatz_id, ts, user_id, user_label, entity, entity_id, action, summary, before_json, after_json, imported)
				VALUES (?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, 1)`,
				einsatzID, input.Str(strOr(e, "ts", now), 32), input.Str(e["user_label"], 160), entity,
				remapEntity(entity, input.Raw(e["entity_id"])), input.Str(e["action"], 40), input.Str(e["summary"], 400),
				jsonOrNull(e["before"]), jsonOrNull(e["after"])); err != nil {
				return err
			}
			count++
		}

		ctx, err := access.ResolveByID(q, p, einsatzID)
		if err != nil {
			return err
		}
		if ctx != nil {
			summary := fmt.Sprintf("Einsatz „%s\" aus Datei importiert (Original-UUID %s, %d Log-Einträge übernommen, exportiert am %s von %s)",
				title, strOr(src, "uuid", "?"), count, strOr(data, "exported_at", "?"), strOr(data, "exported_by", "?"))
			if _, err := access.Event(q, ctx, "einsatz", u, "import", summary, nil,
				map[string]any{"source_uuid": nullable(src, "uuid"), "warnings": warnings}); err != nil {
				return err
			}
		}
		result = &ImportResult{UUID: u, Warnings: warnings}
		return nil
	})
	return result, err
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
