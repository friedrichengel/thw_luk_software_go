package server

import (
	"bufio"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/friedrichengel/thw_luk_software_go/internal/settings"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

// fakeSMTP: minimaler SMTP-Server ohne TLS mit AUTH PLAIN, merkt sich die
// letzte Nachricht und den Login.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	auth string
	data string
	from string
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := func(s string) { c.Write([]byte(s + "\r\n")) }
	w("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			w("250-fake")
			w("250 AUTH PLAIN LOGIN")
		case strings.HasPrefix(up, "AUTH"):
			f.mu.Lock()
			f.auth = line
			f.mu.Unlock()
			w("235 ok")
		case strings.HasPrefix(up, "MAIL FROM"):
			f.mu.Lock()
			f.from = line
			f.mu.Unlock()
			w("250 ok")
		case strings.HasPrefix(up, "RCPT TO"):
			w("250 ok")
		case up == "DATA":
			w("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || strings.TrimRight(l, "\r\n") == "." {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			w("250 queued")
		case up == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func (f *fakeSMTP) port() string {
	_, p, _ := net.SplitHostPort(f.ln.Addr().String())
	return p
}

// Serververwaltung > Server: Mail-Einstellungen testen, speichern (Passwort
// verschluesselt), anzeigen; der Versand nutzt die gespeicherten Werte.
func TestAdminMailSettings(t *testing.T) {
	srv, d := newTestServer(t)
	base := srv.URL
	smtp := startFakeSMTP(t)
	mailAddr := "admin@example.org"
	user.Create(d, "admin", "adminpass123", &mailAddr, nil, "admin", false)

	c := newClient()
	loginAs(t, c, base, "admin", "adminpass123")
	st, body, _ := getBody(t, c, base+"/admin?tab=server")
	if st != 200 || !strings.Contains(body, "E-Mail-Versand") || !strings.Contains(body, "lokales sendmail") {
		t.Fatalf("server-tab: %d", st)
	}
	csrf := csrfFrom(t, body)
	form := func(action string, extra url.Values) url.Values {
		v := url.Values{"csrf": {csrf}, "action": {action}, "tab": {"server"},
			"mail_from": {"stab@example.org"}, "mail_from_name": {"Stab Test"},
			"smtp_host": {"127.0.0.1"}, "smtp_port": {smtp.port()}, "smtp_security": {"none"},
			"smtp_user": {"login"}, "smtp_pass": {"geheim"}, "mail_test_to": {"ziel@example.org"}}
		for k, vals := range extra {
			v[k] = vals
		}
		return v
	}

	// Testmail mit den Formularwerten, ohne zu speichern
	_, body = postForm(t, c, base+"/admin", form("test_mail", nil))
	if !strings.Contains(body, "wurde vom Mailserver angenommen") {
		t.Fatalf("Testmail: %s", snippet(body, "Testmail"))
	}
	smtp.mu.Lock()
	auth, from, data := smtp.auth, smtp.from, smtp.data
	smtp.mu.Unlock()
	if !strings.HasPrefix(strings.ToUpper(auth), "AUTH PLAIN") || !strings.Contains(from, "stab@example.org") || !strings.Contains(data, "Subject: Testmail") {
		t.Fatalf("Mock: auth=%q from=%q data=%q", auth, from, data)
	}
	if settings.Get(d, settings.SMTPHost) != "" {
		t.Fatal("Testmail darf nicht speichern")
	}

	// Ungueltige Eingaben
	_, body = postForm(t, c, base+"/admin", form("set_mail", url.Values{"smtp_port": {"99999"}}))
	if !strings.Contains(body, "SMTP-Port") || settings.Get(d, settings.SMTPHost) != "" {
		t.Fatal("ungueltiger Port nicht abgelehnt")
	}
	_, body = postForm(t, c, base+"/admin", form("set_mail", url.Values{"mail_from": {"kein-at"}}))
	if !strings.Contains(body, "Absenderadresse ungültig") {
		t.Fatal("ungueltiger Absender nicht abgelehnt")
	}

	// Speichern: Passwort verschluesselt, Anzeige ohne Passwort
	_, body = postForm(t, c, base+"/admin", form("set_mail", nil))
	if !strings.Contains(body, "Mail-Einstellungen gespeichert") || !strings.Contains(body, "SMTP 127.0.0.1:"+smtp.port()) || !strings.Contains(body, "Anmeldung als login") {
		t.Fatalf("set_mail: %s", snippet(body, "Mail-Einstellungen"))
	}
	if raw := settings.Get(d, settings.SMTPPass); raw == "" || strings.Contains(raw, "geheim") {
		t.Fatalf("Passwort im Klartext oder fehlt: %q", raw)
	}
	if strings.Contains(body, "geheim") || !strings.Contains(body, "gespeichert, leer lassen zum Behalten") {
		t.Fatal("Passwort erscheint auf der Seite oder Hinweis fehlt")
	}

	// Passwort-vergessen-Mail laeuft ueber die gespeicherten Einstellungen
	smtp.mu.Lock()
	smtp.data = ""
	smtp.mu.Unlock()
	visitor := newClient()
	_, fbody, _ := getBody(t, visitor, base+"/forgot_password")
	postForm(t, visitor, base+"/forgot_password", url.Values{"csrf": {csrfFrom(t, fbody)}, "email": {mailAddr}})
	smtp.mu.Lock()
	data = smtp.data
	smtp.mu.Unlock()
	if !strings.Contains(data, "From: ") || !strings.Contains(data, "stab@example.org") {
		t.Fatalf("Reset-Mail nicht ueber die gespeicherten Einstellungen: %q", data)
	}

	// Passwort entfernen, Felder leeren -> Umgebung gilt wieder
	_, body = postForm(t, c, base+"/admin", form("set_mail", url.Values{"smtp_pass": {""}, "smtp_pass_clear": {"1"}, "smtp_host": {""}}))
	if !strings.Contains(body, "lokales sendmail") || settings.Get(d, settings.SMTPPass) != "" {
		t.Fatal("Zuruecknahme")
	}

	// Org-Admin / Nutzer duerfen nicht
	if _, b := postForm(t, c, base+"/admin", form("set_mail", url.Values{"csrf": {"falsch"}})); strings.Contains(b, "Mail-Einstellungen gespeichert") {
		t.Fatal("ohne CSRF gespeichert")
	}
}

func snippet(body, around string) string {
	i := strings.Index(body, around)
	if i < 0 {
		if len(body) > 300 {
			return body[:300]
		}
		return body
	}
	j := i + 300
	if j > len(body) {
		j = len(body)
	}
	return body[i:j]
}
