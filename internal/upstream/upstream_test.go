package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/config"
)

// Cache: TTL, Deduplizierung paralleler Abrufe, Stale-Fallback bei Fehlern.
func TestCache(t *testing.T) {
	now := time.Unix(1000, 0)
	c := &Cache{now: func() time.Time { return now }}
	var calls int32
	fetch := func() ([]byte, string, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(20 * time.Millisecond)
		return []byte(`{"a":1}`), "application/json", nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := c.Get("k", time.Minute, time.Hour, fetch)
			if err != nil || string(res.Body) != `{"a":1}` || res.Stale {
				t.Errorf("get: %v %s %v", err, res.Body, res.Stale)
			}
		}()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("parallele Abrufe nicht dedupliziert: %d", calls)
	}
	// innerhalb TTL: kein weiterer Abruf
	c.Get("k", time.Minute, time.Hour, fetch)
	if calls != 1 {
		t.Fatalf("TTL nicht beachtet: %d", calls)
	}
	// nach TTL: Abruf schlaegt fehl -> alter Eintrag mit Stale
	now = now.Add(2 * time.Minute)
	failing := func() ([]byte, string, error) { return nil, "", errors.New("down") }
	res, err := c.Get("k", time.Minute, time.Hour, failing)
	if err != nil || !res.Stale || string(res.Body) != `{"a":1}` {
		t.Fatalf("stale fallback: %v %+v", err, res)
	}
	// nach Stale-Frist: Fehler
	now = now.Add(2 * time.Hour)
	if _, err := c.Get("k", time.Minute, time.Hour, failing); err == nil {
		t.Fatal("erwartet Fehler nach Stale-Frist")
	}
	// Forget mit Prefix
	c.Get("stein:org1:a", time.Minute, time.Hour, fetch)
	c.Forget("stein:org1:")
	if _, ok := c.Peek("stein:org1:a"); ok {
		t.Fatal("Forget wirkungslos")
	}
}

// Fehlersperre: nach Ablehnung (403) wird bis zum Ablauf der Pause nicht erneut
// abgerufen; 429 pausiert eine Stunde; Netzfehler kurz.
func TestCacheCooldown(t *testing.T) {
	now := time.Unix(5000, 0)
	c := &Cache{now: func() time.Time { return now }}
	var calls int32
	reject := func(status int) func() ([]byte, string, error) {
		return func() ([]byte, string, error) {
			atomic.AddInt32(&calls, 1)
			return nil, "", &StatusError{Status: status}
		}
	}
	_, err := c.Get("k", time.Minute, time.Hour, reject(403))
	var ae *AuthError
	if !errors.As(err, &ae) || ae.Status != 403 {
		t.Fatalf("erwartet AuthError, got %v", err)
	}
	for i := 0; i < 5; i++ {
		c.Get("k", time.Minute, time.Hour, reject(403))
	}
	if calls != 1 {
		t.Fatalf("abgelehnter Zugang %d-mal probiert", calls)
	}
	now = now.Add(AuthCooldown + time.Second)
	c.Get("k", time.Minute, time.Hour, reject(403))
	if calls != 2 {
		t.Fatalf("nach Pause kein neuer Versuch: %d", calls)
	}
	// 429: eine Stunde Pause
	calls = 0
	c.Get("r", time.Minute, time.Hour, reject(429))
	now = now.Add(AuthCooldown + time.Minute)
	c.Get("r", time.Minute, time.Hour, reject(429))
	if calls != 1 {
		t.Fatalf("429 nach 31 min erneut probiert: %d", calls)
	}
	now = now.Add(RateLimitCooldown)
	c.Get("r", time.Minute, time.Hour, reject(429))
	if calls != 2 {
		t.Fatalf("429 nach einer Stunde nicht erneut probiert: %d", calls)
	}
	// Netzfehler: kurze Pause, keine AuthError
	calls = 0
	down := func() ([]byte, string, error) { atomic.AddInt32(&calls, 1); return nil, "", errors.New("down") }
	_, err = c.Get("n", time.Minute, time.Hour, down)
	if errors.As(err, &ae) {
		t.Fatal("Netzfehler darf kein AuthError sein")
	}
	c.Get("n", time.Minute, time.Hour, down)
	now = now.Add(FailCooldown + time.Second)
	c.Get("n", time.Minute, time.Hour, down)
	if calls != 2 {
		t.Fatalf("Netzfehler-Pause: %d Versuche, erwartet 2", calls)
	}
}

const rssSample = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:ercc="http://example.org/ercc">
<channel><title>Testfeed</title><link>https://example.org/</link><description>Beschreibung &amp; mehr</description>
<image><url>https://example.org/logo.png</url></image>
<item><title>Erste &lt;b&gt;Meldung&lt;/b&gt;</title><link>https://example.org/1</link><pubDate>Mon, 20 Oct 2026 10:00:00 +0200</pubDate>
<guid>g1</guid><dc:creator>Autorin</dc:creator><description>&lt;p&gt;Text&amp;nbsp;eins&lt;/p&gt;</description>
<content:encoded>&lt;p&gt;Voller Text&lt;/p&gt;</content:encoded><enclosure url="https://example.org/1.jpg" type="image/jpeg"/>
<category>Politik</category><category>Berlin</category></item>
<item><title>Zweite</title><link>https://example.org/2</link><ercc:pubDate>2026-10-21T08:00:00Z</ercc:pubDate><description>Zwei</description></item>
</channel></rss>`

const atomSample = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom"><title>Atomfeed</title><link href="https://example.org/atom"/><subtitle>Sub</subtitle>
<entry><title>Eintrag</title><link rel="alternate" href="https://example.org/e1"/><id>urn:1</id><published>2026-10-22T09:00:00Z</published>
<author><name>A. Autor</name></author><summary>Kurz</summary><content type="html">&lt;p&gt;Lang&lt;/p&gt;</content><category term="Sport"/></entry></feed>`

func TestParseFeed(t *testing.T) {
	out, err := ParseFeed([]byte(rssSample), "https://example.org/feed")
	if err != nil {
		t.Fatal(err)
	}
	feed := out["feed"].(map[string]any)
	items := out["items"].([]map[string]any)
	if out["status"] != "ok" || feed["title"] != "Testfeed" || feed["image"] != "https://example.org/logo.png" || feed["description"] != "Beschreibung & mehr" {
		t.Fatalf("feed: %+v", feed)
	}
	if len(items) != 2 {
		t.Fatalf("items: %d", len(items))
	}
	it := items[0]
	if it["title"] != "Erste Meldung" || it["pubDate"] != "Mon, 20 Oct 2026 10:00:00 +0200" || it["guid"] != "g1" || it["author"] != "Autorin" ||
		it["description"] != "Text eins" || it["content"] != "<p>Voller Text</p>" || it["thumbnail"] != "https://example.org/1.jpg" {
		t.Fatalf("item1: %+v", it)
	}
	if cats := it["categories"].([]string); len(cats) != 2 || cats[0] != "Politik" {
		t.Fatalf("categories: %v", cats)
	}
	if items[1]["pubDate"] != "2026-10-21T08:00:00Z" || items[1]["guid"] != "https://example.org/2" {
		t.Fatalf("item2 (ercc:pubDate / guid-Fallback): %+v", items[1])
	}

	out, err = ParseFeed([]byte(atomSample), "u")
	if err != nil {
		t.Fatal(err)
	}
	feed = out["feed"].(map[string]any)
	items = out["items"].([]map[string]any)
	if feed["title"] != "Atomfeed" || feed["link"] != "https://example.org/atom" || len(items) != 1 {
		t.Fatalf("atom: %+v %d", feed, len(items))
	}
	it = items[0]
	if it["link"] != "https://example.org/e1" || it["pubDate"] != "2026-10-22T09:00:00Z" || it["author"] != "A. Autor" || it["description"] != "Kurz" || it["content"] != "<p>Lang</p>" {
		t.Fatalf("atom item: %+v", it)
	}
	if _, err := ParseFeed([]byte("kein xml"), "u"); err == nil {
		t.Fatal("erwartet Fehler bei Nicht-XML")
	}
}

// Waldbrandkarte: nach Ablauf der Stunde bedingt nachfragen; 304 behaelt das
// Bild, ein neues Bild (anderer ETag) ersetzt es.
func TestWaldbrandConditional(t *testing.T) {
	png := func(tag string) []byte {
		return append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte(tag)...)
	}
	current := "v1"
	var hits, conditional int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("If-None-Match") != "" {
			atomic.AddInt32(&conditional, 1)
			if r.Header.Get("If-None-Match") == `"`+current+`"` {
				w.WriteHeader(304)
				return
			}
		}
		w.Header().Set("ETag", `"`+current+`"`)
		w.Header().Set("Last-Modified", "Sun, 25 Oct 2026 04:20:45 GMT")
		w.Header().Set("Content-Type", "image/png")
		w.Write(png(current))
	}))
	defer up.Close()
	now := time.Unix(9000, 0)
	c := &Cache{now: func() time.Time { return now }}
	f := &Feeds{Cfg: &config.Config{WaldbrandURL: up.URL + "/wbx.png"}, Cache: c}
	ctx := context.Background()
	res, err := f.Waldbrand(ctx)
	if err != nil || !strings.HasSuffix(string(res.Body), "v1") || f.WaldbrandLastModified() == "" {
		t.Fatalf("erster Abruf: %v %q", err, res.Body)
	}
	// innerhalb der Stunde: kein Abruf
	f.Waldbrand(ctx)
	if hits != 1 {
		t.Fatalf("innerhalb TTL erneut abgerufen: %d", hits)
	}
	// nach der Stunde: bedingte Anfrage, 304 -> altes Bild bleibt
	now = now.Add(61 * time.Minute)
	res, err = f.Waldbrand(ctx)
	if err != nil || !strings.HasSuffix(string(res.Body), "v1") || conditional != 1 || hits != 2 {
		t.Fatalf("304: %v %q cond=%d hits=%d", err, res.Body, conditional, hits)
	}
	// DWD hat ein neues Bild: naechste Stunde liefert es
	current = "v2"
	now = now.Add(61 * time.Minute)
	res, err = f.Waldbrand(ctx)
	if err != nil || !strings.HasSuffix(string(res.Body), "v2") {
		t.Fatalf("neues Bild: %v %q", err, res.Body)
	}
}

// Feeds: Host-Allowlist fuer RSS, JSON-Durchreichung und Cache-Header-Daten.
func TestFeeds(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/mowas/mapData.json"):
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]any{{"id": "mow.1"}})
		case strings.HasSuffix(r.URL.Path, "/feed.xml"):
			w.Header().Set("Content-Type", "application/rss+xml")
			w.Write([]byte(rssSample))
		case strings.HasSuffix(r.URL.Path, "/alt.xml"):
			// Sender hat den Feed verschoben: Weiterleitung auf erlaubten Host
			http.Redirect(w, r, "/feed.xml", http.StatusMovedPermanently)
		case strings.HasSuffix(r.URL.Path, "/fremd.xml"):
			http.Redirect(w, r, "https://evil.example/feed.xml", http.StatusFound)
		case strings.HasSuffix(r.URL.Path, "/wbx.png"):
			w.Header().Set("Content-Type", "image/png")
			w.Write(append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("karte")...))
		case strings.HasSuffix(r.URL.Path, "/wbx.html"):
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<html>wartung</html>")
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	cfg := &config.Config{NinaBaseURL: up.URL, LHPBaseURL: up.URL, RSSAllowedHosts: config.DefaultRSSHosts(), WaldbrandURL: up.URL + "/wbx.png"}
	f := &Feeds{Cfg: cfg, Cache: &Cache{}}
	ctx := context.Background()

	res, err := f.Nina(ctx)
	if err != nil || !strings.Contains(string(res.Body), "mow.1") || res.FetchedAt.IsZero() {
		t.Fatalf("nina: %v %s", err, res.Body)
	}
	if _, err := f.RSS(ctx, up.URL+"/feed.xml", nil); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("fremder Host muss abgelehnt werden: %v", err)
	}
	if _, err := f.RSS(ctx, "ftp://www.tagesschau.de/x", nil); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("ftp muss abgelehnt werden: %v", err)
	}
	cfg.RSSAllowedHosts = append(cfg.RSSAllowedHosts, "127.0.0.1")
	res, err = f.RSS(ctx, up.URL+"/feed.xml", nil)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if json.Unmarshal(res.Body, &out) != nil || out["status"] != "ok" {
		t.Fatalf("rss json: %s", res.Body)
	}
	// Weiterleitungen: innerhalb erlaubter Hosts folgen, auf fremde Hosts nicht
	res, err = f.RSS(ctx, up.URL+"/alt.xml", nil)
	if err != nil || !strings.Contains(string(res.Body), "Testfeed") {
		t.Fatalf("redirect auf erlaubten Host: %v", err)
	}
	if _, err := f.RSS(ctx, up.URL+"/fremd.xml", nil); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("redirect auf fremden Host muss abgelehnt werden: %v", err)
	}
	if _, err := f.LHP(ctx, "bogus"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("lhp kind: %v", err)
	}
	if _, err := f.NinaGeo(ctx, "../x"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("nina-geo id: %v", err)
	}
	// Waldbrandkarte: PNG wird durchgereicht, Nicht-Bild ist ein Fehler
	res, err = f.Waldbrand(ctx)
	if err != nil || !strings.HasPrefix(res.ContentType, "image/png") || !strings.Contains(string(res.Body), "karte") {
		t.Fatalf("waldbrand: %v %q", err, res.ContentType)
	}
	cfg.WaldbrandURL = up.URL + "/wbx.html"
	f2 := &Feeds{Cfg: cfg, Cache: &Cache{}}
	if _, err := f2.Waldbrand(ctx); err == nil {
		t.Fatal("HTML statt PNG muss ein Fehler sein")
	}
	// 404 beim Upstream ist ein Fehler, kein Cache-Eintrag
	if _, err := f.LHP(ctx, "alerts"); err == nil {
		t.Fatal("lhp 404 muss Fehler liefern")
	}
}

// Eigene Feeds einer Organisation duerfen keine internen Ziele ansteuern:
// weder als IP-Literal noch ueber einen Namen, der dorthin aufloest.
func TestPrivateHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "LOCALHOST.": true, "app.localhost": true, "127.0.0.1": true, "::1": true, "[::1]": true,
		"10.0.0.5": true, "172.16.3.4": true, "192.168.1.1": true, "169.254.169.254": true, "100.64.1.1": true,
		"0.0.0.0": true, "fe80::1": true, "fd00::1": true, "::ffff:127.0.0.1": true, "::ffff:10.0.0.1": true,
		"www.tagesschau.de": false, "8.8.8.8": false, "2001:db8::1": false, "": true,
	} {
		if got := PrivateHost(host); got != want {
			t.Fatalf("PrivateHost(%q) = %v", host, got)
		}
	}
	if _, err := publicDial(context.Background(), "tcp", "localhost:80"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("publicDial localhost: %v", err)
	}
	if _, err := publicDial(context.Background(), "tcp", "127.0.0.1:80"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("publicDial loopback: %v", err)
	}
}

func TestRSSOrgFeedNotPrivate(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(rssSample))
	}))
	defer up.Close()
	cfg := &config.Config{RSSAllowedHosts: config.DefaultRSSHosts()}
	f := &Feeds{Cfg: cfg, Cache: &Cache{}}
	ctx := context.Background()
	alle := func(string) bool { return true }
	// Organisation gibt den Host frei, Ziel ist aber die eigene Maschine
	if _, err := f.RSS(ctx, up.URL+"/feed.xml", alle); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("Org-Feed auf 127.0.0.1 muss abgelehnt werden: %v", err)
	}
	loc := strings.Replace(up.URL, "127.0.0.1", "localhost", 1)
	if _, err := f.RSS(ctx, loc+"/feed.xml", alle); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("Org-Feed auf localhost muss abgelehnt werden: %v", err)
	}
	// Betreiber-Hosts (RSS_ALLOWED_HOSTS) duerfen weiterhin intern liegen
	cfg.RSSAllowedHosts = append(cfg.RSSAllowedHosts, "127.0.0.1")
	if _, err := f.RSS(ctx, up.URL+"/feed.xml", nil); err != nil {
		t.Fatalf("Betreiber-Host intern: %v", err)
	}
}
