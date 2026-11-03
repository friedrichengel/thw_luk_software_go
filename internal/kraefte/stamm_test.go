package kraefte_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/kraefte"
)

// Stammdaten-Quelle: STAMM_FILE vor eingebetteter Datei vor Beispieldatei;
// ohne alles ein Fehler. Die echte Datei liegt nicht im Repository, deshalb
// wird hier nur geprueft, dass das Binary eine der beiden eingebetteten
// Dateien findet und sie parst.
func TestReadStamm(t *testing.T) {
	raw, src, err := kraefte.ReadStamm(luk.Public, "")
	if err != nil || (src != kraefte.StammFile && src != kraefte.StammExample) || len(raw) == 0 {
		t.Fatalf("eingebettet: %q %v", src, err)
	}
	if k := kraefte.New(&config.Config{}, raw); len(k.OVs()) == 0 || len(k.Regionen()) == 0 {
		t.Fatalf("eingebettete Stammdaten leer (%s)", src)
	}

	example, _ := fs.ReadFile(luk.Public, kraefte.StammExample)
	onlyExample := fstest.MapFS{kraefte.StammExample: &fstest.MapFile{Data: example}}
	if _, src, err := kraefte.ReadStamm(onlyExample, ""); err != nil || src != kraefte.StammExample {
		t.Fatalf("Rueckfall auf Beispieldatei: %q %v", src, err)
	}
	both := fstest.MapFS{
		kraefte.StammExample: &fstest.MapFile{Data: example},
		kraefte.StammFile:    &fstest.MapFile{Data: []byte(`{"lv":{"key":"x"},"regionen":[],"ovs":[]}`)},
	}
	if raw, src, err := kraefte.ReadStamm(both, ""); err != nil || src != kraefte.StammFile || string(raw) != `{"lv":{"key":"x"},"regionen":[],"ovs":[]}` {
		t.Fatalf("eigene Datei vor Beispiel: %q %v", src, err)
	}
	if _, _, err := kraefte.ReadStamm(fstest.MapFS{}, ""); err == nil {
		t.Fatal("ohne Dateien kein Fehler")
	}

	// STAMM_FILE: Datei auf dem Rechner gewinnt; fehlt sie, Fehler statt Rueckfall
	p := filepath.Join(t.TempDir(), "stamm.json")
	if err := os.WriteFile(p, []byte(`{"lv":{"key":"disk"},"regionen":[],"ovs":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if raw, src, err := kraefte.ReadStamm(both, p); err != nil || src != p || string(raw) == string(example) {
		t.Fatalf("STAMM_FILE: %q %v", src, err)
	}
	if _, _, err := kraefte.ReadStamm(both, filepath.Join(t.TempDir(), "fehlt.json")); err == nil {
		t.Fatal("fehlende STAMM_FILE ohne Fehler")
	}
}

// Kartenmittelpunkt und Feeds aus den Stammdaten: "karte" gilt, wenn die
// Umgebung MAP_CENTER_* nicht setzt; ohne Abschnitt der Server-Wert.
func TestStammKarteUndFeeds(t *testing.T) {
	k := newKatalog(t)
	c, ok := k.Karte()
	if !ok || c.Name != "THW LV Musterland" || c.Lat != 51.1657 || c.Lng != 10.4515 {
		t.Fatalf("karte: %+v %v", c, ok)
	}
	cfg := &config.Config{MapCenterLat: 1, MapCenterLng: 2, MapCenterName: "Server"}
	if got := k.Center(cfg); got != c {
		t.Fatalf("ohne MAP_CENTER_*: Stammdaten erwartet, got %+v", got)
	}
	cfg.MapCenterSet = true
	if got := k.Center(cfg); got.Name != "Server" || got.Lat != 1 {
		t.Fatalf("mit MAP_CENTER_*: Server erwartet, got %+v", got)
	}
	if feeds, ok := k.Feeds().([]any); !ok || len(feeds) != 2 {
		t.Fatalf("feeds: %v", k.Feeds())
	}

	// ohne Abschnitte: Name aus lv, sonst Server-Standard
	k2 := kraefte.New(&config.Config{}, []byte(`{"lv":{"key":"x","name":"LV X"},"karte":{"lat":"50.5","lng":9},"regionen":[],"ovs":[]}`))
	if c2, ok := k2.Karte(); !ok || c2.Name != "LV X" || c2.Lat != 50.5 || c2.Lng != 9 {
		t.Fatalf("karte ohne Namen: %+v %v", c2, ok)
	}
	k3 := kraefte.New(&config.Config{}, []byte(`{"lv":{},"karte":{"lat":"x"},"regionen":[],"ovs":[]}`))
	if _, ok := k3.Karte(); ok {
		t.Fatal("unvollstaendige karte angenommen")
	}
	cfg.MapCenterSet = false
	if got := k3.Center(cfg); got.Name != "Server" {
		t.Fatalf("ohne karte: Server erwartet, got %+v", got)
	}
	if got := k3.Center(nil); got != config.DefaultMapCenter {
		t.Fatalf("ohne alles: Standard erwartet, got %+v", got)
	}
}
