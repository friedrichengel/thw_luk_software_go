// Package upstream: direkte Abrufe externer Dienste (NINA, LHP, RSS, STEIN,
// Divera) mit einem gemeinsamen Cache. Ersetzt die frueheren Node-Proxies:
// Der Server holt die Daten selbst, cacht sie pro Schluessel, dedupliziert
// parallele Abrufe und liefert bei Upstream-Fehlern bis zu einer Frist die
// letzten bekannten Daten (Stale-Fallback). Alle Aufrufer pruefen vorher die
// Sitzung - die Daten gehen nie ohne Anmeldung an einen Browser.
package upstream

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Result ist eine gecachte Antwort.
type Result struct {
	Body        []byte
	ContentType string
	FetchedAt   time.Time
	Stale       bool // aus dem Cache, weil der aktuelle Abruf fehlschlug
}

type entry struct {
	body  []byte
	ctype string
	at    time.Time
	// Fehlersperre: bis failUntil wird nicht erneut abgerufen (Schutz vor
	// Dauerfeuer auf einen Dienst, der uns ablehnt oder nicht erreichbar ist).
	failUntil time.Time
	failErr   error
}

const (
	// FailCooldown: Pause nach einem fehlgeschlagenen Abruf (Netz, 5xx).
	FailCooldown = 2 * time.Minute
	// AuthCooldown: Pause nach Ablehnung durch den Dienst (401/403) - ein
	// falscher Schluessel darf nicht alle paar Sekunden erneut probiert werden.
	AuthCooldown = 30 * time.Minute
	// RateLimitCooldown: Pause nach 429. STEIN sperrt die IP nach Ueberschreiten
	// des Limits (20 Anfragen/min) fuer eine Stunde - so lange warten wir auch.
	RateLimitCooldown = time.Hour
)

// AuthError: der Dienst hat den Zugang abgelehnt (Schluessel falsch, gesperrt
// oder Rate-Limit). Weitere Abrufe pausieren fuer AuthCooldown.
type AuthError struct {
	Status int
}

func (e *AuthError) Error() string { return fmt.Sprintf("upstream rejected access (%d)", e.Status) }

// IsAuthStatus: HTTP-Status, der als Ablehnung des Zugangs gilt.
func IsAuthStatus(status int) bool {
	return status == 401 || status == 403 || status == 429
}

type call struct {
	done chan struct{}
	res  Result
	err  error
}

// Cache haelt Antworten im Speicher. Null-Wert ist benutzbar.
type Cache struct {
	mu      sync.Mutex
	entries map[string]*entry
	calls   map[string]*call
	now     func() time.Time
}

// ErrUpstream: der Dienst war nicht erreichbar oder antwortete fehlerhaft.
var ErrUpstream = errors.New("upstream unreachable")

func (c *Cache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// Get liefert den Eintrag zu key, wenn er juenger als ttl ist; sonst wird
// fetch genau einmal ausgefuehrt (parallele Aufrufer warten auf dasselbe
// Ergebnis). Schlaegt fetch fehl, kommt ein Eintrag bis zum Alter stale
// mit Stale=true zurueck, danach der Fehler.
func (c *Cache) Get(key string, ttl, stale time.Duration, fetch func() ([]byte, string, error)) (Result, error) {
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]*entry{}
		c.calls = map[string]*call{}
	}
	now := c.clock()
	if e := c.entries[key]; e != nil && e.body != nil && now.Sub(e.at) < ttl {
		c.mu.Unlock()
		return Result{Body: e.body, ContentType: e.ctype, FetchedAt: e.at}, nil
	}
	if e := c.entries[key]; e != nil && now.Before(e.failUntil) {
		// Sperre aktiv: alte Daten liefern, solange sie nicht zu alt sind, sonst den Fehler.
		c.mu.Unlock()
		if e.body != nil && now.Sub(e.at) < stale {
			return Result{Body: e.body, ContentType: e.ctype, FetchedAt: e.at, Stale: true}, nil
		}
		return Result{}, e.failErr
	}
	if cl := c.calls[key]; cl != nil {
		c.mu.Unlock()
		<-cl.done
		return cl.res, cl.err
	}
	cl := &call{done: make(chan struct{})}
	c.calls[key] = cl
	c.mu.Unlock()

	body, ctype, err := fetch()

	c.mu.Lock()
	now = c.clock()
	if err == nil {
		e := &entry{body: body, ctype: ctype, at: now}
		c.entries[key] = e
		cl.res = Result{Body: body, ContentType: ctype, FetchedAt: now}
		c.sweep(now, stale)
	} else {
		cooldown := FailCooldown
		var se *StatusError
		if errors.As(err, &se) && IsAuthStatus(se.Status) {
			err = &AuthError{Status: se.Status}
			cooldown = AuthCooldown
			if se.Status == 429 {
				cooldown = RateLimitCooldown
			}
		}
		e := c.entries[key]
		if e == nil {
			e = &entry{}
			c.entries[key] = e
		}
		e.failUntil = now.Add(cooldown)
		e.failErr = err
		if e.body != nil && now.Sub(e.at) < stale {
			cl.res = Result{Body: e.body, ContentType: e.ctype, FetchedAt: e.at, Stale: true}
		} else {
			cl.err = err
		}
	}
	delete(c.calls, key)
	c.mu.Unlock()
	close(cl.done)
	return cl.res, cl.err
}

// Put legt einen Eintrag direkt ab (z.B. nach einer Teilaktualisierung durch
// einen Webhook) und hebt eine bestehende Fehlersperre auf.
func (c *Cache) Put(key string, body []byte, ctype string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*entry{}
		c.calls = map[string]*call{}
	}
	c.entries[key] = &entry{body: body, ctype: ctype, at: c.clock()}
}

// Peek liefert einen Eintrag beliebigen Alters (fuer Anzeige "Stand").
func (c *Cache) Peek(key string) (Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[key]
	if e == nil || e.body == nil {
		return Result{}, false
	}
	return Result{Body: e.body, ContentType: e.ctype, FetchedAt: e.at}, true
}

// Forget verwirft einen Eintrag (z.B. nach Schluesselwechsel).
func (c *Cache) Forget(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(c.entries, k)
		}
	}
}

// sweep raeumt sehr alte Eintraege ab, damit der Cache bei vielen
// verschiedenen Schluesseln (RSS-URLs, Warn-IDs) nicht unbegrenzt waechst.
func (c *Cache) sweep(now time.Time, stale time.Duration) {
	if len(c.entries) < 500 {
		return
	}
	limit := stale
	if limit < time.Hour {
		limit = time.Hour
	}
	for k, e := range c.entries {
		if now.Sub(e.at) > limit {
			delete(c.entries, k)
		}
	}
}
