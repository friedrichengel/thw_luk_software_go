// Package changelogapi: Changelog "Was ist neu" (alle eingeloggten Konten, auch Admin).
//
//	GET  ?action=list                 {entries:[{version,date,title,show,items[]}], latest, seen}
//	POST ?action=seen {csrf, version} bestaetigten Stand setzen -> {ok:true, seen}
package changelogapi

import (
	"net/http"

	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/changelog"
	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
	"github.com/friedrichengel/thw_luk_software_go/internal/input"
)

func Register(mux *http.ServeMux, app *httpx.App) {
	h := func(w http.ResponseWriter, r *http.Request) {
		if err := handle(w, r, app); err != nil {
			app.Fail(w, r, err)
		}
	}
	mux.HandleFunc("/api/changelog", h)
}

func handle(w http.ResponseWriter, r *http.Request, app *httpx.App) error {
	uid, err := app.RequireLoginAPI(r)
	if err != nil {
		return err
	}
	action := httpx.Query(r, "action")
	switch r.Method {
	case http.MethodGet:
		if action != "list" {
			return apierr.New("unknown_action", 400)
		}
		app.OK(w, map[string]any{
			"entries": app.Changelog.Entries(),
			"latest":  app.Changelog.LatestVersion(),
			"seen":    changelog.SeenVersion(app.DB, uid),
		})
	case http.MethodPost:
		body, err := app.Body(r)
		if err != nil {
			return err
		}
		if action != "seen" {
			return apierr.New("unknown_action", 400)
		}
		v := input.Str(body["version"], 32)
		if v == "" {
			v = app.Changelog.LatestVersion()
		}
		if err := changelog.MarkSeen(app.DB, uid, v); err != nil {
			return err
		}
		app.OK(w, map[string]any{"seen": changelog.SeenVersion(app.DB, uid)})
	default:
		return apierr.New("unknown_action", 400)
	}
	return nil
}
