// Package httpx ist das gemeinsame HTTP-Grundgeruest: die App mit ihren
// Diensten, Helfer fuer JSON-APIs (Body, CSRF, Zugriffskontext, Fehler),
// Seiten-Gates (Login, Admin, Einsatz-Nutzer, Leiter), der PageHelper-Port
// (Kopf, Fussleiste, window.STAB) und die Auslieferung statischer Dateien.
// Die eigentlichen Handler liegen in internal/api/* und internal/pages.
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/auth"
	"github.com/friedrichengel/thw_luk_software_go/internal/changelog"
	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/divera"
	"github.com/friedrichengel/thw_luk_software_go/internal/org"
	"github.com/friedrichengel/thw_luk_software_go/internal/session"
	"github.com/friedrichengel/thw_luk_software_go/internal/update"
	"github.com/friedrichengel/thw_luk_software_go/internal/upstream"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
	"github.com/friedrichengel/thw_luk_software_go/internal/uuid"
)

const MaxBodyBytes = 5 * 1024 * 1024

type App struct {
	Cfg       *config.Config
	DB        *db.DB
	Auth      *auth.Auth
	Sessions  *session.Store
	Changelog *changelog.Service
	Update    *update.Channel
	Feeds     *upstream.Feeds // oeffentliche Hilfsmittel-Quellen (NINA, LHP, RSS)
	Divera    *divera.Client
	// OrgHome liefert den Standort einer Organisation aus den Stammdaten
	// ({lat, lng, name}) oder nil; gesetzt vom Server.
	OrgHome func(orgID int64) map[string]any
	// Center: Kartenmittelpunkt des Servers (MAP_CENTER_*, sonst "karte" der
	// Stammdaten, sonst Standard); gesetzt vom Server.
	Center config.MapCenter
	Log    *log.Logger
}

// Sess liefert die Sitzung des Requests.
func (a *App) Sess(r *http.Request) *session.Session {
	return session.FromContext(r.Context())
}

// Principal baut den Zugriffs-Principal aus der Sitzung (mit Anzeigename).
func (a *App) Principal(r *http.Request) access.Principal {
	s := a.Sess(r)
	p := access.Principal{UID: auth.UID(s), Role: auth.Role(s)}
	if p.UID > 0 {
		if u, _ := user.FindByID(a.DB, p.UID); u != nil {
			p.Label = user.Label(u)
			p.Role = u.Role
		}
	}
	return p
}

// CurrentUser laedt das Konto der Sitzung (nil ohne Anmeldung).
func (a *App) CurrentUser(r *http.Request) *user.User {
	return a.Auth.User(a.Sess(r))
}

// UserOrgID: Organisation des angemeldeten Nutzers oder 0.
func (a *App) UserOrgID(r *http.Request) int64 {
	return a.CurrentUser(r).OrgIDValue()
}

// MapCenter: wirksamer Kartenmittelpunkt einer Organisation: eigener Eintrag
// der Organisation, sonst Standort aus den Stammdaten (OrgHome), sonst der
// Mittelpunkt des Servers (Center). orgID 0 = Server.
func (a *App) MapCenter(orgID int64) (lat, lng float64, name string) {
	lat, lng, name = a.Center.Lat, a.Center.Lng, a.Center.Name
	if orgID <= 0 {
		return
	}
	o, _ := org.FindByID(a.DB, orgID)
	if o == nil {
		return
	}
	if o.HasMapCenter() {
		lat, lng, name = *o.MapLat, *o.MapLng, o.Name
	} else if a.OrgHome != nil {
		if h := a.OrgHome(orgID); h != nil {
			lat, _ = h["lat"].(float64)
			lng, _ = h["lng"].(float64)
			name, _ = h["name"].(string)
		}
	}
	if o.MapName != "" {
		name = o.MapName
	}
	return
}

// RSSFeeds: wirksame Nachrichten-Feeds der Dashboard-Kacheln einer
// Organisation (eigene oder Standard).
func (a *App) RSSFeeds(orgID int64) []org.RSSFeed {
	var o *org.Org
	if orgID > 0 {
		o, _ = org.FindByID(a.DB, orgID)
	}
	return o.EffectiveFeeds()
}

// RSSHostAllowedFor: zusaetzlich zu RSS_ALLOWED_HOSTS die Hosts der
// Standard-Feeds (eingebaut und aus den Stammdaten) und der eigenen Feeds der
// Organisation (fuer den RSS-Abruf des Dashboards).
func (a *App) RSSHostAllowedFor(orgID int64) func(host string) bool {
	var o *org.Org
	if orgID > 0 {
		o, _ = org.FindByID(a.DB, orgID)
	}
	return func(host string) bool { return org.DefaultFeedHostAllowed(host) || o.FeedHostAllowed(host) }
}

// DiveraSource: wirksamer Divera-Zugang des angemeldeten Nutzers (nil = keiner).
func (a *App) DiveraSource(r *http.Request) (*divera.Source, error) {
	u := a.CurrentUser(r)
	if u == nil {
		return nil, nil
	}
	return divera.Resolve(a.DB, u, a.Cfg.Secret)
}

// DiveraClientConfig: Divera-Teil von window.STAB inklusive Heimatstandort
// der Organisation (fuer "Zuruecksetzen in den OV" in der DiveraMap).
func (a *App) DiveraClientConfig(src *divera.Source) map[string]any {
	cfg := divera.ClientConfig(src)
	if src != nil && a.OrgHome != nil {
		if h := a.OrgHome(src.OrgID); h != nil {
			cfg["home"] = h
		}
	}
	return cfg
}

// ---------------------------------------------------------------------------
// JSON-API

// OK schreibt {ok:true, ...data}.
func (a *App) OK(w http.ResponseWriter, data map[string]any) {
	out := map[string]any{"ok": true}
	for k, v := range data {
		out[k] = v
	}
	a.JSON(w, 200, out)
}

// JSON schreibt eine beliebige Antwort.
func (a *App) JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	if err := enc.Encode(v); err != nil {
		a.Log.Printf("api: encode: %v", err)
	}
}

// Fail uebersetzt einen Fehler in {error:code,...} mit HTTP-Status.
func (a *App) Fail(w http.ResponseWriter, r *http.Request, err error) {
	var e *apierr.Error
	if errors.As(err, &e) {
		out := map[string]any{"error": e.Code}
		for k, v := range e.Extra {
			if !strings.HasPrefix(k, "_") {
				out[k] = v
			}
		}
		a.JSON(w, e.Status, out)
		return
	}
	a.Log.Printf("API %s (%s): %v", r.URL.Path, r.URL.Query().Get("action"), err)
	a.JSON(w, 500, map[string]any{"error": "server_error"})
}

// Body liest den JSON-Body eines POST-Requests und prueft das CSRF-Token.
func (a *App) Body(r *http.Request) (map[string]any, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil || len(raw) > MaxBodyBytes {
		return nil, apierr.New("payload_too_large", 413)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		return nil, apierr.New("invalid_json", 400)
	}
	token, _ := body["csrf"].(string)
	if !auth.CSRFCheck(a.Sess(r), token) {
		return nil, apierr.New("csrf", 400)
	}
	return body, nil
}

// CSRFForm prueft das Token eines Formular-/Multipart-Requests.
func (a *App) CSRFForm(r *http.Request) error {
	if !auth.CSRFCheck(a.Sess(r), r.FormValue("csrf")) {
		return apierr.New("csrf", 400)
	}
	return nil
}

// RequireLoginAPI: Session-Nutzer erforderlich (auch Admin).
func (a *App) RequireLoginAPI(r *http.Request) (int64, error) {
	s := a.Sess(r)
	if !auth.IsLoggedIn(s) {
		return 0, apierr.New("unauthorized", 401)
	}
	return auth.UID(s), nil
}

// RequireEinsatzUserAPI: Session-Nutzer mit Einsatzrecht (user/leiter) erforderlich.
func (a *App) RequireEinsatzUserAPI(r *http.Request) (int64, error) {
	s := a.Sess(r)
	if !auth.IsLoggedIn(s) {
		return 0, apierr.New("unauthorized", 401)
	}
	if !auth.IsEinsatzUser(s) {
		return 0, apierr.New("forbidden_admin", 403)
	}
	return auth.UID(s), nil
}

// Ctx: Kontext fuer einen Einsatz (Session-Nutzer).
func (a *App) Ctx(r *http.Request, uuidStr string) (*access.Ctx, error) {
	s := a.Sess(r)
	if auth.IsLoggedIn(s) && auth.IsAdmin(s) {
		return nil, apierr.New("forbidden_admin", 403)
	}
	if !uuid.Valid(uuidStr) {
		return nil, apierr.New("invalid_uuid", 400)
	}
	ctx, err := access.Resolve(a.DB, a.Principal(r), uuidStr)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		if !auth.IsLoggedIn(s) {
			return nil, apierr.New("unauthorized", 401)
		}
		return nil, apierr.New("forbidden", 403)
	}
	return ctx, nil
}

// CtxWrite: schreibender Kontext: kein Freigabelink, Einsatz offen, Capability vorhanden.
func (a *App) CtxWrite(r *http.Request, uuidStr, cap string) (*access.Ctx, error) {
	ctx, err := a.Ctx(r, uuidStr)
	if err != nil {
		return nil, err
	}
	if ctx.IsShare {
		return nil, apierr.New("forbidden", 403)
	}
	if !ctx.IsOpen {
		return nil, apierr.New("einsatz_closed", 409)
	}
	if !ctx.Can(cap) {
		return nil, apierr.New("forbidden", 403)
	}
	return ctx, nil
}

// CtxManage: Eigentuemer-Kontext.
func (a *App) CtxManage(r *http.Request, uuidStr string) (*access.Ctx, error) {
	ctx, err := a.Ctx(r, uuidStr)
	if err != nil {
		return nil, err
	}
	if ctx.IsShare || !ctx.CanManage {
		return nil, apierr.New("forbidden", 403)
	}
	return ctx, nil
}

// ForbiddenOrClosed: 403 forbidden bei offenem, 409 einsatz_closed bei geschlossenem Einsatz.
func ForbiddenOrClosed(ctx *access.Ctx) error {
	if ctx.IsOpen {
		return apierr.New("forbidden", 403)
	}
	return apierr.New("einsatz_closed", 409)
}

// Query liefert einen GET-Parameter.
func Query(r *http.Request, key string) string {
	return r.URL.Query().Get(key)
}

// ---------------------------------------------------------------------------
// Seiten-Gates

// Redirect: 302 auf einen internen Pfad.
func Redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusFound)
}

// SafeRedirect laesst nur interne Pfade zu (kein //evil.com, kein CRLF).
func SafeRedirect(p string) string {
	if p == "/" {
		return p
	}
	if len(p) < 2 || p[0] != '/' || p[1] == '/' || p[1] == '\\' || strings.ContainsAny(p, "\r\n") {
		return ""
	}
	return p
}

// RequireLogin erzwingt Anmeldung (Umleitung auf Login/2FA/Passwortwechsel).
// Liefert false, wenn bereits umgeleitet wurde.
func (a *App) RequireLogin(w http.ResponseWriter, r *http.Request) bool {
	s := a.Sess(r)
	if !auth.IsLoggedIn(s) {
		if s.Has("2fa_pending") {
			Redirect(w, r, "/twofactor")
			return false
		}
		Redirect(w, r, "/login?redirect="+url.QueryEscape(r.URL.RequestURI()))
		return false
	}
	if auth.MustChangePassword(s) {
		p := r.URL.Path
		if !strings.HasPrefix(p, "/settings") && !strings.HasPrefix(p, "/logout") {
			// Ziel merken: nach dem Passwortwechsel geht es dorthin weiter
			Redirect(w, r, "/settings?forced=1&redirect="+url.QueryEscape(r.URL.RequestURI()))
			return false
		}
	}
	return true
}

func (a *App) RequireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !a.RequireLogin(w, r) {
		return false
	}
	if !auth.IsAdmin(a.Sess(r)) {
		a.ForbiddenPage(w, r, "F&uuml;r diese Seite sind Adminrechte erforderlich.")
		return false
	}
	return true
}

// RequireEinsatzUser: Einsatz-Seiten: user oder leiter. Admins werden abgewiesen.
func (a *App) RequireEinsatzUser(w http.ResponseWriter, r *http.Request) bool {
	if !a.RequireLogin(w, r) {
		return false
	}
	if !auth.IsEinsatzUser(a.Sess(r)) {
		a.ForbiddenPage(w, r, "Admin-Konten haben keinen Zugriff auf Eins&auml;tze. Bitte mit einem Nutzer- oder Leiterkonto anmelden.")
		return false
	}
	return true
}

// RequireOrgAdmin: Einsatz-Konto mit Org-Admin-Flag. Liefert das Konto oder
// nil (dann wurde bereits umgeleitet bzw. 403 gezeigt).
func (a *App) RequireOrgAdmin(w http.ResponseWriter, r *http.Request) *user.User {
	if !a.RequireLogin(w, r) {
		return nil
	}
	u := a.CurrentUser(r)
	if u == nil || !u.IsOrgAdmin() {
		a.ForbiddenPage(w, r, "F&uuml;r diese Seite ist das Recht &bdquo;Org-Admin&ldquo; der eigenen Organisation erforderlich.")
		return nil
	}
	return u
}

func (a *App) RequireLeiter(w http.ResponseWriter, r *http.Request) bool {
	if !a.RequireLogin(w, r) {
		return false
	}
	if !auth.IsLeiter(a.Sess(r)) {
		a.ForbiddenPage(w, r, "F&uuml;r diese Aktion ist die Kontorolle &bdquo;Leiter&ldquo; erforderlich.")
		return false
	}
	return true
}

// ForbiddenPage: 403-Seite mit Hinweistext (HTML).
func (a *App) ForbiddenPage(w http.ResponseWriter, r *http.Request, msgHTML string) {
	s := a.Sess(r)
	back := "/login"
	if auth.IsLoggedIn(s) {
		back = auth.HomeURL(s)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	io.WriteString(w, `<!doctype html><html lang="de"><head><meta charset="utf-8"><title>Zugriff verweigert</title>`+
		`<link rel="stylesheet" href="/css/auth.css"></head><body class="auth-page"><div class="card">`+
		`<h1>Zugriff verweigert</h1><p>`+msgHTML+`</p>`+
		`<p><a href="`+E(back)+`">Zur&uuml;ck</a> &nbsp;|&nbsp; <a href="/logout">Abmelden</a></p></div></body></html>`)
}

// RequirePage: fuer HTML-Seiten im Einsatz-Kontext: liefert den Kontext oder
// leitet um/zeigt 403 (dann nil). Login ist immer Pflicht; Lesezugriff
// anderer Staebe laeuft ueber freigegebene Verknuepfungen (IsShare).
func (a *App) RequirePage(w http.ResponseWriter, r *http.Request, uuidStr, page string) *access.Ctx {
	s := a.Sess(r)
	ctx, err := access.Resolve(a.DB, a.Principal(r), uuidStr)
	if err != nil {
		a.Log.Printf("page %s: %v", page, err)
		http.Error(w, "Serverfehler", 500)
		return nil
	}
	if ctx != nil {
		// Passwortwechsel-Zwang etc. durchsetzen (gilt auch fuer Lesezugriff)
		if !a.RequireEinsatzUser(w, r) {
			return nil
		}
		access.LogAccess(a.DB, ctx, a.Auth.IPHash(r), page)
		return ctx
	}
	if !auth.IsLoggedIn(s) {
		Redirect(w, r, "/login?redirect="+url.QueryEscape(r.URL.RequestURI()))
		return nil
	}
	if auth.IsAdmin(s) {
		a.ForbiddenPage(w, r, "Admin-Konten haben keinen Zugriff auf Eins&auml;tze.")
		return nil
	}
	Redirect(w, r, "/einsatz?denied=1")
	return nil
}

// OptionalCtx loest den Einsatz-Kontext aus ?einsatz=... auf (Hilfsmittel-Seiten), sonst nil.
func (a *App) OptionalCtx(r *http.Request) *access.Ctx {
	u := Query(r, "einsatz")
	if u == "" || !uuid.Valid(u) || !auth.IsEinsatzUser(a.Sess(r)) {
		return nil
	}
	ctx, _ := access.Resolve(a.DB, a.Principal(r), u)
	return ctx
}
