package server

import (
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/org"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

// Abgelehnte Webhook-Aufrufe (falsches X-Secret, unbekannter Token) sind je
// IP begrenzt; gueltige Aufrufe zaehlen nicht mit, andere IPs bleiben frei.
func TestWebhookFailureLimit(t *testing.T) {
	_, lo, _ := net.ParseCIDR("127.0.0.0/8")
	srv, d := newTestServerWith(t, func(c *config.Config) { c.TrustedProxies = []*net.IPNet{lo} })
	secret := lastServer.App.Cfg.Secret
	oid, _ := org.Create(d, "OV Hook", "", "ov")
	uid, _ := user.Create(d, "hook_admin", "geheim1234", nil, nil, "leiter", false)
	if err := org.SetSteinMode(d, oid, "webhook"); err != nil {
		t.Fatal(err)
	}
	if err := org.SetWebhookSecret(d, oid, "OV Hook", "HOOK-SECRET-1", uid, secret); err != nil {
		t.Fatal(err)
	}
	good := srv.URL + "/api/stein/webhook/" + org.WebhookToken(secret, oid)
	bad := srv.URL + "/api/stein/webhook/00000000000000000000000000000000"
	post := func(url, xsecret, ip string, n int) int {
		t.Helper()
		body := `{"meta":{"eventId":"E-` + itoa(int64(n)) + `","environmentName":"test"},"items":[]}`
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", ip)
		if xsecret != "" {
			req.Header.Set("X-Secret", xsecret)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	// Gueltige Aufrufe zaehlen nicht: STEIN sperrt sich nicht selbst aus
	for i := 0; i < 25; i++ {
		if st := post(good, "HOOK-SECRET-1", "198.51.100.1", i); st != 200 {
			t.Fatalf("gueltiger Aufruf %d: %d", i, st)
		}
	}
	if st := post(good, "falsch", "198.51.100.1", 100); st != 403 {
		t.Fatalf("falsches Secret nach gueltigen Aufrufen: %d", st)
	}
	// 20 abgelehnte Aufrufe (Secret und Token gemischt) sperren die IP
	for i := 1; i < 20; i++ {
		url, sec := good, "falsch"
		if i%2 == 0 {
			url, sec = bad, "HOOK-SECRET-1"
		}
		if st := post(url, sec, "198.51.100.1", 100+i); st != 403 && st != 404 {
			t.Fatalf("abgelehnter Aufruf %d: %d", i, st)
		}
	}
	if st := post(good, "HOOK-SECRET-1", "198.51.100.1", 300); st != 429 {
		t.Fatalf("gesperrte IP muss 429 bekommen, auch mit richtigem Secret: %d", st)
	}
	if st := post(good, "falsch", "198.51.100.1", 301); st != 429 {
		t.Fatalf("gesperrte IP mit falschem Secret: %d", st)
	}
	// Andere IP bleibt frei
	if st := post(good, "HOOK-SECRET-1", "203.0.113.7", 302); st != 200 {
		t.Fatalf("andere IP gesperrt: %d", st)
	}
}
