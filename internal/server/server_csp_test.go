package server

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

var (
	reScriptTag = regexp.MustCompile(`(?i)<script\b([^>]*)>`)
	reOnAttr    = regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*["']`)
	reNonceCSP  = regexp.MustCompile(`'nonce-([A-Za-z0-9_-]+)'`)
)

// checkCSPPage prueft eine HTML-Antwort: die CSP traegt einen Nonce, jedes
// Inline-Script (ohne src) traegt genau diesen Nonce, kein Element hat ein
// on*-Attribut - sonst bleibt die Seite unter script-src ohne
// 'unsafe-inline' still stehen.
func checkCSPPage(t *testing.T, path, csp, body string) {
	t.Helper()
	m := reNonceCSP.FindStringSubmatch(csp)
	if m == nil {
		t.Fatalf("%s: CSP ohne Script-Nonce: %s", path, csp)
	}
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("%s: script-src mit unsafe-inline", path)
	}
	nonce := m[1]
	inline := 0
	for _, sm := range reScriptTag.FindAllStringSubmatch(body, -1) {
		attrs := sm[1]
		if strings.Contains(attrs, "src=") {
			continue
		}
		inline++
		if !strings.Contains(attrs, `nonce="`+nonce+`"`) {
			t.Fatalf("%s: Inline-Script ohne Nonce: <script%s>", path, attrs)
		}
	}
	if loc := reOnAttr.FindStringIndex(body); loc != nil {
		from := loc[0]
		if from > 60 {
			from -= 60
		}
		to := loc[1] + 80
		if to > len(body) {
			to = len(body)
		}
		t.Fatalf("%s: Event-Handler-Attribut: %q", path, body[from:to])
	}
	if strings.Contains(body, "javascript:") {
		t.Fatalf("%s: javascript:-URL", path)
	}
	_ = inline
}

func TestPagesCSPNonce(t *testing.T) {
	srv, d := newTestServer(t)
	base := srv.URL
	user.Create(d, "leiter_c", "geheim1234", nil, nil, "leiter", false)
	user.Create(d, "admin", "adminpass123", nil, nil, "admin", false)

	c := newClient()
	loginAs(t, c, base, "leiter_c", "geheim1234")
	_, body, _ := getBody(t, c, base+"/einsatz")
	csrf := csrfFrom(t, body)
	st, body := postJSON(t, c, base+"/api/einsatz?action=create", map[string]any{"csrf": csrf, "title": "CSP-Test", "ebene": "ov"})
	if st != 200 {
		t.Fatalf("create: %d %s", st, body)
	}
	eu := regexp.MustCompile(`"uuid":"([0-9a-f-]{36})"`).FindStringSubmatch(body)
	if eu == nil {
		t.Fatalf("keine UUID: %s", body)
	}
	q := "?einsatz=" + eu[1]

	pages := []string{"/", "/einsatz", "/settings", "/stein", "/steinmap", "/logout",
		"/harke" + q, "/kraefteplanung" + q, "/lagekarte" + q, "/schadenskonten" + q, "/anfahrt" + q, "/log" + q, "/verwaltung" + q}
	seen := map[string]bool{}
	for _, p := range pages {
		st, body, hdr := getBody(t, c, base+p)
		if st != 200 {
			t.Fatalf("%s: %d", p, st)
		}
		csp := hdr.Get("Content-Security-Policy")
		checkCSPPage(t, p, csp, body)
		n := reNonceCSP.FindStringSubmatch(csp)[1]
		if seen[n] {
			t.Fatalf("Nonce wiederverwendet: %s", n)
		}
		seen[n] = true
	}
	// Abmelde-Seite (POST) hat ein Inline-Script
	res, err := c.PostForm(base+"/logout", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	b := cspReadBody(res)
	if res.StatusCode != 200 {
		t.Fatalf("logout: %d", res.StatusCode)
	}
	checkCSPPage(t, "POST /logout", res.Header.Get("Content-Security-Policy"), b)

	// Serververwaltung und Update-Seite als Admin, Login-Seiten ohne Sitzung
	ca := newClient()
	loginAs(t, ca, base, "admin", "adminpass123")
	for _, p := range []string{"/admin", "/admin?tab=einstellungen", "/admin?tab=zugaenge", "/admin_update"} {
		st, body, hdr := getBody(t, ca, base+p)
		if st != 200 {
			t.Fatalf("%s: %d", p, st)
		}
		checkCSPPage(t, p, hdr.Get("Content-Security-Policy"), body)
	}
	for _, p := range []string{"/login", "/forgot_password"} {
		res, err := http.Get(base + p)
		if err != nil {
			t.Fatal(err)
		}
		b := cspReadBody(res)
		checkCSPPage(t, p, res.Header.Get("Content-Security-Policy"), b)
	}
}

func cspReadBody(res *http.Response) string {
	defer res.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 32*1024)
	for {
		n, err := res.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			return sb.String()
		}
	}
}
