// Package pages: alle HTML-Seiten der Anwendung als Go-Handler mit
// eingebetteten html/template-Vorlagen (templates/*.html). Die Seiten
// entsprechen fachlich den Seiten der PHP-Fassung, damit CSS und JS des
// Clients unveraendert weiterlaufen; jede Seite ist nur unter dem Pfad ohne
// Endung (/harke) erreichbar.
package pages

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"regexp"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/auth"
	"github.com/friedrichengel/thw_luk_software_go/internal/divera"
	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
	"github.com/friedrichengel/thw_luk_software_go/internal/stab"
)

//go:embed templates/*.html
var tplFS embed.FS

var tpl = template.Must(template.ParseFS(tplFS, "templates/*.html"))

// Deps: Dienste, die die Seiten von aussen brauchen.
type Deps struct {
	SendMail func(to, subject, body string) error
	// OnCredentialChange wird nach Aendern/Loeschen eines Org-Zugangs gerufen
	// (Caches der betroffenen Organisation verwerfen).
	OnCredentialChange func(orgID int64, kind string)
	// CheckCredential prueft einen hinterlegten Zugang live gegen den Dienst
	// und liefert einen Ergebnistext (oder einen Fehler mit Begruendung).
	CheckCredential func(orgID int64, kind string) (string, error)
	// Stamm liefert LV, Regionalstellen und Ortsverbaende aus den Stammdaten
	// (fuer die Auswahl in der Serververwaltung).
	Stamm func() (lv map[string]any, regionen, ovs []map[string]any)
	// SeedOrganisationen legt die gewaehlten (only; nil = alle) Eintraege der
	// Stammdaten als Organisationen an (globaler Admin).
	SeedOrganisationen func(only map[string]bool) (created, existing int, err error)
	// DiveraGroups liest die Divera-Gruppen einer Organisation (fuer die
	// Auswahl, welche im Dashboard erscheinen).
	DiveraGroups func(orgID int64) ([]divera.Group, error)
	// TileTest ruft eine Probekachel von einer Quell-Vorlage ab (Admin > Server).
	TileTest func(source string) (string, error)
	// TileState liefert die wirksamen Kachel-Einstellungen (Quelle, Freigabe, Token).
	TileState func() (source string, public bool, token string)
}

type Pages struct {
	app  *httpx.App
	deps Deps
}

// Register haengt alle Seiten an den Mux (Pfad ohne Endung, z. B. /harke).
func Register(mux *http.ServeMux, app *httpx.App, deps Deps) {
	p := &Pages{app: app, deps: deps}
	get := []string{"GET"}
	both := []string{"GET", "POST"}
	reg := func(name string, methods []string, h http.HandlerFunc) {
		for _, m := range methods {
			mux.HandleFunc(m+" /"+name, h)
		}
	}
	mux.HandleFunc("GET /{$}", p.index)
	reg("index", get, p.index)
	reg("einsatz", get, p.einsatz)
	reg("harke", get, p.harke)
	reg("kraefteplanung", get, p.kraefteplanung)
	reg("lagekarte", get, p.lagekarte)
	reg("schadenskonten", get, p.schadenskonten)
	reg("anfahrt", get, p.anfahrt)
	reg("log", get, p.logPage)
	reg("verwaltung", get, p.verwaltung)
	reg("view", both, p.view)
	reg("login", both, p.login)
	reg("logout", both, p.logout)
	reg("twofactor", both, p.twofactor)
	reg("forgot_password", both, p.forgotPassword)
	reg("reset_password", both, p.resetPassword)
	reg("settings", both, p.settings)
	reg("admin", both, p.admin)
	reg("admin_update", get, p.adminUpdate)
	reg("stein", get, p.stein)
	reg("steinmap", get, p.steinmap)
	reg("divera", get, p.divera)
	reg("healthz", get, p.healthz)
}

// render fuehrt die Vorlage in einen Puffer aus (bei Fehlern sauberer 500)
// und schreibt sie mit dem gewuenschten Status.
func (p *Pages) renderStatus(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, name, data); err != nil {
		p.app.Log.Printf("pages: %s: %v", name, err)
		http.Error(w, "Serverfehler beim Aufbau der Seite.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

func (p *Pages) render(w http.ResponseWriter, name string, data any) {
	p.renderStatus(w, http.StatusOK, name, data)
}

// base: gemeinsame Daten aller Vorlagen.
type base struct {
	AppName      string
	AssetVersion string
	PwaHead      template.HTML
	Header       template.HTML
	Footer       template.HTML
	StabScript   template.HTML
	MapCenter    template.HTML
	CSRF         string
	UUID         string
	Title        string
	Nonce        string // CSP-Nonce fuer Inline-Scripts der Vorlage
}

// pageBase baut die gemeinsamen Teile: Kopf, Fussleiste, window.STAB.
func (p *Pages) pageBase(r *http.Request, ctx *access.Ctx, subtitle, pageName, extraRight string, stabExtra map[string]any) base {
	b := base{
		AppName:      p.app.Cfg.Name,
		AssetVersion: p.app.Cfg.AssetVersion,
		PwaHead:      p.app.PwaHead(r),
		Header:       p.app.Header(ctx, subtitle),
		StabScript:   p.app.StabScript(r, ctx, stabExtra),
		MapCenter:    p.app.MapCenterScript(r),
		CSRF:         auth.CSRFToken(p.app.Sess(r)),
		Nonce:        httpx.Nonce(r),
	}
	if ctx != nil {
		b.UUID = ctx.Einsatz.UUID
		b.Title = ctx.Einsatz.Title
	}
	b.Footer = p.app.Footer(r, ctx, pageName, extraRight)
	return b
}

// einsatzPage: Gate fuer Seiten im Einsatz-Kontext (?einsatz=UUID).
func (p *Pages) einsatzPage(w http.ResponseWriter, r *http.Request, page string) *access.Ctx {
	u := httpx.Query(r, "einsatz")
	if u == "" {
		if !p.app.RequireEinsatzUser(w, r) {
			return nil
		}
		httpx.Redirect(w, r, "/einsatz")
		return nil
	}
	return p.app.RequirePage(w, r, u, page)
}

// ---------------------------------------------------------------------------
// Dashboard

type indexData struct {
	base
	HasCtx    bool
	HasDivera bool
}

func (p *Pages) index(w http.ResponseWriter, r *http.Request) {
	if !p.app.RequireEinsatzUser(w, r) {
		return
	}
	// Optionaler Einsatz-Kontext (aus der Einsatz-Navigation): Box 1 zeigt dann
	// die Anfahrtsuebersicht dieses Einsatzes.
	ctx := p.app.OptionalCtx(r)
	// Divera-Kachel nur mit Recht und hinterlegtem Zugang der Organisation.
	src, _ := p.app.DiveraSource(r)
	d := indexData{HasCtx: ctx != nil, HasDivera: src != nil}
	d.AppName = p.app.Cfg.Name
	d.AssetVersion = p.app.Cfg.AssetVersion
	d.PwaHead = p.app.PwaHead(r)
	d.MapCenter = p.app.MapCenterScript(r)
	d.StabScript = p.app.StabScript(r, ctx, map[string]any{"is_einsatz_user": auth.IsEinsatzUser(p.app.Sess(r)), "divera": p.app.DiveraClientConfig(src)})
	d.Footer = p.app.Footer(r, nil, "Dashboard", "")
	p.render(w, "index.html", d)
}

// ---------------------------------------------------------------------------
// DiveraMap: Fahrzeugstatus und Einsaetze aus Divera 24/7. Nur fuer Konten
// mit Divera-Recht und hinterlegtem Zugang der Organisation.

func (p *Pages) divera(w http.ResponseWriter, r *http.Request) {
	if !p.app.RequireEinsatzUser(w, r) {
		return
	}
	src, err := p.app.DiveraSource(r)
	if err != nil {
		p.app.Log.Printf("divera: %v", err)
		http.Error(w, "Serverfehler", 500)
		return
	}
	if src == nil {
		p.app.ForbiddenPage(w, r, "Divera ist f&uuml;r dieses Konto nicht freigeschaltet. Der Org-Admin vergibt das Recht und hinterlegt den Divera-Zugang der Organisation.")
		return
	}
	d := base{}
	d.Nonce = httpx.Nonce(r)
	d.AppName = p.app.Cfg.Name
	d.AssetVersion = p.app.Cfg.AssetVersion
	d.PwaHead = p.app.PwaHead(r)
	d.MapCenter = p.app.MapCenterScript(r)
	d.StabScript = p.app.StabScript(r, nil, map[string]any{"divera": p.app.DiveraClientConfig(src)})
	d.Footer = p.app.Footer(r, nil, "DiveraMap", "")
	p.render(w, "divera.html", d)
}

// ---------------------------------------------------------------------------
// Einsatzuebersicht

type einsatzData struct {
	base
	IsLeiter bool
	Denied   bool
	Ebenen   []stab.KV
}

func (p *Pages) einsatz(w http.ResponseWriter, r *http.Request) {
	if !p.app.RequireEinsatzUser(w, r) {
		return
	}
	isLeiter := auth.IsLeiter(p.app.Sess(r))
	d := einsatzData{
		base:     p.pageBase(r, nil, "", "Einsätze", "", map[string]any{"is_leiter": isLeiter}),
		IsLeiter: isLeiter,
		Denied:   httpx.Query(r, "denied") != "" && httpx.Query(r, "denied") != "0",
		Ebenen:   stab.Ebenen,
	}
	p.render(w, "einsatz.html", d)
}

// ---------------------------------------------------------------------------
// Einsatz-Seiten

func (p *Pages) harke(w http.ResponseWriter, r *http.Request) {
	ctx := p.einsatzPage(w, r, "harke")
	if ctx == nil {
		return
	}
	p.render(w, "harke.html", p.pageBase(r, ctx, "Führungsharke", "Führungsharke", "", nil))
}

func (p *Pages) kraefteplanung(w http.ResponseWriter, r *http.Request) {
	ctx := p.einsatzPage(w, r, "kraefteplanung")
	if ctx == nil {
		return
	}
	p.render(w, "kraefteplanung.html", p.pageBase(r, ctx, "Kräfteplanung", "Kräfteplanung", "", nil))
}

func (p *Pages) anfahrt(w http.ResponseWriter, r *http.Request) {
	ctx := p.einsatzPage(w, r, "anfahrt")
	if ctx == nil {
		return
	}
	p.render(w, "anfahrt.html", p.pageBase(r, ctx, "Kräfteübersicht", "Kräfteübersicht", "", nil))
}

func (p *Pages) logPage(w http.ResponseWriter, r *http.Request) {
	ctx := p.einsatzPage(w, r, "log")
	if ctx == nil {
		return
	}
	if ctx.IsShare || !ctx.CanLog {
		p.app.ForbiddenPage(w, r, "Das Log ist dem Leiter Stab und dem Ersteller des Einsatzes vorbehalten.")
		return
	}
	p.render(w, "log.html", p.pageBase(r, ctx, "Änderungs- und Zugriffslog", "Log", "", nil))
}

var (
	reKarteParam = regexp.MustCompile(`(?i)^[a-f0-9-]{8,64}$`)
	reFocusParam = regexp.MustCompile(`(?i)^(node|mitglied):[a-f0-9-]{8,64}$`)
)

type lagekarteData struct {
	base
	CanWrite   bool
	Config     template.JS
	TileOrigin string // externe Kachel-Quelle fuer preconnect ("" = eingebauter Cache)
}

func (p *Pages) lagekarte(w http.ResponseWriter, r *http.Request) {
	ctx := p.einsatzPage(w, r, "lagekarte")
	if ctx == nil {
		return
	}
	// Optionale Parameter: Karte + Fokus auf verknuepfte Einheit (node:<id> | mitglied:<id>)
	karte := httpx.Query(r, "karte")
	if !reKarteParam.MatchString(karte) {
		karte = ""
	}
	focus := httpx.Query(r, "focus")
	if !reFocusParam.MatchString(focus) {
		focus = ""
	}
	d := lagekarteData{
		base:       p.pageBase(r, ctx, "Lagekarte", "Lagekarte", "", nil),
		CanWrite:   ctx.Can(stab.CapLagekarte),
		TileOrigin: p.app.Cfg.TileOrigin(),
		Config: httpx.JS(map[string]any{
			"karte":         karte,
			"focus":         focus,
			"catalogUrl":    "/images/taktische_zeichen/catalog.json",
			"symbolBaseUrl": "/images/taktische_zeichen/",
			"thumbBaseUrl":  "/images/taktische_zeichen_thumbs/",
		}),
	}
	p.render(w, "lagekarte.html", d)
}

type schadenskontenData struct {
	base
	Config template.JS
}

func (p *Pages) schadenskonten(w http.ResponseWriter, r *http.Request) {
	ctx := p.einsatzPage(w, r, "schadenskonten")
	if ctx == nil {
		return
	}
	d := schadenskontenData{
		base: p.pageBase(r, ctx, "Schadenskonten", "Schadenskonten", "", nil),
		Config: httpx.JS(map[string]any{
			"tzBasePath":   "/images/taktische_zeichen",
			"tzCatalogUrl": "/images/taktische_zeichen/catalog.json",
			"focusSk":      httpx.Query(r, "sk"),
		}),
	}
	p.render(w, "schadenskonten.html", d)
}

type verwaltungData struct {
	base
	Neu    bool
	Ebenen []stab.KV
}

func (p *Pages) verwaltung(w http.ResponseWriter, r *http.Request) {
	ctx := p.einsatzPage(w, r, "verwaltung")
	if ctx == nil {
		return
	}
	if ctx.IsShare || !ctx.CanManage {
		p.app.ForbiddenPage(w, r, "Die Verwaltung eines Einsatzes ist dem Ersteller vorbehalten.")
		return
	}
	neu := httpx.Query(r, "neu")
	d := verwaltungData{
		base:   p.pageBase(r, ctx, "Verwaltung", "Verwaltung", "", map[string]any{"is_leiter": auth.IsLeiter(p.app.Sess(r))}),
		Neu:    neu != "" && neu != "0",
		Ebenen: stab.Ebenen,
	}
	p.render(w, "verwaltung.html", d)
}

// ---------------------------------------------------------------------------
// Hilfsmittel

type steinData struct {
	base
	Embed bool
}

func (p *Pages) stein(w http.ResponseWriter, r *http.Request) {
	if !p.app.RequireEinsatzUser(w, r) {
		return
	}
	embed := httpx.Query(r, "embed")
	d := steinData{Embed: embed != "" && embed != "0"}
	d.AppName = p.app.Cfg.Name
	d.AssetVersion = p.app.Cfg.AssetVersion
	d.PwaHead = p.app.PwaHead(r)
	d.MapCenter = p.app.MapCenterScript(r)
	d.Footer = p.app.Footer(r, nil, "STEINboard", `<span id="dataStand"></span>`)
	p.render(w, "stein.html", d)
}

func (p *Pages) steinmap(w http.ResponseWriter, r *http.Request) {
	if !p.app.RequireEinsatzUser(w, r) {
		return
	}
	embed := httpx.Query(r, "embed")
	d := steinData{Embed: embed != "" && embed != "0"}
	d.AppName = p.app.Cfg.Name
	d.AssetVersion = p.app.Cfg.AssetVersion
	d.PwaHead = p.app.PwaHead(r)
	d.MapCenter = p.app.MapCenterScript(r)
	d.Footer = p.app.Footer(r, nil, "STEINmap", `<span id="dataStand"></span>`)
	p.render(w, "steinmap.html", d)
}

// healthz: Health-Check fuer Container/Reverse-Proxy ohne Anmeldung.
func (p *Pages) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	var one int
	if err := p.app.DB.QueryRow("SELECT 1").Scan(&one); err != nil || one != 1 {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"ok":false}`))
		return
	}
	w.Write([]byte(`{"ok":true}`))
}
