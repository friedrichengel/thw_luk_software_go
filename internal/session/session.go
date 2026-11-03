// Package session: serverseitige Sitzungen in der SQLite-Datenbank (Tabelle
// sessions). Der Browser bekommt nur eine zufaellige ID als Cookie. Die
// Daten (uid, role, csrf, 2fa_pending, ...) liegen als JSON in der Zeile und
// ueberleben einen Neustart des Prozesses - wichtig fuer Updates.
//
// Das Cookie wird gesetzt, bevor die erste Antwortzeile geschrieben wird:
// die Middleware kapselt den ResponseWriter und schreibt Sitzung und Cookie
// beim ersten WriteHeader/Write bzw. nach dem Handler.
package session

import (
	"context"
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/uuid"
)

const CookieName = "luk_session"

type Store struct {
	DB     *db.DB
	Idle   time.Duration
	Secure func(r *http.Request) bool
	gcMu   sync.Mutex
	lastGC time.Time
}

type Session struct {
	store      *Store
	id         string
	data       map[string]any
	dirty      bool
	isNew      bool
	destroyed  bool
	cookieSent bool
	secure     bool
	loadedSeen time.Time
}

type ctxKey struct{}

// FromContext liefert die Sitzung des Requests (nie nil innerhalb der Middleware).
func FromContext(ctx context.Context) *Session {
	if s, ok := ctx.Value(ctxKey{}).(*Session); ok {
		return s
	}
	return &Session{data: map[string]any{}, isNew: true}
}

func (st *Store) load(r *http.Request) *Session {
	s := &Session{store: st, data: map[string]any{}, isNew: true}
	if st.Secure != nil {
		s.secure = st.Secure(r)
	}
	c, err := r.Cookie(CookieName)
	if err != nil || len(c.Value) != 64 || !uuid.Valid(c.Value) {
		return s
	}
	row, err := db.QueryMap(st.DB, "SELECT data, last_seen FROM sessions WHERE id = ?", c.Value)
	if err != nil || row == nil {
		return s
	}
	seen, ok := db.ParseTime(db.Str(row, "last_seen"))
	if !ok || (st.Idle > 0 && time.Since(seen) > st.Idle) {
		_, _ = st.DB.Exec("DELETE FROM sessions WHERE id = ?", c.Value)
		return s
	}
	var data map[string]any
	if json.Unmarshal([]byte(db.Str(row, "data")), &data) != nil || data == nil {
		data = map[string]any{}
	}
	s.id = c.Value
	s.data = data
	s.isNew = false
	s.cookieSent = true
	s.loadedSeen = seen
	return s
}

// flush schreibt die Sitzung in die Datenbank und setzt bei Bedarf das Cookie.
func (s *Session) flush(w http.ResponseWriter) {
	if s.store == nil {
		return
	}
	if s.destroyed {
		if s.id != "" {
			_, _ = s.store.DB.Exec("DELETE FROM sessions WHERE id = ?", s.id)
		}
		if s.cookieSent {
			http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secure})
		}
		s.destroyed = false
		s.dirty = false
		s.id = ""
		s.isNew = true
		s.cookieSent = false
		s.data = map[string]any{}
		return
	}
	if s.isNew && !s.dirty {
		return // leere Sitzung: kein Cookie, keine Zeile
	}
	now := time.Now()
	nowDB := db.Now()
	if s.dirty {
		b, _ := json.Marshal(s.data)
		if s.id == "" {
			s.id = uuid.RandomHex(32)
		}
		_, err := s.store.DB.Exec(`INSERT INTO sessions (id, data, created_at, last_seen) VALUES (?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET data = excluded.data, last_seen = excluded.last_seen`, s.id, string(b), nowDB, nowDB)
		if err != nil {
			log.Printf("session: speichern: %v", err)
		}
		s.dirty = false
		s.isNew = false
	} else if now.Sub(s.loadedSeen) > time.Minute {
		// last_seen hoechstens einmal pro Minute fortschreiben (weniger Schreiblast)
		_, _ = s.store.DB.Exec("UPDATE sessions SET last_seen = ? WHERE id = ?", nowDB, s.id)
		s.loadedSeen = now
	}
	if !s.cookieSent {
		http.SetCookie(w, &http.Cookie{Name: CookieName, Value: s.id, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secure})
		s.cookieSent = true
	}
}

// Get liefert einen Wert (nil, wenn nicht gesetzt).
func (s *Session) Get(k string) any { return s.data[k] }

func (s *Session) GetString(k string) string {
	if v, ok := s.data[k].(string); ok {
		return v
	}
	return ""
}

func (s *Session) GetInt64(k string) int64 {
	switch v := s.data[k].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

func (s *Session) GetBool(k string) bool {
	if v, ok := s.data[k].(bool); ok {
		return v
	}
	return false
}

// GetMap liefert ein verschachteltes Objekt (z.B. 2fa_pending) oder nil.
func (s *Session) GetMap(k string) map[string]any {
	if v, ok := s.data[k].(map[string]any); ok {
		return v
	}
	return nil
}

func (s *Session) Has(k string) bool {
	_, ok := s.data[k]
	return ok
}

func (s *Session) Set(k string, v any) {
	s.data[k] = v
	s.dirty = true
}

func (s *Session) Delete(k string) {
	if _, ok := s.data[k]; ok {
		delete(s.data, k)
		s.dirty = true
	}
}

// Regenerate vergibt eine neue ID (nach dem Login), Daten bleiben erhalten.
func (s *Session) Regenerate() {
	if s.store != nil && s.id != "" {
		_, _ = s.store.DB.Exec("DELETE FROM sessions WHERE id = ?", s.id)
	}
	s.id = uuid.RandomHex(32)
	s.isNew = true
	s.cookieSent = false
	s.dirty = true
}

// DeleteForUser loescht alle Sitzungen eines Kontos: nach Passwortwechsel
// oder -Reset, Rollenwechsel, 2FA-Reset und Loeschung darf keine alte
// Sitzung weiterlaufen. Wer sein eigenes Passwort aendert, regeneriert die
// eigene Sitzung danach (Regenerate), damit sie als einzige bestehen bleibt.
func DeleteForUser(q db.Q, uid int64) error {
	_, err := q.Exec("DELETE FROM sessions WHERE json_extract(data, '$.uid') = ?", uid)
	return err
}

// Destroy loescht die Sitzung (Logout).
func (s *Session) Destroy() {
	s.destroyed = true
	s.data = map[string]any{}
}

// ---------------------------------------------------------------------------
// Middleware

type writer struct {
	http.ResponseWriter
	s       *Session
	flushed bool
}

func (w *writer) ensure() {
	if !w.flushed {
		w.flushed = true
		w.s.flush(w.ResponseWriter)
	}
}

func (w *writer) WriteHeader(code int) {
	w.ensure()
	w.ResponseWriter.WriteHeader(code)
}

func (w *writer) Write(b []byte) (int, error) {
	w.ensure()
	return w.ResponseWriter.Write(b)
}

// Flush unterstuetzt http.Flusher (Streaming), falls genutzt.
func (w *writer) Flush() {
	w.ensure()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Middleware laedt die Sitzung, haengt sie an den Kontext und sorgt dafuer,
// dass Aenderungen gespeichert und das Cookie vor der Antwort gesetzt werden.
func (st *Store) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.maybeGC()
		s := st.load(r)
		sw := &writer{ResponseWriter: w, s: s}
		next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), ctxKey{}, s)))
		sw.ensure()
	})
}

func (st *Store) maybeGC() {
	if st.Idle <= 0 || rand.Intn(100) != 0 {
		return
	}
	st.gcMu.Lock()
	defer st.gcMu.Unlock()
	if time.Since(st.lastGC) < 5*time.Minute {
		return
	}
	st.lastGC = time.Now()
	_, _ = st.DB.Exec("DELETE FROM sessions WHERE last_seen < ?", db.Ago(st.Idle))
}
