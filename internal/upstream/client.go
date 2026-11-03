package upstream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// MaxBytes begrenzt Upstream-Antworten (JSON/XML); Downloads (PDF) duerfen mehr.
const (
	MaxBytes         = 20 * 1024 * 1024
	MaxDownloadBytes = 25 * 1024 * 1024
)

// Client ist der gemeinsame HTTP-Client fuer alle externen Dienste: kurze
// Verbindungs-, moderate Antwortzeiten, keine Weiterleitungen (ein Upstream
// darf uns nicht auf fremde Hosts schicken).
var Client = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		ResponseHeaderTimeout: 20 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		MaxIdleConnsPerHost:   4,
		ForceAttemptHTTP2:     true,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// StatusError: der Upstream hat mit einem Fehler- oder Weiterleitungsstatus
// geantwortet (Weiterleitungen werden nicht automatisch verfolgt; Location
// steht fuer Aufrufer bereit, die gezielt folgen wollen).
type StatusError struct {
	Status   int
	Body     []byte
	Location string
}

func (e *StatusError) Error() string { return fmt.Sprintf("upstream status %d", e.Status) }

// Request beschreibt einen Upstream-Abruf.
type Request struct {
	Method   string
	URL      string
	Headers  map[string]string
	Body     []byte
	MaxBytes int64
	// PublicOnly: nur oeffentliche Ziele (kein localhost, keine privaten oder
	// link-lokalen Netze), geprueft nach der Namensaufloesung. Fuer Adressen,
	// die nicht der Betreiber, sondern eine Organisation vorgibt (SSRF).
	PublicOnly bool
}

// Do fuehrt den Abruf aus und liefert Body, Content-Type und Status. Ein
// Nicht-2xx-Status ist ein *StatusError (Body enthalten, damit Aufrufer wie
// Divera die Upstream-Antwort durchreichen koennen).
func Do(ctx context.Context, rq Request) ([]byte, string, int, error) {
	body, hdr, status, err := DoHeaders(ctx, rq)
	return body, hdr.Get("Content-Type"), status, err
}

// DoHeaders ist Do mit den vollstaendigen Antwort-Headern (fuer bedingte
// Abrufe mit ETag/Last-Modified; ein 304 kommt als *StatusError mit Status 304).
func DoHeaders(ctx context.Context, rq Request) ([]byte, http.Header, int, error) {
	method := rq.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if rq.Body != nil {
		body = bytes.NewReader(rq.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rq.URL, body)
	if err != nil {
		return nil, http.Header{}, 0, err
	}
	req.Header.Set("User-Agent", "LuK-Stabsansicht/1.0")
	if _, ok := rq.Headers["Accept"]; !ok {
		req.Header.Set("Accept", "application/json")
	}
	for k, v := range rq.Headers {
		req.Header.Set(k, v)
	}
	cl := Client
	if rq.PublicOnly {
		cl = publicClient
	}
	resp, err := cl.Do(req)
	if err != nil {
		if errors.Is(err, ErrNotAllowed) {
			return nil, http.Header{}, 0, ErrNotAllowed
		}
		return nil, http.Header{}, 0, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	defer resp.Body.Close()
	max := rq.MaxBytes
	if max <= 0 {
		max = MaxBytes
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, resp.Header, resp.StatusCode, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	if int64(len(raw)) > max {
		return nil, resp.Header, resp.StatusCode, fmt.Errorf("%w: Antwort zu gross", ErrUpstream)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return raw, resp.Header, resp.StatusCode, &StatusError{Status: resp.StatusCode, Body: raw, Location: resp.Header.Get("Location")}
	}
	return raw, resp.Header, resp.StatusCode, nil
}

// GetJSON holt eine JSON-Antwort (nur 2xx).
func GetJSON(ctx context.Context, url string, headers map[string]string) ([]byte, string, error) {
	body, ctype, _, err := Do(ctx, Request{URL: url, Headers: headers})
	if err != nil {
		return nil, "", err
	}
	if ctype == "" {
		ctype = "application/json; charset=utf-8"
	}
	return body, ctype, nil
}

// publicClient ist Client mit einem Dialer, der nur oeffentliche Adressen
// ansteuert (Request.PublicOnly).
var publicClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		DialContext:           publicDial,
		ResponseHeaderTimeout: 20 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		MaxIdleConnsPerHost:   4,
		ForceAttemptHTTP2:     true,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// PrivateHost: Hostname oder IP-Literal, das auf das eigene System oder ein
// internes Netz zeigt (localhost, Loopback, private und link-lokale Bereiche,
// CGNAT, unspezifiziert, Multicast). Von Organisationen eingetragene
// Adressen duerfen solche Ziele nicht ansteuern.
func PrivateHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if h == "" || h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil {
		return privateIP(ip)
	}
	return false
}

func privateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// 0.0.0.0/8, CGNAT 100.64.0.0/10, Broadcast
		return v4[0] == 0 || (v4[0] == 100 && v4[1]&0xc0 == 64) || v4.Equal(net.IPv4bcast)
	}
	return false
}

// publicDial loest den Host auf und verbindet nur zu oeffentlichen Adressen;
// ein DNS-Name, der auf 127.0.0.1 oder ein privates Netz zeigt, laeuft so
// ins Leere (ErrNotAllowed).
func publicDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if PrivateHost(host) {
		return nil, ErrNotAllowed
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	var last error = ErrNotAllowed
	for _, ip := range ips {
		if privateIP(ip.IP) {
			continue
		}
		c, err := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}
