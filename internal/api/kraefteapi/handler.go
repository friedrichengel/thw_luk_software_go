// Package kraefteapi: Kraeftekatalog (Stammdaten aller THW-Einheiten,
// STEIN-Live-Status, Bindung in offenen Einsaetzen). Nur lesend; geschrieben
// wird der Verweis (kraft_key) ueber die Knoten-/Mitglieder-/Schicht-Aktionen
// in einsatzapi.
//
//	GET ?action=katalog&einsatz=U   Stamm + STEIN-Status + Bindungen aus Sicht dieses Einsatzes
//	GET ?action=stamm               nur Stammdaten (STEINboard/STEINmap)
//	GET ?action=bindungen           kraft_key => Bindungen in offenen Einsaetzen
//	GET ?action=bereiche            Auswahlliste "eigener Bereich" fuer die Verwaltung
//	GET ?action=typen               Typschluessel => {short,label,stein_cat,symbol}
package kraefteapi

import (
	"net/http"

	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
	"github.com/friedrichengel/thw_luk_software_go/internal/kraefte"
)

func Register(mux *http.ServeMux, app *httpx.App, kat *kraefte.Katalog) {
	h := func(w http.ResponseWriter, r *http.Request) {
		if err := handle(w, r, app, kat); err != nil {
			app.Fail(w, r, err)
		}
	}
	mux.HandleFunc("/api/kraefte", h)
}

func handle(w http.ResponseWriter, r *http.Request, app *httpx.App, kat *kraefte.Katalog) error {
	if r.Method != http.MethodGet {
		return apierr.New("unknown_action", 400)
	}
	switch httpx.Query(r, "action") {
	case "katalog":
		ctx, err := app.Ctx(r, httpx.Query(r, "einsatz"))
		if err != nil {
			return err
		}
		k, err := kat.KatalogFor(app.DB, ctx, app.UserOrgID(r))
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"katalog": k})
	case "stamm":
		if _, err := app.RequireEinsatzUserAPI(r); err != nil {
			return err
		}
		app.OK(w, map[string]any{"stamm": kat.Stamm()})
	case "bindungen":
		uid, err := app.RequireEinsatzUserAPI(r)
		if err != nil {
			return err
		}
		b, err := kat.Bindungen(app.DB, uid, nil)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"bindungen": b})
	case "bereiche":
		if _, err := app.RequireEinsatzUserAPI(r); err != nil {
			return err
		}
		app.OK(w, map[string]any{"bereiche": kat.Bereiche()})
	case "typen":
		if _, err := app.RequireEinsatzUserAPI(r); err != nil {
			return err
		}
		app.OK(w, map[string]any{"typen": kraefte.AllTypInfo()})
	default:
		return apierr.New("unknown_action", 400)
	}
	return nil
}
