// Package diveraapi: Divera-Daten fuer DiveraMap und Dashboard-Kachel.
//
// Jeder Aufruf braucht eine Sitzung mit Einsatzrecht, das Divera-Recht des
// Kontos (read/write) und einen hinterlegten Divera-Zugang der eigenen
// Organisation (internal/divera.Resolve). Die Divera-Antworten werden 1:1
// durchgereicht (JSON bzw. PDF), Schluessel bleiben auf dem Server.
//
//	GET  ?action=all                 Personaluebersicht (pull/all, Namen gekuerzt)
//	GET  ?action=vehicles            Fahrzeugstatus (pull/vehicle-status)
//	GET  ?action=operations          Einsaetze (alarms)
//	GET  ?action=download&id=N       Einsatz-PDF
//	POST ?action=set_status&id=N     {csrf, status, status_id, status_note, lat, lng}; nur Recht "write"
//
// Fehler: divera_disabled (403, kein Recht/kein Zugang), divera_forbidden
// (403, Status setzen ohne Schreibrecht), divera_upstream (502),
// invalid_id (400).
package diveraapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/divera"
	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
	"github.com/friedrichengel/thw_luk_software_go/internal/upstream"
)

func Register(mux *http.ServeMux, app *httpx.App) {
	mux.HandleFunc("/api/divera", func(w http.ResponseWriter, r *http.Request) {
		if err := handle(w, r, app); err != nil {
			app.Fail(w, r, err)
		}
	})
}

func writeRaw(w http.ResponseWriter, body []byte, ctype string, status int, res *upstream.Result) {
	if ctype == "" {
		ctype = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if res != nil {
		w.Header().Set("X-Cache-At", res.FetchedAt.UTC().Format(time.RFC3339))
		if res.Stale {
			w.Header().Set("X-Cache-Stale", "1")
		}
	}
	w.WriteHeader(status)
	w.Write(body)
}

func upstreamErr(app *httpx.App, action string, err error) error {
	if errors.Is(err, divera.ErrBadID) {
		return apierr.New("invalid_id", 400)
	}
	var ae *upstream.AuthError
	if errors.As(err, &ae) {
		// Schluessel abgelehnt: der Cache pausiert weitere Abrufe (AuthCooldown).
		app.Log.Printf("divera %s: Zugang abgelehnt (%d), Pause", action, ae.Status)
		return apierr.New("divera_auth_failed", 502)
	}
	app.Log.Printf("divera %s: %v", action, err)
	return apierr.New("divera_upstream", 502)
}

func handle(w http.ResponseWriter, r *http.Request, app *httpx.App) error {
	if _, err := app.RequireEinsatzUserAPI(r); err != nil {
		return err
	}
	src, err := app.DiveraSource(r)
	if err != nil {
		return err
	}
	if src == nil {
		return apierr.New("divera_disabled", 403)
	}
	action := httpx.Query(r, "action")
	cl := app.Divera
	switch action {
	case "all", "vehicles", "operations":
		if r.Method != http.MethodGet {
			return apierr.New("unknown_action", 400)
		}
		var res upstream.Result
		switch action {
		case "all":
			res, err = cl.All(r.Context(), src)
			if err == nil {
				// Ausgeblendete Gruppen (Sammelgruppen) der Organisation entfernen
				res.Body = divera.FilterGroups(res.Body, src.HiddenGroups)
			}
		case "vehicles":
			res, err = cl.Vehicles(r.Context(), src)
		default:
			res, err = cl.Operations(r.Context(), src)
		}
		if err != nil {
			return upstreamErr(app, action, err)
		}
		ctype := res.ContentType
		if !strings.Contains(strings.ToLower(ctype), "json") {
			ctype = "application/json; charset=utf-8"
		}
		writeRaw(w, res.Body, ctype, 200, &res)
	case "download":
		if r.Method != http.MethodGet {
			return apierr.New("unknown_action", 400)
		}
		body, ctype, err := cl.Download(r.Context(), src, strings.TrimSpace(httpx.Query(r, "id")))
		if err != nil {
			return upstreamErr(app, action, err)
		}
		w.Header().Set("Content-Disposition", "inline")
		writeRaw(w, body, ctype, 200, nil)
	case "set_status":
		if r.Method != http.MethodPost {
			return apierr.New("unknown_action", 400)
		}
		body, err := app.Body(r)
		if err != nil {
			return err
		}
		if !src.Write {
			return apierr.New("divera_forbidden", 403)
		}
		resp, status, err := cl.SetStatus(r.Context(), src, strings.TrimSpace(httpx.Query(r, "id")), body)
		if err != nil {
			return upstreamErr(app, action, err)
		}
		if status < 200 || status > 599 {
			status = 502
		}
		writeRaw(w, resp, "application/json; charset=utf-8", status, nil)
	default:
		return apierr.New("unknown_action", 400)
	}
	return nil
}
