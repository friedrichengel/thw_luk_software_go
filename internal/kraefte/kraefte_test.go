package kraefte_test

import (
	"io/fs"
	"path/filepath"
	"testing"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/einsatz"
	"github.com/friedrichengel/thw_luk_software_go/internal/kraefte"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

// newKatalog laedt die Beispiel-Stammdaten (LV Musterland), nie die echte
// Datei der Installation: die Tests muessen ohne sie laufen.
func newKatalog(t *testing.T) *kraefte.Katalog {
	t.Helper()
	raw, err := fs.ReadFile(luk.Public, kraefte.StammExample)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{SteinBaseURL: "http://127.0.0.1:9"}
	return kraefte.New(cfg, raw)
}

func asset(bu, cat, name, status string) map[string]any {
	return map[string]any{"buId": bu, "groupId": float64(4), "category": cat, "name": name, "status": status, "lastModified": "2026-10-01T00:00:00Z"}
}

func TestNormTyp(t *testing.T) {
	tt := kraefte.NormTyp("WP (A)")
	if tt.Key != "fgrwpa" || tt.Short != "FGr WP (A)" || !tt.Known {
		t.Fatalf("normTyp STEIN: %+v", tt)
	}
	t2 := kraefte.NormTyp("FGr WP(A) (2)")
	if t2.Key != "fgrwpa" || t2.Nr == nil || *t2.Nr != 2 {
		t.Fatalf("normTyp Stamm-Label: %+v", t2)
	}
	cases := map[string]string{"ZTr TZ": "ztr", "B1 ASH": "bgrash", "Log-V": "fgrlogv", "Ztr (Log)": "ztrlog", "Öl (C)": "fgroelc", "ÖGA (Höhenrettung)": "oega"}
	for in, want := range cases {
		if got := kraefte.NormTyp(in).Key; got != want {
			t.Errorf("normTyp(%q) = %q, want %q", in, got, want)
		}
	}
	if kraefte.NormTyp("B1 ASH").Nr == nil || *kraefte.NormTyp("B1 ASH").Nr != 1 {
		t.Error("B1 ASH nr")
	}
	for in, want := range map[string]string{"fgrob": "fgro", "bgrash": "bgr", "fgrn": "fgrn", "ztr": "ztr", "fgrwpa": "fgrwp", "fgroelc": "fgroel"} {
		if got := kraefte.TypFamily(in); got != want {
			t.Errorf("typFamily(%q) = %q, want %q", in, got, want)
		}
	}
	if kraefte.TypInfo("fgrn")["symbol"] == nil || kraefte.TypInfo("xyz")["symbol"] != nil {
		t.Error("typInfo symbol")
	}
}

func TestSteinByKey(t *testing.T) {
	k := newKatalog(t)
	sbk := k.SteinByKey([]any{
		asset("101", "O (B)", "FGr O", "ready"),
		asset("101", "ZTr TZ", "2.TZ ZTr", "inuse"),
		asset("101", "ZTr TZ", "1.TZ ZTr", "ready"),
		asset("101", "Tr ESS", "ESS", "ready"),
		asset("102", "O (C)", "O", "ready"),
		asset("102", "W (A)", "W", "ready"),
	})
	get := func(key, field string) string {
		m, _ := sbk[key].(map[string]any)
		if m == nil {
			return ""
		}
		s, _ := m[field].(string)
		return s
	}
	if get("101:fgro", "name") != "FGr O" || get("101:tress", "status") != "ready" {
		t.Fatalf("Familie O (B) -> FGr O, Tr ESS exakt: %v", sbk)
	}
	if get("101:ztr", "name") != "1.TZ ZTr" || get("101:ztr#2", "name") != "2.TZ ZTr" {
		t.Fatalf("zwei ZTr nach Name verteilt: %v %v", sbk["101:ztr"], sbk["101:ztr#2"])
	}
	if get("102:fgroc", "name") != "O" || get("102:fgrwb", "name") != "W" {
		t.Fatalf("exakt vor Familie: %v %v", sbk["102:fgroc"], sbk["102:fgrwb"])
	}
	if _, ok := sbk["_fahrzeuge"].(map[string]any); !ok {
		t.Fatal("_fahrzeuge fehlt")
	}
}

func TestStammLookup(t *testing.T) {
	k := newKatalog(t)
	for key, want := range map[string]bool{"103:fgrn": true, "102:bgr#2": true, "n-fremdstadt:ztr": true, "": true, "x y": false, "103:": false} {
		if kraefte.ValidKey(key) != want {
			t.Errorf("validKey(%q) != %v", key, want)
		}
	}
	st := k.Stamm()
	if len(k.OVs()) < 7 || len(k.Regionen()) < 4 || st["ovs"] == nil {
		t.Fatalf("Stammdaten: %d OVs, %d Regionen", len(k.OVs()), len(k.Regionen()))
	}
	ov, e, ok := k.Lookup("103:fgrn")
	if !ok || ov["name"] != "Südstadt" || e["short"] != "FGr N" {
		t.Fatalf("lookup: %v %v %v", ok, ov, e)
	}
	if l := k.KeyLabel("102:bgr#2"); l != "BGr 2 Musterhausen" {
		t.Fatalf("keyLabel: %q", l)
	}
	for key, want := range map[string]bool{"": true, "rst_nord": true, "ov:103": true, "ov:999": false, "ov:n-fremdstadt": false, "lv_nachbar": false} {
		if k.ValidBereich(key) != want {
			t.Errorf("validBereich(%q) != %v", key, want)
		}
	}
	// "" + 3 eigene Regionen + 4 eigene OVs (keine RSt/LV-Dienststellen, keine Nachbarn)
	if len(k.Bereiche()) != 8 {
		t.Errorf("bereiche: %d", len(k.Bereiche()))
	}
	st2 := k.SteinAssets(0)
	if st2.OK || st2.Error == nil || *st2.Error != "not_configured" {
		t.Fatalf("stein ohne Key: %+v", st2)
	}
}

func TestBindungen(t *testing.T) {
	k := newKatalog(t)
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"), luk.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	lid, _ := user.Create(d, "leiter1", "pw", nil, nil, "leiter", false)
	nid, _ := user.Create(d, "nobody", "pw", nil, nil, "user", false)
	p := access.Principal{UID: lid, Role: "leiter", Label: "L"}
	u, _ := einsatz.CreateEinsatz(d, p, "Hochwasser Elbe", "lv", "Stab LV", "")
	c, _ := access.Resolve(d, p, u)
	eid := c.Einsatz.ID
	root, _ := einsatz.CreateNode(d, c, map[string]any{"type": "fuehrung", "bezeichnung": "Stab LV"})
	bgr, err := einsatz.CreateNode(d, c, map[string]any{"type": "einheit", "bezeichnung": "BGr 1", "parent_id": root.ID, "kraft_key": "103:bgr"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := einsatz.UpdateNode(d, c, bgr.ID, bgr.Version, map[string]any{"kraft_key": "ungültig!"}, einsatz.Keys(einsatz.NodeFields)); !apierr.Is(err, "invalid_kraft_key") {
		t.Fatalf("kraft_key geprueft: %v", err)
	}
	// abgelaufene Schicht mit Abloesung bindet nicht
	if _, err := einsatz.CreateSchicht(d, c, map[string]any{"node_id": bgr.ID, "von": "2026-10-01T08:00", "bis": "2026-10-01T20:00", "staerke": "1/3/8", "kraft_key": "102:bgr"}); err != nil {
		t.Fatal(err)
	}
	sch3, _ := einsatz.CreateSchicht(d, c, map[string]any{"node_id": bgr.ID, "von": "2026-10-01T20:00", "bis": "2026-10-02T08:00", "staerke": "1/3/8", "status": "angefordert"})
	sch3b, _ := einsatz.SetSchichtStatus(d, c, sch3.ID, sch3.Version, "anfahrt")
	bind, err := k.Bindungen(d, lid, &eid)
	if err != nil {
		t.Fatal(err)
	}
	if b := bind["103:bgr"]; len(b) == 0 || b[0]["own"] != true || b[0]["einsatz_title"] != "Hochwasser Elbe" {
		t.Fatalf("Bindung fuer Mitglied sichtbar: %v", bind)
	}
	if _, ok := bind["102:bgr"]; ok {
		t.Fatal("abgelaufene Schicht bindet nicht mehr")
	}
	bindN, _ := k.Bindungen(d, nid, nil)
	if b := bindN["103:bgr"]; len(b) == 0 || b[0]["einsatz_title"] != nil || b[0]["visible"] != false {
		t.Fatalf("anonymisiert: %v", bindN)
	}
	// Knoten abgerueckt, Schicht auf Anfahrt -> abgeleiteter Status, bleibt gebunden
	nAb, _ := einsatz.SetNodeStatus(d, c, bgr.ID, bgr.Version, "abgerueckt")
	bind, _ = k.Bindungen(d, lid, &eid)
	if b := bind["103:bgr"]; len(b) == 0 || b[0]["status"] != "anfahrt" {
		t.Fatalf("abgeleiteter Status: %v", bind)
	}
	// Schicht ebenfalls abgerueckt -> frei; geplante Abloesung am abgerueckten Knoten bindet nicht
	einsatz.SetSchichtStatus(d, c, sch3.ID, sch3b.Version, "abgerueckt")
	sch4, _ := einsatz.CreateSchicht(d, c, map[string]any{"node_id": bgr.ID, "von": "2030-01-01T08:00", "bis": "2030-01-01T20:00", "staerke": "1/3/8", "kraft_key": "102:bgr#2"})
	bind, _ = k.Bindungen(d, lid, &eid)
	if _, ok := bind["103:bgr"]; ok {
		t.Fatal("Einheit abgerueckt: Knoten bindet nicht")
	}
	if _, ok := bind["102:bgr#2"]; ok {
		t.Fatal("Einheit abgerueckt: Schicht bindet nicht")
	}
	nPl, _ := einsatz.SetNodeStatus(d, c, bgr.ID, nAb.Version, "geplant")
	bind, _ = k.Bindungen(d, lid, &eid)
	if _, ok := bind["103:bgr"]; ok {
		t.Fatal("Schicht abgerueckt, Knoten geplant: frei")
	}
	_ = nPl
	einsatz.SetSchichtStatus(d, c, sch4.ID, sch4.Version, "vor_ort")
	bind, _ = k.Bindungen(d, lid, &eid)
	if b := bind["103:bgr"]; len(b) == 0 || b[0]["status"] != "vor_ort" {
		t.Fatalf("Abloesung vor Ort: Einheit gebunden: %v", bind)
	}
	if b := bind["102:bgr#2"]; len(b) == 0 || b[0]["kind"] != "schicht" {
		t.Fatalf("Abloesung gebunden: %v", bind)
	}
	kat, err := k.KatalogFor(d, c, 0)
	if err != nil {
		t.Fatal(err)
	}
	var bgrE map[string]any
	for _, ov := range kat["ovs"].([]map[string]any) {
		if ov["key"] != "103" {
			continue
		}
		for _, e := range ov["einheiten"].([]map[string]any) {
			if e["key"] == "103:bgr" {
				bgrE = e
			}
		}
	}
	if bgrE == nil || len(bgrE["bindungen"].([]map[string]any)) != 1 {
		t.Fatalf("Katalog mit Bindungen: %v", bgrE)
	}
	stein := kat["stein"].(map[string]any)
	if _, ok := stein["ok"].(bool); !ok || kat["typen"].(map[string]any)["fgrn"] == nil || kat["bereich_key"] == nil {
		t.Fatalf("Katalog-Struktur: %v", kat["stein"])
	}
}
