package settings_test

import (
	"path/filepath"
	"testing"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/settings"
)

func TestSettings(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"), luk.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v := settings.Get(d, settings.TileSource); v != "" {
		t.Fatalf("leer erwartet: %q", v)
	}
	if err := settings.Set(d, settings.TileSource, "  https://x.example/{z}/{x}/{y}.png ", 1); err != nil {
		t.Fatal(err)
	}
	if v := settings.Get(d, settings.TileSource); v != "https://x.example/{z}/{x}/{y}.png" {
		t.Fatalf("getrimmt erwartet: %q", v)
	}
	settings.Set(d, settings.TileSource, "https://y.example/{z}/{x}/{y}.png", 1)
	if v := settings.Get(d, settings.TileSource); v != "https://y.example/{z}/{x}/{y}.png" {
		t.Fatalf("ersetzen: %q", v)
	}
	if v, set := settings.Bool(d, settings.TilePublic); v || set {
		t.Fatal("ungesetzter Schalter")
	}
	settings.Set(d, settings.TilePublic, "1", 1)
	if v, set := settings.Bool(d, settings.TilePublic); !v || !set {
		t.Fatal("gesetzter Schalter")
	}
	settings.Set(d, settings.TilePublic, "", 1)
	if _, set := settings.Bool(d, settings.TilePublic); set {
		t.Fatal("leer muss loeschen")
	}
}
