package kraefte

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/config"
)

// Gemeinsamer STEIN-Vorrat: Organisationen teilen frische Daten, alte Daten
// bleiben bei Ausfall erhalten (auch ueber einen Neustart), doppelte
// Webhook-Meldungen fuehren zu einem Abruf.
func TestSteinStore(t *testing.T) {
	var hits int32
	var fail atomic.Bool
	var status atomic.Value
	status.Store("ready")
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		ids := strings.Split(r.URL.Query().Get("buIds"), ",")
		w.Header().Set("Content-Type", "application/json")
		out := []string{}
		for _, id := range ids {
			if id == "101" || id == "103" {
				out = append(out, `{"buId":"`+id+`","groupId":1,"status":"`+status.Load().(string)+`","name":"GKW `+id+`"}`)
			}
		}
		io.WriteString(w, "["+strings.Join(out, ",")+"]")
	}))
	defer up.Close()

	raw, _ := fs.ReadFile(luk.Public, StammExample)
	dir := t.TempDir()
	cfg := &config.Config{SteinBaseURL: up.URL, DataDir: dir}
	newKat := func() *Katalog {
		k := New(cfg, raw)
		k.SteinKey = func(orgID int64) string {
			if orgID == 1 || orgID == 2 {
				return "key"
			}
			return ""
		}
		k.WebhookDebounce = 30 * time.Millisecond
		return k
	}
	k := newKat()

	// Erster Abruf je Organisation (Berechtigungen kennenlernen)
	r1 := k.SteinAssets(1)
	if !r1.OK || len(r1.Assets) != 2 || !r1.BUs["101"] || !r1.BUs["103"] || r1.Stale {
		t.Fatalf("org1: %+v", r1)
	}
	r2 := k.SteinAssets(2)
	if !r2.OK || len(r2.Assets) != 2 || hits != 2 {
		t.Fatalf("org2: %+v hits=%d", r2, hits)
	}
	// innerhalb der Frist: kein weiterer Abruf
	k.SteinAssets(1)
	k.SteinAssets(2)
	if hits != 2 {
		t.Fatalf("innerhalb TTL abgerufen: %d", hits)
	}
	// Daten veraltet: org1 frischt auf, org2 nutzt den frischen Stand mit
	k.store.mu.Lock()
	for _, e := range k.store.bus {
		e.At = time.Now().Add(-10 * time.Minute)
	}
	k.store.mu.Unlock()
	k.SteinAssets(1)
	k.SteinAssets(2)
	if hits != 3 {
		t.Fatalf("geteilter Vorrat: %d Abrufe, erwartet 3", hits)
	}
	// Gefilterte Anfrage (STEINmap) liefert nur die gewuenschte Einheit
	rf := k.SteinAssetsFor(2, []string{"101"})
	if !rf.OK || len(rf.Assets) != 1 || rf.BUs["103"] {
		t.Fatalf("filter: %+v", rf)
	}
	// Keine Berechtigung fuer fremde Einheit
	if r := k.SteinAssetsFor(2, []string{"999"}); !r.OK || len(r.Assets) != 0 {
		t.Fatalf("fremde Einheit: %+v", r)
	}

	// STEIN faellt aus: alter Stand bleibt, als veraltet markiert, Pause danach
	fail.Store(true)
	k.store.mu.Lock()
	for _, e := range k.store.bus {
		e.At = time.Now().Add(-3 * time.Hour)
	}
	k.store.save() // Stand auf der Platte entspricht dem alten Zeitstempel
	k.store.mu.Unlock()
	before := hits
	r := k.SteinAssets(1)
	if !r.OK || !r.Stale || len(r.Assets) != 2 || r.Error == nil || *r.Error != "fetch_failed" {
		t.Fatalf("ausfall: %+v", r)
	}
	k.SteinAssets(1)
	k.SteinAssets(1)
	if hits != before+1 {
		t.Fatalf("waehrend der Pause erneut probiert: %d", hits-before)
	}

	// Neustart: Vorrat kommt von der Platte, STEIN weiter weg -> alter Stand bleibt
	k2 := newKat()
	r = k2.SteinAssets(1)
	if !r.OK || !r.Stale || len(r.Assets) != 2 {
		t.Fatalf("nach Neustart: %+v", r)
	}

	// STEIN wieder da: Webhook-Meldungen von zwei Organisationen fuer dieselbe
	// Einheit -> ein Abruf, neuer Status sichtbar
	fail.Store(false)
	status.Store("notready")
	k2.store.mu.Lock()
	for _, o := range k2.store.orgs {
		o.failUntil = time.Time{}
	}
	k2.store.mu.Unlock()
	before = hits
	k2.SteinWebhookTouch(1, []string{"101"})
	k2.SteinWebhookTouch(2, []string{"101"})
	deadline := time.Now().Add(2 * time.Second)
	for {
		k2.store.mu.Lock()
		e := k2.store.bus["101"]
		fresh := e != nil && time.Since(e.At) < time.Minute
		k2.store.mu.Unlock()
		if fresh {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Webhook-Nachabruf blieb aus")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if hits != before+1 {
		t.Fatalf("doppelte Meldung: %d Abrufe, erwartet 1", hits-before)
	}
	r = k2.SteinAssetsFor(1, []string{"101"})
	if len(r.Assets) != 1 || !strings.Contains(mstr(r.Assets[0].(map[string]any), "status"), "notready") {
		t.Fatalf("neuer Status fehlt: %+v", r.Assets)
	}
}
