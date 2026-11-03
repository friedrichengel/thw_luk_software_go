// Package updateapi: System-Update (nur Kontorolle admin), Port der PHP-Fassung.
//
// Die App fuehrt ein Docker-Update nicht selbst aus. Sie legt Archive und
// Trigger in UPDATE_DIR ab (internal/update), der Host-Dienst luk-updater macht
// docker load / pull / compose up und meldet den Stand in status.json zurueck.
// Im Binary-Modus tauscht die App ihr eigenes Binary aus.
//
//	GET  ?action=status
//	     -> {ok, enabled, mode, image, version, status:{state,step,message,...}, pending:[...], log}
//	POST JSON {action:"begin", csrf, name, size, sha256}        -> {ok, upload_id}
//	POST multipart action=chunk, csrf, upload_id, index, chunk=<Datei> -> {ok, received, chunks}
//	     Chunks muessen lueckenlos in Reihenfolge kommen (index 0,1,2,...), max. 16 MB.
//	POST JSON {action:"finish", csrf, upload_id, minisig?}      -> {ok, name}
//	     Docker: prueft Groesse, SHA-256 und Archivkopf, verschiebt nach inbox/.
//	     Binary: prueft SHA-256, Signatur (wenn UPDATE_PUBKEY), stellt <exe>.new bereit.
//	POST JSON {action:"apply", csrf, name?}                      -> {ok}   Binary: bereitgestelltes Binary einspielen + Neustart
//	POST JSON {action:"cancel", csrf, upload_id}                 -> {ok}
//	POST JSON {action:"pull", csrf}                              -> {ok}   Docker: Image aus Registry ziehen
//	POST JSON {action:"rollback", csrf}                          -> {ok}   vorheriges Image/Binary wiederherstellen
//	POST JSON {action:"discard", csrf}                           -> {ok, removed}  wartende Eintraege verwerfen
//
// Fehler: unauthorized, forbidden, not_enabled, busy, bad_name, too_large,
//
//	bad_sha256, bad_index, bad_chunk, incomplete, sha256_mismatch,
//	not_an_archive, not_a_binary, signature_missing, signature_invalid,
//	binary_invalid, no_previous, unknown_upload, io, csrf, unknown_action
package updateapi

import (
	"net/http"
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/auth"
	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
	"github.com/friedrichengel/thw_luk_software_go/internal/input"
)

func Register(mux *http.ServeMux, app *httpx.App) {
	h := func(w http.ResponseWriter, r *http.Request) {
		if err := handle(app, w, r); err != nil {
			app.Fail(w, r, err)
		}
	}
	mux.HandleFunc("/api/update", h)
}

func handle(app *httpx.App, w http.ResponseWriter, r *http.Request) error {
	s := app.Sess(r)
	if !auth.IsLoggedIn(s) {
		return apierr.New("unauthorized", 401)
	}
	if !auth.IsAdmin(s) {
		return apierr.New("forbidden", 403)
	}
	user := app.Auth.UserLabel(s)
	upd := app.Update

	switch r.Method {
	case http.MethodGet:
		action := httpx.Query(r, "action")
		if action == "" {
			action = "status"
		}
		if action != "status" {
			return apierr.New("unknown_action", 400)
		}
		app.OK(w, upd.Info())
		return nil
	case http.MethodPost:
	default:
		return apierr.New("unknown_action", 405)
	}

	// Chunk-Upload kommt als multipart/form-data, alles andere als JSON.
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, 20*1024*1024)
		if err := r.ParseMultipartForm(1 * 1024 * 1024); err != nil {
			return apierr.New("bad_chunk", 400)
		}
		if err := app.CSRFForm(r); err != nil {
			return err
		}
		if r.FormValue("action") != "chunk" {
			return apierr.New("unknown_action", 400)
		}
		f, hdr, err := r.FormFile("chunk")
		if err != nil {
			return apierr.New("bad_chunk", 400)
		}
		defer f.Close()
		received, chunks, err := upd.AppendChunk(r.FormValue("upload_id"), input.Int(r.FormValue("index"), -1), f, hdr.Size)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"received": received, "chunks": chunks})
		return nil
	}

	b, err := app.Body(r)
	if err != nil {
		return err
	}
	action, _ := b["action"].(string)
	switch action {
	case "begin":
		id, err := upd.BeginUpload(input.Str(b["name"], 160), input.Int64(b["size"], 0), strings.ToLower(input.Str(b["sha256"], 64)), user)
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"upload_id": id})
	case "finish":
		sig := input.StrRaw(b["minisig"], 4096)
		id, _ := b["upload_id"].(string)
		name, err := upd.FinishUpload(id, sig, user)
		if err != nil {
			return err
		}
		app.Log.Printf("update: %s von %s bereitgestellt", name, user)
		app.OK(w, map[string]any{"name": name, "mode": upd.Mode()})
	case "apply":
		if err := upd.ApplyBinary(input.Str(b["name"], 160), user); err != nil {
			return err
		}
		app.Log.Printf("update: Binary eingespielt von %s", user)
		app.OK(w, nil)
	case "cancel":
		id, _ := b["upload_id"].(string)
		upd.CancelUpload(id)
		app.OK(w, nil)
	case "pull", "rollback":
		if err := upd.Trigger(action, user); err != nil {
			return err
		}
		app.Log.Printf("update: %s ausgeloest von %s", action, user)
		app.OK(w, nil)
	case "discard":
		n, err := upd.DiscardPending()
		if err != nil {
			return err
		}
		app.OK(w, map[string]any{"removed": n})
	default:
		return apierr.New("unknown_action", 400)
	}
	return nil
}
