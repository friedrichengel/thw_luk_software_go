package server

import (
	"net/url"
	"testing"

	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

// Sitzungen enden, wenn sich die Zugangsdaten oder die Rolle eines Kontos
// aendern: Passwortwechsel (eigene Sitzung bleibt, alle anderen enden),
// Passwort-Reset und Rollenwechsel durch den Admin, Loeschung. Eine
// Rollenaenderung direkt in der Datenbank wirkt ohne neuen Login.
func TestSessionsEndOnCredentialChange(t *testing.T) {
	srv, d := newTestServer(t)
	base := srv.URL
	uid, _ := user.Create(d, "leiter_s", "geheim1234", nil, nil, "leiter", false)
	user.Create(d, "admin", "adminpass123", nil, nil, "admin", false)
	api := base + "/api/einsatz?action=list"
	users := base + "/api/einsatz?action=users"

	a, b := newClient(), newClient()
	loginAs(t, a, base, "leiter_s", "geheim1234")
	loginAs(t, b, base, "leiter_s", "geheim1234")
	if st, _, _ := getBody(t, b, api); st != 200 {
		t.Fatalf("zweite Sitzung vor Wechsel: %d", st)
	}

	// Eigener Passwortwechsel: a bleibt angemeldet, b nicht
	_, body, _ := getBody(t, a, base+"/settings")
	csrf := csrfFrom(t, body)
	postForm(t, a, base+"/settings", url.Values{"csrf": {csrf}, "action": {"change_password"},
		"current_password": {"geheim1234"}, "new_password": {"ganzneu12345"}, "new_password2": {"ganzneu12345"}})
	if st, _, _ := getBody(t, a, api); st != 200 {
		t.Fatalf("eigene Sitzung nach Passwortwechsel: %d", st)
	}
	if st, _, _ := getBody(t, b, api); st != 401 {
		t.Fatalf("fremde Sitzung nach Passwortwechsel muss enden: %d", st)
	}

	// Rolle direkt in der Datenbank herabgestuft: wirkt beim naechsten Request
	if st, _, _ := getBody(t, a, users); st != 200 {
		t.Fatalf("users als Leiter: %d", st)
	}
	user.SetRole(d, uid, "user")
	if st, _, _ := getBody(t, a, users); st != 403 {
		t.Fatalf("users nach Herabstufung muss 403 sein: %d", st)
	}
	user.SetRole(d, uid, "leiter")

	// Admin: Passwort-Reset beendet die Sitzung
	ca := newClient()
	loginAs(t, ca, base, "admin", "adminpass123")
	_, body, _ = getBody(t, ca, base+"/admin")
	acsrf := csrfFrom(t, body)
	postForm(t, ca, base+"/admin", url.Values{"csrf": {acsrf}, "action": {"reset_password"}, "uid": {itoa(uid)}})
	if st, _, _ := getBody(t, a, api); st != 401 {
		t.Fatalf("Sitzung nach Admin-Passwort-Reset muss enden: %d", st)
	}

	// Admin: Rollenwechsel beendet die Sitzung
	user.UpdatePassword(d, uid, "geheim1234", true)
	loginAs(t, a, base, "leiter_s", "geheim1234")
	if st, _, _ := getBody(t, a, api); st != 200 {
		t.Fatalf("neu angemeldet: %d", st)
	}
	postForm(t, ca, base+"/admin", url.Values{"csrf": {acsrf}, "action": {"set_role"}, "uid": {itoa(uid)}, "role": {"user"}})
	if st, _, _ := getBody(t, a, api); st != 401 {
		t.Fatalf("Sitzung nach Rollenwechsel muss enden: %d", st)
	}

	// Konto geloescht: Sitzung endet beim naechsten Request
	loginAs(t, a, base, "leiter_s", "geheim1234")
	user.Delete(d, uid)
	if st, _, _ := getBody(t, a, api); st != 401 {
		t.Fatalf("Sitzung nach Loeschung muss enden: %d", st)
	}
}
