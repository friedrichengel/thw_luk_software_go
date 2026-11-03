// Package einsatz: Datenzugriff fuer Einsatz, Mitglieder und Fuehrungsharke
// (Knoten, Mitglieder einer Fuehrungsstelle, Schichten) inkl. Versions-
// pruefung und Ereignis-Log, dazu Freigabelinks und Verknuepfungen. Wird von
// den API-Endpunkten und von Export/Import genutzt.
package einsatz

import (
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/db"
)

// Feldlisten je Tabelle (was der Client aendern darf): Name => Hoechstlaenge.
type FieldDef struct {
	Key string
	Max int
}

var NodeFields = []FieldDef{
	{"type", 20}, {"bezeichnung", 160}, {"funkruf", 120}, {"ov", 120}, {"kraft_key", 80}, {"note", 500},
	{"extern_link_url", 500}, {"extern_link_label", 160},
}

// NodeFieldsKraefte: Felder, die auch mit Kraefte-Recht (Stammdaten) geaendert werden duerfen.
var NodeFieldsKraefte = []string{"funkruf", "ov", "kraft_key", "note"}

var MitgliedFields = []FieldDef{
	{"funktion", 120}, {"name", 160}, {"bezeichnung", 160}, {"funkruf", 120},
	{"telefon", 60}, {"ov", 120}, {"kraft_key", 80}, {"note", 500},
}

var MitgliedFieldsKraefte = []string{"funktion", "name", "funkruf", "telefon", "ov", "kraft_key", "note"}

const (
	VorwarnDefaultMin = 180
	VorwarnMaxMin     = 7 * 24 * 60
)

var SchichtFields = []FieldDef{
	{"von", 32}, {"bis", 32}, {"staerke", 40}, {"name", 160}, {"telefon", 60},
	{"funkruf", 120}, {"ov", 120}, {"kraft_key", 80}, {"note", 200},
}

// Keys liefert die Feldnamen einer Feldliste.
func Keys(defs []FieldDef) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Key)
	}
	return out
}

// Field ist ein zu schreibender Spaltenwert; Fields haelt die Reihenfolge.
type Field struct {
	K string
	V any
}

type Fields []Field

func (f Fields) Get(k string) (any, bool) {
	for _, x := range f {
		if x.K == k {
			return x.V, true
		}
	}
	return nil, false
}

func (f Fields) Str(k string) string {
	if v, ok := f.Get(k); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (f Fields) Has(k string) bool {
	_, ok := f.Get(k)
	return ok
}

func (f *Fields) Set(k string, v any) {
	for i, x := range *f {
		if x.K == k {
			(*f)[i].V = v
			return
		}
	}
	*f = append(*f, Field{k, v})
}

// ---------------------------------------------------------------------------
// Zeilen (API-Form)

type Node struct {
	ID              string  `json:"id"`
	ParentID        *string `json:"parent_id"`
	SortOrder       int     `json:"sort_order"`
	Type            string  `json:"type"`
	Bezeichnung     string  `json:"bezeichnung"`
	Funkruf         string  `json:"funkruf"`
	OV              string  `json:"ov"`
	KraftKey        string  `json:"kraft_key"`
	Note            string  `json:"note"`
	Status          string  `json:"status"`
	StatusAt        *string `json:"status_at"`
	ExternLinkURL   string  `json:"extern_link_url"`
	ExternLinkLabel string  `json:"extern_link_label"`
	Version         int     `json:"version"`
	UpdatedAt       string  `json:"updated_at"`
	DeletedAt       *string `json:"deleted_at"`
	EinsatzID       int64   `json:"-"`
}

func NodeRow(m map[string]any) *Node {
	return &Node{
		ID:              db.Str(m, "id"),
		ParentID:        db.StrPtr(m, "parent_id"),
		SortOrder:       int(db.Int(m, "sort_order")),
		Type:            db.Str(m, "type"),
		Bezeichnung:     db.Str(m, "bezeichnung"),
		Funkruf:         db.Str(m, "funkruf"),
		OV:              db.Str(m, "ov"),
		KraftKey:        db.Str(m, "kraft_key"),
		Note:            db.Str(m, "note"),
		Status:          db.Str(m, "status"),
		StatusAt:        db.StrPtr(m, "status_at"),
		ExternLinkURL:   db.Str(m, "extern_link_url"),
		ExternLinkLabel: db.Str(m, "extern_link_label"),
		Version:         int(db.Int(m, "version")),
		UpdatedAt:       db.Str(m, "updated_at"),
		DeletedAt:       db.StrPtr(m, "deleted_at"),
		EinsatzID:       db.Int(m, "einsatz_id"),
	}
}

type Mitglied struct {
	ID          string  `json:"id"`
	NodeID      string  `json:"node_id"`
	Kind        string  `json:"kind"`
	SortOrder   int     `json:"sort_order"`
	Funktion    string  `json:"funktion"`
	Name        string  `json:"name"`
	Bezeichnung string  `json:"bezeichnung"`
	Funkruf     string  `json:"funkruf"`
	Telefon     string  `json:"telefon"`
	OV          string  `json:"ov"`
	KraftKey    string  `json:"kraft_key"`
	Note        string  `json:"note"`
	Status      string  `json:"status"`
	StatusAt    *string `json:"status_at"`
	Version     int     `json:"version"`
	UpdatedAt   string  `json:"updated_at"`
	DeletedAt   *string `json:"deleted_at"`
	EinsatzID   int64   `json:"-"`
}

func MitgliedRow(m map[string]any) *Mitglied {
	return &Mitglied{
		ID:          db.Str(m, "id"),
		NodeID:      db.Str(m, "node_id"),
		Kind:        db.Str(m, "kind"),
		SortOrder:   int(db.Int(m, "sort_order")),
		Funktion:    db.Str(m, "funktion"),
		Name:        db.Str(m, "name"),
		Bezeichnung: db.Str(m, "bezeichnung"),
		Funkruf:     db.Str(m, "funkruf"),
		Telefon:     db.Str(m, "telefon"),
		OV:          db.Str(m, "ov"),
		KraftKey:    db.Str(m, "kraft_key"),
		Note:        db.Str(m, "note"),
		Status:      db.Str(m, "status"),
		StatusAt:    db.StrPtr(m, "status_at"),
		Version:     int(db.Int(m, "version")),
		UpdatedAt:   db.Str(m, "updated_at"),
		DeletedAt:   db.StrPtr(m, "deleted_at"),
		EinsatzID:   db.Int(m, "einsatz_id"),
	}
}

type Schicht struct {
	ID         string  `json:"id"`
	NodeID     string  `json:"node_id"`
	MitgliedID *string `json:"mitglied_id"`
	Von        string  `json:"von"`
	Bis        string  `json:"bis"`
	Staerke    string  `json:"staerke"`
	Name       string  `json:"name"`
	Telefon    string  `json:"telefon"`
	Funkruf    string  `json:"funkruf"`
	OV         string  `json:"ov"`
	KraftKey   string  `json:"kraft_key"`
	Note       string  `json:"note"`
	Aktiv      bool    `json:"aktiv"`
	Status     string  `json:"status"`
	StatusAt   *string `json:"status_at"`
	Version    int     `json:"version"`
	UpdatedAt  string  `json:"updated_at"`
	DeletedAt  *string `json:"deleted_at"`
	EinsatzID  int64   `json:"-"`
}

func SchichtRow(m map[string]any) *Schicht {
	status := db.Str(m, "status")
	if status == "" {
		status = "geplant"
	}
	return &Schicht{
		ID:         db.Str(m, "id"),
		NodeID:     db.Str(m, "node_id"),
		MitgliedID: db.StrPtr(m, "mitglied_id"),
		Von:        db.Str(m, "von"),
		Bis:        db.Str(m, "bis"),
		Staerke:    db.Str(m, "staerke"),
		Name:       db.Str(m, "name"),
		Telefon:    db.Str(m, "telefon"),
		Funkruf:    db.Str(m, "funkruf"),
		OV:         db.Str(m, "ov"),
		KraftKey:   db.Str(m, "kraft_key"),
		Note:       db.Str(m, "note"),
		Aktiv:      db.Int(m, "aktiv") == 1,
		Status:     status,
		StatusAt:   db.StrPtr(m, "status_at"),
		Version:    int(db.Int(m, "version")),
		UpdatedAt:  db.Str(m, "updated_at"),
		DeletedAt:  db.StrPtr(m, "deleted_at"),
		EinsatzID:  db.Int(m, "einsatz_id"),
	}
}

// ---------------------------------------------------------------------------
// Labels fuer Log-Texte

func NodeLabel(m map[string]any) string {
	if m == nil {
		return "?"
	}
	b := strings.TrimSpace(db.Str(m, "bezeichnung"))
	t := "Einheit"
	if db.Str(m, "type") == "fuehrung" {
		t = "Führungsstelle"
	}
	if b == "" {
		b = "ohne Bezeichnung"
	}
	return t + " „" + b + "\""
}

func MitgliedLabel(m map[string]any) string {
	if m == nil {
		return "?"
	}
	if db.Str(m, "kind") == "fachberater" {
		f := strings.TrimSpace(db.Str(m, "funktion"))
		n := strings.TrimSpace(db.Str(m, "name"))
		if n != "" {
			f = f + " " + n
		}
		return "Fachberater „" + strings.TrimSpace(f) + "\""
	}
	b := strings.TrimSpace(db.Str(m, "bezeichnung"))
	if b == "" {
		b = "ohne Bezeichnung"
	}
	return "Interne Einheit „" + b + "\""
}

// SchichtLabel: "Schicht von – bis (staerke) bei <Ziel>".
func SchichtLabel(s map[string]any, target map[string]any) string {
	t := ""
	if target != nil {
		if _, isMitglied := target["kind"]; isMitglied {
			t = MitgliedLabel(target)
		} else {
			t = NodeLabel(target)
		}
	}
	timeStr := strings.TrimSpace(strings.ReplaceAll(db.Str(s, "von"), "T", " ")) + " – " + strings.TrimSpace(strings.ReplaceAll(db.Str(s, "bis"), "T", " "))
	out := "Schicht " + timeStr
	if st := db.Str(s, "staerke"); st != "" {
		out += " (" + st + ")"
	}
	if t != "" {
		out += " bei " + t
	}
	return out
}

var fieldLabels = map[string]string{
	"type": "Typ", "bezeichnung": "Bezeichnung", "funkruf": "Funkrufname", "ov": "OV", "note": "Bemerkung",
	"extern_link_url": "Externer Link", "extern_link_label": "Link-Beschriftung",
	"funktion": "Funktion", "name": "Name", "telefon": "Telefon",
	"von": "Von", "bis": "Bis", "staerke": "Stärke", "aktiv": "Aktiv",
	"title": "Titel", "description": "Beschreibung", "taktische_zeichen": "Taktische Zeichen",
	"ebene": "Ebene", "stab_name": "Stabsname", "vorwarn_min": "Vorwarnzeit", "kind": "Art",
	"kraft_key": "Katalog-Einheit", "bereich_key": "Eigener Bereich", "ort_name": "Einsatzort",
	"ort_lat": "Einsatzort", "ort_lng": "Einsatzort", "stein_aktiv": "STEIN-Daten",
	"center_lat": "Kartenmitte", "center_lng": "Kartenmitte", "zoom": "Zoom", "basemap": "Basiskarte",
	"geometry": "Geometrie", "props": "Eigenschaften", "abschnitte": "Abschnitte",
	"ref_kind": "Verknüpfung", "ref_id": "Verknüpfung",
}

// FieldLabel liefert die deutsche Bezeichnung eines Feldes fuer Log-Texte.
func FieldLabel(k string) string {
	if l, ok := fieldLabels[k]; ok {
		return l
	}
	return k
}

// Uniq entfernt Duplikate unter Beibehaltung der Reihenfolge.
func Uniq(list []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
