package pages

import (
	"io"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/einsatz"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

// Alle Einsatz-Seiten sowie die Formular-Aktionen von Einstellungen und
// Serververwaltung einmal rendern - faengt Vorlagenfehler zur Laufzeit.
func TestEinsatzPagesRender(t *testing.T) {
	srv, d := newTestServer(t)
	base := srv.URL
	lid, err := user.Create(d, "leiter1", "geheim1234", nil, nil, "leiter", false)
	if err != nil {
		t.Fatal(err)
	}
	p := access.Principal{UID: lid, Role: "leiter", Label: "leiter1"}
	uuid, err := einsatz.CreateEinsatz(d, p, "Hochwasser", "lv", "Stab LV", "")
	if err != nil {
		t.Fatal(err)
	}
	c := newClient(t)
	loginAs(t, c, base, "leiter1", "geheim1234")
	for _, page := range []string{"harke", "kraefteplanung", "lagekarte", "schadenskonten", "anfahrt", "log", "verwaltung", "index", "stein"} {
		res, err := c.Get(base + "/" + page + "?einsatz=" + uuid + "&neu=1&karte=abcdef12&focus=node:abcdef12&sk=x")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d %s", page, res.StatusCode, b)
		}
		if page != "stein" && !strings.Contains(string(b), "Hochwasser") {
			t.Fatalf("%s: Einsatztitel fehlt", page)
		}
		if page == "lagekarte" && !strings.Contains(string(b), `"karte":"abcdef12"`) {
			t.Fatalf("lagekarte: LAGEKARTE_CONFIG fehlt: %s", b)
		}
		if page == "lagekarte" && !strings.Contains(string(b), `id="lk-geo-input"`) {
			t.Fatalf("lagekarte: Adresssuche fehlt")
		}
	}
	// Freigabelink-Seite mit gueltigem Token
	ctx, _ := access.Resolve(d, p, uuid)
	link, err := einsatz.CreateShareLink(d, ctx, "test-secret-0123456789abcdef0123456789", base, "Nachbar", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := c.Get(base + "/view?t=" + link.Token)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "Hochwasser") {
		t.Fatalf("view: %d", res.StatusCode)
	}
	// Einstellungen: 2FA-Setup starten (QR-Block) und Profil speichern
	csrf := csrfOf(t, string(b))
	res, _ = c.PostForm(base+"/settings", url.Values{"csrf": {csrf}, "action": {"2fa_start"}})
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || strings.Contains(string(b), "qrserver") || !strings.Contains(string(b), `data-otpauth="otpauth://totp/`) || !strings.Contains(string(b), "/js/qrcode-generator.min.js") {
		t.Fatalf("settings 2fa_start: %d", res.StatusCode)
	}
	res, _ = c.PostForm(base+"/settings", url.Values{"csrf": {csrf}, "action": {"update_profile"}, "display_name": {"Leiter Eins"}, "email": {"l@example.org"}})
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "Profil aktualisiert.") {
		t.Fatalf("settings profile: %d", res.StatusCode)
	}
	u, _ := user.FindByID(d, lid)
	if u.EmailStr() != "l@example.org" || u.DisplayNameStr() != "Leiter Eins" {
		t.Fatalf("profil nicht gespeichert: %+v", u)
	}
	// Admin: Nutzer anlegen
	if _, err := user.Create(d, "admin", "adminpass123", nil, nil, "admin", false); err != nil {
		t.Fatal(err)
	}
	ca := newClient(t)
	loginAs(t, ca, base, "admin", "adminpass123")
	res, _ = ca.Get(base + "/admin")
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	csrf = csrfOf(t, string(b))
	res, _ = ca.PostForm(base+"/admin", url.Values{"csrf": {csrf}, "action": {"create_user"}, "new_username": {"m.muster"}, "new_role": {"user"}})
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "Neuer Nutzer angelegt") || !strings.Contains(string(b), "m.muster") {
		t.Fatalf("admin create_user: %d", res.StatusCode)
	}
	res, _ = ca.PostForm(base+"/admin", url.Values{"csrf": {csrf}, "action": {"delete_user"}, "uid": {itoa(lid)}})
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(b), "Ersteller von Einsätzen") {
		t.Fatalf("admin delete owner: Fehlertext fehlt")
	}
}

func csrfOf(t *testing.T, body string) string {
	t.Helper()
	m := reCSRF.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("kein CSRF-Token")
	}
	return m[1]
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
