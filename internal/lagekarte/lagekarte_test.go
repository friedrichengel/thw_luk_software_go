package lagekarte

import (
	"path/filepath"
	"reflect"
	"testing"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/einsatz"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

func strPtr(s string) *string { return &s }

func setup(t *testing.T) (*db.DB, *access.Ctx, *einsatz.Node) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"), luk.Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	uid, err := user.Create(d, "s2user", "pw", nil, strPtr("Sven S2"), "leiter", false)
	if err != nil {
		t.Fatal(err)
	}
	p := access.Principal{UID: uid, Role: "leiter", Label: "Sven S2"}
	u, err := einsatz.CreateEinsatz(d, p, "Hochwasser Elbe", "lv", "Stab LV", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := access.Resolve(d, p, u)
	if err != nil || ctx == nil {
		t.Fatalf("ctx: %v", err)
	}
	root, err := einsatz.CreateNode(d, ctx, map[string]any{"type": "fuehrung", "bezeichnung": "Stab LV"})
	if err != nil {
		t.Fatal(err)
	}
	bgr, err := einsatz.CreateNode(d, ctx, map[string]any{"type": "einheit", "bezeichnung": "BGr 1", "parent_id": root.ID})
	if err != nil {
		t.Fatal(err)
	}
	return d, ctx, bgr
}

func TestLagekarte(t *testing.T) {
	d, ctx, bgr := setup(t)
	karte, err := Create(d, ctx, map[string]any{"title": "Lage Nord"}, 52.52, 13.405)
	if err != nil || karte.Title != "Lage Nord" || karte.CenterLat == nil || *karte.Zoom != 11 {
		t.Fatalf("create: %v %+v", err, karte)
	}
	ab, err := AbschnittCreate(d, ctx, karte.ID, "Ostseite", nil)
	if err != nil || ab.Nr != 1 {
		t.Fatalf("abschnitt: %v %+v", err, ab)
	}
	f1, err := FeatureCreate(d, ctx, karte.ID, map[string]any{
		"kind": "marker", "geometry": map[string]any{"lat": 52.5, "lng": 13.4},
		"props":      map[string]any{"file": "THW_Einheiten/Bergungsgruppe.svg", "label": "BGr", "baseSize": 48.0, "rotation": 0.0, "info": map[string]any{"ov": "OV A"}},
		"abschnitte": []any{1.0}, "ref_kind": "node", "ref_id": bgr.ID,
	})
	if err != nil || f1.RefID == nil || *f1.RefID != bgr.ID || !reflect.DeepEqual(f1.Abschnitte, []int{1}) {
		t.Fatalf("marker: %v %+v", err, f1)
	}
	if p := f1.Props.(map[string]any); p["file"] != "THW_Einheiten/Bergungsgruppe.svg" || p["baseSize"].(float64) != 48 {
		t.Fatalf("props: %+v", p)
	}
	if _, err := FeatureCreate(d, ctx, karte.ID, map[string]any{"kind": "marker", "geometry": map[string]any{"lat": 999.0, "lng": 0.0}, "props": map[string]any{"file": "a/b.svg"}}); !apierr.Is(err, "invalid_geometry") {
		t.Fatalf("expected invalid_geometry, got %v", err)
	}
	if _, err := FeatureCreate(d, ctx, karte.ID, map[string]any{"kind": "marker", "geometry": map[string]any{"lat": 1.0, "lng": 0.0}, "props": map[string]any{"file": "../x.svg"}}); !apierr.Is(err, "invalid_symbol") {
		t.Fatalf("expected invalid_symbol, got %v", err)
	}
	f2, err := FeatureCreate(d, ctx, karte.ID, map[string]any{"kind": "polygon", "geometry": map[string]any{"latlngs": []any{[]any{52.5, 13.4}, []any{52.6, 13.4}, []any{52.6, 13.5}}},
		"props": map[string]any{"style": map[string]any{"color": "#ff0000", "weight": 4.0}}, "abschnitte": []any{1.0}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FeatureCreate(d, ctx, karte.ID, map[string]any{"kind": "arrow", "geometry": map[string]any{"start": []any{52.5, 13.4}, "end": []any{52.55, 13.45}}, "props": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := FeatureCreate(d, ctx, karte.ID, map[string]any{"kind": "text", "geometry": map[string]any{"lat": 52.5, "lng": 13.4}, "props": map[string]any{"text": "Hallo", "fontSize": 20.0}}); err != nil {
		t.Fatal(err)
	}
	f2u, err := FeatureUpdate(d, ctx, f2.ID, 1, map[string]any{"props": map[string]any{"style": map[string]any{"color": "#00ff00"}, "note": "Sperrgebiet"}})
	if err != nil || f2u.Version != 2 || f2u.Props.(map[string]any)["style"].(map[string]any)["color"] != "#00ff00" {
		t.Fatalf("update: %v %+v", err, f2u)
	}
	if _, err := FeatureUpdate(d, ctx, f2.ID, 1, map[string]any{"abschnitte": []any{}}); !apierr.Is(err, "conflict") {
		t.Fatalf("expected conflict, got %v", err)
	}
	del, err := AbschnittDelete(d, ctx, karte.ID, 1, 1)
	if err != nil || len(del.Features) != 2 {
		t.Fatalf("abschnittDelete: %v %+v", err, del)
	}
	raw, _ := einsatz.RowByID(d, "lagekarte_features", f1.ID)
	if !reflect.DeepEqual(FeatureRow(raw).Abschnitte, []int{}) {
		t.Fatalf("abschnitte nicht bereinigt: %+v", FeatureRow(raw).Abschnitte)
	}
	refs, _ := FeatureRefs(d, ctx.Einsatz.ID)
	if len(refs) != 1 || refs[0].RefID != bgr.ID {
		t.Fatalf("refs: %+v", refs)
	}
	list, _ := List(d, ctx.Einsatz.ID, false)
	if len(list) != 1 || list[0].FeatureCount == nil || *list[0].FeatureCount != 4 {
		t.Fatalf("list: %+v", list)
	}
	imp, err := ImportMapData(d, ctx, "Import", map[string]any{
		"center": []any{52.0, 13.0}, "zoom": 9.0,
		"abschnitte": []any{map[string]any{"id": 3.0, "name": "Drei"}},
		"markers":    []any{map[string]any{"lat": 52.1, "lng": 13.1, "file": "x.svg", "label": "M"}, map[string]any{"lat": 999.0, "lng": 0.0, "file": "x.svg"}},
		"shapes":     []any{map[string]any{"type": "circle", "lat": 52.1, "lng": 13.1, "radius": 100.0}},
		"texts":      []any{map[string]any{"lat": 52.1, "lng": 13.1, "text": "T"}},
	}, 52.52, 13.405)
	if err != nil || *imp.Zoom != 9 || *imp.CenterLat != 52.0 {
		t.Fatalf("import: %v %+v", err, imp)
	}
	feats, _ := Features(d, imp.ID, false)
	abs, _ := Abschnitte(d, imp.ID, false)
	if len(feats) != 3 || len(abs) != 1 || abs[0].Nr != 3 {
		t.Fatalf("import content: %d features, %+v", len(feats), abs)
	}
	if _, err := Delete(d, ctx, imp.ID, imp.Version); err != nil {
		t.Fatal(err)
	}
	if feats, _ := Features(d, imp.ID, false); len(feats) != 0 {
		t.Fatal("delete kaskadiert nicht")
	}
}
