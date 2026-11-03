// Package einsatzapi: Einsaetze, Mitglieder/Rollen, Fuehrungsharke (Knoten,
// Mitglieder, Schichten), Status, Log, Auf Anfahrt, Freigabelinks,
// Verknuepfungen, Export/Import, Snapshots.
//
// Alle Antworten JSON. Fehler: {"error": "<code>", ...} mit HTTP-Status.
// Versionskonflikt: 409 {"error":"conflict","current":{...aktuelle Zeile}}.
// Geschlossener Einsatz: 409 {"error":"einsatz_closed"}.
//
//	GET  ?action=list                                  eigene/freigegebene Einsaetze
//	GET  ?action=users                                 Nutzer fuer Rollenzuweisung (nur Leiter)
//	GET  ?action=load&einsatz=U                        Kontext, Mitglieder, Tree, Lagekarten-Liste (loggt Zugriff)
//	GET  ?action=tree&einsatz=U                        Knoten, Mitglieder, Schichten + seq
//	GET  ?action=events&einsatz=U&since=N              Live-Sync: Ereignisse > N
//	GET  ?action=seq&einsatz=U                         nur aktuelle seq (Sync-Cursor)
//	GET  ?action=log&einsatz=U                         Aenderungslog + Zugriffslog (Leiter Stab / Eigentuemer)
//	GET  ?action=anfahrt&einsatz=U                     Einheiten mit Status angefordert / auf Anfahrt
//	GET  ?action=export&einsatz=U                      JSON-Download des gesamten Einsatzes (Leiter Stab / Eigentuemer)
//	GET  ?action=share_list&einsatz=U                  Freigabelinks (Eigentuemer)
//	GET  ?action=link_list&einsatz=U                   Verknuepfungen ein-/ausgehend (Eigentuemer)
//	GET  ?action=snapshots&einsatz=U                   Snapshots (Eigentuemer)
//	GET  ?action=snapshot_download&einsatz=U&id=N      Snapshot-JSON (Eigentuemer)
//
//	POST (JSON-Body mit csrf):
//	  create            {title, ebene, stab_name, note}                       (Leiter)
//	  update_meta       {einsatz, version, title?, ebene?, stab_name?, note?, vorwarn_min?,
//	                     bereich_key?, ort_name?, ort_lat?, ort_lng?, stein_aktiv?}        (Eigentuemer)
//	  close | reopen | delete  {einsatz}                                      (Eigentuemer)
//	  member_set        {einsatz, user_id, roles[]}  leere roles = entfernen (Eigentuemer)
//	  org_member_set    {einsatz, org_id, roles[]}   Freigabe an eine Organisation (Eigentuemer)
//	GET ?action=orgs                                  Organisationen fuer die Freigabe (Leiter)
//	  node_create       {einsatz, id?, parent_id?, type, bezeichnung, note, funkruf, ov, kraft_key?, before_id?, after_id?}
//	  node_update       {einsatz, id, version, fields{...}}
//	  node_move         {einsatz, id, version, parent_id, before_id?, after_id?}
//	  node_delete       {einsatz, id, version}
//	  node_status       {einsatz, id, version, status}   nur type=einheit -> 409 fuehrung_no_status
//	  node_to_mitglied  {einsatz, id, version, target_node_id}
//	  mitglied_create   {einsatz, node_id, kind, fields...}
//	  mitglied_update   {einsatz, id, version, fields{...}}
//	  mitglied_move     {einsatz, id, version, node_id}
//	  mitglied_delete   {einsatz, id, version}
//	  mitglied_status   {einsatz, id, version, status}
//	  mitglied_to_node  {einsatz, id, version, parent_id}
//	  schicht_create    {einsatz, node_id, mitglied_id?, von, bis, staerke, name, telefon, funkruf, ov, note, aktiv, status?}
//	  schicht_update    {einsatz, id, version, fields{...}}
//	  schicht_delete    {einsatz, id, version}
//	  schicht_status    {einsatz, id, version, status}
//	  share_create      {einsatz, label, expires_at?}                         (Eigentuemer)
//	  share_revoke      {einsatz, id}                                         (Eigentuemer)
//	  link_request      {einsatz, token|url, label?}                          (Eigentuemer)
//	  link_remove       {einsatz, id}                                         (Eigentuemer)
//	  link_decide       {einsatz, id, approve}                                (Eigentuemer)
//	  snapshot_create   {einsatz}                                             (Eigentuemer)
//	  import            multipart/form-data: csrf, file (JSON-Export)         (Leiter)
package einsatzapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/auth"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/einsatz"
	"github.com/friedrichengel/thw_luk_software_go/internal/export"
	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
	"github.com/friedrichengel/thw_luk_software_go/internal/input"
	"github.com/friedrichengel/thw_luk_software_go/internal/kraefte"
	"github.com/friedrichengel/thw_luk_software_go/internal/lagekarte"
	"github.com/friedrichengel/thw_luk_software_go/internal/org"
	"github.com/friedrichengel/thw_luk_software_go/internal/schadenskonten"
	"github.com/friedrichengel/thw_luk_software_go/internal/stab"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

type handler struct {
	app *httpx.App
	kat *kraefte.Katalog
}

func Register(mux *http.ServeMux, app *httpx.App, kat *kraefte.Katalog) {
	h := &handler{app: app, kat: kat}
	mux.HandleFunc("GET /api/einsatz", h.get)
	mux.HandleFunc("POST /api/einsatz", h.post)
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	if err := h.handleGet(w, r); err != nil {
		h.app.Fail(w, r, err)
	}
}

func (h *handler) post(w http.ResponseWriter, r *http.Request) {
	if err := h.handlePost(w, r); err != nil {
		h.app.Fail(w, r, err)
	}
}

func unknown() error { return apierr.New("unknown_action", 400) }

var reFilename = regexp.MustCompile(`[^\p{L}\p{N}_.\-]+`)

// ---------------------------------------------------------------------------

func (h *handler) handleGet(w http.ResponseWriter, r *http.Request) error {
	a := h.app
	action := httpx.Query(r, "action")
	uuidStr := httpx.Query(r, "einsatz")
	switch action {
	case "list":
		uid, err := a.RequireEinsatzUserAPI(r)
		if err != nil {
			return err
		}
		items, err := einsatz.ListForUser(a.DB, uid)
		if err != nil {
			return err
		}
		shared, err := einsatz.SharedForUser(a.DB, uid)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"items": items, "shared": shared, "can_create": auth.IsLeiter(a.Sess(r))})
		return nil
	case "users":
		if _, err := a.RequireEinsatzUserAPI(r); err != nil {
			return err
		}
		if !auth.IsLeiter(a.Sess(r)) {
			return apierr.New("forbidden_leiter", 403)
		}
		users, err := user.AllEinsatzUsers(a.DB)
		if err != nil {
			return err
		}
		out := []map[string]any{}
		for _, u := range users {
			out = append(out, map[string]any{"id": u.ID, "username": u.Username, "display_name": u.DisplayNameStr(), "label": user.Label(u), "role": u.Role})
		}
		a.OK(w, map[string]any{"users": out})
		return nil
	case "orgs":
		if _, err := a.RequireEinsatzUserAPI(r); err != nil {
			return err
		}
		if !auth.IsLeiter(a.Sess(r)) {
			return apierr.New("forbidden_leiter", 403)
		}
		orgs, err := org.All(a.DB)
		if err != nil {
			return err
		}
		out := []map[string]any{}
		for _, o := range orgs {
			out = append(out, map[string]any{"id": o.ID, "name": o.Name, "label": o.Label(), "ebene": o.Ebene, "ebene_label": o.EbeneLabel()})
		}
		a.OK(w, map[string]any{"orgs": out})
		return nil
	case "load":
		ctx, err := a.Ctx(r, uuidStr)
		if err != nil {
			return err
		}
		access.LogAccess(a.DB, ctx, a.Auth.IPHash(r), "api:load")
		eid := ctx.Einsatz.ID
		members := []einsatz.Member{}
		orgMembers := []einsatz.OrgMember{}
		if !ctx.IsShare {
			if members, err = einsatz.Members(a.DB, eid); err != nil {
				return err
			}
			if orgMembers, err = einsatz.OrgMembers(a.DB, eid); err != nil {
				return err
			}
		}
		tree, err := einsatz.TreeOf(a.DB, eid, false)
		if err != nil {
			return err
		}
		karten, err := lagekarte.List(a.DB, eid, false)
		if err != nil {
			return err
		}
		refs, err := lagekarte.FeatureRefs(a.DB, eid)
		if err != nil {
			return err
		}
		sk, err := schadenskonten.List(a.DB, eid, false)
		if err != nil {
			return err
		}
		seq, err := einsatz.CurrentSeq(a.DB, eid)
		if err != nil {
			return err
		}
		since := seq - 40
		if since < 0 {
			since = 0
		}
		recent, err := einsatz.EventsSince(a.DB, eid, since, 500)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{
			"ctx": access.ClientContext(ctx), "members": members, "org_members": orgMembers, "tree": tree, "lagekarten": karten,
			"feature_refs": refs, "schadenskonten": sk, "recent_events": recent,
		})
		return nil
	case "tree":
		ctx, err := a.Ctx(r, uuidStr)
		if err != nil {
			return err
		}
		tree, err := einsatz.TreeOf(a.DB, ctx.Einsatz.ID, false)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"ctx": access.ClientContext(ctx), "tree": tree})
		return nil
	case "seq":
		ctx, err := a.Ctx(r, uuidStr)
		if err != nil {
			return err
		}
		seq, err := einsatz.CurrentSeq(a.DB, ctx.Einsatz.ID)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"seq": seq, "is_open": ctx.IsOpen})
		return nil
	case "events":
		ctx, err := a.Ctx(r, uuidStr)
		if err != nil {
			return err
		}
		since := int64(input.Int(httpx.Query(r, "since"), 0))
		eid := ctx.Einsatz.ID
		events, err := einsatz.EventsSince(a.DB, eid, since, 500)
		if err != nil {
			return err
		}
		seq := since
		if len(events) > 0 {
			seq = events[len(events)-1].Seq
		} else if cur, err := einsatz.CurrentSeq(a.DB, eid); err == nil && cur > seq {
			seq = cur
		}
		a.OK(w, map[string]any{"events": events, "seq": seq, "status": ctx.Einsatz.Status, "is_open": ctx.IsOpen})
		return nil
	case "log":
		ctx, err := a.Ctx(r, uuidStr)
		if err != nil {
			return err
		}
		if ctx.IsShare || !ctx.CanLog {
			return apierr.New("forbidden", 403)
		}
		eid := ctx.Einsatz.ID
		events, err := einsatz.Log(a.DB, eid, input.Int(httpx.Query(r, "limit"), 2000), input.Int(httpx.Query(r, "offset"), 0))
		if err != nil {
			return err
		}
		acc, err := einsatz.AccessLog(a.DB, eid, 2000)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"ctx": access.ClientContext(ctx), "events": events, "access": acc})
		return nil
	case "anfahrt":
		ctx, err := a.Ctx(r, uuidStr)
		if err != nil {
			return err
		}
		an, err := einsatz.Anfahrt(a.DB, ctx.Einsatz.ID)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"ctx": access.ClientContext(ctx), "items": an.Items, "seq": an.Seq})
		return nil
	case "export":
		ctx, err := a.Ctx(r, uuidStr)
		if err != nil {
			return err
		}
		if ctx.IsShare || !ctx.CanLog {
			return apierr.New("forbidden", 403)
		}
		data, err := export.Export(a.DB, ctx, a.Cfg.Version)
		if err != nil {
			return err
		}
		if _, err := access.Event(a.DB, ctx, "einsatz", ctx.Einsatz.UUID, "export", "Einsatz als JSON exportiert", nil, nil); err != nil {
			return err
		}
		fn := reFilename.ReplaceAllString(ctx.Einsatz.Title, "_")
		if fn == "" {
			fn = "einsatz"
		}
		b, err := json.MarshalIndent(data, "", "    ")
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="Einsatz_`+fn+"_"+time.Now().Format("20060102_1504")+`.json"`)
		w.Write(b)
		return nil
	case "share_list":
		ctx, err := a.CtxManage(r, uuidStr)
		if err != nil {
			return err
		}
		eid := ctx.Einsatz.ID
		links, err := einsatz.ShareLinks(a.DB, eid)
		if err != nil {
			return err
		}
		inc, err := einsatz.LinksIncoming(a.DB, eid)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"links": links, "incoming": inc})
		return nil
	case "link_list":
		ctx, err := a.CtxManage(r, uuidStr)
		if err != nil {
			return err
		}
		eid := ctx.Einsatz.ID
		out, err := einsatz.LinksOutgoing(a.DB, eid)
		if err != nil {
			return err
		}
		inc, err := einsatz.LinksIncoming(a.DB, eid)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"outgoing": out, "incoming": inc})
		return nil
	case "snapshots":
		ctx, err := a.CtxManage(r, uuidStr)
		if err != nil {
			return err
		}
		snaps, err := export.Snapshots(a.DB, ctx.Einsatz.ID)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"snapshots": snaps})
		return nil
	case "snapshot_download":
		ctx, err := a.CtxManage(r, uuidStr)
		if err != nil {
			return err
		}
		id := int64(input.Int(httpx.Query(r, "id"), 0))
		js, ok, err := export.SnapshotJSON(a.DB, ctx.Einsatz.ID, id)
		if err != nil {
			return err
		}
		if !ok {
			return apierr.New("not_found", 404)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="Snapshot_%d.json"`, id))
		io.WriteString(w, js)
		return nil
	}
	return unknown()
}

// ---------------------------------------------------------------------------

func (h *handler) handlePost(w http.ResponseWriter, r *http.Request) error {
	a := h.app
	action := httpx.Query(r, "action")
	if action == "import" {
		return h.handleImport(w, r)
	}
	body, err := a.Body(r)
	if err != nil {
		return err
	}
	uid, err := a.RequireEinsatzUserAPI(r)
	if err != nil {
		return err
	}
	uuidStr := input.Raw(body["einsatz"])
	s := a.Sess(r)

	idOf := func() (string, error) { return input.RequireID(body["id"]) }
	versionOf := func() (int, error) { return input.Version(body["version"]) }
	fieldsOf := func() map[string]any {
		if f := input.Map(body["fields"]); f != nil {
			return f
		}
		return body
	}

	switch action {
	case "create":
		if !auth.IsLeiter(s) {
			return apierr.New("forbidden_leiter", 403)
		}
		title := input.Str(body["title"], 200)
		if title == "" {
			return apierr.New("missing_title", 400)
		}
		ebene := input.Raw(body["ebene"])
		if ebene == "" {
			ebene = "ov"
		}
		if !stab.EbeneExists(ebene) {
			return apierr.New("invalid_ebene", 400)
		}
		p := a.Principal(r)
		p.UID = uid
		u, err := einsatz.CreateEinsatz(a.DB, p, title, ebene, input.Str(body["stab_name"], 160), input.StrRaw(body["note"], 2000))
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"uuid": u})
		return nil
	case "update_meta":
		return h.updateMeta(w, r, uuidStr, body)
	case "close":
		ctx, err := a.CtxManage(r, uuidStr)
		if err != nil {
			return err
		}
		if !ctx.IsOpen {
			a.OK(w, map[string]any{"noop": true})
			return nil
		}
		err = a.DB.Tx(func(q db.Q) error {
			if _, err := export.Snapshot(q, ctx, "close", a.Cfg.Version); err != nil {
				return err
			}
			if _, err := q.Exec("UPDATE einsaetze SET status = 'closed', closed_at = ?, closed_by = ?, version = version + 1, updated_at = ? WHERE id = ?",
				db.Now(), ctx.UID, db.Now(), ctx.Einsatz.ID); err != nil {
				return err
			}
			_, err := access.Event(q, ctx, "einsatz", ctx.Einsatz.UUID, "close", "Einsatz abgeschlossen (Snapshot erstellt)",
				map[string]any{"status": "open"}, map[string]any{"status": "closed"})
			return err
		})
		if err != nil {
			return err
		}
		a.OK(w, nil)
		return nil
	case "reopen":
		ctx, err := a.CtxManage(r, uuidStr)
		if err != nil {
			return err
		}
		if ctx.IsOpen {
			a.OK(w, map[string]any{"noop": true})
			return nil
		}
		err = a.DB.Tx(func(q db.Q) error {
			if _, err := q.Exec("UPDATE einsaetze SET status = 'open', closed_at = NULL, closed_by = NULL, version = version + 1, updated_at = ? WHERE id = ?",
				db.Now(), ctx.Einsatz.ID); err != nil {
				return err
			}
			_, err := access.Event(q, ctx, "einsatz", ctx.Einsatz.UUID, "reopen", "Einsatz wieder geöffnet",
				map[string]any{"status": "closed"}, map[string]any{"status": "open"})
			return err
		})
		if err != nil {
			return err
		}
		a.OK(w, nil)
		return nil
	case "delete":
		ctx, err := a.CtxManage(r, uuidStr)
		if err != nil {
			return err
		}
		err = a.DB.Tx(func(q db.Q) error {
			if _, err := access.Event(q, ctx, "einsatz", ctx.Einsatz.UUID, "delete", "Einsatz gelöscht (Soft-Delete)", nil, nil); err != nil {
				return err
			}
			_, err := q.Exec("UPDATE einsaetze SET deleted_at = ?, deleted_by = ?, updated_at = ? WHERE id = ?", db.Now(), ctx.UID, db.Now(), ctx.Einsatz.ID)
			return err
		})
		if err != nil {
			return err
		}
		a.OK(w, nil)
		return nil
	case "member_set":
		ctx, err := a.CtxManage(r, uuidStr)
		if err != nil {
			return err
		}
		userID := int64(input.Int(body["user_id"], 0))
		if userID <= 0 {
			return apierr.New("invalid_user", 400)
		}
		res, err := einsatz.SetMemberRoles(a.DB, ctx, userID, body["roles"])
		if err != nil {
			return err
		}
		members, err := einsatz.Members(a.DB, ctx.Einsatz.ID)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"member": res, "members": members})
		return nil
	case "org_member_set":
		ctx, err := a.CtxManage(r, uuidStr)
		if err != nil {
			return err
		}
		orgID := int64(input.Int(body["org_id"], 0))
		if orgID <= 0 {
			return apierr.New("org_not_found", 404)
		}
		res, err := einsatz.SetOrgMemberRoles(a.DB, ctx, orgID, body["roles"])
		if err != nil {
			return err
		}
		orgMembers, err := einsatz.OrgMembers(a.DB, ctx.Einsatz.ID)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"org_member": res, "org_members": orgMembers})
		return nil

	// ---- Knoten ----
	case "node_create":
		ctx, err := a.CtxWrite(r, uuidStr, stab.CapHarke)
		if err != nil {
			return err
		}
		node, err := einsatz.CreateNode(a.DB, ctx, body)
		if err != nil {
			return err
		}
		seq, err := einsatz.CurrentSeq(a.DB, ctx.Einsatz.ID)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"node": node, "seq": seq})
		return nil
	case "node_update":
		ctx, err := a.Ctx(r, uuidStr)
		if err != nil {
			return err
		}
		allowed, err := nodeAllowedFields(ctx)
		if err != nil {
			return err
		}
		id, err := idOf()
		if err != nil {
			return err
		}
		v, err := versionOf()
		if err != nil {
			return err
		}
		node, err := einsatz.UpdateNode(a.DB, ctx, id, v, fieldsOf(), allowed)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"node": node})
		return nil
	case "node_move":
		ctx, err := a.CtxWrite(r, uuidStr, stab.CapHarke)
		if err != nil {
			return err
		}
		id, err := idOf()
		if err != nil {
			return err
		}
		v, err := versionOf()
		if err != nil {
			return err
		}
		node, err := einsatz.MoveNode(a.DB, ctx, id, v, body["parent_id"], input.Raw(body["before_id"]), input.Raw(body["after_id"]))
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"node": node})
		return nil
	case "node_delete":
		ctx, err := a.CtxWrite(r, uuidStr, stab.CapHarke)
		if err != nil {
			return err
		}
		id, err := idOf()
		if err != nil {
			return err
		}
		v, err := versionOf()
		if err != nil {
			return err
		}
		del, err := einsatz.DeleteNode(a.DB, ctx, id, v)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"deleted": del})
		return nil
	case "node_status":
		ctx, err := a.Ctx(r, uuidStr)
		if err != nil {
			return err
		}
		if !ctx.CanStatus() {
			return httpx.ForbiddenOrClosed(ctx)
		}
		id, err := idOf()
		if err != nil {
			return err
		}
		v, err := versionOf()
		if err != nil {
			return err
		}
		node, err := einsatz.SetNodeStatus(a.DB, ctx, id, v, input.Raw(body["status"]))
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"node": node})
		return nil
	case "node_to_mitglied":
		ctx, err := a.CtxWrite(r, uuidStr, stab.CapHarke)
		if err != nil {
			return err
		}
		id, err := idOf()
		if err != nil {
			return err
		}
		v, err := versionOf()
		if err != nil {
			return err
		}
		target, err := input.RequireID(body["target_node_id"])
		if err != nil {
			return err
		}
		res, err := einsatz.NodeToMitglied(a.DB, ctx, id, v, target)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"mitglied": res.Mitglied, "node_deleted": res.NodeDeleted})
		return nil

	// ---- Mitglieder (Fachberater / interne Einheit) ----
	case "mitglied_create":
		ctx, err := a.CtxWrite(r, uuidStr, stab.CapHarke)
		if err != nil {
			return err
		}
		m, err := einsatz.CreateMitglied(a.DB, ctx, body)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"mitglied": m})
		return nil
	case "mitglied_update":
		ctx, err := a.Ctx(r, uuidStr)
		if err != nil {
			return err
		}
		allowed, err := mitgliedAllowedFields(ctx)
		if err != nil {
			return err
		}
		id, err := idOf()
		if err != nil {
			return err
		}
		v, err := versionOf()
		if err != nil {
			return err
		}
		m, err := einsatz.UpdateMitglied(a.DB, ctx, id, v, fieldsOf(), allowed)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"mitglied": m})
		return nil
	case "mitglied_move":
		ctx, err := a.CtxWrite(r, uuidStr, stab.CapHarke)
		if err != nil {
			return err
		}
		id, err := idOf()
		if err != nil {
			return err
		}
		v, err := versionOf()
		if err != nil {
			return err
		}
		nodeID, err := input.RequireID(body["node_id"])
		if err != nil {
			return err
		}
		m, err := einsatz.MoveMitglied(a.DB, ctx, id, v, nodeID)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"mitglied": m})
		return nil
	case "mitglied_delete":
		ctx, err := a.CtxWrite(r, uuidStr, stab.CapHarke)
		if err != nil {
			return err
		}
		id, err := idOf()
		if err != nil {
			return err
		}
		v, err := versionOf()
		if err != nil {
			return err
		}
		del, err := einsatz.DeleteMitglied(a.DB, ctx, id, v)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"deleted": del})
		return nil
	case "mitglied_status":
		ctx, err := a.Ctx(r, uuidStr)
		if err != nil {
			return err
		}
		if !ctx.CanStatus() {
			return httpx.ForbiddenOrClosed(ctx)
		}
		id, err := idOf()
		if err != nil {
			return err
		}
		v, err := versionOf()
		if err != nil {
			return err
		}
		m, err := einsatz.SetMitgliedStatus(a.DB, ctx, id, v, input.Raw(body["status"]))
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"mitglied": m})
		return nil
	case "mitglied_to_node":
		ctx, err := a.CtxWrite(r, uuidStr, stab.CapHarke)
		if err != nil {
			return err
		}
		id, err := idOf()
		if err != nil {
			return err
		}
		v, err := versionOf()
		if err != nil {
			return err
		}
		res, err := einsatz.MitgliedToNode(a.DB, ctx, id, v, body["parent_id"])
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"node": res.Node, "mitglied_deleted": res.MitgliedDeleted})
		return nil

	// ---- Schichten ----
	case "schicht_create":
		ctx, err := a.CtxWrite(r, uuidStr, stab.CapKraefte)
		if err != nil {
			return err
		}
		sch, err := einsatz.CreateSchicht(a.DB, ctx, body)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"schicht": sch})
		return nil
	case "schicht_update":
		ctx, err := a.CtxWrite(r, uuidStr, stab.CapKraefte)
		if err != nil {
			return err
		}
		id, err := idOf()
		if err != nil {
			return err
		}
		v, err := versionOf()
		if err != nil {
			return err
		}
		sch, err := einsatz.UpdateSchicht(a.DB, ctx, id, v, fieldsOf())
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"schicht": sch})
		return nil
	case "schicht_delete":
		ctx, err := a.CtxWrite(r, uuidStr, stab.CapKraefte)
		if err != nil {
			return err
		}
		id, err := idOf()
		if err != nil {
			return err
		}
		v, err := versionOf()
		if err != nil {
			return err
		}
		del, err := einsatz.DeleteSchicht(a.DB, ctx, id, v)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"deleted": del})
		return nil
	case "schicht_status":
		ctx, err := a.Ctx(r, uuidStr)
		if err != nil {
			return err
		}
		if !ctx.CanStatus() {
			return httpx.ForbiddenOrClosed(ctx)
		}
		id, err := idOf()
		if err != nil {
			return err
		}
		v, err := versionOf()
		if err != nil {
			return err
		}
		sch, err := einsatz.SetSchichtStatus(a.DB, ctx, id, v, input.Raw(body["status"]))
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"schicht": sch})
		return nil

	// ---- Freigabelinks / Verknuepfungen / Snapshots ----
	case "share_create":
		ctx, err := a.CtxManage(r, uuidStr)
		if err != nil {
			return err
		}
		label := input.Str(body["label"], 120)
		if label == "" {
			label = "Freigabe " + time.Now().Format("02.01.2006 15:04")
		}
		var expiresAt *string
		if exp := input.Str(body["expires_at"], 32); exp != "" {
			t, ok := parseLocalTime(exp)
			if !ok {
				return apierr.New("invalid_date", 400)
			}
			s := t.UTC().Format("2006-01-02 15:04:05")
			expiresAt = &s
		}
		link, err := einsatz.CreateShareLink(a.DB, ctx, a.Cfg.Secret, a.Cfg.BaseURL, label, expiresAt)
		if err != nil {
			return err
		}
		links, err := einsatz.ShareLinks(a.DB, ctx.Einsatz.ID)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"link": link, "links": links})
		return nil
	case "link_request":
		ctx, err := a.CtxManage(r, uuidStr)
		if err != nil {
			return err
		}
		raw := input.Raw(body["token"])
		if raw == "" {
			raw = input.Raw(body["url"])
		}
		token := einsatz.ExtractToken(raw)
		if token == "" {
			return apierr.New("link_invalid", 400)
		}
		link, err := einsatz.LinkRequest(a.DB, ctx, a.Cfg.Secret, token, input.Str(body["label"], 120))
		if err != nil {
			return err
		}
		out, err := einsatz.LinksOutgoing(a.DB, ctx.Einsatz.ID)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"link": link, "outgoing": out})
		return nil
	case "link_remove":
		ctx, err := a.CtxManage(r, uuidStr)
		if err != nil {
			return err
		}
		if err := einsatz.LinkRemove(a.DB, ctx, int64(input.Int(body["id"], 0))); err != nil {
			return err
		}
		out, err := einsatz.LinksOutgoing(a.DB, ctx.Einsatz.ID)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"outgoing": out})
		return nil
	case "link_decide":
		ctx, err := a.CtxManage(r, uuidStr)
		if err != nil {
			return err
		}
		if _, err := einsatz.LinkDecide(a.DB, ctx, int64(input.Int(body["id"], 0)), input.Truthy(body["approve"])); err != nil {
			return err
		}
		inc, err := einsatz.LinksIncoming(a.DB, ctx.Einsatz.ID)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"incoming": inc})
		return nil
	case "share_revoke":
		ctx, err := a.CtxManage(r, uuidStr)
		if err != nil {
			return err
		}
		if err := einsatz.RevokeShareLink(a.DB, ctx, int64(input.Int(body["id"], 0))); err != nil {
			return err
		}
		links, err := einsatz.ShareLinks(a.DB, ctx.Einsatz.ID)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"links": links})
		return nil
	case "snapshot_create":
		ctx, err := a.CtxManage(r, uuidStr)
		if err != nil {
			return err
		}
		id, err := export.Snapshot(a.DB, ctx, "manual", a.Cfg.Version)
		if err != nil {
			return err
		}
		snaps, err := export.Snapshots(a.DB, ctx.Einsatz.ID)
		if err != nil {
			return err
		}
		a.OK(w, map[string]any{"id": id, "snapshots": snaps})
		return nil
	}
	return unknown()
}

// parseLocalTime liest datetime-local ("2026-10-01T08:00") und aehnliche Formen in Serverzeit.
func parseLocalTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func nodeAllowedFields(ctx *access.Ctx) ([]string, error) {
	if ctx.Can(stab.CapHarke) {
		return einsatz.Keys(einsatz.NodeFields), nil
	}
	if ctx.Can(stab.CapKraefte) {
		return einsatz.NodeFieldsKraefte, nil
	}
	return nil, httpx.ForbiddenOrClosed(ctx)
}

func mitgliedAllowedFields(ctx *access.Ctx) ([]string, error) {
	if ctx.Can(stab.CapHarke) {
		return einsatz.Keys(einsatz.MitgliedFields), nil
	}
	if ctx.Can(stab.CapKraefte) {
		return einsatz.MitgliedFieldsKraefte, nil
	}
	return nil, httpx.ForbiddenOrClosed(ctx)
}

// updateMeta: Einsatzdaten (Titel, Ebene, Stab, Bemerkung, Vorwarnzeit,
// Kraeftekatalog-Bereich, Einsatzort, STEIN) mit Versionspruefung.
func (h *handler) updateMeta(w http.ResponseWriter, r *http.Request, uuidStr string, body map[string]any) error {
	a := h.app
	ctx, err := a.CtxManage(r, uuidStr)
	if err != nil {
		return err
	}
	version, err := input.Version(body["version"])
	if err != nil {
		return err
	}
	e := ctx.Einsatz
	fields := einsatz.Fields{}
	if input.Has(body, "title") {
		t := input.Str(body["title"], 200)
		if t == "" {
			return apierr.New("missing_title", 400)
		}
		fields = append(fields, einsatz.Field{K: "title", V: t})
	}
	if input.Has(body, "ebene") {
		eb := input.Raw(body["ebene"])
		if !stab.EbeneExists(eb) {
			return apierr.New("invalid_ebene", 400)
		}
		fields = append(fields, einsatz.Field{K: "ebene", V: eb})
	}
	if input.Has(body, "stab_name") {
		fields = append(fields, einsatz.Field{K: "stab_name", V: input.Str(body["stab_name"], 160)})
	}
	if input.Has(body, "note") {
		fields = append(fields, einsatz.Field{K: "note", V: input.StrRaw(body["note"], 2000)})
	}
	if input.Has(body, "vorwarn_min") {
		v := int(input.Float(body["vorwarn_min"]))
		if v < 0 || v > einsatz.VorwarnMaxMin {
			return apierr.New("invalid_vorwarn", 400)
		}
		fields = append(fields, einsatz.Field{K: "vorwarn_min", V: v})
	}
	// Kraeftekatalog: eigener Bereich, Einsatzort, STEIN-Daten
	if input.Has(body, "bereich_key") {
		bk := input.Str(body["bereich_key"], 80)
		if h.kat == nil || !h.kat.ValidBereich(bk) {
			return apierr.New("invalid_bereich", 400)
		}
		fields = append(fields, einsatz.Field{K: "bereich_key", V: bk})
	}
	if input.Has(body, "ort_name") {
		fields = append(fields, einsatz.Field{K: "ort_name", V: input.Str(body["ort_name"], 160)})
	}
	if input.Has(body, "ort_lat") || input.Has(body, "ort_lng") {
		lat, lng := body["ort_lat"], body["ort_lng"]
		if lat == nil || lat == "" || lng == nil || lng == "" {
			fields = append(fields, einsatz.Field{K: "ort_lat", V: nil}, einsatz.Field{K: "ort_lng", V: nil})
		} else {
			if !input.IsNumeric(lat) || !input.IsNumeric(lng) {
				return apierr.New("invalid_coords", 400)
			}
			fl, fg := input.Float(lat), input.Float(lng)
			if fl < -90 || fl > 90 || fg < -180 || fg > 180 {
				return apierr.New("invalid_coords", 400)
			}
			fields = append(fields, einsatz.Field{K: "ort_lat", V: fl}, einsatz.Field{K: "ort_lng", V: fg})
		}
	}
	if input.Has(body, "stein_aktiv") {
		v := 0
		if input.Truthy(body["stein_aktiv"]) {
			v = 1
		}
		fields = append(fields, einsatz.Field{K: "stein_aktiv", V: v})
	}
	if len(fields) == 0 {
		return apierr.New("no_fields", 400)
	}
	if e.Version != version {
		return apierr.Conflict(access.ClientContext(ctx))
	}
	cur, err := db.QueryMap(a.DB, "SELECT * FROM einsaetze WHERE id = ?", e.ID)
	if err != nil {
		return err
	}
	set := []string{}
	params := []any{}
	for _, f := range fields {
		set = append(set, f.K+" = ?")
		params = append(params, f.V)
	}
	params = append(params, db.Now(), e.ID, version)
	res, err := a.DB.Exec("UPDATE einsaetze SET "+strings.Join(set, ", ")+", version = version + 1, updated_at = ? WHERE id = ? AND version = ?", params...)
	if err != nil {
		return err
	}
	if db.Affected(res) == 0 {
		return apierr.New("conflict", 409)
	}
	changed := []string{}
	before := map[string]any{}
	after := map[string]any{}
	for _, f := range fields {
		after[f.K] = f.V
		before[f.K] = cur[f.K]
		if db.Str(cur, f.K) != input.Raw(f.V) {
			changed = append(changed, einsatz.FieldLabel(f.K))
		}
	}
	changed = einsatz.Uniq(changed)
	if len(changed) > 0 {
		if _, err := access.Event(a.DB, ctx, "einsatz", e.UUID, "update", "Einsatzdaten geändert: "+strings.Join(changed, ", "), before, after); err != nil {
			return err
		}
	}
	ctx2, err := access.Resolve(a.DB, a.Principal(r), uuidStr)
	if err != nil {
		return err
	}
	var cc any
	if ctx2 != nil {
		cc = access.ClientContext(ctx2)
	}
	a.OK(w, map[string]any{"ctx": cc, "version": version + 1})
	return nil
}

func (h *handler) handleImport(w http.ResponseWriter, r *http.Request) error {
	a := h.app
	uid, err := a.RequireEinsatzUserAPI(r)
	if err != nil {
		return err
	}
	if !auth.IsLeiter(a.Sess(r)) {
		return apierr.New("forbidden_leiter", 403)
	}
	r.Body = http.MaxBytesReader(w, r.Body, 52*1024*1024)
	if err := r.ParseMultipartForm(8 * 1024 * 1024); err != nil {
		return apierr.New("payload_too_large", 413)
	}
	if err := a.CSRFForm(r); err != nil {
		return err
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		return apierr.New("missing_file", 400)
	}
	defer f.Close()
	if hdr.Size > 50*1024*1024 {
		return apierr.New("payload_too_large", 413)
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return apierr.New("invalid_json", 400)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil || data == nil {
		return apierr.New("invalid_json", 400)
	}
	p := a.Principal(r)
	p.UID = uid
	valid := func(string) bool { return true }
	if h.kat != nil {
		valid = h.kat.ValidBereich
	}
	res, err := export.Import(a.DB, p, data, valid)
	if err != nil {
		return err
	}
	a.OK(w, map[string]any{"uuid": res.UUID, "warnings": res.Warnings})
	return nil
}
