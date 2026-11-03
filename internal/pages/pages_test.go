package pages

import (
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/auth"
	"github.com/friedrichengel/thw_luk_software_go/internal/changelog"
	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
	"github.com/friedrichengel/thw_luk_software_go/internal/session"
	"github.com/friedrichengel/thw_luk_software_go/internal/update"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

func newTestServer(t *testing.T) (*httptest.Server, *db.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"), luk.Schema)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	cfg := &config.Config{Secret: "test-secret-0123456789abcdef0123456789", Name: "THW LuK", AssetVersion: "3.0.0.test", BaseURL: "http://localhost", CopyrightYear: "2026"}
	app := &httpx.App{
		Cfg:       cfg,
		DB:        d,
		Auth:      &auth.Auth{Cfg: cfg, DB: d},
		Sessions:  &session.Store{DB: d, Idle: time.Hour},
		Changelog: changelog.New([]byte(`{"entries":[]}`), "3.0.0"),
		Update:    update.New(cfg),
		Log:       log.Default(),
	}
	mux := http.NewServeMux()
	Register(mux, app, Deps{})
	srv := httptest.NewServer(app.Sessions.Middleware(mux))
	t.Cleanup(srv.Close)
	return srv, d
}

func newClient(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
}

var reCSRF = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func loginAs(t *testing.T, c *http.Client, base, username, password string) *http.Response {
	t.Helper()
	res, err := c.Get(base + "/login")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	m := reCSRF.FindSubmatch(body)
	if m == nil {
		t.Fatalf("kein CSRF-Token in Login-Seite: %s", body)
	}
	form := url.Values{"csrf": {string(m[1])}, "username": {username}, "password": {password}}
	res, err = c.PostForm(base+"/login", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

func TestPages(t *testing.T) {
	srv, d := newTestServer(t)
	base := srv.URL

	// ohne Login -> Umleitung zum Login mit redirect
	c := newClient(t)
	res, err := c.Get(base + "/harke?einsatz=abc")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 302 || !strings.HasPrefix(res.Header.Get("Location"), "/login?redirect=") {
		t.Fatalf("harke ohne Login: %d %s", res.StatusCode, res.Header.Get("Location"))
	}

	// healthz
	res, err = http.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || strings.TrimSpace(string(b)) != `{"ok":true}` {
		t.Fatalf("healthz: %d %s", res.StatusCode, b)
	}

	// Leiter: Login -> /einsatz
	if _, err := user.Create(d, "leiter1", "geheim1234", nil, nil, "leiter", false); err != nil {
		t.Fatal(err)
	}
	res = loginAs(t, c, base, "leiter1", "geheim1234")
	if res.StatusCode != 302 || res.Header.Get("Location") != "/einsatz" {
		t.Fatalf("login: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	res, err = c.Get(base + "/einsatz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "window.STAB") || !strings.Contains(string(b), "ez-new-btn") {
		t.Fatalf("einsatz: %d", res.StatusCode)
	}
	// Uebersicht, Startseite und Hilfsmittel
	for _, p := range []string{"/einsatz", "/", "/stein", "/steinmap", "/settings", "/view?t=x"} {
		res, err = c.Get(base + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		want := 200
		if p == "/view?t=x" {
			want = 404
		}
		if res.StatusCode != want {
			t.Fatalf("%s: %d", p, res.StatusCode)
		}
	}
	// harke ohne Einsatz -> Uebersicht
	res, _ = c.Get(base + "/harke")
	res.Body.Close()
	if res.StatusCode != 302 || res.Header.Get("Location") != "/einsatz" {
		t.Fatalf("harke leer: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	// unbekannter Einsatz -> denied
	res, _ = c.Get(base + "/harke?einsatz=0123456789ab")
	res.Body.Close()
	if res.StatusCode != 302 || res.Header.Get("Location") != "/einsatz?denied=1" {
		t.Fatalf("harke unbekannt: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	// falsches Passwort
	c2 := newClient(t)
	res = loginAs(t, c2, base, "leiter1", "falsch")
	if res.StatusCode != 200 {
		t.Fatalf("login falsch: %d", res.StatusCode)
	}

	// Admin: /admin 200, /einsatz 403
	if _, err := user.Create(d, "admin", "adminpass123", nil, nil, "admin", false); err != nil {
		t.Fatal(err)
	}
	ca := newClient(t)
	res = loginAs(t, ca, base, "admin", "adminpass123")
	if res.StatusCode != 302 || res.Header.Get("Location") != "/admin" {
		t.Fatalf("admin login: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = ca.Get(base + "/admin")
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "Serververwaltung") || !strings.Contains(string(b), "leiter1") {
		t.Fatalf("admin: %d", res.StatusCode)
	}
	// Admin mit Weiterleitung auf Dashboard/Einsatzseite (PWA-Start, Lesezeichen):
	// nicht in "Zugriff verweigert" laufen, sondern zur Serververwaltung.
	for redirect, want := range map[string]string{"/einsatz": "/admin", "/": "/admin", "/harke?einsatz=abc": "/admin", "/admin_update": "/admin_update", "/settings": "/settings"} {
		ca2 := newClient(t)
		res, _ = ca2.Get(base + "/login?redirect=" + url.QueryEscape(redirect))
		b, _ = io.ReadAll(res.Body)
		res.Body.Close()
		m := reCSRF.FindSubmatch(b)
		if m == nil {
			t.Fatal("kein CSRF-Token")
		}
		res, _ = ca2.PostForm(base+"/login", url.Values{"csrf": {string(m[1])}, "username": {"admin"}, "password": {"adminpass123"}, "redirect": {redirect}})
		res.Body.Close()
		if res.StatusCode != 302 || res.Header.Get("Location") != want {
			t.Fatalf("admin login redirect=%s: %d %s (erwartet %s)", redirect, res.StatusCode, res.Header.Get("Location"), want)
		}
	}
	res, _ = ca.Get(base + "/einsatz")
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("admin einsatz: %d", res.StatusCode)
	}
	res, _ = ca.Get(base + "/admin_update")
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "LUK_UPDATE") {
		t.Fatalf("admin_update: %d", res.StatusCode)
	}

	// Passwortwechsel-Zwang
	if _, err := user.Create(d, "neu", "neupass12345", nil, nil, "user", true); err != nil {
		t.Fatal(err)
	}
	cn := newClient(t)
	res = loginAs(t, cn, base, "neu", "neupass12345")
	res.Body.Close()
	res, _ = cn.Get(base + "/einsatz")
	res.Body.Close()
	if res.StatusCode != 302 || res.Header.Get("Location") != "/settings?forced=1&redirect=%2Feinsatz" {
		t.Fatalf("forced: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = cn.Get(base + "/settings?forced=1&redirect=%2Feinsatz")
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "no-rotate-prompt") || !strings.Contains(string(b), `name="redirect" value="/einsatz"`) {
		t.Fatalf("settings forced: %d", res.StatusCode)
	}
	// Passwort wechseln: danach direkt weiter zum urspruenglichen Ziel, und
	// die Einsatzseiten sind ohne weiteren Umweg erreichbar.
	m := reCSRF.FindSubmatch(b)
	if m == nil {
		t.Fatal("kein CSRF-Token in Einstellungen")
	}
	res, _ = cn.PostForm(base+"/settings?forced=1", url.Values{"csrf": {string(m[1])}, "action": {"change_password"},
		"current_password": {"neupass12345"}, "new_password": {"ganzneu12345"}, "new_password2": {"ganzneu12345"}, "redirect": {"/einsatz"}})
	res.Body.Close()
	if res.StatusCode != 302 || res.Header.Get("Location") != "/einsatz" {
		t.Fatalf("nach Passwortwechsel: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = cn.Get(base + "/einsatz")
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("einsatz nach Passwortwechsel: %d", res.StatusCode)
	}
	// Freiwilliger Wechsel bleibt auf der Einstellungsseite (Hinweis)
	res, _ = cn.Get(base + "/settings")
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	m = reCSRF.FindSubmatch(b)
	res, _ = cn.PostForm(base+"/settings", url.Values{"csrf": {string(m[1])}, "action": {"change_password"},
		"current_password": {"ganzneu12345"}, "new_password": {"nochneuer12345"}, "new_password2": {"nochneuer12345"}})
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "Passwort geändert.") {
		t.Fatalf("freiwilliger Wechsel: %d", res.StatusCode)
	}

	// Logout
	res, _ = c.Get(base + "/logout")
	res.Body.Close()
	res, _ = c.Get(base + "/einsatz")
	res.Body.Close()
	if res.StatusCode != 302 {
		t.Fatalf("nach logout: %d", res.StatusCode)
	}
}
