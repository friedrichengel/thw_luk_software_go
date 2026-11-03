// Package schadenskontenapi: API fuer Schadenskonten eines Einsatzes.
//
// GET  ?action=list&einsatz=U        Schadenskonten (mit refs + fremd) + Tree + seq
//
// POST (JSON-Body mit csrf; Schreibrecht "schadenskonten"):
//
//	create        {einsatz, id?, title, description?, taktische_zeichen?}
//	update        {einsatz, id, version, title?, description?, taktische_zeichen?}
//	delete        {einsatz, id, version}
//	reorder       {einsatz, order: [id, ...]}
//	ref_add       {einsatz, id, ref_kind, ref_id, force?}  409 ref_assigned wenn anderswo zugeordnet
//	ref_remove    {einsatz, id, ref_kind, ref_id}
//	fremd_create  {einsatz, id (Schadenskonto), kind, bezeichnung, funkruf, ov, staerke, note}
//	fremd_update  {einsatz, fremd_id, version, fields...}
//	fremd_delete  {einsatz, fremd_id, version}
package schadenskontenapi

import (
	"net/http"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/einsatz"
	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
	"github.com/friedrichengel/thw_luk_software_go/internal/input"
	"github.com/friedrichengel/thw_luk_software_go/internal/schadenskonten"
	"github.com/friedrichengel/thw_luk_software_go/internal/stab"
)

func Register(mux *http.ServeMux, app *httpx.App) {
	h := func(w http.ResponseWriter, r *http.Request) {
		if err := serve(app, w, r); err != nil {
			app.Fail(w, r, err)
		}
	}
	mux.HandleFunc("/api/schadenskonten", h)
}

func serve(app *httpx.App, w http.ResponseWriter, r *http.Request) error {
	action := httpx.Query(r, "action")
	switch r.Method {
	case http.MethodGet:
		if action != "list" {
			return apierr.New("unknown_action", 400)
		}
		ctx, err := app.Ctx(r, httpx.Query(r, "einsatz"))
		if err != nil {
			return err
		}
		einsatzID := ctx.Einsatz.ID
		access.LogAccess(app.DB, ctx, app.Auth.IPHash(r), "api:schadenskonten")
		items, err := schadenskonten.List(app.DB, einsatzID, false)
		if err != nil {
			return err
		}
		tree, err := einsatz.TreeOf(app.DB, einsatzID, false)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"ctx": access.ClientContext(ctx), "items": items, "tree": tree})
		return nil
	case http.MethodPost:
		return handlePost(app, w, r, action)
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
	ctx, err := app.CtxWrite(r, input.Raw(body["einsatz"]), stab.CapSchadenskonten)
	if err != nil {
		return err
	}
	idAndVersion := func(key string) (string, int, error) {
		id, err := input.RequireID(body[key])
		if err != nil {
			return "", 0, err
		}
		v, err := input.Version(body["version"])
		return id, v, err
	}

	switch action {
	case "create":
		it, err := schadenskonten.Create(app.DB, ctx, body)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"item": it})
	case "update":
		id, v, err := idAndVersion("id")
		if err != nil {
			return err
		}
		it, err := schadenskonten.Update(app.DB, ctx, id, v, body)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"item": it})
	case "delete":
		id, v, err := idAndVersion("id")
		if err != nil {
			return err
		}
		d, err := schadenskonten.Delete(app.DB, ctx, id, v)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"deleted": d})
	case "reorder":
		order := input.List(body["order"])
		if order == nil {
			order = []any{}
		}
		if err := schadenskonten.Reorder(app.DB, ctx, order); err != nil {
			return err
		}
		app.OK(w, nil)
	case "ref_add":
		id, err := input.RequireID(body["id"])
		if err != nil {
			return err
		}
		res, err := schadenskonten.RefAdd(app.DB, ctx, id, input.Raw(body["ref_kind"]), input.Raw(body["ref_id"]), !input.Empty(body["force"]))
		if err != nil {
			return err
		}
		app.OK(w, res)
	case "ref_remove":
		id, err := input.RequireID(body["id"])
		if err != nil {
			return err
		}
		ref, err := schadenskonten.RefRemove(app.DB, ctx, id, input.Raw(body["ref_kind"]), input.Raw(body["ref_id"]))
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"ref": ref})
	case "fremd_create":
		id, err := input.RequireID(body["id"])
		if err != nil {
			return err
		}
		f, err := schadenskonten.FremdCreate(app.DB, ctx, id, body)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"fremd": f})
	case "fremd_update":
		id, v, err := idAndVersion("fremd_id")
		if err != nil {
			return err
		}
		f, err := schadenskonten.FremdUpdate(app.DB, ctx, id, v, body)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"fremd": f})
	case "fremd_delete":
		id, v, err := idAndVersion("fremd_id")
		if err != nil {
			return err
		}
		d, err := schadenskonten.FremdDelete(app.DB, ctx, id, v)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"deleted": d})
	default:
		return apierr.New("unknown_action", 400)
	}
	return nil
}
