package server

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

// postLogin schickt das Login-Formular, optional mit einer Client-IP in
// X-Forwarded-For (wirkt nur mit TRUSTED_PROXIES).
func postLogin(t *testing.T, c *http.Client, base, username, password, ip string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/login", nil)
	if ip != "" {
		req.Header.Set("X-Forwarded-For", ip)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	m := reCSRF.FindSubmatch(body)
	if m == nil {
		t.Fatal("kein CSRF-Token in Login-Seite")
	}
	form := url.Values{"csrf": {string(m[1])}, "username": {username}, "password": {password}}
	req, _ = http.NewRequest(http.MethodPost, base+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if ip != "" {
		req.Header.Set("X-Forwarded-For", ip)
	}
	res, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// Mit https in APP_BASE_URL bekommt das Sitzungs-Cookie Secure, auch wenn
// kein vertrauter Proxy X-Forwarded-Proto liefert.
func TestSecureCookieWithHTTPSBaseURL(t *testing.T) {
	srv, _ := newTestServerWith(t, func(c *config.Config) { c.BaseURL = "https://luk.example" })
	res, err := http.Get(srv.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	found := false
	for _, ck := range res.Cookies() {
		if ck.Name == "luk_session" {
			found = true
			if !ck.Secure || !ck.HttpOnly {
				t.Fatalf("Sitzungs-Cookie ohne Secure/HttpOnly: %s", ck.String())
			}
		}
	}
	if !found {
		t.Fatal("kein Sitzungs-Cookie auf der Login-Seite")
	}
	// Ohne https in der Basis-URL und ohne TLS bleibt das Cookie ohne Secure
	srv2, _ := newTestServer(t)
	res, _ = http.Get(srv2.URL + "/login")
	res.Body.Close()
	for _, ck := range res.Cookies() {
		if ck.Name == "luk_session" && ck.Secure {
			t.Fatal("Secure ohne https gesetzt")
		}
	}
}

// Fehlversuche sperren ein Konto nur fuer die IP, von der sie kamen: wer nur
// den Benutzernamen kennt, kann den echten Nutzer nicht aussperren.
func TestLockoutPerAccountAndIP(t *testing.T) {
	_, lo, _ := net.ParseCIDR("127.0.0.0/8")
	srv, d := newTestServerWith(t, func(c *config.Config) { c.TrustedProxies = []*net.IPNet{lo} })
	user.Create(d, "opfer", "geheim1234", nil, nil, "leiter", false)
	attacker := newClient()
	for i := 0; i < 5; i++ {
		res := postLogin(t, attacker, srv.URL, "opfer", "falsch12345", "198.51.100.1")
		res.Body.Close()
	}
	res := postLogin(t, attacker, srv.URL, "opfer", "geheim1234", "198.51.100.1")
	res.Body.Close()
	if res.StatusCode == 302 {
		t.Fatal("gesperrte IP konnte sich anmelden")
	}
	res = postLogin(t, newClient(), srv.URL, "opfer", "geheim1234", "203.0.113.7")
	res.Body.Close()
	if res.StatusCode != 302 {
		t.Fatalf("Nutzer von anderer IP ausgesperrt: %d", res.StatusCode)
	}
}
