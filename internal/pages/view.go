package pages

import (
	"net/http"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/auth"
	"github.com/friedrichengel/thw_luk_software_go/internal/einsatz"
	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
)

// Freigabelink: /view?t=<token>
//
// Ein Freigabelink gibt KEINEN direkten Lesezugriff. Er wird vom Eigentuemer
// eines eigenen Einsatzes eingetragen (hier oder in dessen Verwaltung unter
// "Verknuepfte Einsaetze"). Danach koennen alle Mitglieder des eigenen
// Einsatzes den fremden Einsatz lesend oeffnen. Login ist Pflicht.

var viewErrorTexts = map[string]string{
	"link_invalid": "Dieser Freigabelink ist unbekannt, abgelaufen oder wurde widerrufen.",
	"link_self":    "Ein Einsatz kann nicht mit sich selbst verknüpft werden.",
	"link_exists":  "Für diesen Einsatz wurde der Link bereits eingetragen.",
	"forbidden":    "Nur der Eigentümer eines Einsatzes kann Verknüpfungen eintragen.",
}

type viewData struct {
	base
	Link  *einsatz.ShareLinkInfo
	Token string
	Info  string
	Error string
	Own   []einsatz.ListItem
}

func (p *Pages) view(w http.ResponseWriter, r *http.Request) {
	if !p.app.RequireEinsatzUser(w, r) {
		return
	}
	s := p.app.Sess(r)
	uid := auth.UID(s)
	raw := httpx.Query(r, "t")
	if r.Method == http.MethodPost && r.FormValue("t") != "" {
		raw = r.FormValue("t")
	}
	token := einsatz.ExtractToken(raw)
	var link *einsatz.ShareLinkInfo
	if token != "" {
		var err error
		link, err = einsatz.ShareLinkByToken(p.app.DB, p.app.Cfg.Secret, token)
		if err != nil {
			p.app.Log.Printf("view: %v", err)
		}
	}
	d := viewData{Link: link, Token: token}
	if link != nil && r.Method == http.MethodPost {
		if !p.csrfOrFail(w, r) {
			return
		}
		bUUID := r.FormValue("einsatz")
		var ctxB *access.Ctx
		if bUUID != "" {
			ctxB, _ = access.Resolve(p.app.DB, p.app.Principal(r), bUUID)
		}
		if ctxB == nil || ctxB.IsShare || !ctxB.CanManage || !ctxB.IsOpen {
			d.Error = viewErrorTexts["forbidden"]
		} else if _, err := einsatz.LinkRequest(p.app.DB, ctxB, p.app.Cfg.Secret, token, r.FormValue("label")); err != nil {
			if code := apierr.Code(err); code != "" {
				if t, ok := viewErrorTexts[code]; ok {
					d.Error = t
				} else {
					d.Error = "Fehler: " + code
				}
			} else {
				p.app.Log.Printf("view: link_request: %v", err)
				d.Error = "Fehler: server_error"
			}
		} else {
			d.Info = "Verknüpfung hergestellt. Alle Mitglieder von „" + ctxB.Einsatz.Title + "\" sehen „" + link.Title +
				"\" jetzt lesend in ihrer Einsatzliste unter „Freigegeben von anderen Stäben\"."
		}
	}
	// Eigene offene Einsaetze, fuer die der Nutzer Eigentuemer ist
	all, err := einsatz.ListForUser(p.app.DB, uid)
	if err != nil {
		p.app.Log.Printf("view: %v", err)
	}
	for _, e := range all {
		if e.IsOwner && e.Status == "open" {
			d.Own = append(d.Own, e)
		}
	}
	d.AppName = p.app.Cfg.Name
	d.AssetVersion = p.app.Cfg.AssetVersion
	d.CSRF = auth.CSRFToken(s)
	status := http.StatusOK
	if link == nil {
		status = http.StatusNotFound
	}
	p.renderStatus(w, status, "view.html", d)
}
