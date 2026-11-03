// Package kraefte: Kraeftekatalog - Stammdaten aller THW-Einheiten
// (public/daten/stammdaten.json der Installation, im Binary eingebettet;
// ohne sie die Beispieldatei stammdaten.example.json), Live-Status aus STEIN
// (direkt von stein.app mit dem Schluessel der Organisation, gecacht) und die Bindung von Einheiten in
// offenen Einsaetzen (kraft_key an Knoten, Mitgliedern und Schichten).
// Einzige Quelle fuer Einsatz-Seiten, STEINboard und STEINmap.
//
// Schluessel einer Einheit (kraft_key):   "<buid>:<typkey>[#<nr>]"
//
//	z.B. "103:fgrn" (FGr N Suedstadt), "102:bgr#2" (2. BGr Musterhausen)
//
// Nachbar-OVs anderer Landesverbaende ohne STEIN-BU-ID: "n-<slug>:<typkey>".
//
// Typ-Normalisierung (NormTyp) bildet die unterschiedlichen Schreibweisen
// aus STEIN ("N", "WP (A)", "ZTr TZ") und aus den Stammdaten ("FGr N",
// "FGr WP(A)", "ZTr") auf einen gemeinsamen Typschluessel ab. Das
// JavaScript-Gegenstueck fuer STEINboard/STEINmap steht in
// public/js/kraefte_typ.js und muss bei Aenderungen nachgezogen werden.
package kraefte

import (
	"regexp"
	"strconv"
	"strings"
)

// TypDef: Kurzform, Langbezeichnung, STEIN-Kategorie, taktisches Zeichen
// (relativ zu /images/taktische_zeichen/).
type TypDef struct {
	Key      string
	Short    string
	Label    string
	SteinCat string
	Symbol   string
}

// Typen in fester Reihenfolge (wie PHP TYPEN).
var Typen = []TypDef{
	{"ztr", "ZTr", "Zugtrupp", "ZTr TZ", "THW_Einheiten/Zugtrupp.svg"},
	{"ztrfk", "ZTr FK", "Zugtrupp FZ Führung und Kommunikation", "ZTr FZ-FK", "THW_Einheiten/Zugtrupp_FZ-FK.svg"},
	{"ztrlog", "ZTr Log", "Zugtrupp FZ Logistik", "ZTr FZ-Log", "THW_Einheiten/Zugtrupp_FZ-Logistik.svg"},
	{"bgr", "BGr", "Bergungsgruppe", "B", "THW_Einheiten/Bergungsgruppe.svg"},
	{"bgrash", "BGr (ASH)", "Bergungsgruppe mit Abstützsystem Holz", "B ASH", "THW_Einheiten/Bergungsgruppe_ASH.svg"},
	{"bgregs", "BGr (EGS)", "Bergungsgruppe mit Einsatz-Gerüstsystem", "B EGS", "THW_Einheiten/Bergungsgruppe_EGS.svg"},
	{"fgrn", "FGr N", "Fachgruppe Notversorgung und Notinstandsetzung", "N", "THW_Einheiten/FGr_Notversorgung_und_Notinstandsetzung.svg"},
	{"fgre", "FGr E", "Fachgruppe Elektroversorgung", "E", "THW_Einheiten/FGr_Elektroversorgung.svg"},
	{"fgri", "FGr I", "Fachgruppe Infrastruktur", "I", "THW_Einheiten/FGr_Infrastruktur.svg"},
	{"fgrbel", "FGr Bel", "Fachgruppe Beleuchtung", "Bel", "THW_Einheiten/FGr_Beleuchtung.svg"},
	{"fgrbrb", "FGr BrB", "Fachgruppe Brückenbau", "BrB", "THW_Einheiten/FGr_Brückenbau.svg"},
	{"fgrf", "FGr F", "Fachgruppe Führungsunterstützung", "F", "THW_Einheiten/FGr_Führungsunterstützung.svg"},
	{"fgrfk", "FGr FK", "Fachgruppe Führung und Kommunikation", "FK", "THW_Einheiten/FGr_Führung-Kommunikation.svg"},
	{"fgrka", "FGr K (A)", "Fachgruppe Kommunikation (A)", "K (A)", "THW_Einheiten/FGr_Kommunikation_A.svg"},
	{"fgrkb", "FGr K (B)", "Fachgruppe Kommunikation (B)", "K (B)", "THW_Einheiten/FGr_Kommunikation_B.svg"},
	{"fgrlogv", "FGr Log-V", "Fachgruppe Logistik-Verpflegung", "Log-V", "THW_Einheiten/FGr_Logistik-Verpflegung.svg"},
	{"fgrlogmw", "FGr Log-MW", "Fachgruppe Logistik-Materialwirtschaft", "Log-MW", "THW_Einheiten/FGr_Logistik_Materialwirtschaft.svg"},
	{"fgro", "FGr O", "Fachgruppe Ortung", "O (A)", "THW_Einheiten/FGr_Ortung.svg"},
	{"fgroa", "FGr O (A)", "Fachgruppe Ortung (A)", "O (A)", "THW_Einheiten/FGr_Ortung.svg"},
	{"fgrob", "FGr O (B)", "Fachgruppe Ortung (B)", "O (B)", "THW_Einheiten/FGr_Ortung.svg"},
	{"fgroc", "FGr O (C)", "Fachgruppe Ortung (C)", "O (C)", "THW_Einheiten/FGr_Ortung.svg"},
	{"fgroel", "FGr Öl", "Fachgruppe Ölschaden", "Öl (A)", "THW_Einheiten/FGr_Ölschaden.svg"},
	{"fgroela", "FGr Öl (A)", "Fachgruppe Ölschaden (A)", "Öl (A)", "THW_Einheiten/FGr_Ölschaden_A.svg"},
	{"fgroelb", "FGr Öl (B)", "Fachgruppe Ölschaden (B)", "Öl (B)", "THW_Einheiten/FGr_Ölschaden_B.svg"},
	{"fgroelc", "FGr Öl (C)", "Fachgruppe Ölschaden (C)", "Öl (C)", "THW_Einheiten/FGr_Ölschaden_C.svg"},
	{"fgrr", "FGr R", "Fachgruppe Räumen", "R (A)", "THW_Einheiten/FGr_Räumen.svg"},
	{"fgrra", "FGr R (A)", "Fachgruppe Räumen (A)", "R (A)", "THW_Einheiten/FGr_Räumen_A.svg"},
	{"fgrrb", "FGr R (B)", "Fachgruppe Räumen (B)", "R (B)", "THW_Einheiten/FGr_Räumen_B.svg"},
	{"fgrrc", "FGr R (C)", "Fachgruppe Räumen (C)", "R (C)", "THW_Einheiten/FGr_Räumen_C.svg"},
	{"fgrsb", "FGr SB", "Fachgruppe Schwere Bergung", "SB (A)", "THW_Einheiten/FGr_Schwere_Bergung.svg"},
	{"fgrsba", "FGr SB (A)", "Fachgruppe Schwere Bergung (A)", "SB (A)", "THW_Einheiten/FGr_Schwere_Bergung_A.svg"},
	{"fgrsbb", "FGr SB (B)", "Fachgruppe Schwere Bergung (B)", "SB (B)", "THW_Einheiten/FGr_Schwere_Bergung_B.svg"},
	{"fgrsp", "FGr Sp", "Fachgruppe Sprengen", "Sp", "THW_Einheiten/FGr_Sprengen.svg"},
	{"fgrtw", "FGr TW", "Fachgruppe Trinkwasserversorgung", "TW", "THW_Einheiten/FGr_Trinkwasser.svg"},
	{"fgrw", "FGr W", "Fachgruppe Wassergefahren", "W", "THW_Einheiten/FGr_Wassergefahren.svg"},
	{"fgrwa", "FGr W (A)", "Fachgruppe Wassergefahren (A)", "W (A)", "THW_Einheiten/FGr_Wassergefahren_A.svg"},
	{"fgrwb", "FGr W (B)", "Fachgruppe Wassergefahren (B)", "W (B)", "THW_Einheiten/FGr_Wassergefahren_B.svg"},
	{"fgrwp", "FGr WP", "Fachgruppe Wasserschaden/Pumpen", "WP (A)", "THW_Einheiten/FGr_Wasserschaden-Pumpen.svg"},
	{"fgrwpa", "FGr WP (A)", "Fachgruppe Wasserschaden/Pumpen (A)", "WP (A)", "THW_Einheiten/FGr_Wassserschaden-Pumpen_A.svg"},
	{"fgrwpb", "FGr WP (B)", "Fachgruppe Wasserschaden/Pumpen (B)", "WP (B)", "THW_Einheiten/FGr_Wassserschaden-Pumpen_B.svg"},
	{"fgrwpc", "FGr WP (C)", "Fachgruppe Wasserschaden/Pumpen (C)", "WP (C)", "THW_Einheiten/FGr_Wassserschaden-Pumpen_C.svg"},
	{"trul", "Tr UL", "Trupp Unbemannte Luftfahrtsysteme", "Tr UL", "THW_Einheiten/Trupp_UL.svg"},
	{"tress", "Tr ESS", "Trupp Einsatzstellensicherungssystem", "Tr ESS", "THW_Einheiten/Trupp_ESS.svg"},
	{"trmhp", "Tr MHP", "Trupp Mobiler Hochwasserpegel", "Tr MHP", "THW_Einheiten/Trupp_MHP.svg"},
	{"trts", "Tr TS", "Trupp Schwerer Transport", "Tr TS", "THW_Einheiten/Trupp_TS.svg"},
	{"trlogm", "Tr Log-M", "Trupp Logistik-Material", "Tr Log-M", "THW_Einheiten/FGr_Logistik_Materialerhaltungstrupp.svg"},
	{"trlogvg", "Tr Log-VG", "Trupp Logistik-Verbrauchsgüter", "Tr Log-VG", "THW_Einheiten/FGr_Logistik_Materialwirtschaft_Verbrauchsgüterversorgungstrupp.svg"},
	{"trerlo", "Tr ErLo", "Trupp Erkundung und Lotsen", "Tr ErLo", "THW_Einheiten/Lotsengruppe.svg"},
	{"oega", "ÖGA", "Örtliche Gefahrenabwehr", "ÖGA", "THW_Einheiten/Örtliche_Gefahrenabwehr.svg"},
	{"ovstab", "OV-Stab", "OV-Stab", "OV-Stab", "THW_Einheiten/OV_Stab.svg"},
	{"stab", "Stab", "Stab", "Stab", "THW_Einheiten/Stab.svg"},
	{"ent", "ENT", "Einsatznachsorgeteam", "ENT", "THW_Einheiten/ENT.svg"},
	{"hcp", "HCP", "High Capacity Pumping", "HCP", "THW_Einheiten/FGr_Wasserschaden-Pumpen.svg"},
	{"mt", "MT", "Media Team", "MT", "THW_Einheiten/Media_Team.svg"},
	{"vost", "VOST", "Virtual Operations Support Team", "VOST", "THW_Einheiten/VOST.svg"},
	{"seeba", "SEEBA", "Schnelleinsatzeinheit Bergung Ausland", "SEEBA", "THW_Einheiten/SEEBA.svg"},
	{"seewa", "SEEWA", "Schnelleinsatzeinheit Wasser Ausland", "SEEWA", "THW_Einheiten/SEEWA.svg"},
	{"seelift", "SEELift", "Schnelleinsatzeinheit Lufttransport", "SEELift", "THW_Einheiten/SEELift.svg"},
	{"sysbr500", "Sys BR500", "System BR500", "Sys BR500", "THW_Einheiten/SysBR.svg"},
	{"gesamterov", "Gesamter OV", "Gesamter Ortsverband", "Gesamter OV", "THW_Einheiten/OV_Stab.svg"},
}

// TypKeys: Schluessel in Tabellenreihenfolge.
var TypKeys = func() []string {
	out := make([]string, 0, len(Typen))
	for _, t := range Typen {
		out = append(out, t.Key)
	}
	return out
}()

var typByKey = func() map[string]TypDef {
	m := map[string]TypDef{}
	for _, t := range Typen {
		m[t.Key] = t
	}
	return m
}()

// fgrShorts: Fachgruppen-Kurzformen, die STEIN ohne "FGr"-Praefix liefert.
var fgrShorts = map[string]bool{}

func init() {
	for _, s := range []string{
		"n", "e", "i", "bel", "brb", "f", "fk", "fkf", "fkfk", "fkfm", "ka", "kb", "logv", "logmw",
		"o", "oa", "ob", "oc", "oel", "oela", "oelb", "oelc", "r", "ra", "rb", "rc",
		"sb", "sba", "sbb", "sp", "spr", "tw", "w", "wa", "wb", "wp", "wpa", "wpb", "wpc",
	} {
		fgrShorts[s] = true
	}
}

var (
	reFamily   = regexp.MustCompile(`^(fgro|fgroel|fgrr|fgrsb|fgrw|fgrwp|fgrk)[abc]$`)
	reNrSuffix = regexp.MustCompile(`^(.*?)\s*\((\d)\)\s*\)?\s*$`)
	reBNr      = regexp.MustCompile(`(?i)^(b|bgr)\s*(\d)\b(.*)$`)
	reNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)
	reBgr      = regexp.MustCompile(`^b(?:gr)?(ash|egs)?$`)
	reTrupp    = regexp.MustCompile(`^tr(ul|ess|mhp|ts|logm|logvg|erlo)$`)
	reSpaces   = regexp.MustCompile(`\s+`)
)

// TypFamily: Typ-Familie ohne Variante: fgroa/fgrob/fgroc -> fgro, bgrash -> bgr.
// Fuer die tolerante Zuordnung STEIN <-> Stammdaten, wenn die Variante
// (A/B/C) auf einer Seite fehlt oder abweicht. Kopie in kraefte_typ.js.
func TypFamily(key string) string {
	if m := reFamily.FindStringSubmatch(key); m != nil {
		return m[1]
	}
	if key == "bgrash" || key == "bgregs" {
		return "bgr"
	}
	return key
}

// Typ ist das Ergebnis der Normalisierung einer Typ-Schreibweise.
type Typ struct {
	Key   string `json:"key"`
	Nr    *int   `json:"nr"`
	Short string `json:"short"`
	Label string `json:"label"`
	Known bool   `json:"known"`
}

// NormTyp bildet STEIN-Kategorien und Stamm-Labels auf den Typschluessel ab.
func NormTyp(raw string) Typ {
	r := strings.TrimSpace(raw)
	var nr *int
	// Laufende Nummer "(1)", "(2)" am Ende
	if m := reNrSuffix.FindStringSubmatch(r); m != nil {
		r = m[1]
		n, _ := strconv.Atoi(m[2])
		nr = &n
	}
	r = strings.TrimRight(r, ") \t")
	// "B1", "BGr1", "B1 ASH"
	if m := reBNr.FindStringSubmatch(r); m != nil {
		if nr == nil {
			n, _ := strconv.Atoi(m[2])
			nr = &n
		}
		r = m[1] + m[3]
	}
	s := strings.ToLower(r)
	s = strings.NewReplacer("ö", "oe", "ä", "ae", "ü", "ue", "ß", "ss").Replace(s)
	s = reNonAlnum.ReplaceAllString(s, "")
	if s == "" {
		return Typ{Key: "", Nr: nr, Short: strings.TrimSpace(raw), Label: strings.TrimSpace(raw), Known: false}
	}
	var key string
	switch {
	case strings.HasPrefix(s, "ztr"):
		if strings.Contains(s, "fk") {
			key = "ztrfk"
		} else if strings.Contains(s, "log") {
			key = "ztrlog"
		} else {
			key = "ztr"
		}
	case reBgr.MatchString(s):
		key = "bgr" + reBgr.FindStringSubmatch(s)[1]
	case strings.HasPrefix(s, "fgr"):
		key = s
	case reTrupp.MatchString(s):
		key = s
	case fgrShorts[s]:
		key = "fgr" + s
	default:
		key = s
	}
	// Aliasse
	switch key {
	case "fgrspr":
		key = "fgrsp"
	case "fgrfkf":
		key = "fgrf"
	case "fgrfkfk", "fgrfkfm":
		key = "fgrfk"
	}
	if strings.HasPrefix(key, "oega") { // "ÖGA (Höhenrettung)"
		key = "oega"
	}
	shortRaw := strings.TrimSpace(reSpaces.ReplaceAllString(r, " "))
	if t, ok := typByKey[key]; ok {
		return Typ{Key: key, Nr: nr, Short: t.Short, Label: t.Label, Known: true}
	}
	return Typ{Key: key, Nr: nr, Short: shortRaw, Label: shortRaw, Known: false}
}

// TypInfo liefert die Beschreibung eines Typschluessels (symbol nil, wenn unbekannt).
func TypInfo(key string) map[string]any {
	if t, ok := typByKey[key]; ok {
		return map[string]any{"key": key, "short": t.Short, "label": t.Label, "stein_cat": t.SteinCat, "symbol": t.Symbol}
	}
	return map[string]any{"key": key, "short": key, "label": key, "stein_cat": "", "symbol": nil}
}

// AllTypInfo: Typschluessel => TypInfo (fuer api/kraefte?action=typen und den Katalog).
func AllTypInfo() map[string]any {
	out := map[string]any{}
	for _, k := range TypKeys {
		out[k] = TypInfo(k)
	}
	return out
}
