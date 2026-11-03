// Package lagekarte: Lagekarten, Einsatzabschnitte (Layer) und Kartenobjekte.
// Jedes Kartenobjekt ist eine eigene versionierte Zeile; mehrere Personen
// koennen gleichzeitig zeichnen.
package lagekarte

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/einsatz"
	"github.com/friedrichengel/thw_luk_software_go/internal/input"
	"github.com/friedrichengel/thw_luk_software_go/internal/kraefte"
	"github.com/friedrichengel/thw_luk_software_go/internal/uuid"
)

var Kinds = []string{"marker", "polygon", "polyline", "rectangle", "circle", "arrow", "text"}

var KindLabels = map[string]string{
	"marker": "Taktisches Zeichen", "polygon": "Fläche", "polyline": "Linie", "rectangle": "Rechteck",
	"circle": "Kreis", "arrow": "Pfeil", "text": "Text",
}

const MaxPoints = 5000

var (
	reColor      = regexp.MustCompile(`(?i)^#[0-9a-f]{6}$`)
	reMarkerFile = regexp.MustCompile(`^[\p{L}\p{N}_\-. ()]+(/[\p{L}\p{N}_\-. ()]+)*\.svg$`)
)

func init() {
	einsatz.RegisterRow("lagekarten", func(m map[string]any) any { return KarteRow(m) })
	einsatz.RegisterRow("lagekarte_features", func(m map[string]any) any { return FeatureRow(m) })
}

// ------------------------------------------------------------------
// Rows
// ------------------------------------------------------------------

type Karte struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	CenterLat    *float64 `json:"center_lat"`
	CenterLng    *float64 `json:"center_lng"`
	Zoom         *int     `json:"zoom"`
	Basemap      string   `json:"basemap"`
	SortOrder    int      `json:"sort_order"`
	Version      int      `json:"version"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	DeletedAt    *string  `json:"deleted_at"`
	FeatureCount *int     `json:"feature_count"`
}

type Abschnitt struct {
	LagekarteID string  `json:"lagekarte_id"`
	Nr          int     `json:"nr"`
	Name        string  `json:"name"`
	Version     int     `json:"version"`
	DeletedAt   *string `json:"deleted_at"`
}

type Feature struct {
	ID          string  `json:"id"`
	LagekarteID string  `json:"lagekarte_id"`
	Kind        string  `json:"kind"`
	Geometry    any     `json:"geometry"`
	Props       any     `json:"props"`
	Abschnitte  []int   `json:"abschnitte"`
	RefKind     *string `json:"ref_kind"`
	RefID       *string `json:"ref_id"`
	Version     int     `json:"version"`
	UpdatedAt   string  `json:"updated_at"`
	DeletedAt   *string `json:"deleted_at"`
}

type FeatureRef struct {
	ID          string `json:"id"`
	LagekarteID string `json:"lagekarte_id"`
	Kind        string `json:"kind"`
	RefKind     string `json:"ref_kind"`
	RefID       string `json:"ref_id"`
}

// AbschnittDeleteResult: geloeschter Abschnitt + bereinigte Kartenobjekte.
type AbschnittDeleteResult struct {
	Abschnitt map[string]any `json:"abschnitt"`
	Features  []*Feature     `json:"features"`
}

func KarteRow(m map[string]any) *Karte {
	k := &Karte{
		ID:        db.Str(m, "id"),
		Title:     db.Str(m, "title"),
		CenterLat: db.FloatPtr(m, "center_lat"),
		CenterLng: db.FloatPtr(m, "center_lng"),
		Basemap:   db.Str(m, "basemap"),
		SortOrder: int(db.Int(m, "sort_order")),
		Version:   int(db.Int(m, "version")),
		CreatedAt: db.Str(m, "created_at"),
		UpdatedAt: db.Str(m, "updated_at"),
		DeletedAt: db.StrPtr(m, "deleted_at"),
	}
	if !db.IsNull(m, "zoom") {
		z := int(db.Int(m, "zoom"))
		k.Zoom = &z
	}
	if v, ok := m["feature_count"]; ok && v != nil {
		fc := int(db.Int(m, "feature_count"))
		k.FeatureCount = &fc
	}
	return k
}

func AbschnittRow(m map[string]any) *Abschnitt {
	return &Abschnitt{
		LagekarteID: db.Str(m, "lagekarte_id"),
		Nr:          int(db.Int(m, "nr")),
		Name:        db.Str(m, "name"),
		Version:     int(db.Int(m, "version")),
		DeletedAt:   db.StrPtr(m, "deleted_at"),
	}
}

// decodeObj entspricht json_decode(..., true) ?: [] - leere Werte werden zu {}.
func decodeObj(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil || v == nil {
		return map[string]any{}
	}
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			return map[string]any{}
		}
	case []any:
		if len(x) == 0 {
			return map[string]any{}
		}
	case bool:
		if !x {
			return map[string]any{}
		}
	}
	return v
}

func decodeAbschnitte(s string) []int {
	out := []int{}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return out
	}
	for _, x := range input.List(v) {
		out = append(out, input.Int(x, 0))
	}
	return out
}

func FeatureRow(m map[string]any) *Feature {
	return &Feature{
		ID:          db.Str(m, "id"),
		LagekarteID: db.Str(m, "lagekarte_id"),
		Kind:        db.Str(m, "kind"),
		Geometry:    decodeObj(db.Str(m, "geometry")),
		Props:       decodeObj(db.Str(m, "props")),
		Abschnitte:  decodeAbschnitte(db.Str(m, "abschnitte")),
		RefKind:     db.StrPtr(m, "ref_kind"),
		RefID:       db.StrPtr(m, "ref_id"),
		Version:     int(db.Int(m, "version")),
		UpdatedAt:   db.Str(m, "updated_at"),
		DeletedAt:   db.StrPtr(m, "deleted_at"),
	}
}

func delClause(includeDeleted bool, prefix string) string {
	if includeDeleted {
		return ""
	}
	return " AND " + prefix + "deleted_at IS NULL"
}

// ------------------------------------------------------------------
// Karten
// ------------------------------------------------------------------

func List(q db.Q, einsatzID int64, includeDeleted bool) ([]*Karte, error) {
	rows, err := db.QueryMaps(q, `SELECT k.*, (SELECT COUNT(*) FROM lagekarte_features f WHERE f.lagekarte_id = k.id AND f.deleted_at IS NULL) AS feature_count
		FROM lagekarten k WHERE k.einsatz_id = ?`+delClause(includeDeleted, "k.")+` ORDER BY k.sort_order, k.created_at`, einsatzID)
	if err != nil {
		return nil, err
	}
	out := []*Karte{}
	for _, r := range rows {
		out = append(out, KarteRow(r))
	}
	return out, nil
}

// KarteByID liefert die rohe Zeile einer nicht geloeschten Karte des Einsatzes oder nil.
func KarteByID(q db.Q, einsatzID int64, id string) (map[string]any, error) {
	if !uuid.ValidClientID(id) {
		return nil, nil
	}
	return db.QueryMap(q, "SELECT * FROM lagekarten WHERE id = ? AND einsatz_id = ? AND deleted_at IS NULL LIMIT 1", id, einsatzID)
}

func Abschnitte(q db.Q, karteID string, includeDeleted bool) ([]*Abschnitt, error) {
	rows, err := db.QueryMaps(q, "SELECT * FROM lagekarte_abschnitte WHERE lagekarte_id = ?"+delClause(includeDeleted, "")+" ORDER BY nr", karteID)
	if err != nil {
		return nil, err
	}
	out := []*Abschnitt{}
	for _, r := range rows {
		out = append(out, AbschnittRow(r))
	}
	return out, nil
}

func Features(q db.Q, karteID string, includeDeleted bool) ([]*Feature, error) {
	rows, err := db.QueryMaps(q, "SELECT * FROM lagekarte_features WHERE lagekarte_id = ?"+delClause(includeDeleted, "")+" ORDER BY created_at", karteID)
	if err != nil {
		return nil, err
	}
	out := []*Feature{}
	for _, r := range rows {
		out = append(out, FeatureRow(r))
	}
	return out, nil
}

func AllFeatures(q db.Q, einsatzID int64, includeDeleted bool) ([]*Feature, error) {
	rows, err := db.QueryMaps(q, "SELECT * FROM lagekarte_features WHERE einsatz_id = ?"+delClause(includeDeleted, "")+" ORDER BY created_at", einsatzID)
	if err != nil {
		return nil, err
	}
	out := []*Feature{}
	for _, r := range rows {
		out = append(out, FeatureRow(r))
	}
	return out, nil
}

// FeatureRefs: kompakte Liste aller Verknuepfungen Kartenobjekt -> Einheit (fuer Harke/Schadenskonten).
func FeatureRefs(q db.Q, einsatzID int64) ([]FeatureRef, error) {
	rows, err := db.QueryMaps(q, `SELECT f.id, f.lagekarte_id, f.kind, f.ref_kind, f.ref_id FROM lagekarte_features f
		JOIN lagekarten k ON k.id = f.lagekarte_id AND k.deleted_at IS NULL
		WHERE f.einsatz_id = ? AND f.deleted_at IS NULL AND f.ref_id IS NOT NULL`, einsatzID)
	if err != nil {
		return nil, err
	}
	out := []FeatureRef{}
	for _, r := range rows {
		out = append(out, FeatureRef{ID: db.Str(r, "id"), LagekarteID: db.Str(r, "lagekarte_id"), Kind: db.Str(r, "kind"), RefKind: db.Str(r, "ref_kind"), RefID: db.Str(r, "ref_id")})
	}
	return out, nil
}

func AllAbschnitte(q db.Q, einsatzID int64, includeDeleted bool) ([]*Abschnitt, error) {
	rows, err := db.QueryMaps(q, "SELECT * FROM lagekarte_abschnitte WHERE einsatz_id = ?"+delClause(includeDeleted, "")+" ORDER BY lagekarte_id, nr", einsatzID)
	if err != nil {
		return nil, err
	}
	out := []*Abschnitt{}
	for _, r := range rows {
		out = append(out, AbschnittRow(r))
	}
	return out, nil
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func Create(q db.Q, c *access.Ctx, body map[string]any, defLat, defLng float64) (*Karte, error) {
	einsatzID := c.Einsatz.ID
	id := input.IDOrNew(body["id"])
	if ex, err := einsatz.RowByIDIncludingDeleted(q, "lagekarten", id); err != nil {
		return nil, err
	} else if ex != nil {
		return nil, apierr.New("id_exists", 409)
	}
	title := input.Str(body["title"], 200)
	if title == "" {
		title = "Lagekarte"
	}
	lat, lng := defLat, defLng
	if v, ok := body["center_lat"]; ok && v != nil && input.IsNumeric(v) {
		lat = input.Float(v)
	}
	if v, ok := body["center_lng"]; ok && v != nil && input.IsNumeric(v) {
		lng = input.Float(v)
	}
	zoom := 11
	if v, ok := body["zoom"]; ok && v != nil {
		zoom = clampInt(input.Int(v, 11), 1, 20)
	}
	basemap := "osm"
	if b, _ := body["basemap"].(string); b == "osm" || b == "sat" {
		basemap = b
	}
	now := db.Now()
	var out *Karte
	err := db.InTx(q, func(q db.Q) error {
		n, err := db.QueryInt(q, "SELECT COALESCE(MAX(sort_order), 0) FROM lagekarten WHERE einsatz_id = ? AND deleted_at IS NULL", einsatzID)
		if err != nil {
			return err
		}
		sortOrder := int(n) + 10
		if _, err := q.Exec(`INSERT INTO lagekarten (id, einsatz_id, title, center_lat, center_lng, zoom, basemap, sort_order, version, created_at, created_by, updated_at, updated_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
			id, einsatzID, title, lat, lng, zoom, basemap, sortOrder, now, db.NullIfZero(c.UID), now, db.NullIfZero(c.UID)); err != nil {
			return err
		}
		row, err := einsatz.RowByID(q, "lagekarten", id)
		if err != nil {
			return err
		}
		k := KarteRow(row)
		if _, err := access.Event(q, c, "lagekarte", id, "insert", "Lagekarte „"+title+"\" angelegt", nil, k); err != nil {
			return err
		}
		if err := einsatz.TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = k
		return nil
	})
	return out, err
}

func Update(q db.Q, c *access.Ctx, id string, version int, in map[string]any) (*Karte, error) {
	einsatzID := c.Einsatz.ID
	cur, err := KarteByID(q, einsatzID, id)
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
			t = "Lagekarte"
		}
		fields = append(fields, einsatz.Field{K: "title", V: t})
	}
	if input.Has(in, "center_lat") && input.IsNumeric(in["center_lat"]) {
		fields = append(fields, einsatz.Field{K: "center_lat", V: input.Float(in["center_lat"])})
	}
	if input.Has(in, "center_lng") && input.IsNumeric(in["center_lng"]) {
		fields = append(fields, einsatz.Field{K: "center_lng", V: input.Float(in["center_lng"])})
	}
	if input.Has(in, "zoom") {
		fields = append(fields, einsatz.Field{K: "zoom", V: clampInt(input.Int(in["zoom"], 11), 1, 20)})
	}
	if input.Has(in, "basemap") {
		if b, _ := in["basemap"].(string); b == "osm" || b == "sat" {
			fields = append(fields, einsatz.Field{K: "basemap", V: b})
		}
	}
	if len(fields) == 0 {
		return nil, apierr.New("no_fields", 400)
	}
	var out *Karte
	err = db.InTx(q, func(q db.Q) error {
		row, err := einsatz.UpdateVersioned(q, "lagekarten", id, version, fields, c.UID)
		if err != nil {
			return err
		}
		k := KarteRow(row)
		changed := []string{}
		before := map[string]any{}
		for _, f := range fields {
			before[f.K] = cur[f.K]
			if db.Str(cur, f.K) != input.Raw(f.V) {
				changed = append(changed, einsatz.FieldLabel(f.K))
			}
		}
		changed = einsatz.Uniq(changed)
		if len(changed) > 0 {
			if _, err := access.Event(q, c, "lagekarte", id, "update", "Lagekarte „"+k.Title+"\" geändert: "+strings.Join(changed, ", "), before, k); err != nil {
				return err
			}
			if err := einsatz.TouchEinsatz(q, einsatzID); err != nil {
				return err
			}
		}
		out = k
		return nil
	})
	return out, err
}

func Delete(q db.Q, c *access.Ctx, id string, version int) (map[string]any, error) {
	einsatzID := c.Einsatz.ID
	cur, err := KarteByID(q, einsatzID, id)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, apierr.New("not_found", 404)
	}
	var out map[string]any
	err = db.InTx(q, func(q db.Q) error {
		if _, err := einsatz.SoftDelete(q, "lagekarten", id, version, c.UID); err != nil {
			return err
		}
		now := db.Now()
		if _, err := q.Exec("UPDATE lagekarte_features SET deleted_at = ?, deleted_by = ?, version = version + 1 WHERE lagekarte_id = ? AND deleted_at IS NULL", now, db.NullIfZero(c.UID), id); err != nil {
			return err
		}
		if _, err := q.Exec("UPDATE lagekarte_abschnitte SET deleted_at = ?, deleted_by = ?, version = version + 1 WHERE lagekarte_id = ? AND deleted_at IS NULL", now, db.NullIfZero(c.UID), id); err != nil {
			return err
		}
		after := map[string]any{"id": id, "deleted_at": now}
		if _, err := access.Event(q, c, "lagekarte", id, "delete", "Lagekarte „"+db.Str(cur, "title")+"\" gelöscht", KarteRow(cur), after); err != nil {
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

// ------------------------------------------------------------------
// Abschnitte
// ------------------------------------------------------------------

func abschnitt(q db.Q, karteID string, nr int) (map[string]any, error) {
	return db.QueryMap(q, "SELECT * FROM lagekarte_abschnitte WHERE lagekarte_id = ? AND nr = ? AND deleted_at IS NULL LIMIT 1", karteID, nr)
}

func AbschnittCreate(q db.Q, c *access.Ctx, karteID, name string, wantedNr *int) (*Abschnitt, error) {
	einsatzID := c.Einsatz.ID
	karte, err := KarteByID(q, einsatzID, karteID)
	if err != nil {
		return nil, err
	}
	if karte == nil {
		return nil, apierr.New("not_found", 404)
	}
	name = input.Str(name, 80)
	if name == "" {
		return nil, apierr.New("missing_name", 400)
	}
	var out *Abschnitt
	err = db.InTx(q, func(q db.Q) error {
		maxNr, err := db.QueryInt(q, "SELECT COALESCE(MAX(nr), 0) FROM lagekarte_abschnitte WHERE lagekarte_id = ?", karteID)
		if err != nil {
			return err
		}
		nr := int(maxNr) + 1
		if nr < 1 {
			nr = 1
		}
		if wantedNr != nil && *wantedNr >= 1 {
			ex, err := db.QueryMap(q, "SELECT 1 AS x FROM lagekarte_abschnitte WHERE lagekarte_id = ? AND nr = ?", karteID, *wantedNr)
			if err != nil {
				return err
			}
			if ex == nil {
				nr = *wantedNr
			}
		}
		now := db.Now()
		if _, err := q.Exec(`INSERT INTO lagekarte_abschnitte (lagekarte_id, nr, einsatz_id, name, version, created_at, created_by, updated_at, updated_by) VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?)`,
			karteID, nr, einsatzID, name, now, db.NullIfZero(c.UID), now, db.NullIfZero(c.UID)); err != nil {
			return err
		}
		row, err := abschnitt(q, karteID, nr)
		if err != nil {
			return err
		}
		a := AbschnittRow(row)
		if _, err := access.Event(q, c, "abschnitt", fmt.Sprintf("%s:%d", karteID, nr), "insert",
			fmt.Sprintf("Einsatzabschnitt %d „%s\" auf Lagekarte „%s\" angelegt", nr, name, db.Str(karte, "title")), nil, a); err != nil {
			return err
		}
		if err := einsatz.TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = a
		return nil
	})
	return out, err
}

func AbschnittUpdate(q db.Q, c *access.Ctx, karteID string, nr, version int, name string) (*Abschnitt, error) {
	einsatzID := c.Einsatz.ID
	karte, err := KarteByID(q, einsatzID, karteID)
	if err != nil {
		return nil, err
	}
	if karte == nil {
		return nil, apierr.New("not_found", 404)
	}
	cur, err := abschnitt(q, karteID, nr)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, apierr.New("not_found", 404)
	}
	if int(db.Int(cur, "version")) != version {
		return nil, apierr.Conflict(AbschnittRow(cur))
	}
	name = input.Str(name, 80)
	if name == "" {
		return nil, apierr.New("missing_name", 400)
	}
	var out *Abschnitt
	err = db.InTx(q, func(q db.Q) error {
		res, err := q.Exec("UPDATE lagekarte_abschnitte SET name = ?, version = version + 1, updated_at = ?, updated_by = ? WHERE lagekarte_id = ? AND nr = ? AND version = ? AND deleted_at IS NULL",
			name, db.Now(), db.NullIfZero(c.UID), karteID, nr, version)
		if err != nil {
			return err
		}
		if db.Affected(res) == 0 {
			now, _ := abschnitt(q, karteID, nr)
			if now == nil {
				now = cur
			}
			return apierr.Conflict(AbschnittRow(now))
		}
		row, err := abschnitt(q, karteID, nr)
		if err != nil {
			return err
		}
		a := AbschnittRow(row)
		if _, err := access.Event(q, c, "abschnitt", fmt.Sprintf("%s:%d", karteID, nr), "update",
			fmt.Sprintf("Einsatzabschnitt %d auf „%s\" umbenannt: „%s\" → „%s\"", nr, db.Str(karte, "title"), db.Str(cur, "name"), name),
			map[string]any{"name": db.Str(cur, "name")}, a); err != nil {
			return err
		}
		if err := einsatz.TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = a
		return nil
	})
	return out, err
}

func AbschnittDelete(q db.Q, c *access.Ctx, karteID string, nr, version int) (*AbschnittDeleteResult, error) {
	einsatzID := c.Einsatz.ID
	karte, err := KarteByID(q, einsatzID, karteID)
	if err != nil {
		return nil, err
	}
	if karte == nil {
		return nil, apierr.New("not_found", 404)
	}
	cur, err := abschnitt(q, karteID, nr)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, apierr.New("not_found", 404)
	}
	if int(db.Int(cur, "version")) != version {
		return nil, apierr.Conflict(AbschnittRow(cur))
	}
	var out *AbschnittDeleteResult
	err = db.InTx(q, func(q db.Q) error {
		now := db.Now()
		res, err := q.Exec("UPDATE lagekarte_abschnitte SET deleted_at = ?, deleted_by = ?, version = version + 1 WHERE lagekarte_id = ? AND nr = ? AND version = ? AND deleted_at IS NULL",
			now, db.NullIfZero(c.UID), karteID, nr, version)
		if err != nil {
			return err
		}
		if db.Affected(res) == 0 {
			return apierr.Conflict(AbschnittRow(cur))
		}
		// Abschnitt aus allen Kartenobjekten entfernen (eigene Events, damit
		// Clients die betroffenen Objekte nachziehen)
		feats, err := Features(q, karteID, false)
		if err != nil {
			return err
		}
		affected := []*Feature{}
		for _, f := range feats {
			if !containsInt(f.Abschnitte, nr) {
				continue
			}
			newAb := []int{}
			for _, x := range f.Abschnitte {
				if x != nr {
					newAb = append(newAb, x)
				}
			}
			abJSON, _ := json.Marshal(newAb)
			if _, err := q.Exec("UPDATE lagekarte_features SET abschnitte = ?, version = version + 1, updated_at = ?, updated_by = ? WHERE id = ?",
				string(abJSON), now, db.NullIfZero(c.UID), f.ID); err != nil {
				return err
			}
			raw, err := einsatz.RowByID(q, "lagekarte_features", f.ID)
			if err != nil {
				return err
			}
			row := FeatureRow(raw)
			if _, err := access.Event(q, c, "feature", f.ID, "update", FeatureLabel(row, karte)+fmt.Sprintf(": Abschnitt %d entfernt", nr),
				map[string]any{"abschnitte": f.Abschnitte}, row); err != nil {
				return err
			}
			affected = append(affected, row)
		}
		after := map[string]any{"lagekarte_id": karteID, "nr": nr, "deleted_at": now}
		if _, err := access.Event(q, c, "abschnitt", fmt.Sprintf("%s:%d", karteID, nr), "delete",
			fmt.Sprintf("Einsatzabschnitt %d „%s\" auf „%s\" gelöscht", nr, db.Str(cur, "name"), db.Str(karte, "title")), AbschnittRow(cur), after); err != nil {
			return err
		}
		if err := einsatz.TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = &AbschnittDeleteResult{Abschnitt: after, Features: affected}
		return nil
	})
	return out, err
}

func containsInt(list []int, n int) bool {
	for _, x := range list {
		if x == n {
			return true
		}
	}
	return false
}

// ------------------------------------------------------------------
// Features
// ------------------------------------------------------------------

// FeatureLabel: "Taktisches Zeichen „BGr" auf Lagekarte „Lage Nord"".
func FeatureLabel(f *Feature, karte map[string]any) string {
	kind := f.Kind
	if l, ok := KindLabels[kind]; ok {
		kind = l
	}
	p := input.Map(f.Props)
	name := ""
	if f.Kind == "marker" {
		name = input.Raw(p["label"])
	}
	if f.Kind == "text" {
		name = input.Trunc(input.Raw(p["text"]), 40)
	}
	out := kind
	if name != "" {
		out += " „" + name + "\""
	}
	if karte != nil {
		out += " auf Lagekarte „" + db.Str(karte, "title") + "\""
	}
	return out
}

func num(v any, min, max float64) (float64, error) {
	if !input.IsNumeric(v) {
		return 0, apierr.New("invalid_geometry", 400)
	}
	f := input.Float(v)
	if math.IsNaN(f) || math.IsInf(f, 0) || f < min || f > max {
		return 0, apierr.New("invalid_geometry", 400)
	}
	return f, nil
}

func latlng(pair any) ([]float64, error) {
	l := input.List(pair)
	if l == nil || len(l) < 2 {
		return nil, apierr.New("invalid_geometry", 400)
	}
	lat, err := num(l[0], -90, 90)
	if err != nil {
		return nil, err
	}
	lng, err := num(l[1], -180, 180)
	if err != nil {
		return nil, err
	}
	return []float64{lat, lng}, nil
}

func SanitizeGeometry(kind string, g any) (map[string]any, error) {
	m := input.Map(g)
	if m == nil {
		return nil, apierr.New("invalid_geometry", 400)
	}
	switch kind {
	case "marker", "text":
		lat, err := num(m["lat"], -90, 90)
		if err != nil {
			return nil, err
		}
		lng, err := num(m["lng"], -180, 180)
		if err != nil {
			return nil, err
		}
		return map[string]any{"lat": lat, "lng": lng}, nil
	case "circle":
		lat, err := num(m["lat"], -90, 90)
		if err != nil {
			return nil, err
		}
		lng, err := num(m["lng"], -180, 180)
		if err != nil {
			return nil, err
		}
		radius, err := num(m["radius"], 0.1, 5000000)
		if err != nil {
			return nil, err
		}
		return map[string]any{"lat": lat, "lng": lng, "radius": radius}, nil
	case "rectangle":
		b := input.List(m["bounds"])
		if b == nil || len(b) != 2 {
			return nil, apierr.New("invalid_geometry", 400)
		}
		p0, err := latlng(b[0])
		if err != nil {
			return nil, err
		}
		p1, err := latlng(b[1])
		if err != nil {
			return nil, err
		}
		return map[string]any{"bounds": [][]float64{p0, p1}}, nil
	case "polygon", "polyline":
		pts := input.List(m["latlngs"])
		if pts == nil || len(pts) < 2 || len(pts) > MaxPoints {
			return nil, apierr.New("invalid_geometry", 400)
		}
		out := make([][]float64, 0, len(pts))
		for _, p := range pts {
			ll, err := latlng(p)
			if err != nil {
				return nil, err
			}
			out = append(out, ll)
		}
		return map[string]any{"latlngs": out}, nil
	case "arrow":
		s, err := latlng(m["start"])
		if err != nil {
			return nil, err
		}
		e, err := latlng(m["end"])
		if err != nil {
			return nil, err
		}
		return map[string]any{"start": s, "end": e}, nil
	}
	return nil, apierr.New("invalid_kind", 400)
}

func color(v any, def string) string {
	s := ""
	if x, ok := v.(string); ok {
		s = strings.TrimSpace(x)
	}
	if reColor.MatchString(s) {
		return strings.ToLower(s)
	}
	return def
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func SanitizeStyle(s any) map[string]any {
	m := input.Map(s)
	if m == nil {
		m = map[string]any{}
	}
	col := color(m["color"], "#d94b2b")
	w := 3
	if input.IsNumeric(m["weight"]) {
		w = int(input.Float(m["weight"]))
	}
	fo := 0.3
	if input.IsNumeric(m["fillOpacity"]) {
		fo = input.Float(m["fillOpacity"])
	}
	op := 1.0
	if input.IsNumeric(m["opacity"]) {
		op = input.Float(m["opacity"])
	}
	return map[string]any{
		"color":       col,
		"weight":      clampInt(w, 1, 20),
		"fillColor":   color(m["fillColor"], col),
		"fillOpacity": clampFloat(fo, 0, 1),
		"opacity":     clampFloat(op, 0, 1),
	}
}

func intOr(m map[string]any, k string, def int) int {
	if v, ok := m[k]; ok && v != nil {
		return input.Int(v, def)
	}
	return def
}

func SanitizeProps(kind string, p any) (map[string]any, error) {
	m := input.Map(p)
	if m == nil {
		m = map[string]any{}
	}
	switch kind {
	case "marker":
		file := input.Str(m["file"], 200)
		if file == "" || strings.Contains(file, "..") || strings.Contains(file, "\\") {
			return nil, apierr.New("invalid_symbol", 400)
		}
		if !reMarkerFile.MatchString(file) {
			return nil, apierr.New("invalid_symbol", 400)
		}
		info := input.Map(m["info"])
		if info == nil {
			info = map[string]any{}
		}
		outInfo := map[string]any{
			"ov":      input.Str(info["ov"], 120),
			"call":    input.Str(info["call"], 120),
			"staerke": input.Str(info["staerke"], 60),
			"note":    input.StrRaw(info["note"], 500),
		}
		// Optionaler Verweis in den Kraeftekatalog (Freitext-Zeichen ohne Harke-Verknuepfung)
		if kk := input.Str(info["kraft_key"], kraefte.KraftKeyMax); kk != "" && kraefte.ValidKey(kk) {
			outInfo["kraft_key"] = kk
		}
		return map[string]any{
			"file":     file,
			"label":    input.Str(m["label"], 160),
			"baseSize": clampInt(intOr(m, "baseSize", 40), 10, 400),
			"rotation": clampInt(intOr(m, "rotation", 0), 0, 359),
			"info":     outInfo,
		}, nil
	case "text":
		return map[string]any{
			"text":      input.StrRaw(m["text"], 500),
			"color":     color(m["color"], "#222222"),
			"fontSize":  clampInt(intOr(m, "fontSize", 16), 8, 96),
			"bold":      !input.Empty(m["bold"]),
			"italic":    !input.Empty(m["italic"]),
			"underline": !input.Empty(m["underline"]),
			"note":      input.StrRaw(m["note"], 500),
		}, nil
	default:
		return map[string]any{
			"style": SanitizeStyle(m["style"]),
			"note":  input.StrRaw(m["note"], 500),
			"label": input.Str(m["label"], 160),
		}, nil
	}
}

func SanitizeAbschnitte(a any) []int {
	out := []int{}
	for _, x := range input.List(a) {
		n := input.Int(x, -1)
		if n >= 1 && n < 100000 && !containsInt(out, n) {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// SanitizeRef prueft eine Verknuepfung zu Knoten/Mitglied des Einsatzes.
func SanitizeRef(q db.Q, einsatzID int64, kind, id any) (*string, *string, error) {
	k, _ := kind.(string)
	i, _ := id.(string)
	if k == "" || i == "" {
		return nil, nil, nil
	}
	if (k != "node" && k != "mitglied") || !uuid.ValidClientID(i) {
		return nil, nil, apierr.New("invalid_ref", 400)
	}
	table := "einsatz_mitglieder"
	if k == "node" {
		table = "einsatz_nodes"
	}
	row, err := einsatz.RowByID(q, table, i)
	if err != nil {
		return nil, nil, err
	}
	if row == nil || db.Int(row, "einsatz_id") != einsatzID {
		return nil, nil, apierr.New("ref_not_found", 404)
	}
	return &k, &i, nil
}

func nullStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func strPtrVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func FeatureCreate(q db.Q, c *access.Ctx, karteID string, body map[string]any) (*Feature, error) {
	einsatzID := c.Einsatz.ID
	karte, err := KarteByID(q, einsatzID, karteID)
	if err != nil {
		return nil, err
	}
	if karte == nil {
		return nil, apierr.New("not_found", 404)
	}
	kind := input.Str(body["kind"], 20)
	if !containsStr(Kinds, kind) {
		return nil, apierr.New("invalid_kind", 400)
	}
	id := input.IDOrNew(body["id"])
	if ex, err := einsatz.RowByIDIncludingDeleted(q, "lagekarte_features", id); err != nil {
		return nil, err
	} else if ex != nil {
		return nil, apierr.New("id_exists", 409)
	}
	geometry, err := SanitizeGeometry(kind, body["geometry"])
	if err != nil {
		return nil, err
	}
	props, err := SanitizeProps(kind, body["props"])
	if err != nil {
		return nil, err
	}
	abschnitte := SanitizeAbschnitte(body["abschnitte"])
	refKind, refID, err := SanitizeRef(q, einsatzID, body["ref_kind"], body["ref_id"])
	if err != nil {
		return nil, err
	}
	now := db.Now()
	var out *Feature
	err = db.InTx(q, func(q db.Q) error {
		if _, err := q.Exec(`INSERT INTO lagekarte_features (id, lagekarte_id, einsatz_id, kind, geometry, props, abschnitte, ref_kind, ref_id, version, created_at, created_by, updated_at, updated_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
			id, karteID, einsatzID, kind, toJSON(geometry), toJSON(props), toJSON(abschnitte), nullStr(refKind), nullStr(refID),
			now, db.NullIfZero(c.UID), now, db.NullIfZero(c.UID)); err != nil {
			return err
		}
		raw, err := einsatz.RowByID(q, "lagekarte_features", id)
		if err != nil {
			return err
		}
		f := FeatureRow(raw)
		verb := "gezeichnet"
		if kind == "marker" {
			verb = "platziert"
		} else if kind == "text" {
			verb = "eingefügt"
		}
		summary := FeatureLabel(f, karte) + " " + verb
		if refID != nil {
			summary += " (verknüpft mit Einheit)"
		}
		if _, err := access.Event(q, c, "feature", id, "insert", summary, nil, f); err != nil {
			return err
		}
		if err := einsatz.TouchEinsatz(q, einsatzID); err != nil {
			return err
		}
		out = f
		return nil
	})
	return out, err
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func jsonEqual(a, b string) bool {
	var va, vb any
	if json.Unmarshal([]byte(a), &va) != nil || json.Unmarshal([]byte(b), &vb) != nil {
		return a == b
	}
	return reflect.DeepEqual(va, vb)
}

func FeatureUpdate(q db.Q, c *access.Ctx, id string, version int, in map[string]any) (*Feature, error) {
	einsatzID := c.Einsatz.ID
	cur, err := einsatz.RowByID(q, "lagekarte_features", id)
	if err != nil {
		return nil, err
	}
	if cur == nil || db.Int(cur, "einsatz_id") != einsatzID {
		return nil, apierr.New("not_found", 404)
	}
	karte, err := KarteByID(q, einsatzID, db.Str(cur, "lagekarte_id"))
	if err != nil {
		return nil, err
	}
	kind := db.Str(cur, "kind")
	fields := einsatz.Fields{}
	changed := []string{}
	if input.Has(in, "geometry") {
		g, err := SanitizeGeometry(kind, in["geometry"])
		if err != nil {
			return nil, err
		}
		js := toJSON(g)
		fields = append(fields, einsatz.Field{K: "geometry", V: js})
		if !jsonEqual(js, db.Str(cur, "geometry")) {
			changed = append(changed, "Position/Form")
		}
	}
	if input.Has(in, "props") {
		p, err := SanitizeProps(kind, in["props"])
		if err != nil {
			return nil, err
		}
		js := toJSON(p)
		fields = append(fields, einsatz.Field{K: "props", V: js})
		if !jsonEqual(js, db.Str(cur, "props")) {
			changed = append(changed, "Eigenschaften")
		}
	}
	if input.Has(in, "abschnitte") {
		js := toJSON(SanitizeAbschnitte(in["abschnitte"]))
		fields = append(fields, einsatz.Field{K: "abschnitte", V: js})
		if !jsonEqual(js, db.Str(cur, "abschnitte")) {
			changed = append(changed, "Abschnitte")
		}
	}
	if input.Has(in, "ref_kind") || input.Has(in, "ref_id") {
		rk, ri, err := SanitizeRef(q, einsatzID, in["ref_kind"], in["ref_id"])
		if err != nil {
			return nil, err
		}
		fields = append(fields, einsatz.Field{K: "ref_kind", V: nullStr(rk)}, einsatz.Field{K: "ref_id", V: nullStr(ri)})
		if db.Str(cur, "ref_id") != strPtrVal(ri) {
			if ri != nil {
				changed = append(changed, "Verknüpfung gesetzt")
			} else {
				changed = append(changed, "Verknüpfung gelöst")
			}
		}
	}
	if len(fields) == 0 {
		return nil, apierr.New("no_fields", 400)
	}
	var out *Feature
	err = db.InTx(q, func(q db.Q) error {
		row, err := einsatz.UpdateVersioned(q, "lagekarte_features", id, version, fields, c.UID)
		if err != nil {
			return err
		}
		f := FeatureRow(row)
		if len(changed) > 0 {
			if _, err := access.Event(q, c, "feature", id, "update", FeatureLabel(f, karte)+" geändert: "+strings.Join(changed, ", "), FeatureRow(cur), f); err != nil {
				return err
			}
			if err := einsatz.TouchEinsatz(q, einsatzID); err != nil {
				return err
			}
		}
		out = f
		return nil
	})
	return out, err
}

func FeatureDelete(q db.Q, c *access.Ctx, id string, version int) (map[string]any, error) {
	einsatzID := c.Einsatz.ID
	cur, err := einsatz.RowByID(q, "lagekarte_features", id)
	if err != nil {
		return nil, err
	}
	if cur == nil || db.Int(cur, "einsatz_id") != einsatzID {
		return nil, apierr.New("not_found", 404)
	}
	karte, err := KarteByID(q, einsatzID, db.Str(cur, "lagekarte_id"))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = db.InTx(q, func(q db.Q) error {
		if _, err := einsatz.SoftDelete(q, "lagekarte_features", id, version, c.UID); err != nil {
			return err
		}
		after := map[string]any{"id": id, "lagekarte_id": db.Str(cur, "lagekarte_id"), "deleted_at": db.Now()}
		if _, err := access.Event(q, c, "feature", id, "delete", FeatureLabel(FeatureRow(cur), karte)+" gelöscht", FeatureRow(cur), after); err != nil {
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

func valOr(m map[string]any, k string, def any) any {
	if v, ok := m[k]; ok && v != nil {
		return v
	}
	return def
}

// ImportMapData: Import einer Lagekarte im Datei-Format (map_data mit
// markers/shapes/texts/abschnitte). Legt eine neue Karte an.
func ImportMapData(q db.Q, c *access.Ctx, title string, mapData map[string]any, defLat, defLng float64) (*Karte, error) {
	var center []any
	if l := input.List(mapData["center"]); l != nil && len(l) == 2 {
		center = l
	}
	var out *Karte
	err := db.InTx(q, func(q db.Q) error {
		body := map[string]any{
			"title":   title,
			"zoom":    valOr(mapData, "zoom", 11),
			"basemap": valOr(mapData, "basemap", "osm"),
		}
		if center != nil {
			body["center_lat"] = center[0]
			body["center_lng"] = center[1]
		}
		karte, err := Create(q, c, body, defLat, defLng)
		if err != nil {
			return err
		}
		kid := karte.ID
		for _, a := range input.List(mapData["abschnitte"]) {
			am := input.Map(a)
			if am == nil {
				continue
			}
			nr := input.Int(am["id"], 0)
			if nr < 1 {
				continue
			}
			name := input.Str(am["name"], 80)
			if name == "" {
				name = fmt.Sprintf("Abschnitt %d", nr)
			}
			if _, err := AbschnittCreate(q, c, kid, name, &nr); err != nil {
				return err
			}
		}
		count := 0
		tryCreate := func(body map[string]any) error {
			_, err := FeatureCreate(q, c, kid, body)
			if err != nil {
				var ae *apierr.Error
				if errors.As(err, &ae) {
					return nil // einzelnes kaputtes Objekt ueberspringen
				}
				return err
			}
			count++
			return nil
		}
		for _, m := range input.List(mapData["markers"]) {
			mm := input.Map(m)
			if mm == nil {
				continue
			}
			size := valOr(mm, "baseSize", valOr(mm, "size", 40))
			if err := tryCreate(map[string]any{
				"kind":     "marker",
				"geometry": map[string]any{"lat": mm["lat"], "lng": mm["lng"]},
				"props": map[string]any{"file": valOr(mm, "file", ""), "label": valOr(mm, "label", ""), "baseSize": size,
					"rotation": valOr(mm, "rotation", 0), "info": valOr(mm, "info", map[string]any{})},
				"abschnitte": valOr(mm, "abschnitte", []any{}),
			}); err != nil {
				return err
			}
		}
		for _, s := range input.List(mapData["shapes"]) {
			sm := input.Map(s)
			if sm == nil {
				continue
			}
			typ := input.Raw(sm["type"])
			var geom map[string]any
			switch typ {
			case "circle":
				geom = map[string]any{"lat": sm["lat"], "lng": sm["lng"], "radius": sm["radius"]}
			case "rectangle":
				geom = map[string]any{"bounds": sm["bounds"]}
			case "polygon", "polyline":
				geom = map[string]any{"latlngs": sm["latlngs"]}
			case "arrow":
				geom = map[string]any{"start": sm["start"], "end": sm["end"]}
			}
			if geom == nil {
				continue
			}
			if err := tryCreate(map[string]any{
				"kind": typ, "geometry": geom,
				"props":      map[string]any{"style": valOr(sm, "style", map[string]any{}), "note": valOr(sm, "note", "")},
				"abschnitte": valOr(sm, "abschnitte", []any{}),
			}); err != nil {
				return err
			}
		}
		for _, t := range input.List(mapData["texts"]) {
			tm := input.Map(t)
			if tm == nil {
				continue
			}
			if err := tryCreate(map[string]any{
				"kind":     "text",
				"geometry": map[string]any{"lat": tm["lat"], "lng": tm["lng"]},
				"props": map[string]any{"text": valOr(tm, "text", ""), "color": valOr(tm, "color", "#222222"), "fontSize": valOr(tm, "fontSize", 16),
					"bold": valOr(tm, "bold", false), "italic": valOr(tm, "italic", false), "underline": valOr(tm, "underline", false)},
				"abschnitte": valOr(tm, "abschnitte", []any{}),
			}); err != nil {
				return err
			}
		}
		if _, err := access.Event(q, c, "lagekarte", kid, "import", fmt.Sprintf("Lagekarte „%s\" aus Datei importiert (%d Objekte)", title, count), nil, map[string]any{"count": count}); err != nil {
			return err
		}
		raw, err := einsatz.RowByID(q, "lagekarten", kid)
		if err != nil {
			return err
		}
		out = KarteRow(raw)
		return nil
	})
	return out, err
}
