package httpx

import (
	"encoding/json"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/auth"
	"github.com/friedrichengel/thw_luk_software_go/internal/changelog"
	"github.com/friedrichengel/thw_luk_software_go/internal/stab"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

// E escaped fuer HTML-Attribute und -Text (htmlspecialchars ENT_QUOTES).
func E(s string) string {
	return html.EscapeString(s)
}

// JS kodiert einen Wert als JSON fuer ein Inline-Script (HTML-sicher: < > & werden
// als < usw. geschrieben).
func JS(v any) template.JS {
	b, err := json.Marshal(v)
	if err != nil {
		return template.JS("null")
	}
	return template.JS(b)
}

type navItem struct {
	Label  string
	URL    string
	Active bool
}

// Seiten im Einsatz-Kontext: key => [label, url]
var einsatzPages = []struct{ Key, Label, URL string }{
	{"harke", "Führungsharke", "/harke"},
	{"kraefteplanung", "Kräfteplanung", "/kraefteplanung"},
	{"lagekarte", "Lagekarte", "/lagekarte"},
	{"schadenskonten", "Schadenskonten", "/schadenskonten"},
	{"anfahrt", "Kräfteübersicht", "/anfahrt"},
	{"log", "Log", "/log"},
	{"verwaltung", "Verwaltung", "/verwaltung"},
}

var hilfsmittel = []struct{ Label, URL string }{
	{"Dashboard", "/"},
	{"STEINboard", "/stein"},
	{"STEINmap", "/steinmap"},
}

// hilfsmittelDivera: nur mit Divera-Recht und Zugang der Organisation.
var hilfsmittelDivera = struct{ Label, URL string }{"DiveraMap", "/divera"}

// scriptName: Seitenpfad fuer die Navigation ("/harke"); die Startseite ist "/".
func scriptName(r *http.Request) string {
	p := r.URL.Path
	if p == "/" || p == "/index" {
		return "/"
	}
	return p
}

// FooterLinks: Navigationsleiste im Footer. Zwei Bereiche in fester
// Reihenfolge - "Einsatz", dann "Hilfsmittel" - mit immer derselben Grammatik:
//
//	Bereichsname[: Punkt · Punkt · Punkt]
//
// Aufgeklappt ist der Bereich, in dem die aktuelle Seite liegt; der andere
// zeigt nur seinen Namen (im Einsatz-Kontext zusaetzlich den laufenden
// Einsatz als einzigen Punkt, der zurueck in die Harke fuehrt). Admins haben
// keinen Einsatzzugriff und sehen nur die Serververwaltung.
func (a *App) FooterLinks(r *http.Request, ctx *access.Ctx) template.HTML {
	s := a.Sess(r)
	if !auth.IsLoggedIn(s) {
		return ""
	}
	script := scriptName(r)
	if auth.IsAdmin(s) {
		// Admins haben nur ein, zwei Seiten - ein Menue waere Selbstzweck,
		// deshalb schlichte Links. System-Update gibt es nur mit Update-Kanal.
		out := `<a href="/admin" class="footer-pop-btn footer-pop-solo` + activeCls(script == "/admin", " footer-pop-btn-active") + `">Serververwaltung</a>`
		if a.Update != nil && a.Update.Enabled() {
			out += ` <a href="/admin_update" class="footer-pop-btn footer-pop-solo` + activeCls(script == "/admin_update", " footer-pop-btn-active") + `">System-Update</a>`
		}
		return template.HTML(out)
	}
	if !auth.IsEinsatzUser(s) {
		return ""
	}
	// Einsatz-Kontext auch auf den Hilfsmittel-Seiten (?einsatz=... in der URL)
	if ctx == nil {
		ctx = a.OptionalCtx(r)
	}
	titel := ""
	if ctx != nil {
		titel = ctx.Einsatz.Title
	}
	// Das Leerzeichen zwischen den Bereichen ist die Umbruchstelle, an der
	// die Leiste auf schmalen Bildschirmen umbrechen darf.
	src, _ := a.DiveraSource(r)
	return template.HTML(footerMenu("Einsatz", einsatzItems(ctx, script), titel) + " " + footerMenu("Hilfsmittel", hilfsmittelItems(script, ctx, src != nil), ""))
}

func activeCls(active bool, cls string) string {
	if active {
		return cls
	}
	return ""
}

func einsatzItems(ctx *access.Ctx, script string) []navItem {
	items := []navItem{{Label: "Übersicht", URL: "/einsatz", Active: script == "/einsatz"}}
	if ctx == nil {
		return items
	}
	q := "?einsatz=" + url.QueryEscape(ctx.Einsatz.UUID)
	for _, p := range einsatzPages {
		if p.Key == "log" && !(ctx.CanLog && !ctx.IsShare) {
			continue
		}
		if p.Key == "verwaltung" && !(ctx.CanManage && !ctx.IsShare) {
			continue
		}
		items = append(items, navItem{Label: p.Label, URL: p.URL + q, Active: script == p.URL})
	}
	return items
}

func hilfsmittelItems(script string, ctx *access.Ctx, withDivera bool) []navItem {
	items := []navItem{}
	for _, p := range hilfsmittel {
		active := script == p.URL
		items = append(items, navItem{Label: p.Label, URL: withEinsatz(p.URL, ctx), Active: active})
	}
	if withDivera {
		p := hilfsmittelDivera
		items = append(items, navItem{Label: p.Label, URL: withEinsatz(p.URL, ctx), Active: script == p.URL})
	}
	return items
}

func withEinsatz(u string, ctx *access.Ctx) string {
	if ctx == nil {
		return u
	}
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "einsatz=" + url.QueryEscape(ctx.Einsatz.UUID)
}

// footerMenu: ein Bereich als Klappmenue - gleiche Bauart wie die Konto-Schaltflaeche.
func footerMenu(label string, items []navItem, ersatz string) string {
	aktuell := ersatz
	for _, it := range items {
		if it.Active {
			aktuell = it.Label
			break
		}
	}
	var b strings.Builder
	b.WriteString(`<details class="footer-pop"><summary class="footer-pop-btn"><span class="footer-pop-label">` + E(label) + `</span>`)
	if aktuell != "" {
		b.WriteString(`<span class="footer-pop-current">` + E(aktuell) + `</span>`)
	}
	b.WriteString(`<span class="footer-pop-caret" aria-hidden="true"></span></summary><div class="footer-pop-menu">`)
	for _, it := range items {
		b.WriteString(`<a href="` + E(it.URL) + `" class="footer-pop-item` + activeCls(it.Active, " footer-pop-item-active") + `">` + E(it.Label) + `</a>`)
	}
	b.WriteString(`</div></details>`)
	return b.String()
}

// PwaHead: Meta-Tags, Manifest, gemeinsame CSS/JS, Changelog-Konfiguration.
func (a *App) PwaHead(r *http.Request) template.HTML {
	s := a.Sess(r)
	v := E(a.Cfg.AssetVersion)
	loggedIn := auth.IsLoggedIn(s)
	seen := ""
	csrf := ""
	if loggedIn {
		seen = changelog.SeenVersion(a.DB, auth.UID(s))
		csrf = auth.CSRFToken(s)
	}
	cl := a.Changelog.ClientConfig(loggedIn, seen, auth.MustChangePassword(s), csrf)
	var b strings.Builder
	b.WriteString(`<meta name="theme-color" content="#003399">` + "\n")
	b.WriteString(`<meta name="color-scheme" content="light">` + "\n")
	b.WriteString(`<meta name="mobile-web-app-capable" content="yes">` + "\n")
	b.WriteString(`<meta name="apple-mobile-web-app-capable" content="yes">` + "\n")
	b.WriteString(`<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">` + "\n")
	b.WriteString(`<meta name="apple-mobile-web-app-title" content="` + E(a.Cfg.Name) + `">` + "\n")
	b.WriteString(`<meta name="application-name" content="` + E(a.Cfg.Name) + `">` + "\n")
	b.WriteString(`<link rel="manifest" href="/manifest.webmanifest">` + "\n")
	b.WriteString(`<link rel="apple-touch-icon" href="/images/favicon_dashboard.png">` + "\n")
	b.WriteString(`<link rel="stylesheet" href="/css/tablet.css?v=` + v + `">` + "\n")
	b.WriteString(`<link rel="stylesheet" href="/css/luk.css?v=` + v + `">` + "\n")
	b.WriteString(`<script src="/js/pwa.js?v=` + v + `" defer></script>` + "\n")
	b.WriteString(`<script` + NonceAttr(r) + `>window.LUK_CHANGELOG = ` + string(JS(cl)) + ";</script>\n")
	b.WriteString(`<script src="/js/changelog.js?v=` + v + `" defer></script>` + "\n")
	b.WriteString(`<script src="/js/footer_menu.js?v=` + v + `" defer></script>` + "\n")
	return template.HTML(b.String())
}

// VersionStamp: "Seitenname[v2.1]", die Version ist ein Knopf fuer "Was ist neu".
func (a *App) VersionStamp(pageName string) string {
	return E(pageName) + `[<button type="button" class="luk-version-btn" title="Was ist neu? Änderungen dieser und früherer Versionen anzeigen">v` + E(a.Changelog.DisplayVersion()) + `</button>]`
}

// MapCenterScript: window.CURRENT_OV (Kartenmittelpunkt der Organisation des
// angemeldeten Nutzers, Kachel-URL) und window.LUK_FEEDS (Nachrichten-Feeds
// der Dashboard-Kacheln) fuer die Karten- und Dashboard-Skripte.
func (a *App) MapCenterScript(r *http.Request) template.HTML {
	orgID := a.UserOrgID(r)
	lat, lng, name := a.MapCenter(orgID)
	payload := map[string]any{
		"buid":  "",
		"name":  name,
		"short": name,
		"lat":   lat,
		"lng":   lng,
		// Kachel-URL der Karten (TILE_URL): zentraler Kachel-Cache
		"tile_url": a.Cfg.TileURL,
	}
	return template.HTML("<script" + NonceAttr(r) + ">window.CURRENT_OV = " + string(JS(payload)) + ";\nwindow.LUK_FEEDS = " + string(JS(a.RSSFeeds(orgID))) + ";</script>\n")
}

// StabScript: window.STAB mit Rollenmatrix, Statuswerten, Einsatz-Kontext,
// Rechten und API-Pfaden fuer alle Einsatz-Seiten.
func (a *App) StabScript(r *http.Request, ctx *access.Ctx, extra map[string]any) template.HTML {
	s := a.Sess(r)
	cfg := stab.ClientConfig()
	if ctx != nil && ctx.IsShare {
		cfg["csrf"] = ""
	} else {
		cfg["csrf"] = auth.CSRFToken(s)
	}
	cfg["sync_interval_ms"] = a.Cfg.SyncIntervalMs
	cfg["api"] = map[string]any{
		"einsatz":        "/api/einsatz",
		"lagekarten":     "/api/lagekarten",
		"schadenskonten": "/api/schadenskonten",
		"kraefte":        "/api/kraefte",
	}
	// Kartenmittelpunkt: Organisation des Einsatzes, sonst des Nutzers
	mcOrg := int64(0)
	if ctx != nil && ctx.Einsatz != nil {
		mcOrg = ctx.Einsatz.OrgIDValue()
	}
	if mcOrg == 0 {
		mcOrg = a.UserOrgID(r)
	}
	mcLat, mcLng, _ := a.MapCenter(mcOrg)
	cfg["map_center"] = map[string]any{"lat": mcLat, "lng": mcLng}
	if ctx != nil {
		cfg["ctx"] = access.ClientContext(ctx)
	} else {
		cfg["ctx"] = nil
	}
	if auth.IsLoggedIn(s) {
		cfg["account"] = map[string]any{"uid": auth.UID(s), "role": auth.Role(s), "label": a.Auth.UserLabel(s)}
	} else {
		cfg["account"] = nil
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return template.HTML("<script" + NonceAttr(r) + ">window.STAB = " + string(JS(cfg)) + ";</script>\n")
}

// Header: Standard-Kopf (taktische Zeit, Titel, Logo).
func (a *App) Header(ctx *access.Ctx, subtitle string) template.HTML {
	var b strings.Builder
	b.WriteString(`<div class="header" id="header">` + "\n")
	b.WriteString(`  <div id="tacticalClock" class="title"></div>` + "\n")
	if ctx != nil || subtitle != "" {
		b.WriteString(`  <div class="header-mid">`)
		if ctx != nil {
			e := ctx.Einsatz
			b.WriteString(`<div class="header-einsatz-title">` + E(e.Title) + `</div>`)
			meta := []string{stab.EbeneLabel(e.Ebene)}
			if e.StabName != "" {
				meta = append(meta, e.StabName)
			}
			if subtitle != "" {
				meta = append(meta, subtitle)
			}
			if e.Status != "open" {
				meta = append(meta, "ABGESCHLOSSEN")
			}
			if ctx.IsShare {
				m := "Leseansicht"
				if ctx.ViaEinsatz != nil {
					m += " · freigegeben für „" + ctx.ViaEinsatz.Title + "\""
				}
				meta = append(meta, m)
			}
			b.WriteString(`<div class="header-einsatz-meta">` + E(strings.Join(meta, " · ")) + `</div>`)
		} else {
			b.WriteString(`<div class="header-einsatz-title">` + E(subtitle) + `</div>`)
		}
		b.WriteString("</div>\n")
	}
	b.WriteString(`  <div class="header-right"><img src="/images/image.png" alt="THW" /></div>` + "\n")
	b.WriteString("</div>\n")
	return template.HTML(b.String())
}

// Footer inkl. Navigation und Session-Info. extraRight nimmt seitenspezifische
// Statusangaben auf (HTML, z.B. den Datenstand auf STEINboard/STEINmap).
func (a *App) Footer(r *http.Request, ctx *access.Ctx, pageName string, extraRight string) template.HTML {
	var b strings.Builder
	b.WriteString(`<div class="footer">` + "\n")
	b.WriteString(`  <div class="footer-left">`)
	b.WriteString(string(a.FooterLinks(r, ctx)))
	b.WriteString("</div>\n")
	b.WriteString(`  <div class="footer-right">`)
	if ctx != nil {
		b.WriteString(`<span id="luk-sync-indicator" class="luk-sync" title="Live-Sync"></span>`)
	}
	b.WriteString(a.SessionInfo(r))
	if extraRight != "" {
		b.WriteString(` <span class="footer-stand">` + extraRight + `</span>`)
	}
	b.WriteString(` <span class="footer-stamp">&copy; ` + E(a.Cfg.CopyrightYear) + `, ` + a.VersionStamp(pageName) + `</span>`)
	b.WriteString("</div>\n")
	b.WriteString("</div>\n")
	return template.HTML(b.String())
}

// SessionInfo: Konto-Schaltflaeche mit Einstellungen und Abmelden.
func (a *App) SessionInfo(r *http.Request) string {
	s := a.Sess(r)
	u := auth.Username(s)
	if u == "" {
		return ""
	}
	rolle := user.RoleLabelOf(auth.Role(s))
	orgAdminLink := ""
	if cu := a.CurrentUser(r); cu != nil && cu.IsOrgAdmin() {
		orgAdminLink = `<a href="/admin" class="footer-pop-item">Organisation verwalten</a>`
	}
	return `<details class="footer-pop footer-pop-right">` +
		`<summary class="footer-pop-btn" title="Konto">` +
		`<span class="footer-pop-label">` + E(u) + `</span>` +
		`<span class="footer-pop-current footer-pop-role">` + E(rolle) + `</span>` +
		`<span class="footer-pop-caret" aria-hidden="true"></span>` +
		`</summary><div class="footer-pop-menu">` +
		`<a href="/settings" class="footer-pop-item">Einstellungen</a>` +
		orgAdminLink +
		`<form method="post" action="/logout" class="footer-pop-form"><input type="hidden" name="csrf" value="` + E(auth.CSRFToken(s)) + `">` +
		`<button type="submit" class="footer-pop-item footer-pop-logout">Abmelden</button></form>` +
		`</div></details>`
}
