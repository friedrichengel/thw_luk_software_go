// Package stab ist die einzige Quelle fuer Stabsrollen, Berechtigungs-
// Matrix, Statuswerte und Stabsebenen. Server und Client (window.STAB)
// lesen dieselben Konstanten.
//
// Capabilities (Schreibrechte je Modul):
//
//	harke          Fuehrungsharke (Knoten anlegen/verschieben/loeschen, Stammdaten)
//	kraefte        Kraefteplanung (Schichten)
//	lagekarte      Lagekarte (Karten, Abschnitte, Kartenobjekte)
//	schadenskonten Schadenskonten
//	log            Aenderungs- und Zugriffslog einsehen
//
// Der Eigentuemer (Ersteller) eines Einsatzes hat zusaetzlich immer die
// Verwaltungsrechte (Mitglieder, Freigabelinks, Abschluss, Loeschen) und
// das Log - aber keine inhaltlichen Schreibrechte, solange er sich keine
// Rolle zuweist. Beim Anlegen bekommt der Ersteller automatisch die Rolle
// "Leiter Stab", die er wieder entfernen kann.
package stab

import "encoding/json"

const (
	CapHarke          = "harke"
	CapKraefte        = "kraefte"
	CapLagekarte      = "lagekarte"
	CapSchadenskonten = "schadenskonten"
	CapLog            = "log"
)

var Caps = []string{CapHarke, CapKraefte, CapLagekarte, CapSchadenskonten, CapLog}

type Role struct {
	Key   string   `json:"-"`
	Label string   `json:"label"`
	Short string   `json:"short"`
	Caps  []string `json:"caps"`
}

// Roles in fachlicher Reihenfolge.
var Roles = []Role{
	{"ltr_stab", "Leiter Stab", "LtrStab", []string{"harke", "kraefte", "lagekarte", "schadenskonten", "log"}},
	{"s1", "S1 Personal/Innerer Dienst", "S1", []string{"harke", "kraefte"}},
	{"s2", "S2 Lage", "S2", []string{"harke", "lagekarte", "schadenskonten"}},
	{"s3", "S3 Einsatz", "S3", []string{"harke", "schadenskonten"}},
	{"s4", "S4 Versorgung", "S4", []string{}},
	{"s5", "S5 Presse/Medien", "S5", []string{}},
	{"s6", "S6 IuK", "S6", []string{}},
	{"etb", "ETB Einsatztagebuch", "ETB", []string{}},
	{"lagekarte", "Lagekarte", "LK", []string{"harke", "lagekarte", "schadenskonten"}},
	{"sichter", "Sichter", "Sichter", []string{}},
	{"u_s1", "Unterstützung S1", "U-S1", []string{"harke", "kraefte"}},
	{"u_s2", "Unterstützung S2", "U-S2", []string{"harke", "lagekarte", "schadenskonten"}},
	{"u_s3", "Unterstützung S3", "U-S3", []string{"harke", "schadenskonten"}},
	{"u_s4", "Unterstützung S4", "U-S4", []string{}},
	{"u_s5", "Unterstützung S5", "U-S5", []string{}},
	{"u_s6", "Unterstützung S6", "U-S6", []string{}},
}

var roleByKey = func() map[string]Role {
	m := map[string]Role{}
	for _, r := range Roles {
		m[r.Key] = r
	}
	return m
}()

type KV struct {
	Key   string
	Label string
}

// Ebenen: Stabsebenen, vom Ersteller beim Anlegen gewaehlt.
var Ebenen = []KV{
	{"ov", "Ortsverband (OV)"},
	{"rst", "Regionalstelle (RSt)"},
	{"lv", "Landesverband (LV)"},
	{"leitung", "Leitungsebene"},
}

// Status: Anforderungs-/Anfahrtsstatus einer Einheit, in fachlicher Reihenfolge.
var Status = []KV{
	{"geplant", "geplant"},
	{"angefordert", "angefordert"},
	{"anfahrt", "auf Anfahrt"},
	{"vor_ort", "vor Ort"},
	{"abgerueckt", "abgerückt"},
}

// StatusAnfahrt: Status, die in der Uebersicht "Auf Anfahrt" erscheinen.
var StatusAnfahrt = []string{"angefordert", "anfahrt"}

func Exists(role string) bool {
	_, ok := roleByKey[role]
	return ok
}

func Label(role string) string {
	if r, ok := roleByKey[role]; ok {
		return r.Label
	}
	return role
}

func Short(role string) string {
	if r, ok := roleByKey[role]; ok {
		return r.Short
	}
	return role
}

// CapsFor vereinigt die Capabilities einer Rollenliste (Reihenfolge wie Caps).
func CapsFor(roles []string) []string {
	have := map[string]bool{}
	for _, r := range roles {
		if rr, ok := roleByKey[r]; ok {
			for _, c := range rr.Caps {
				have[c] = true
			}
		}
	}
	out := []string{}
	for _, c := range Caps {
		if have[c] {
			out = append(out, c)
		}
	}
	return out
}

func HasCap(caps []string, cap string) bool {
	for _, c := range caps {
		if c == cap {
			return true
		}
	}
	return false
}

// NormalizeRoles filtert unbekannte und doppelte Rollen aus beliebiger Eingabe.
func NormalizeRoles(list any) []string {
	out := []string{}
	add := func(s string) {
		if !Exists(s) {
			return
		}
		for _, o := range out {
			if o == s {
				return
			}
		}
		out = append(out, s)
	}
	switch v := list.(type) {
	case []string:
		for _, s := range v {
			add(s)
		}
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				add(s)
			}
		}
	}
	return out
}

func StatusExists(s string) bool {
	for _, st := range Status {
		if st.Key == s {
			return true
		}
	}
	return false
}

func StatusLabel(s string) string {
	for _, st := range Status {
		if st.Key == s {
			return st.Label
		}
	}
	return s
}

func IsAnfahrt(s string) bool {
	for _, x := range StatusAnfahrt {
		if x == s {
			return true
		}
	}
	return false
}

func EbeneExists(e string) bool {
	for _, x := range Ebenen {
		if x.Key == e {
			return true
		}
	}
	return false
}

func EbeneLabel(e string) string {
	for _, x := range Ebenen {
		if x.Key == e {
			return x.Label
		}
	}
	return e
}

// OrderedMap haelt die Reihenfolge beim JSON-Encoding (PHP-Arrays sind geordnet).
type OrderedMap []KV

func (o OrderedMap) MarshalJSON() ([]byte, error) {
	b := []byte("{")
	for i, kv := range o {
		if i > 0 {
			b = append(b, ","...)
		}
		k, _ := json.Marshal(kv.Key)
		v, _ := json.Marshal(kv.Label)
		b = append(b, k...)
		b = append(b, ":"...)
		b = append(b, v...)
	}
	return append(b, "}"...), nil
}

// RolesJSON haelt die Rollenreihenfolge beim JSON-Encoding.
type RolesJSON []Role

func (rs RolesJSON) MarshalJSON() ([]byte, error) {
	b := []byte("{")
	for i, r := range rs {
		if i > 0 {
			b = append(b, ","...)
		}
		k, _ := json.Marshal(r.Key)
		b = append(b, k...)
		b = append(b, ":"...)
		v, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		b = append(b, v...)
	}
	return append(b, "}"...), nil
}

// ClientConfig liefert die Konfiguration fuer das Frontend (window.STAB).
func ClientConfig() map[string]any {
	return map[string]any{
		"roles":          RolesJSON(Roles),
		"ebenen":         OrderedMap(Ebenen),
		"status":         OrderedMap(Status),
		"status_anfahrt": StatusAnfahrt,
	}
}
