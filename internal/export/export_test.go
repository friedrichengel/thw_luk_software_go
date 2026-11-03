package export

import (
	"encoding/json"
	"path/filepath"
	"testing"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/einsatz"
	"github.com/friedrichengel/thw_luk_software_go/internal/lagekarte"
	"github.com/friedrichengel/thw_luk_software_go/internal/schadenskonten"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

func strPtr(s string) *string { return &s }

func TestExportImport(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"), luk.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	lid, _ := user.Create(d, "leiter1", "pw", nil, strPtr("Leiter Eins"), "leiter", false)
	s1, _ := user.Create(d, "s1user", "pw", nil, strPtr("Susi S1"), "user", false)
	s2, _ := user.Create(d, "s2user", "pw", nil, strPtr("Sven S2"), "user", false)
	p := access.Principal{UID: lid, Role: "leiter", Label: "Leiter Eins"}
	u, _ := einsatz.CreateEinsatz(d, p, "Hochwasser Elbe", "lv", "Stab LV", "")
	ctx, _ := access.Resolve(d, p, u)
	if _, err := einsatz.SetMemberRoles(d, ctx, s1, []any{"s1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := einsatz.SetMemberRoles(d, ctx, s2, []any{"s2", "u_s3"}); err != nil {
		t.Fatal(err)
	}
	root, _ := einsatz.CreateNode(d, ctx, map[string]any{"type": "fuehrung", "bezeichnung": "Stab LV"})
	ea, _ := einsatz.CreateNode(d, ctx, map[string]any{"type": "fuehrung", "bezeichnung": "EA Nord", "parent_id": root.ID})
	bgr, _ := einsatz.CreateNode(d, ctx, map[string]any{"type": "einheit", "bezeichnung": "BGr 1", "parent_id": ea.ID, "kraft_key": "103:bgr"})
	fb, _ := einsatz.CreateMitglied(d, ctx, map[string]any{"node_id": root.ID, "kind": "fachberater", "funktion": "FB Strom", "name": "Meier"})
	sch, _ := einsatz.CreateSchicht(d, ctx, map[string]any{"node_id": bgr.ID, "von": "2026-10-01T08:00", "bis": "2026-10-01T20:00", "staerke": "1/3/8"})
	if _, err := einsatz.UpdateSchicht(d, ctx, sch.ID, sch.Version, map[string]any{"kraft_key": "102:bgr"}); err != nil {
		t.Fatal(err)
	}
	if _, err := einsatz.CreateSchicht(d, ctx, map[string]any{"node_id": root.ID, "mitglied_id": fb.ID, "von": "2026-10-01T08:00", "bis": "2026-10-01T20:00"}); err != nil {
		t.Fatal(err)
	}
	// Loeschung: geloeschte Zeilen bleiben im Export
	tmp, _ := einsatz.CreateNode(d, ctx, map[string]any{"type": "einheit", "bezeichnung": "Temp", "parent_id": ea.ID})
	if _, err := einsatz.DeleteNode(d, ctx, tmp.ID, tmp.Version); err != nil {
		t.Fatal(err)
	}
	karte, _ := lagekarte.Create(d, ctx, map[string]any{"title": "Lage Nord"}, 52.52, 13.405)
	if _, err := lagekarte.AbschnittCreate(d, ctx, karte.ID, "Ostseite", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := lagekarte.FeatureCreate(d, ctx, karte.ID, map[string]any{"kind": "marker", "geometry": map[string]any{"lat": 52.5, "lng": 13.4},
		"props": map[string]any{"file": "THW_Einheiten/Bergungsgruppe.svg", "label": "BGr"}, "abschnitte": []any{1.0}, "ref_kind": "node", "ref_id": bgr.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := lagekarte.FeatureCreate(d, ctx, karte.ID, map[string]any{"kind": "polygon", "geometry": map[string]any{"latlngs": []any{[]any{52.5, 13.4}, []any{52.6, 13.4}, []any{52.6, 13.5}}}, "props": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := lagekarte.FeatureCreate(d, ctx, karte.ID, map[string]any{"kind": "arrow", "geometry": map[string]any{"start": []any{52.5, 13.4}, "end": []any{52.55, 13.45}}, "props": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := lagekarte.FeatureCreate(d, ctx, karte.ID, map[string]any{"kind": "text", "geometry": map[string]any{"lat": 52.5, "lng": 13.4}, "props": map[string]any{"text": "Hallo"}}); err != nil {
		t.Fatal(err)
	}
	sk1, _ := schadenskonten.Create(d, ctx, map[string]any{"title": "Deichbruch km 12"})
	sk2, _ := schadenskonten.Create(d, ctx, map[string]any{"title": "Evakuierung Dorf"})
	if _, err := schadenskonten.RefAdd(d, ctx, sk2.ID, "node", bgr.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := schadenskonten.FremdCreate(d, ctx, sk2.ID, map[string]any{"kind": "fw", "bezeichnung": "LF 20", "staerke": "1/2/6"}); err != nil {
		t.Fatal(err)
	}
	_ = sk1
	access.LogAccess(d, ctx, "abc", "harke")

	exp, err := Export(d, ctx, "3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if exp["format"] != Format || len(exp["nodes"].([]*einsatz.Node)) != 4 || len(exp["events"].([]einsatz.LogEvent)) < 15 || len(exp["access_log"].([]einsatz.AccessEntry)) != 1 {
		t.Fatalf("export unvollstaendig: nodes=%d events=%d", len(exp["nodes"].([]*einsatz.Node)), len(exp["events"].([]einsatz.LogEvent)))
	}
	events := exp["events"].([]einsatz.LogEvent)
	if events[0].Seq > events[len(events)-1].Seq {
		t.Fatal("events nicht aelteste zuerst")
	}
	js, err := json.Marshal(exp)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(js, &data); err != nil {
		t.Fatal(err)
	}
	res, err := Import(d, p, data, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	ctxI, _ := access.Resolve(d, p, res.UUID)
	if ctxI == nil || !ctxI.IsOwner {
		t.Fatal("import angelegt")
	}
	tree, _ := einsatz.TreeOf(d, ctxI.Einsatz.ID, false)
	if len(tree.Nodes) != 3 || len(tree.Schichten) != 2 || len(tree.Mitglieder) != 1 {
		t.Fatalf("tree: %d nodes, %d schichten", len(tree.Nodes), len(tree.Schichten))
	}
	kk := 0
	for _, n := range tree.Nodes {
		if n.KraftKey == "103:bgr" {
			kk++
		}
	}
	ks := 0
	for _, s := range tree.Schichten {
		if s.KraftKey == "102:bgr" {
			ks++
		}
	}
	if kk != 1 || ks != 1 {
		t.Fatal("kraft_key nicht uebernommen")
	}
	skI, _ := schadenskonten.List(d, ctxI.Einsatz.ID, false)
	if len(skI) != 2 || len(skI[1].Refs) != 1 || len(skI[1].Fremd) != 1 {
		t.Fatalf("schadenskonten: %+v", skI)
	}
	if r, _ := einsatz.RowByID(d, "einsatz_nodes", skI[1].Refs[0].RefID); r == nil || db.Int(r, "einsatz_id") != ctxI.Einsatz.ID {
		t.Fatal("ref nicht umgeschrieben")
	}
	fI, _ := lagekarte.AllFeatures(d, ctxI.Einsatz.ID, false)
	if len(fI) != 4 {
		t.Fatalf("features: %d", len(fI))
	}
	var mk *lagekarte.Feature
	for _, f := range fI {
		if f.Kind == "marker" {
			mk = f
		}
	}
	if mk == nil || mk.RefID == nil {
		t.Fatal("marker ohne ref")
	}
	if r, _ := einsatz.RowByID(d, "einsatz_nodes", *mk.RefID); r == nil || db.Int(r, "einsatz_id") != ctxI.Einsatz.ID {
		t.Fatal("feature ref nicht umgeschrieben")
	}
	abI, _ := lagekarte.AllAbschnitte(d, ctxI.Einsatz.ID, false)
	if len(abI) != 1 {
		t.Fatalf("abschnitte: %d", len(abI))
	}
	logI, _ := einsatz.Log(d, ctxI.Einsatz.ID, 2000, 0)
	if len(logI) < 15 || !logI[len(logI)-1].Imported || logI[0].Imported || logI[0].Action != "import" {
		t.Fatalf("log: %d, first=%+v", len(logI), logI[0])
	}
	members, _ := einsatz.Members(d, ctxI.Einsatz.ID)
	if len(members) != 3 {
		t.Fatalf("members: %+v", members)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings: %+v", res.Warnings)
	}
	// Snapshot
	sid, err := Snapshot(d, ctx, "manual", "3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if js, ok, _ := SnapshotJSON(d, ctx.Einsatz.ID, sid); !ok || len(js) < 100 {
		t.Fatal("snapshot")
	}
	snaps, _ := Snapshots(d, ctx.Einsatz.ID)
	if len(snaps) != 1 || snaps[0].CreatedBy != "Leiter Eins" || snaps[0].Size < 100 {
		t.Fatalf("snapshots: %+v", snaps)
	}
	// unbekanntes Mitglied -> Warnung
	data["members"] = []any{map[string]any{"username": "gibtsnicht", "display_name": "Niemand", "roles": []any{"s1"}}}
	res2, err := Import(d, p, data, nil)
	if err != nil || len(res2.Warnings) != 1 {
		t.Fatalf("warnings: %v %+v", err, res2)
	}
	data["format"] = "x"
	if _, err := Import(d, p, data, nil); err == nil {
		t.Fatal("invalid_format erwartet")
	}
}
