// Package lagekartenapi: API fuer Lagekarten eines Einsatzes (Karten,
// Einsatzabschnitte, Kartenobjekte).
//
// GET  ?action=list&einsatz=U                      Karten des Einsatzes
// GET  ?action=load&einsatz=U&karte=ID             Karte + Abschnitte + Objekte + Tree + seq
//
// POST (JSON-Body mit csrf; Schreibrecht "lagekarte"):
//
//	create            {einsatz, title, center_lat?, center_lng?, zoom?, basemap?}
//	update            {einsatz, karte, version, title?, center_lat?, center_lng?, zoom?, basemap?}
//	delete            {einsatz, karte, version}
//	abschnitt_create  {einsatz, karte, name}
//	abschnitt_update  {einsatz, karte, nr, version, name}
//	abschnitt_delete  {einsatz, karte, nr, version}
//	feature_create    {einsatz, karte, id?, kind, geometry, props, abschnitte, ref_kind?, ref_id?}
//	feature_update    {einsatz, id, version, geometry?, props?, abschnitte?, ref_kind?, ref_id?}
//	feature_delete    {einsatz, id, version}
//	import_json       {einsatz, title, map_data}   Datei-Import (altes Lagekarten-Format) als neue Karte
package lagekartenapi

import (
	"net/http"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/einsatz"
	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
	"github.com/friedrichengel/thw_luk_software_go/internal/input"
	"github.com/friedrichengel/thw_luk_software_go/internal/lagekarte"
	"github.com/friedrichengel/thw_luk_software_go/internal/stab"
)

func Register(mux *http.ServeMux, app *httpx.App) {
	h := func(w http.ResponseWriter, r *http.Request) {
		if err := serve(app, w, r); err != nil {
			app.Fail(w, r, err)
		}
	}
	mux.HandleFunc("/api/lagekarten", h)
}

func serve(app *httpx.App, w http.ResponseWriter, r *http.Request) error {
	action := httpx.Query(r, "action")
	switch r.Method {
	case http.MethodGet:
		return handleGet(app, w, r, action)
	case http.MethodPost:
		return handlePost(app, w, r, action)
	}
	return apierr.New("unknown_action", 400)
}

func handleGet(app *httpx.App, w http.ResponseWriter, r *http.Request, action string) error {
	uuidStr := httpx.Query(r, "einsatz")
	switch action {
	case "list":
		ctx, err := app.Ctx(r, uuidStr)
		if err != nil {
			return err
		}
		items, err := lagekarte.List(app.DB, ctx.Einsatz.ID, false)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"ctx": access.ClientContext(ctx), "items": items})
		return nil
	case "load":
		ctx, err := app.Ctx(r, uuidStr)
		if err != nil {
			return err
		}
		einsatzID := ctx.Einsatz.ID
		karteID := httpx.Query(r, "karte")
		karte, err := lagekarte.KarteByID(app.DB, einsatzID, karteID)
		if err != nil {
			return err
		}
		if karte == nil {
			return apierr.New("not_found", 404)
		}
		access.LogAccess(app.DB, ctx, app.Auth.IPHash(r), "api:lagekarte")
		abschnitte, err := lagekarte.Abschnitte(app.DB, karteID, false)
		if err != nil {
			return err
		}
		features, err := lagekarte.Features(app.DB, karteID, false)
		if err != nil {
			return err
		}
		tree, err := einsatz.TreeOf(app.DB, einsatzID, false)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{
			"ctx":        access.ClientContext(ctx),
			"karte":      lagekarte.KarteRow(karte),
			"abschnitte": abschnitte,
			"features":   features,
			"tree":       tree,
		})
		return nil
	}
	return apierr.New("unknown_action", 400)
}

func handlePost(app *httpx.App, w http.ResponseWriter, r *http.Request, action string) error {
	body, err := app.Body(r)
	if err != nil {
		return err
	}
	if _, err := app.RequireEinsatzUserAPI(r); err != nil {
		return err
	}
	uuidStr := input.Raw(body["einsatz"])
	ctx, err := app.CtxWrite(r, uuidStr, stab.CapLagekarte)
	if err != nil {
		return err
	}
	karteID := input.Raw(body["karte"])
	// Mittelpunkt neuer Karten: Organisation des Einsatzes (eigener Eintrag,
	// Stammdaten-Standort oder Server)
	lat, lng, _ := app.MapCenter(ctx.Einsatz.OrgIDValue())

	switch action {
	case "create":
		k, err := lagekarte.Create(app.DB, ctx, body, lat, lng)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"karte": k})
	case "update":
		id, err := input.RequireID(karteID)
		if err != nil {
			return err
		}
		v, err := input.Version(body["version"])
		if err != nil {
			return err
		}
		k, err := lagekarte.Update(app.DB, ctx, id, v, body)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"karte": k})
	case "delete":
		id, err := input.RequireID(karteID)
		if err != nil {
			return err
		}
		v, err := input.Version(body["version"])
		if err != nil {
			return err
		}
		d, err := lagekarte.Delete(app.DB, ctx, id, v)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"deleted": d})
	case "abschnitt_create":
		id, err := input.RequireID(karteID)
		if err != nil {
			return err
		}
		a, err := lagekarte.AbschnittCreate(app.DB, ctx, id, input.Raw(body["name"]), nil)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"abschnitt": a})
	case "abschnitt_update":
		id, err := input.RequireID(karteID)
		if err != nil {
			return err
		}
		v, err := input.Version(body["version"])
		if err != nil {
			return err
		}
		a, err := lagekarte.AbschnittUpdate(app.DB, ctx, id, input.Int(body["nr"], 0), v, input.Raw(body["name"]))
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"abschnitt": a})
	case "abschnitt_delete":
		id, err := input.RequireID(karteID)
		if err != nil {
			return err
		}
		v, err := input.Version(body["version"])
		if err != nil {
			return err
		}
		res, err := lagekarte.AbschnittDelete(app.DB, ctx, id, input.Int(body["nr"], 0), v)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"abschnitt": res.Abschnitt, "features": res.Features})
	case "feature_create":
		id, err := input.RequireID(karteID)
		if err != nil {
			return err
		}
		f, err := lagekarte.FeatureCreate(app.DB, ctx, id, body)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"feature": f})
	case "feature_update":
		id, err := input.RequireID(body["id"])
		if err != nil {
			return err
		}
		v, err := input.Version(body["version"])
		if err != nil {
			return err
		}
		f, err := lagekarte.FeatureUpdate(app.DB, ctx, id, v, body)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"feature": f})
	case "feature_delete":
		id, err := input.RequireID(body["id"])
		if err != nil {
			return err
		}
		v, err := input.Version(body["version"])
		if err != nil {
			return err
		}
		d, err := lagekarte.FeatureDelete(app.DB, ctx, id, v)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"deleted": d})
	case "import_json":
		md := input.Map(body["map_data"])
		if md == nil {
			return apierr.New("invalid_map_data", 400)
		}
		title := input.Str(body["title"], 200)
		if title == "" {
			title = "Importierte Lagekarte"
		}
		k, err := lagekarte.ImportMapData(app.DB, ctx, title, md, lat, lng)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"karte": k})
	default:
		return apierr.New("unknown_action", 400)
	}
	return nil
}
