package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func cspFor(t *testing.T, tileOrigin string) string {
	t.Helper()
	h := SecurityHeadersWith(tileOrigin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec.Header().Get("Content-Security-Policy")
}

func directive(csp, name string) string {
	for _, part := range strings.Split(csp, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, name+" ") {
			return part
		}
	}
	return ""
}

// Die eingebetteten Bibliotheken (ExcelJS, jsPDF, dom-to-image, html2canvas,
// pdf-lib) muessen ohne eval laden; die CSP darf 'unsafe-eval' nicht
// enthalten, und ohne externe Kachel-Quelle bleibt alles bei 'self'.
func TestSecurityHeadersDefaultCSP(t *testing.T) {
	csp := cspFor(t, "")
	if strings.Contains(csp, "unsafe-eval") {
		t.Fatalf("CSP enthaelt unsafe-eval: %s", csp)
	}
	if got := directive(csp, "connect-src"); got != "connect-src 'self'" {
		t.Fatalf("connect-src = %q", got)
	}
	if got := directive(csp, "img-src"); got != "img-src 'self' data: https:" {
		t.Fatalf("img-src = %q", got)
	}
}

// Externe Kachel-Quelle: Der Karten-PDF-Export laedt die Kacheln per fetch
// nach, deshalb gehoert der Ursprung in connect-src; ueber http:// zusaetzlich
// in img-src (https ist dort ohnehin erlaubt).
func TestSecurityHeadersTileOrigin(t *testing.T) {
	csp := cspFor(t, "https://tiles.example.org")
	if got := directive(csp, "connect-src"); got != "connect-src 'self' https://tiles.example.org" {
		t.Fatalf("connect-src = %q", got)
	}
	if got := directive(csp, "img-src"); got != "img-src 'self' data: https:" {
		t.Fatalf("img-src (https) = %q", got)
	}

	csp = cspFor(t, " http://tiles.example.org ")
	if got := directive(csp, "connect-src"); got != "connect-src 'self' http://tiles.example.org" {
		t.Fatalf("connect-src (http) = %q", got)
	}
	if got := directive(csp, "img-src"); got != "img-src 'self' data: https: http://tiles.example.org" {
		t.Fatalf("img-src (http) = %q", got)
	}
}

// SafeRedirect: nur lokale Pfade; "//", "/\\" und Steuerzeichen (Tab, CR, LF)
// fuehren sonst auf fremde Domains, weil Browser die Zeichen entfernen.
func TestSafeRedirect(t *testing.T) {
	for in, want := range map[string]string{
		"/": "/", "/harke?einsatz=1": "/harke?einsatz=1", "/a b": "/a b", "": "", "x": "", "//evil.example": "", "/\\evil.example": "", "/\t/evil.example": "", "/\r\n/evil": "", "/\x00": "", "/\x7f": "", "https://evil.example": "",
	} {
		if got := SafeRedirect(in); got != want {
			t.Fatalf("SafeRedirect(%q) = %q, want %q", in, got, want)
		}
	}
}
