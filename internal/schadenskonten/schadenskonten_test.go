package schadenskonten

import (
	"path/filepath"
	"testing"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/einsatz"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

func strPtr(s string) *string { return &s }

func TestSchadenskonten(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"), luk.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	uid, _ := user.Create(d, "s2user", "pw", nil, strPtr("Sven S2"), "leiter", false)
	p := access.Principal{UID: uid, Role: "leiter", Label: "Sven S2"}
	u, _ := einsatz.CreateEinsatz(d, p, "Hochwasser Elbe", "lv", "Stab LV", "")
	ctx, _ := access.Resolve(d, p, u)
	root, _ := einsatz.CreateNode(d, ctx, map[string]any{"type": "fuehrung", "bezeichnung": "Stab LV"})
	bgr, _ := einsatz.CreateNode(d, ctx, map[string]any{"type": "einheit", "bezeichnung": "BGr 1", "parent_id": root.ID})

	sk1, err := Create(d, ctx, map[string]any{"title": "Deichbruch km 12"})
	if err != nil {
		t.Fatal(err)
	}
	sk2, err := Create(d, ctx, map[string]any{"title": "Evakuierung Dorf"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RefAdd(d, ctx, sk1.ID, "node", bgr.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := RefAdd(d, ctx, sk2.ID, "node", bgr.ID, false); !apierr.Is(err, "ref_assigned") {
		t.Fatalf("expected ref_assigned, got %v", err)
	}
	mv, err := RefAdd(d, ctx, sk2.ID, "node", bgr.ID, true)
	if err != nil || len(mv["removed_from"].([]string)) != 1 || mv["removed_from"].([]string)[0] != sk1.ID {
		t.Fatalf("force: %v %+v", err, mv)
	}
	noop, _ := RefAdd(d, ctx, sk2.ID, "node", bgr.ID, false)
	if noop["noop"] != true {
		t.Fatalf("noop: %+v", noop)
	}
	fr, err := FremdCreate(d, ctx, sk2.ID, map[string]any{"kind": "fw", "bezeichnung": "LF 20", "staerke": "1/2/6"})
	if err != nil || fr.Kind != "fw" {
		t.Fatalf("fremd: %v %+v", err, fr)
	}
	if _, err := FremdCreate(d, ctx, sk2.ID, map[string]any{"kind": "fw", "staerke": "abc"}); !apierr.Is(err, "invalid_staerke") {
		t.Fatalf("expected invalid_staerke, got %v", err)
	}
	list, err := List(d, ctx.Einsatz.ID, false)
	if err != nil || len(list) != 2 || len(list[1].Refs) != 1 || len(list[1].Fremd) != 1 || len(list[0].Refs) != 0 {
		t.Fatalf("list: %v %+v", err, list)
	}
	up, err := Update(d, ctx, sk1.ID, 1, map[string]any{"taktische_zeichen": map[string]any{"gefahren": []any{
		map[string]any{"file": "Hochwasser.svg", "label": "HW"}, map[string]any{"label": "Frei", "custom": true}, map[string]any{"file": "../x.svg", "label": "boese"},
	}}})
	if err != nil || up.Version != 2 {
		t.Fatalf("update: %v", err)
	}
	tz := up.TaktischeZeichen.(map[string]any)
	if g := tz["gefahren"].([]any); len(g) != 2 || g[1].(map[string]any)["custom"] != true {
		t.Fatalf("tz: %+v", tz)
	}
	if s := tz["schaeden"].([]any); len(s) != 0 {
		t.Fatalf("tz schaeden: %+v", s)
	}
	if err := Reorder(d, ctx, []any{sk2.ID, sk1.ID}); err != nil {
		t.Fatal(err)
	}
	list, _ = List(d, ctx.Einsatz.ID, false)
	if list[0].ID != sk2.ID {
		t.Fatalf("reorder: %+v", list)
	}
	ref, _ := RefRemove(d, ctx, sk2.ID, "node", bgr.ID)
	if ref["removed"] != true {
		t.Fatalf("refRemove: %+v", ref)
	}
	fu, err := FremdUpdate(d, ctx, fr.ID, 1, map[string]any{"bezeichnung": "LF 20/1"})
	if err != nil || fu.Bezeichnung != "LF 20/1" || fu.Version != 2 {
		t.Fatalf("fremdUpdate: %v %+v", err, fu)
	}
	if _, err := FremdDelete(d, ctx, fr.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := Delete(d, ctx, sk2.ID, 1); err != nil {
		t.Fatal(err)
	}
	list, _ = List(d, ctx.Einsatz.ID, false)
	if len(list) != 1 || list[0].ID != sk1.ID {
		t.Fatalf("after delete: %+v", list)
	}
}
