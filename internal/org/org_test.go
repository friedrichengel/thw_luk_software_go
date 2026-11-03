package org_test

import (
	"path/filepath"
	"strings"
	"testing"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/einsatz"
	"github.com/friedrichengel/thw_luk_software_go/internal/org"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

func openDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"), luk.Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestCrypto(t *testing.T) {
	enc, err := org.Encrypt("secret-a", "mein-schluessel-1234")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(enc, "schluessel") {
		t.Fatal("Klartext im Chiffrat")
	}
	plain, err := org.Decrypt("secret-a", enc)
	if err != nil || plain != "mein-schluessel-1234" {
		t.Fatalf("decrypt: %v %q", err, plain)
	}
	if _, err := org.Decrypt("secret-b", enc); err == nil {
		t.Fatal("falsches Secret darf nicht entschluesseln")
	}
	enc2, _ := org.Encrypt("secret-a", "mein-schluessel-1234")
	if enc2 == enc {
		t.Fatal("Nonce fehlt: gleiche Chiffrate")
	}
}

// EnsureDefault: Altbestand (Konten, Einsaetze) wird einer Standard-
// Organisation zugeordnet; ohne Einsatz-Konten entsteht keine Organisation.
func TestEnsureDefault(t *testing.T) {
	d := openDB(t)
	if err := org.EnsureDefault(d, "LV Test"); err != nil {
		t.Fatal(err)
	}
	if n, _ := org.Count(d); n != 0 {
		t.Fatalf("leere DB: %d Organisationen", n)
	}
	user.Create(d, "admin", "adminpass123", nil, nil, "admin", false)
	lid, _ := user.Create(d, "leiter1", "geheim1234", nil, nil, "leiter", false)
	uuid, err := einsatz.CreateEinsatz(d, access.Principal{UID: lid, Role: "leiter", Label: "L"}, "Alt", "ov", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := org.EnsureDefault(d, "LV Test"); err != nil {
		t.Fatal(err)
	}
	orgs, _ := org.All(d)
	if len(orgs) != 1 || orgs[0].Name != "LV Test" || orgs[0].Ebene != "lv" {
		t.Fatalf("standard-org: %+v", orgs)
	}
	l, _ := user.FindByID(d, lid)
	if l.OrgIDValue() != orgs[0].ID {
		t.Fatalf("leiter nicht zugeordnet: %+v", l.OrgID)
	}
	a, _ := user.FindByUsername(d, "admin")
	if a.OrgID != nil {
		t.Fatal("admin darf keine Organisation bekommen")
	}
	ctx, _ := access.Resolve(d, access.Principal{UID: lid, Role: "leiter"}, uuid)
	if ctx.Einsatz.OrgIDValue() != orgs[0].ID {
		t.Fatalf("einsatz nicht zugeordnet: %+v", ctx.Einsatz.OrgID)
	}
	// idempotent
	if err := org.EnsureDefault(d, "LV Test"); err != nil {
		t.Fatal(err)
	}
	if n, _ := org.Count(d); n != 1 {
		t.Fatalf("zweiter Lauf: %d Organisationen", n)
	}
	// Neuer Einsatz erbt die Organisation des Erstellers
	u2, _ := einsatz.CreateEinsatz(d, access.Principal{UID: lid, Role: "leiter", Label: "L"}, "Neu", "ov", "", "")
	ctx2, _ := access.Resolve(d, access.Principal{UID: lid, Role: "leiter"}, u2)
	if ctx2.Einsatz.OrgIDValue() != orgs[0].ID {
		t.Fatal("neuer Einsatz ohne Organisation")
	}
}

// Uebernahme aus den Stammdaten: Auswahl, Erkennung der Standard-Organisation
// als LV, Namens-Zuordnung, Auslassen externer Eintraege, Idempotenz.
func TestSeedFromStamm(t *testing.T) {
	d := openDB(t)
	lv := map[string]any{"key": "muster", "name": "LV Musterland", "kurz": "LV MU"}
	regionen := []map[string]any{
		{"key": "rst_nord", "name": "Regionalstelle Nordstadt", "kurz": "RSt Nordstadt"},
		{"key": "rst_fremd", "name": "Regionalstelle Fremd", "extern": true},
	}
	ovs := []map[string]any{
		{"key": "101", "name": "Nordstadt", "region": "rst_nord"},
		{"key": "102", "name": "Musterhausen", "region": "rst_nord"},
		{"key": "999", "name": "Nachbar", "region": "rst_fremd", "extern": true},
	}
	// Standard-Organisation wie EnsureDefault sie anlegt (frei, Ebene lv)
	std, _ := org.Create(d, "THW LV Musterland", "", "lv")
	// nur ein OV auswaehlen
	res, err := org.SeedFromStamm(d, lv, regionen, ovs, map[string]bool{org.StammKeyOV("101"): true})
	if err != nil || res.Created != 1 || res.Existing != 0 {
		t.Fatalf("auswahl: %+v %v", res, err)
	}
	all, _ := org.All(d)
	if len(all) != 2 {
		t.Fatalf("nach Auswahl %d Organisationen", len(all))
	}
	// alles anlegen: LV wird auf die Standard-Organisation gemappt, extern ausgelassen
	res, err = org.SeedFromStamm(d, lv, regionen, ovs, nil)
	if err != nil || res.Created != 2 || res.Existing != 2 {
		t.Fatalf("alle: %+v %v", res, err)
	}
	stdOrg, _ := org.FindByID(d, std)
	if stdOrg.StammKey != org.StammKeyLV("muster") || stdOrg.Name != "THW LV Musterland" {
		t.Fatalf("standard-org nicht als LV erkannt: %+v", stdOrg)
	}
	all, _ = org.All(d)
	if len(all) != 4 {
		t.Fatalf("erwartet 4 Organisationen (LV, RSt, 2 OV), got %d", len(all))
	}
	for _, o := range all {
		if o.Name == "Regionalstelle Fremd" || o.Name == "OV Nachbar" {
			t.Fatal("externe Eintraege duerfen nicht angelegt werden")
		}
	}
	// idempotent
	res, _ = org.SeedFromStamm(d, lv, regionen, ovs, nil)
	if res.Created != 0 || res.Existing != 4 {
		t.Fatalf("zweiter Lauf: %+v", res)
	}
}

func TestCredentials(t *testing.T) {
	d := openDB(t)
	uid, _ := user.Create(d, "oa", "geheim1234", nil, nil, "leiter", false)
	oid, err := org.Create(d, "OV A", "A", "ov")
	if err != nil {
		t.Fatal(err)
	}
	user.SetOrg(d, uid, &oid)
	user.SetOrgAdmin(d, uid, true)
	u, _ := user.FindByID(d, uid)
	if !u.IsOrgAdmin() {
		t.Fatal("org-admin flag")
	}
	if org.HasCredential(d, oid, org.KindStein) {
		t.Fatal("noch kein Zugang")
	}
	if err := org.SetCredential(d, oid, org.KindStein, "stein-token-abcdef1234", uid, "s"); err != nil {
		t.Fatal(err)
	}
	if err := org.SetCredential(d, oid, "bogus", "x", uid, "s"); err == nil {
		t.Fatal("unbekannte Art akzeptiert")
	}
	v, err := org.Credential(d, oid, org.KindStein, "s")
	if err != nil || v != "stein-token-abcdef1234" {
		t.Fatalf("credential: %v %q", err, v)
	}
	if v, _ := org.Credential(d, oid, org.KindDiveraAccess, "s"); v != "" {
		t.Fatal("nicht hinterlegter Zugang muss leer sein")
	}
	infos, _ := org.CredentialInfos(d, oid)
	if len(infos) != 3 || !infos[0].Set || infos[0].Hint != "…1234" || infos[0].SetByLabel != "oa" || infos[1].Set {
		t.Fatalf("infos: %+v", infos)
	}
	// Ersetzen
	org.SetCredential(d, oid, org.KindStein, "neuer-token-9999", uid, "s")
	if v, _ := org.Credential(d, oid, org.KindStein, "s"); v != "neuer-token-9999" {
		t.Fatalf("ersetzen: %q", v)
	}
	org.DeleteCredential(d, oid, org.KindStein)
	if org.HasCredential(d, oid, org.KindStein) {
		t.Fatal("loeschen wirkungslos")
	}
	// Webhook-Secrets je Ortsverband in eigener Tabelle
	if err := org.SetWebhookSecret(d, oid, "OV Nord", "hook-secret-nord-1", uid, "s"); err != nil {
		t.Fatal(err)
	}
	org.SetWebhookSecret(d, oid, "OV Sued", "hook-secret-sued-1", uid, "s")
	org.SetWebhookSecret(d, oid, "ov nord", "hook-secret-nord-2", uid, "s") // Bezeichnung ohne Gross/Klein: ersetzt
	secrets, _ := org.WebhookSecrets(d, oid, "s")
	if len(secrets) != 2 || secrets["OV Nord"] != "hook-secret-nord-2" || secrets["OV Sued"] != "hook-secret-sued-1" {
		t.Fatalf("webhook secrets: %+v", secrets)
	}
	whInfos, _ := org.WebhookSecretInfos(d, oid)
	if len(whInfos) != 2 || whInfos[0].Label != "OV Nord" || whInfos[0].Hint != "…rd-2" {
		t.Fatalf("webhook infos: %+v", whInfos)
	}
	org.DeleteWebhookSecret(d, oid, "OV Sued")
	if secrets, _ = org.WebhookSecrets(d, oid, "s"); len(secrets) != 1 {
		t.Fatal("webhook secret nicht geloescht")
	}
	if org.KindExists("stein_webhook:x") {
		t.Fatal("Webhook-Secrets sind keine Zugangsart mehr")
	}
	// Loeschen der Organisation raeumt Zuordnung und Zugaenge ab
	org.SetCredential(d, oid, org.KindDiveraAccess, "divera-key-12345678", uid, "s")
	if err := org.Delete(d, oid); err != nil {
		t.Fatal(err)
	}
	u, _ = user.FindByID(d, uid)
	if u.OrgID != nil || org.HasCredential(d, oid, org.KindDiveraAccess) {
		t.Fatal("ON DELETE SET NULL / CASCADE fehlt")
	}
}
