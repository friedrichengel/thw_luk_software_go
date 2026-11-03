package einsatz

import (
	"path/filepath"
	"testing"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.sqlite"), luk.Schema)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestHarkeBasics(t *testing.T) {
	d := openTestDB(t)
	lid, err := user.Create(d, "leiter1", "pw", nil, strPtr("Leiter Eins"), "leiter", false)
	if err != nil {
		t.Fatal(err)
	}
	p := access.Principal{UID: lid, Role: "leiter", Label: "Leiter Eins"}
	uuidStr, err := CreateEinsatz(d, p, "Hochwasser Elbe", "lv", "Stab LV", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := access.Resolve(d, p, uuidStr)
	if err != nil || ctx == nil || !ctx.IsOwner || !ctx.Can("harke") || !ctx.CanLog {
		t.Fatalf("ctx: %v %+v", err, ctx)
	}
	root, err := CreateNode(d, ctx, map[string]any{"type": "fuehrung", "bezeichnung": "Stab LV"})
	if err != nil {
		t.Fatal(err)
	}
	bgr, err := CreateNode(d, ctx, map[string]any{"type": "einheit", "bezeichnung": "BGr 1", "parent_id": root.ID, "id": "client-id-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if bgr.ID != "client-id-0001" || bgr.ParentID == nil || *bgr.ParentID != root.ID {
		t.Fatalf("node: %+v", bgr)
	}
	if _, err := CreateNode(d, ctx, map[string]any{"type": "einheit", "id": "client-id-0001"}); !apierr.Is(err, "id_exists") {
		t.Fatalf("expected id_exists, got %v", err)
	}
	n1, err := UpdateNode(d, ctx, bgr.ID, 1, map[string]any{"bezeichnung": "BGr 1 (OV A)"}, Keys(NodeFields))
	if err != nil || n1.Version != 2 || n1.Bezeichnung != "BGr 1 (OV A)" {
		t.Fatalf("update: %v %+v", err, n1)
	}
	if _, err := UpdateNode(d, ctx, bgr.ID, 1, map[string]any{"bezeichnung": "x"}, Keys(NodeFields)); !apierr.Is(err, "conflict") {
		t.Fatalf("expected conflict, got %v", err)
	}
	if _, err := MoveNode(d, ctx, root.ID, 1, bgr.ID, "", ""); !apierr.Is(err, "cycle") {
		t.Fatalf("expected cycle, got %v", err)
	}
	if _, err := DeleteNode(d, ctx, root.ID, 1); !apierr.Is(err, "has_children") {
		t.Fatalf("expected has_children, got %v", err)
	}
	fb, err := CreateMitglied(d, ctx, map[string]any{"node_id": root.ID, "kind": "fachberater", "funktion": "FB Strom", "name": "Meier"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateMitglied(d, ctx, map[string]any{"node_id": bgr.ID, "kind": "einheit"}); !apierr.Is(err, "not_fuehrung") {
		t.Fatalf("expected not_fuehrung, got %v", err)
	}
	sch, err := CreateSchicht(d, ctx, map[string]any{"node_id": bgr.ID, "von": "2026-10-01T08:00", "bis": "2026-10-01T20:00", "staerke": "1/3/8"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateSchicht(d, ctx, map[string]any{"node_id": root.ID, "mitglied_id": fb.ID, "von": "2026-10-01T08:00", "bis": "2026-10-01T20:00"}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateSchicht(d, ctx, map[string]any{"node_id": root.ID, "von": "a", "bis": "b"}); !apierr.Is(err, "fuehrung_no_schicht") {
		t.Fatalf("expected fuehrung_no_schicht, got %v", err)
	}
	if _, err := CreateSchicht(d, ctx, map[string]any{"node_id": bgr.ID, "von": "a", "bis": "b", "staerke": "abc"}); !apierr.Is(err, "invalid_staerke") {
		t.Fatalf("expected invalid_staerke, got %v", err)
	}
	n2, err := SetNodeStatus(d, ctx, bgr.ID, n1.Version, "angefordert")
	if err != nil {
		t.Fatal(err)
	}
	an, err := Anfahrt(d, ctx.Einsatz.ID)
	if err != nil || len(an.Items) != 1 || an.Items[0].Path != "Stab LV" {
		t.Fatalf("anfahrt: %v %+v", err, an)
	}
	s2, err := SetSchichtStatus(d, ctx, sch.ID, sch.Version, "anfahrt")
	if err != nil || s2.Status != "anfahrt" || s2.Version != sch.Version+1 {
		t.Fatalf("schicht status: %v %+v", err, s2)
	}
	ev, err := EventsSince(d, ctx.Einsatz.ID, 0, 500)
	if err != nil || len(ev) < 8 {
		t.Fatalf("events: %v %d", err, len(ev))
	}
	tree, err := TreeOf(d, ctx.Einsatz.ID, false)
	if err != nil || tree.Seq != ev[len(ev)-1].Seq || len(tree.Nodes) != 2 || len(tree.Schichten) != 2 {
		t.Fatalf("tree: %v %+v", err, tree)
	}
	// Umwandlung Mitglied -> Knoten -> Mitglied
	ie, err := CreateMitglied(d, ctx, map[string]any{"node_id": root.ID, "kind": "einheit", "bezeichnung": "Stab S2"})
	if err != nil {
		t.Fatal(err)
	}
	conv, err := MitgliedToNode(d, ctx, ie.ID, 1, root.ID)
	if err != nil || conv.Node.Bezeichnung != "Stab S2" || *conv.Node.ParentID != root.ID {
		t.Fatalf("mitgliedToNode: %v %+v", err, conv)
	}
	back, err := NodeToMitglied(d, ctx, conv.Node.ID, conv.Node.Version, root.ID)
	if err != nil || back.Mitglied.Kind != "einheit" || back.Mitglied.Bezeichnung != "Stab S2" {
		t.Fatalf("nodeToMitglied: %v %+v", err, back)
	}
	_ = n2
	// Freigabelink + Verknuepfung
	link, err := CreateShareLink(d, ctx, "secret", "http://localhost", "Nachbarstab", nil)
	if err != nil || len(link.Token) != 48 {
		t.Fatalf("share: %v %+v", err, link)
	}
	if ExtractToken("https://x.example/view?t="+link.Token) != link.Token {
		t.Fatal("extractToken")
	}
	l2id, _ := user.Create(d, "leiter2", "pw", nil, strPtr("Leiter Zwei"), "leiter", false)
	p2 := access.Principal{UID: l2id, Role: "leiter", Label: "Leiter Zwei"}
	uuidB, _ := CreateEinsatz(d, p2, "Nachbar", "ov", "Stab OV", "")
	ctxB, _ := access.Resolve(d, p2, uuidB)
	if c, _ := access.Resolve(d, p2, uuidStr); c != nil {
		t.Fatal("token allein gibt keinen Zugriff")
	}
	req, err := LinkRequest(d, ctxB, "secret", link.Token, "Nachbarn")
	if err != nil || req.Status != "approved" || !req.Effective || req.EinsatzA.UUID != uuidStr {
		t.Fatalf("linkRequest: %v %+v", err, req)
	}
	if _, err := LinkRequest(d, ctxB, "secret", link.Token, ""); !apierr.Is(err, "link_exists") {
		t.Fatalf("expected link_exists, got %v", err)
	}
	cs, _ := access.Resolve(d, p2, uuidStr)
	if cs == nil || !cs.IsShare || cs.Can("harke") || cs.ViaEinsatz.UUID != uuidB {
		t.Fatalf("share ctx: %+v", cs)
	}
	shared, _ := SharedForUser(d, l2id)
	if len(shared) != 1 {
		t.Fatalf("shared: %+v", shared)
	}
	if _, err := LinkDecide(d, ctx, req.ID, false); err != nil {
		t.Fatal(err)
	}
	if c, _ := access.Resolve(d, p2, uuidStr); c != nil {
		t.Fatal("entzogen wirkt sofort")
	}
	if _, err := LinkDecide(d, ctx, req.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := RevokeShareLink(d, ctx, link.ID); err != nil {
		t.Fatal(err)
	}
	if c, _ := access.Resolve(d, p2, uuidStr); c != nil {
		t.Fatal("widerruf wirkt sofort")
	}
}

func strPtr(s string) *string { return &s }
