// Package steinhookapi: Empfaenger fuer STEIN-Webhooks.
//
//	POST /api/stein/webhook/{token}   Header X-Secret: <Secret des Ortsverbands aus STEIN>
//
// Der Token im Pfad gehoert zu einer Organisation (org.WebhookToken, HMAC aus
// APP_SECRET). STEIN vergibt das X-Secret je Ortsverband; der Org-Admin
// hinterlegt es als Zugang "stein_webhook:<Bezeichnung>". Die Nachricht
// enthaelt nur Verweise (type, action, id, parentId); der Server merkt die
// betroffenen Ortsverbaende vor und holt sie gebuendelt mit dem STEIN-
// Schluessel der Organisation nach (kraefte.SteinWebhookTouch). Jedes
// Ereignis wird protokolliert (Nachweis fuer den Org-Admin); Duplikate werden
// ueber meta.eventId erkannt. Antwort immer schnell (STEIN erwartet < 2 s),
// Fehler ohne Details (403/404 verraten nichts ueber Organisationen).
package steinhookapi

import (
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
	"github.com/friedrichengel/thw_luk_software_go/internal/kraefte"
	"github.com/friedrichengel/thw_luk_software_go/internal/org"
)

const (
	maxBody       = 256 * 1024
	hookWindowMin = 15 // Sperrfenster fuer abgelehnte Aufrufe (auth.hookWindow)
)

// seen: eventId -> Zeitpunkt, fuer die Duplikaterkennung (at least once).
var (
	seenMu sync.Mutex
	seen   = map[string]time.Time{}
)

func markSeen(key string, now time.Time) bool {
	seenMu.Lock()
	defer seenMu.Unlock()
	if len(seen) > 5000 {
		for k, t := range seen {
			if now.Sub(t) > time.Hour {
				delete(seen, k)
			}
		}
	}
	if t, ok := seen[key]; ok && now.Sub(t) < time.Hour {
		return true
	}
	seen[key] = now
	return false
}

type payload struct {
	Meta struct {
		EventID         string `json:"eventId"`
		EnvironmentName string `json:"environmentName"`
		Timestamp       string `json:"timestamp"`
	} `json:"meta"`
	Items []struct {
		Type       string `json:"type"`
		Action     string `json:"action"`
		ID         any    `json:"id"`
		ParentID   any    `json:"parentId"`
		ParentType string `json:"parentType"`
	} `json:"items"`
}

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%v", x)
	default:
		return fmt.Sprintf("%v", x)
	}
}

func Register(mux *http.ServeMux, app *httpx.App, kat *kraefte.Katalog) {
	mux.HandleFunc("/api/stein/webhook/{token}", func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, app, kat)
	})
}

func reply(w http.ResponseWriter, status int, v map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func handle(w http.ResponseWriter, r *http.Request, app *httpx.App, kat *kraefte.Katalog) {
	if r.Method != http.MethodPost {
		reply(w, 405, map[string]any{"error": "method not allowed"})
		return
	}
	// Abgelehnte Aufrufe je IP begrenzen (Token oder Secret durchprobieren).
	ipHash := app.Auth.IPHash(r)
	if limited, _ := app.Auth.IsWebhookRateLimited(ipHash); limited {
		reply(w, 429, map[string]any{"error": "too many requests"})
		return
	}
	reject := func(status int, msg string) {
		if hit, _ := app.Auth.RecordWebhookFailure(ipHash); hit {
			app.Log.Printf("stein-webhook: zu viele abgelehnte Aufrufe von einer IP, vorerst gesperrt (%d min)", hookWindowMin)
		}
		reply(w, status, map[string]any{"error": msg})
	}
	o, err := org.FindByWebhookToken(app.DB, app.Cfg.Secret, r.PathValue("token"))
	if err != nil || o == nil {
		reject(404, "not found")
		return
	}
	provided := strings.TrimSpace(r.Header.Get("X-Secret"))
	secrets, err := org.WebhookSecrets(app.DB, o.ID, app.Cfg.Secret)
	if err != nil || provided == "" {
		reject(403, "forbidden")
		return
	}
	label := ""
	for l, s := range secrets {
		if hmac.Equal([]byte(s), []byte(provided)) {
			label = l
			break
		}
	}
	if label == "" {
		app.Log.Printf("stein-webhook: Organisation %d: X-Secret abgelehnt", o.ID)
		reject(403, "forbidden")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		reply(w, 413, map[string]any{"error": "payload too large"})
		return
	}
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		reply(w, 400, map[string]any{"error": "invalid json"})
		return
	}
	now := time.Now()
	// Dasselbe STEIN-Ereignis erreicht den Server einmal je Organisation, die
	// einen Webhook eingerichtet hat. Duplikate werden je Organisation erkannt,
	// damit niemand mit dem Secret einer Organisation Ereignisse einer anderen
	// unterdruecken kann (das Vormerken der BUs ist idempotent).
	if p.Meta.EventID != "" && markSeen(strconv.FormatInt(o.ID, 10)+":"+p.Meta.EventID, now) {
		reply(w, 200, map[string]any{"ok": true, "duplicate": true})
		return
	}
	buIDs := []string{}
	if len(p.Items) == 0 {
		_ = org.LogWebhookEvent(app.DB, o.ID, org.WebhookEvent{EventID: p.Meta.EventID, Environment: p.Meta.EnvironmentName, SecretLabel: label})
	}
	for _, it := range p.Items {
		id, parent := str(it.ID), str(it.ParentID)
		_ = org.LogWebhookEvent(app.DB, o.ID, org.WebhookEvent{EventID: p.Meta.EventID, Environment: p.Meta.EnvironmentName,
			ItemType: it.Type, Action: it.Action, ItemID: id, ParentID: parent, SecretLabel: label})
		switch it.Type {
		case "asset":
			if parent != "" && (it.ParentType == "" || it.ParentType == "bu") {
				buIDs = append(buIDs, parent)
			}
		case "bu":
			if id != "" {
				buIDs = append(buIDs, id)
			}
		}
	}
	if len(buIDs) > 0 {
		kat.SteinWebhookTouch(o.ID, buIDs)
	}
	reply(w, 200, map[string]any{"ok": true, "items": len(p.Items)})
}
