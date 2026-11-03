package pages

import (
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/auth"
	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
	"github.com/friedrichengel/thw_luk_software_go/internal/mail"
	"github.com/friedrichengel/thw_luk_software_go/internal/org"
	"github.com/friedrichengel/thw_luk_software_go/internal/settings"
	"github.com/friedrichengel/thw_luk_software_go/internal/stab"
	"github.com/friedrichengel/thw_luk_software_go/internal/tiles"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

var reUsername = regexp.MustCompile(`^[\p{L}\p{N}_.@+-]{3,64}$`)

// adminScope: Geltungsbereich der Serververwaltung (/admin). Der globale Admin sieht
// alle Konten und Organisationen, ein Org-Admin nur die Konten seiner
// Organisation und deren Zugaenge (STEIN, Divera).
type adminScope struct {
	Global bool
	OrgID  int64
	Org    *org.Org
}

type adminUser struct {
	ID          int64
	Username    string
	DisplayName string
	Email       string
	Role        string
	RoleLabel   string
	IsAdmin     bool
	TwoFactor   bool
	MustChange  bool
	CreatedAt   string
	IsSelf      bool
	OrgID       int64
	OrgLabel    string
	OrgAdmin    bool
	Divera      string
	DiveraLabel string
}

// feedView: eine Dashboard-Kachel im Formular (eigene Werte und Standard).
type feedView struct {
	Slot     string
	Title    string // eigener Titel ("" = Standard)
	URLs     string // eigene Adressen, eine je Zeile ("" = Standard)
	DefTitle string
	DefURLs  string
	Own      bool // eigene Werte gesetzt
}

type adminOrg struct {
	ID         int64
	StammKey   string
	Name       string
	Kurz       string
	Ebene      string
	EbeneLabel string
	Users      int64
	Admins     int64
	Einsaetze  int64
	Creds      int
}

// adminDiveraGroup: Divera-Gruppe mit Anzeige-Zustand fuer das Dashboard.
type adminDiveraGroup struct {
	ID     int64
	Name   string
	Hidden bool
}

// stammItem: ein Eintrag der Stammdaten zur Auswahl in der Uebernahme.
type stammItem struct {
	Key    string
	Name   string
	Exists bool
}

// stammGroup: Regionalstelle mit ihren Ortsverbaenden.
type stammGroup struct {
	stammItem
	OVs []stammItem
}

type adminData struct {
	base
	StammLV         *stammItem
	StammGroups     []stammGroup
	StammMissing    int
	Global          bool
	OrgName         string
	Tab             string
	Users           []adminUser
	Count           int
	Orgs            []adminOrg
	OrgCount        int
	Creds           []org.CredentialInfo
	DiveraKeySet    bool // Divera-Zugangsschluessel der Organisation hinterlegt
	DiveraGroups    []adminDiveraGroup
	DiveraGroupsErr string

	SteinMode       string
	WebhookURL      string
	WebhookSecrets  []org.CredentialInfo
	WebhookEvents   []org.WebhookEvent
	WebhookLastHour int64
	WebhookLast     string
	TileSource      string // gespeicherte externe Quelle ("" = Standard OpenStreetMap)
	TileEffective   string // wirksame Quelle
	TilePublic      bool
	TileToken       string
	TileEndpoint    string              // eigene Kachel-URL fuer andere Installationen
	Mail            settings.MailValues // in der Oberflaeche gespeicherte Mail-Einstellungen
	MailEnv         settings.MailValues // Werte der Umgebung (Platzhalter)
	MailTransport   string              // wirksamer Versandweg als Text
	MailSender      string              // wirksamer Absender
	MailTestTo      string              // Vorbelegung fuer die Testmail (eigene Adresse)

	// Reiter "Einstellungen" (Org-Admin): Kartenmittelpunkt und Nachrichten-Feeds
	MapOwn            bool   // eigener Mittelpunkt gesetzt
	MapLat            string // Formularwerte (leer = Standard)
	MapLng            string
	MapName           string
	MapDefLat         float64 // Standard (Stammdaten-Standort oder Server)
	MapDefLng         float64
	MapDefName        string
	MapDefSource      string
	MapEffLat         float64 // wirksam
	MapEffLng         float64
	TileURL           string
	Feeds             []feedView
	Notice            string
	Created           bool
	Error             string
	GeneratedPassword string
	RoleLabels        []user.RoleLabel
	DiveraLevels      []user.RoleLabel
	Ebenen            []stab.KV
}

func (p *Pages) admin(w http.ResponseWriter, r *http.Request) {
	if !p.app.RequireLogin(w, r) {
		return
	}
	s := p.app.Sess(r)
	me := p.app.CurrentUser(r)
	if me == nil {
		httpx.Redirect(w, r, "/login")
		return
	}
	var sc adminScope
	switch {
	case me.Role == "admin":
		sc.Global = true
	case me.IsOrgAdmin():
		sc.OrgID = me.OrgIDValue()
		sc.Org, _ = org.FindByID(p.app.DB, sc.OrgID)
		if sc.Org == nil {
			p.app.ForbiddenPage(w, r, "Deine Organisation existiert nicht mehr.")
			return
		}
	default:
		p.app.ForbiddenPage(w, r, "F&uuml;r diese Seite sind Adminrechte oder das Recht &bdquo;Org-Admin&ldquo; erforderlich.")
		return
	}
	d := adminData{Global: sc.Global, RoleLabels: user.RoleLabels, DiveraLevels: user.DiveraLevels, Ebenen: stab.Ebenen}
	if sc.Org != nil {
		d.OrgName = sc.Org.Name
		// Org-Admins vergeben nur Einsatz-Rollen
		d.RoleLabels = []user.RoleLabel{}
		for _, rl := range user.RoleLabels {
			if rl.Key != "admin" {
				d.RoleLabels = append(d.RoleLabels, rl)
			}
		}
	}
	d.Tab = httpx.Query(r, "tab")
	switch {
	case d.Tab == "orgs" && sc.Global, d.Tab == "server" && sc.Global, d.Tab == "zugaenge" && !sc.Global, d.Tab == "einstellungen" && !sc.Global:
	default:
		d.Tab = "users"
	}

	if r.Method == http.MethodPost {
		if !p.csrfOrFail(w, r) {
			return
		}
		if tab := r.FormValue("tab"); tab != "" {
			d.Tab = tab
		}
		if err := p.adminAction(r, sc, me, &d); err != nil {
			d.Error = err.Error()
		}
	}

	if err := p.adminLists(sc, me, &d); err != nil {
		p.app.Log.Printf("admin: %v", err)
		http.Error(w, "Serverfehler", http.StatusInternalServerError)
		return
	}
	d.AppName = p.app.Cfg.Name
	d.AssetVersion = p.app.Cfg.AssetVersion
	d.PwaHead = p.app.PwaHead(r)
	title := "Serververwaltung"
	if sc.Org != nil {
		title = "Organisation " + sc.Org.Name
	}
	d.Header = p.app.Header(nil, title)
	d.Footer = p.app.Footer(r, nil, "Serververwaltung", "")
	d.CSRF = auth.CSRFToken(s)
	p.render(w, "admin.html", d)
}

// adminTarget laedt das Zielkonto einer Aktion und prueft den Geltungsbereich.
func (p *Pages) adminTarget(sc adminScope, uid int64) (*user.User, error) {
	t, _ := user.FindByID(p.app.DB, uid)
	if t == nil {
		return nil, errText("Nutzer nicht gefunden.")
	}
	if !sc.Global && (t.Role == "admin" || t.OrgIDValue() != sc.OrgID) {
		return nil, errText("Dieser Nutzer gehört nicht zu deiner Organisation.")
	}
	return t, nil
}

func parseOrgID(s string) *int64 {
	id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || id <= 0 {
		return nil
	}
	return &id
}

func (p *Pages) adminAction(r *http.Request, sc adminScope, me *user.User, d *adminData) error {
	db := p.app.DB
	action := r.FormValue("action")
	uid, _ := strconv.ParseInt(r.FormValue("uid"), 10, 64)
	selfUID := me.ID
	switch action {
	case "create_user":
		newUsername := strings.TrimSpace(r.FormValue("new_username"))
		newDisplay := strings.TrimSpace(r.FormValue("new_display_name"))
		newEmail := strings.TrimSpace(r.FormValue("new_email"))
		newRole := user.NormalizeRole(r.FormValue("new_role"))
		if !sc.Global && newRole == "admin" {
			return errText("Org-Admins können keine Admin-Konten anlegen.")
		}
		if newUsername == "" || !reUsername.MatchString(newUsername) {
			return errText("Benutzername: 3-64 Zeichen, Buchstaben/Ziffern/._@+-")
		}
		if ex, _ := user.FindByUsername(db, newUsername); ex != nil {
			return errText("Benutzername existiert bereits.")
		}
		if newEmail != "" && !validEmail(newEmail) {
			return errText("Ungültige E-Mail-Adresse.")
		}
		if newEmail != "" {
			if ex, _ := user.FindByEmail(db, newEmail); ex != nil {
				return errText("E-Mail-Adresse wird bereits verwendet.")
			}
		}
		var orgID *int64
		if !sc.Global {
			orgID = &sc.OrgID
		} else if newRole != "admin" {
			orgID = parseOrgID(r.FormValue("new_org_id"))
			if orgID != nil {
				if o, _ := org.FindByID(db, *orgID); o == nil {
					return errText("Organisation nicht gefunden.")
				}
			}
		}
		pw := user.GeneratePassword(14)
		id, err := user.Create(db, newUsername, pw, strOrNil(newEmail), strOrNil(newDisplay), newRole, true)
		if err != nil {
			return err
		}
		if orgID != nil {
			if err := user.SetOrg(db, id, orgID); err != nil {
				return err
			}
		}
		d.GeneratedPassword = pw
		d.Created = true
	case "set_username":
		if _, err := p.adminTarget(sc, uid); err != nil {
			return err
		}
		newName := strings.TrimSpace(r.FormValue("username"))
		if newName == "" || !reUsername.MatchString(newName) {
			return errText("Benutzername: 3-64 Zeichen, Buchstaben/Ziffern/._@+-")
		}
		if ex, _ := user.FindByUsername(db, newName); ex != nil && ex.ID != uid {
			return errText("Benutzername existiert bereits.")
		}
		if err := user.UpdateUsername(db, uid, newName); err != nil {
			return err
		}
		d.Notice = "Benutzername aktualisiert."
	case "set_display_name":
		if _, err := p.adminTarget(sc, uid); err != nil {
			return err
		}
		newDisplay := strings.TrimSpace(r.FormValue("display_name"))
		if err := user.UpdateDisplayName(db, uid, strOrNil(newDisplay)); err != nil {
			return err
		}
		d.Notice = "Anzeigename aktualisiert."
	case "reset_password":
		if _, err := p.adminTarget(sc, uid); err != nil {
			return err
		}
		pw := user.GeneratePassword(14)
		if err := user.UpdatePassword(db, uid, pw, false); err != nil {
			return err
		}
		if err := user.SetMustChangePassword(db, uid, true); err != nil {
			return err
		}
		d.GeneratedPassword = pw
		d.Notice = "Neues Initialpasswort gesetzt. Bitte sicher übergeben."
	case "disable_2fa":
		if _, err := p.adminTarget(sc, uid); err != nil {
			return err
		}
		if err := user.DisableTwoFactor(db, uid); err != nil {
			return err
		}
		d.Notice = "2FA für den Nutzer wurde zurückgesetzt."
	case "set_email":
		if _, err := p.adminTarget(sc, uid); err != nil {
			return err
		}
		email := strings.TrimSpace(r.FormValue("email"))
		if email != "" && !validEmail(email) {
			return errText("Ungültige E-Mail.")
		}
		if email != "" {
			if other, _ := user.FindByEmail(db, email); other != nil && other.ID != uid {
				return errText("E-Mail-Adresse wird bereits verwendet.")
			}
		}
		if err := user.UpdateEmail(db, uid, strOrNil(email)); err != nil {
			return err
		}
		d.Notice = "E-Mail aktualisiert."
	case "set_role":
		target, err := p.adminTarget(sc, uid)
		if err != nil {
			return err
		}
		role := user.NormalizeRole(r.FormValue("role"))
		if !sc.Global && role == "admin" {
			return errText("Org-Admins können keine Admin-Rolle vergeben.")
		}
		if target.Role == "admin" && role != "admin" {
			if n, _ := user.CountAdmins(db); n <= 1 {
				return errText("Der letzte Admin kann nicht herabgestuft werden.")
			}
		}
		if uid == selfUID && sc.Global && role != "admin" {
			return errText("Die eigene Admin-Rolle kann nicht entzogen werden.")
		}
		if err := user.SetRole(db, uid, role); err != nil {
			return err
		}
		if role == "admin" {
			// Admin-Konten haben keine Organisation und keine Einsatzrechte.
			_ = user.SetOrg(db, uid, nil)
			_ = user.SetOrgAdmin(db, uid, false)
			_ = user.SetDiveraAccess(db, uid, "off")
		}
		d.Notice = "Kontorolle geändert."
	case "delete_user":
		target, err := p.adminTarget(sc, uid)
		if err != nil {
			return err
		}
		if uid == selfUID {
			return errText("Das eigene Konto kann nicht gelöscht werden.")
		}
		if target.Role == "admin" {
			if n, _ := user.CountAdmins(db); n <= 1 {
				return errText("Der letzte Admin kann nicht gelöscht werden.")
			}
		}
		// Eigentuemer von Einsaetzen duerfen nicht geloescht werden (FK RESTRICT)
		if owns, _ := user.OwnsEinsaetze(db, uid); owns {
			return errText("Der Nutzer ist Ersteller von Einsätzen und kann nicht gelöscht werden. Bitte Konto stattdessen per Passwort-Reset sperren.")
		}
		if err := user.Delete(db, uid); err != nil {
			return err
		}
		d.Notice = "Nutzer gelöscht."
	case "set_org":
		if !sc.Global {
			return errText("Nur der globale Admin ordnet Organisationen zu.")
		}
		target, err := p.adminTarget(sc, uid)
		if err != nil {
			return err
		}
		if target.Role == "admin" {
			return errText("Admin-Konten gehören zu keiner Organisation.")
		}
		orgID := parseOrgID(r.FormValue("org_id"))
		if orgID != nil {
			if o, _ := org.FindByID(db, *orgID); o == nil {
				return errText("Organisation nicht gefunden.")
			}
		}
		if err := user.SetOrg(db, uid, orgID); err != nil {
			return err
		}
		if orgID == nil {
			_ = user.SetOrgAdmin(db, uid, false)
		}
		d.Notice = "Organisation zugeordnet."
	case "set_org_admin":
		target, err := p.adminTarget(sc, uid)
		if err != nil {
			return err
		}
		on := r.FormValue("org_admin") == "1"
		if target.Role == "admin" || target.OrgIDValue() == 0 {
			return errText("Org-Admin setzt ein Einsatz-Konto mit Organisation voraus.")
		}
		if !on && target.OrgAdmin {
			if n, _ := user.CountOrgAdmins(db, target.OrgIDValue()); n <= 1 && !sc.Global {
				return errText("Der letzte Org-Admin der Organisation kann sich das Recht nicht selbst entziehen.")
			}
		}
		if err := user.SetOrgAdmin(db, uid, on); err != nil {
			return err
		}
		if on {
			d.Notice = "Org-Admin-Recht vergeben."
		} else {
			d.Notice = "Org-Admin-Recht entzogen."
		}
	case "set_divera":
		if sc.Global {
			return errText("Divera-Rechte vergibt der Org-Admin der jeweiligen Organisation.")
		}
		target, err := p.adminTarget(sc, uid)
		if err != nil {
			return err
		}
		if target.Role == "admin" {
			return errText("Admin-Konten haben keinen Divera-Zugriff.")
		}
		if err := user.SetDiveraAccess(db, uid, r.FormValue("divera")); err != nil {
			return err
		}
		d.Notice = "Divera-Recht geändert."
	case "set_divera_groups":
		if sc.Global {
			return errText("Die Divera-Gruppen stellt der Org-Admin der jeweiligen Organisation ein.")
		}
		shown := map[string]bool{}
		for _, id := range r.Form["show"] {
			shown[strings.TrimSpace(id)] = true
		}
		hidden := []int64{}
		for _, raw := range strings.Split(r.FormValue("all_ids"), ",") {
			raw = strings.TrimSpace(raw)
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id == 0 || shown[raw] {
				continue
			}
			hidden = append(hidden, id)
		}
		if err := org.SetDiveraHiddenGroups(db, sc.OrgID, hidden); err != nil {
			return err
		}
		d.Notice = "Divera-Gruppen für das Dashboard gespeichert (" + strconv.Itoa(len(hidden)) + " ausgeblendet)."
	case "set_credential":
		if sc.Global {
			return errText("Zugänge hinterlegt der Org-Admin der jeweiligen Organisation.")
		}
		kind := r.FormValue("kind")
		if !org.KindExists(kind) {
			return errText("Unbekannte Zugangsart.")
		}
		value := strings.TrimSpace(r.FormValue("value"))
		if len(value) < 8 {
			return errText("Der Schlüssel ist zu kurz.")
		}
		if len(value) > 2048 {
			return errText("Der Schlüssel ist zu lang.")
		}
		if err := org.SetCredential(db, sc.OrgID, kind, value, selfUID, p.app.Cfg.Secret); err != nil {
			return err
		}
		if p.deps.OnCredentialChange != nil {
			p.deps.OnCredentialChange(sc.OrgID, kind)
		}
		d.Notice = org.KindLabel(kind) + " hinterlegt."
	case "check_credential":
		if sc.Global {
			return errText("Zugänge prüft der Org-Admin der jeweiligen Organisation.")
		}
		kind := r.FormValue("kind")
		if !org.KindExists(kind) {
			return errText("Unbekannte Zugangsart.")
		}
		if !org.HasCredential(db, sc.OrgID, kind) {
			return errText("Für diese Zugangsart ist kein Schlüssel hinterlegt.")
		}
		if p.deps.CheckCredential == nil {
			return errText("Prüfung in dieser Umgebung nicht verfügbar.")
		}
		msg, err := p.deps.CheckCredential(sc.OrgID, kind)
		if err != nil {
			return errText("Prüfung " + org.KindLabel(kind) + " fehlgeschlagen: " + err.Error())
		}
		d.Notice = org.KindLabel(kind) + ": " + msg
	case "set_stein_mode":
		if sc.Global {
			return errText("Den STEIN-Abruf stellt der Org-Admin der jeweiligen Organisation ein.")
		}
		mode := org.NormalizeSteinMode(r.FormValue("stein_mode"))
		if err := org.SetSteinMode(db, sc.OrgID, mode); err != nil {
			return err
		}
		if p.deps.OnCredentialChange != nil {
			p.deps.OnCredentialChange(sc.OrgID, org.KindStein)
		}
		if mode == org.SteinModeWebhook {
			d.Notice = "STEIN-Abruf auf Webhook umgestellt. Bitte die Webhook-URL in STEIN je Ortsverband eintragen und die Secrets hinterlegen."
		} else {
			d.Notice = "STEIN-Abruf auf regelmäßigen Abruf (alle 2 Minuten) gestellt."
		}
	case "add_webhook_secret":
		if sc.Global {
			return errText("Webhook-Secrets hinterlegt der Org-Admin der jeweiligen Organisation.")
		}
		label := strings.TrimSpace(r.FormValue("label"))
		if len(label) < 2 || len(label) > 60 || strings.ContainsAny(label, ":\n\r") {
			return errText("Bezeichnung des Ortsverbands: 2-60 Zeichen, ohne Doppelpunkt.")
		}
		value := strings.TrimSpace(r.FormValue("value"))
		if len(value) < 8 || len(value) > 2048 {
			return errText("Das Secret ist zu kurz oder zu lang.")
		}
		if err := org.SetWebhookSecret(db, sc.OrgID, label, value, selfUID, p.app.Cfg.Secret); err != nil {
			return err
		}
		d.Notice = "Webhook-Secret für „" + label + "“ hinterlegt."
	case "delete_webhook_secret":
		if sc.Global {
			return errText("Webhook-Secrets verwaltet der Org-Admin der jeweiligen Organisation.")
		}
		label := strings.TrimSpace(r.FormValue("label"))
		if err := org.DeleteWebhookSecret(db, sc.OrgID, label); err != nil {
			return err
		}
		d.Notice = "Webhook-Secret für „" + label + "“ entfernt."
	case "delete_credential":
		if sc.Global {
			return errText("Zugänge verwaltet der Org-Admin der jeweiligen Organisation.")
		}
		kind := r.FormValue("kind")
		if !org.KindExists(kind) {
			return errText("Unbekannte Zugangsart.")
		}
		if err := org.DeleteCredential(db, sc.OrgID, kind); err != nil {
			return err
		}
		if p.deps.OnCredentialChange != nil {
			p.deps.OnCredentialChange(sc.OrgID, kind)
		}
		d.Notice = org.KindLabel(kind) + " entfernt."
	case "set_tiles":
		if !sc.Global {
			return errText("Server-Einstellungen setzt nur der globale Admin.")
		}
		source := strings.TrimSpace(r.FormValue("tile_source"))
		if source != "" && !tiles.ValidSource(source) {
			return errText("Kachelquelle: Adresse muss mit http(s):// beginnen und {z}, {x} und {y} enthalten.")
		}
		token := strings.TrimSpace(r.FormValue("tile_token"))
		if len(token) > 200 || strings.ContainsAny(token, " \t\r\n&?#") {
			return errText("Token: bis 200 Zeichen, ohne Leerzeichen und ohne & ? #.")
		}
		public := "0"
		if r.FormValue("tile_public") == "1" {
			public = "1"
		}
		if err := settings.Set(db, settings.TileSource, source, selfUID); err != nil {
			return err
		}
		if err := settings.Set(db, settings.TilePublic, public, selfUID); err != nil {
			return err
		}
		if err := settings.Set(db, settings.TileToken, token, selfUID); err != nil {
			return err
		}
		if source == "" {
			d.Notice = "Kachel-Einstellungen gespeichert. Quelle: OpenStreetMap (Standard)."
		} else {
			d.Notice = "Kachel-Einstellungen gespeichert. Quelle: " + source
		}
	case "set_map":
		if sc.Global {
			return errText("Den Kartenmittelpunkt setzt der Org-Admin der Organisation.")
		}
		name := strings.TrimSpace(r.FormValue("map_name"))
		if len(name) > 80 || strings.ContainsAny(name, "\r\n") {
			return errText("Bezeichnung: bis 80 Zeichen.")
		}
		latS, lngS := strings.TrimSpace(r.FormValue("map_lat")), strings.TrimSpace(r.FormValue("map_lng"))
		var lat, lng *float64
		if latS != "" || lngS != "" {
			la, err1 := strconv.ParseFloat(strings.ReplaceAll(latS, ",", "."), 64)
			ln, err2 := strconv.ParseFloat(strings.ReplaceAll(lngS, ",", "."), 64)
			if err1 != nil || err2 != nil || la < -90 || la > 90 || ln < -180 || ln > 180 {
				return errText("Mittelpunkt: Breite (-90 bis 90) und Länge (-180 bis 180) als Dezimalzahlen, beide oder keine.")
			}
			lat, lng = &la, &ln
		}
		if err := org.SetMapCenter(db, sc.OrgID, lat, lng, name); err != nil {
			return err
		}
		if lat == nil {
			d.Notice = "Kartenmittelpunkt: Standard wird verwendet."
		} else {
			d.Notice = "Kartenmittelpunkt gespeichert."
		}
	case "set_feeds", "reset_feeds":
		if sc.Global {
			return errText("Die Nachrichten-Feeds setzt der Org-Admin der Organisation.")
		}
		var feeds []org.RSSFeed
		if action == "set_feeds" {
			for _, slot := range org.FeedSlots {
				title := strings.TrimSpace(r.FormValue("feed_title_" + slot))
				if len(title) > 40 || strings.ContainsAny(title, "\r\n") {
					return errText("Reiter-Titel: bis 40 Zeichen.")
				}
				var urls []string
				for _, line := range strings.Split(r.FormValue("feed_urls_"+slot), "\n") {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					if !org.ValidFeedURL(line, p.app.Cfg.RSSAllowPrivate) {
						return errText("Feed-Adresse ungültig: " + line + " (http(s)://host/…, ohne Zugangsdaten).")
					}
					urls = append(urls, line)
				}
				if len(urls) > 10 {
					return errText("Je Kachel höchstens 10 Feed-Adressen.")
				}
				feeds = append(feeds, org.RSSFeed{Slot: slot, Title: title, URLs: urls})
			}
		}
		if err := org.SetRSSFeeds(db, sc.OrgID, feeds); err != nil {
			return err
		}
		if action == "reset_feeds" {
			d.Notice = "Nachrichten-Feeds auf den Standard zurückgesetzt."
		} else {
			d.Notice = "Nachrichten-Feeds gespeichert."
		}
	case "set_mail":
		if !sc.Global {
			return errText("Server-Einstellungen setzt nur der globale Admin.")
		}
		v, err := mailForm(r)
		if err != nil {
			return err
		}
		for key, val := range map[string]string{
			settings.MailFrom: v.From, settings.MailFromName: v.FromName, settings.SMTPHost: v.Host,
			settings.SMTPPort: v.Port, settings.SMTPSecurity: v.Security, settings.SMTPUser: v.User,
		} {
			if err := settings.Set(db, key, val, selfUID); err != nil {
				return err
			}
		}
		if r.FormValue("smtp_pass_clear") == "1" {
			if err := settings.Set(db, settings.SMTPPass, "", selfUID); err != nil {
				return err
			}
		} else if pass := r.FormValue("smtp_pass"); strings.TrimSpace(pass) != "" {
			if err := settings.SetSecret(db, settings.SMTPPass, pass, p.app.Cfg.Secret, selfUID); err != nil {
				return err
			}
		}
		d.Notice = "Mail-Einstellungen gespeichert."
	case "test_mail":
		if !sc.Global {
			return errText("Server-Einstellungen prüft nur der globale Admin.")
		}
		to := strings.TrimSpace(r.FormValue("mail_test_to"))
		if !mail.ValidAddress(to) {
			return errText("Testmail: Empfängeradresse fehlt oder ist ungültig.")
		}
		v, err := mailForm(r)
		if err != nil {
			return err
		}
		// Formularwerte gehen vor, dann gespeicherte Einstellungen, dann Umgebung
		cfg := settings.MailConfig(db, p.app.Cfg)
		pass := r.FormValue("smtp_pass")
		v.PassSet = strings.TrimSpace(pass) != ""
		settings.ApplyMail(cfg, v, pass)
		body := "Dies ist eine Testmail der Serververwaltung (" + p.app.Cfg.Name + ").\n\n" +
			"Gesendet von " + p.app.Cfg.BaseURL + " durch " + me.Username + ".\n" +
			"Umlaute-Test: aeoeue -> äöü ß\n"
		if err := mail.Send(cfg, to, "Testmail - "+p.app.Cfg.Name, body); err != nil {
			return errText("Testmail fehlgeschlagen (" + mailTransport(cfg) + "): " + err.Error())
		}
		d.Notice = "Testmail an " + to + " wurde vom Mailserver angenommen (" + mailTransport(cfg) + "). Bitte Posteingang und Spam-Ordner prüfen. Die Eingaben sind noch nicht gespeichert."
	case "test_tiles":
		if !sc.Global {
			return errText("Server-Einstellungen prüft nur der globale Admin.")
		}
		if p.deps.TileTest == nil {
			return errText("Testabruf in dieser Umgebung nicht verfügbar.")
		}
		source := strings.TrimSpace(r.FormValue("tile_source"))
		msg, err := p.deps.TileTest(source)
		if err != nil {
			return errText("Testabruf fehlgeschlagen: " + err.Error())
		}
		d.Notice = msg
	case "create_org":
		if !sc.Global {
			return errText("Nur der globale Admin legt Organisationen an.")
		}
		name := strings.TrimSpace(r.FormValue("name"))
		if len(name) < 2 || len(name) > 120 {
			return errText("Name der Organisation: 2-120 Zeichen.")
		}
		if ex, _ := org.FindByName(db, name); ex != nil {
			return errText("Eine Organisation mit diesem Namen existiert bereits.")
		}
		if _, err := org.Create(db, name, strings.TrimSpace(r.FormValue("kurz")), r.FormValue("ebene")); err != nil {
			return err
		}
		d.Notice = "Organisation angelegt."
	case "seed_orgs":
		if !sc.Global {
			return errText("Nur der globale Admin legt Organisationen an.")
		}
		if p.deps.SeedOrganisationen == nil {
			return errText("Stammdaten in dieser Umgebung nicht verfügbar.")
		}
		var only map[string]bool
		if r.FormValue("all") != "1" {
			only = map[string]bool{}
			for _, k := range r.Form["keys"] {
				if k = strings.TrimSpace(k); k != "" {
					only[k] = true
				}
			}
			if len(only) == 0 {
				return errText("Bitte mindestens einen Eintrag auswählen oder „Alle anlegen“ verwenden.")
			}
		}
		created, existing, err := p.deps.SeedOrganisationen(only)
		if err != nil {
			return err
		}
		d.Notice = strconv.Itoa(created) + " Organisationen aus den Stammdaten angelegt, " + strconv.Itoa(existing) + " waren bereits vorhanden."
	case "update_org":
		if !sc.Global {
			return errText("Nur der globale Admin ändert Organisationen.")
		}
		oid := parseOrgID(r.FormValue("org_id"))
		if oid == nil {
			return errText("Organisation nicht gefunden.")
		}
		name := strings.TrimSpace(r.FormValue("name"))
		if len(name) < 2 || len(name) > 120 {
			return errText("Name der Organisation: 2-120 Zeichen.")
		}
		if ex, _ := org.FindByName(db, name); ex != nil && ex.ID != *oid {
			return errText("Eine Organisation mit diesem Namen existiert bereits.")
		}
		if err := org.Update(db, *oid, name, strings.TrimSpace(r.FormValue("kurz")), r.FormValue("ebene")); err != nil {
			return err
		}
		d.Notice = "Organisation aktualisiert."
	case "delete_org":
		if !sc.Global {
			return errText("Nur der globale Admin löscht Organisationen.")
		}
		oid := parseOrgID(r.FormValue("org_id"))
		if oid == nil {
			return errText("Organisation nicht gefunden.")
		}
		if n, _ := org.UserCount(db, *oid); n > 0 {
			return errText("Die Organisation hat noch Konten. Bitte zuerst umziehen.")
		}
		if n, _ := org.EinsatzCount(db, *oid); n > 0 {
			return errText("Der Organisation sind noch Einsätze zugeordnet.")
		}
		if err := org.Delete(db, *oid); err != nil {
			return err
		}
		d.Notice = "Organisation gelöscht."
	}
	return nil
}

func (p *Pages) adminLists(sc adminScope, me *user.User, d *adminData) error {
	db := p.app.DB
	orgs, err := org.All(db)
	if err != nil {
		return err
	}
	orgLabel := map[int64]string{}
	for _, o := range orgs {
		orgLabel[o.ID] = o.Name
		if sc.Global {
			ao := adminOrg{ID: o.ID, StammKey: o.StammKey, Name: o.Name, Kurz: o.Kurz, Ebene: o.Ebene, EbeneLabel: o.EbeneLabel()}
			ao.Users, _ = org.UserCount(db, o.ID)
			ao.Admins, _ = user.CountOrgAdmins(db, o.ID)
			ao.Einsaetze, _ = org.EinsatzCount(db, o.ID)
			if infos, err := org.CredentialInfos(db, o.ID); err == nil {
				for _, c := range infos {
					if c.Set {
						ao.Creds++
					}
				}
			}
			d.Orgs = append(d.Orgs, ao)
		}
	}
	d.OrgCount = len(orgs)
	if sc.Global {
		d.TileSource = settings.Get(db, settings.TileSource)
		if p.deps.TileState != nil {
			d.TileEffective, d.TilePublic, d.TileToken = p.deps.TileState()
		}
		d.TileEndpoint = p.app.Cfg.BaseURL + "/tiles/{z}/{x}/{y}.png"
		d.Mail = settings.Mail(db)
		d.MailEnv = settings.MailEnv(p.app.Cfg)
		eff := settings.MailConfig(db, p.app.Cfg)
		d.MailTransport = mailTransport(eff)
		d.MailSender = eff.MailFromName + " <" + eff.MailFrom + ">"
		if me.Email != nil {
			d.MailTestTo = *me.Email
		}
	}
	if !sc.Global {
		o, _ := org.FindByID(db, sc.OrgID) // frisch lesen: Aktion dieses Requests
		d.TileURL = p.app.Cfg.TileURL
		d.MapDefLat, d.MapDefLng, d.MapDefName = p.app.Center.Lat, p.app.Center.Lng, p.app.Center.Name
		d.MapDefSource = "Server (MAP_CENTER_* bzw. Stammdaten)"
		if p.app.OrgHome != nil {
			if h := p.app.OrgHome(sc.OrgID); h != nil {
				d.MapDefLat, _ = h["lat"].(float64)
				d.MapDefLng, _ = h["lng"].(float64)
				d.MapDefName, _ = h["name"].(string)
				d.MapDefSource = "Stammdaten (Standort des Ortsverbands)"
			}
		}
		d.MapEffLat, d.MapEffLng, _ = p.app.MapCenter(sc.OrgID)
		if o != nil {
			d.MapName = o.MapName
			if o.HasMapCenter() {
				d.MapOwn = true
				d.MapLat = strconv.FormatFloat(*o.MapLat, 'f', 5, 64)
				d.MapLng = strconv.FormatFloat(*o.MapLng, 'f', 5, 64)
			}
		}
		own := map[string]org.RSSFeed{}
		if o != nil {
			for _, f := range o.RSSFeeds {
				own[f.Slot] = f
			}
		}
		for _, def := range org.DefaultRSSFeeds() {
			fv := feedView{Slot: def.Slot, DefTitle: def.Title, DefURLs: strings.Join(def.URLs, "\n")}
			if f, ok := own[def.Slot]; ok {
				fv.Own = true
				fv.Title = f.Title
				fv.URLs = strings.Join(f.URLs, "\n")
			}
			d.Feeds = append(d.Feeds, fv)
		}
	}
	if sc.Global && p.deps.Stamm != nil {
		have := map[string]bool{}
		for _, o := range orgs {
			if o.StammKey != "" {
				have[o.StammKey] = true
			}
		}
		lv, regionen, ovs := p.deps.Stamm()
		sval := func(m map[string]any, k string) string { s, _ := m[k].(string); return strings.TrimSpace(s) }
		if lv != nil && sval(lv, "key") != "" {
			k := org.StammKeyLV(sval(lv, "key"))
			d.StammLV = &stammItem{Key: k, Name: sval(lv, "name"), Exists: have[k]}
			if !have[k] {
				d.StammMissing++
			}
		}
		byRegion := map[string][]stammItem{}
		for _, o := range ovs {
			if ext, _ := o["extern"].(bool); ext || sval(o, "name") == "" {
				continue
			}
			k := org.StammKeyOV(sval(o, "key"))
			byRegion[sval(o, "region")] = append(byRegion[sval(o, "region")], stammItem{Key: k, Name: "OV " + sval(o, "name"), Exists: have[k]})
			if !have[k] {
				d.StammMissing++
			}
		}
		for _, r := range regionen {
			if ext, _ := r["extern"].(bool); ext {
				continue
			}
			k := sval(r, "key")
			g := stammGroup{stammItem: stammItem{Key: k, Name: sval(r, "name"), Exists: have[k]}, OVs: byRegion[k]}
			sort.Slice(g.OVs, func(i, j int) bool { return g.OVs[i].Name < g.OVs[j].Name })
			if !have[k] {
				d.StammMissing++
			}
			d.StammGroups = append(d.StammGroups, g)
		}
	}
	var users []*user.User
	if sc.Global {
		users, err = user.All(db)
	} else {
		users, err = user.AllInOrg(db, sc.OrgID)
	}
	if err != nil {
		return err
	}
	for _, u := range users {
		d.Users = append(d.Users, adminUser{
			ID: u.ID, Username: u.Username, DisplayName: u.DisplayNameStr(), Email: u.EmailStr(),
			Role: u.Role, RoleLabel: user.RoleLabelOf(u.Role), IsAdmin: u.Role == "admin", TwoFactor: u.TwoFactorEnabled,
			MustChange: u.MustChangePassword, CreatedAt: u.CreatedAt, IsSelf: u.ID == me.ID,
			OrgID: u.OrgIDValue(), OrgLabel: orgLabel[u.OrgIDValue()], OrgAdmin: u.OrgAdmin,
			Divera: u.DiveraAccess, DiveraLabel: user.DiveraLabelOf(u.DiveraAccess),
		})
	}
	d.Count = len(users)
	if !sc.Global {
		d.Creds, err = org.CredentialInfos(db, sc.OrgID)
		if err != nil {
			return err
		}
		d.DiveraKeySet = org.HasCredential(db, sc.OrgID, org.KindDiveraAccess)
		if d.DiveraKeySet && p.deps.DiveraGroups != nil {
			if groups, err := p.deps.DiveraGroups(sc.OrgID); err != nil {
				d.DiveraGroupsErr = err.Error()
			} else {
				// Organisation frisch lesen: eine Aktion in diesem Request kann die
				// Auswahl gerade geaendert haben (sc.Org stammt vom Seitenaufruf).
				hidden := map[int64]bool{}
				if cur, _ := org.FindByID(db, sc.OrgID); cur != nil {
					for _, id := range cur.DiveraHiddenGroups {
						hidden[id] = true
					}
				}
				for _, g := range groups {
					d.DiveraGroups = append(d.DiveraGroups, adminDiveraGroup{ID: g.ID, Name: g.Name, Hidden: hidden[g.ID]})
				}
			}
		}

		d.SteinMode = org.SteinModeOf(db, sc.OrgID)
		d.WebhookURL = p.app.Cfg.BaseURL + "/api/stein/webhook/" + org.WebhookToken(p.app.Cfg.Secret, sc.OrgID)
		if d.WebhookSecrets, err = org.WebhookSecretInfos(db, sc.OrgID); err != nil {
			return err
		}
		if d.WebhookEvents, err = org.WebhookEvents(db, sc.OrgID, 20); err != nil {
			return err
		}
		d.WebhookLastHour, d.WebhookLast = org.WebhookStats(db, sc.OrgID)
	}
	return nil
}

// ---------------------------------------------------------------------------
// System-Update

type adminUpdateData struct {
	base
	Enabled  bool
	Binary   bool
	Version  string
	Deployed string
	Image    string
	Config   template.JS
}

func (p *Pages) adminUpdate(w http.ResponseWriter, r *http.Request) {
	if !p.app.RequireAdmin(w, r) {
		return
	}
	s := p.app.Sess(r)
	info := p.app.Update.Info()
	enabled, _ := info["enabled"].(bool)
	mode, _ := info["mode"].(string)
	version, _ := info["version"].(string)
	deployed, _ := info["deployed"].(string)
	image, _ := info["image"].(string)
	csrf := auth.CSRFToken(s)
	d := adminUpdateData{
		Enabled:  enabled,
		Binary:   mode == "binary",
		Version:  version,
		Deployed: deployed,
		Image:    image,
		Config:   httpx.JS(map[string]any{"csrf": csrf, "api": "/api/update", "info": info}),
	}
	d.AppName = p.app.Cfg.Name
	d.AssetVersion = p.app.Cfg.AssetVersion
	d.PwaHead = p.app.PwaHead(r)
	d.Header = p.app.Header(nil, "System-Update")
	d.Footer = p.app.Footer(r, nil, "System-Update", "")
	d.CSRF = csrf
	p.render(w, "admin_update.html", d)
}

// mailForm liest und prueft die Felder des Mail-Formulars (Admin > Server).
// Leere Felder bedeuten "Wert aus der Umgebung".
func mailForm(r *http.Request) (settings.MailValues, error) {
	v := settings.MailValues{
		From:     strings.TrimSpace(r.FormValue("mail_from")),
		FromName: strings.TrimSpace(r.FormValue("mail_from_name")),
		Host:     strings.TrimSpace(r.FormValue("smtp_host")),
		Port:     strings.TrimSpace(r.FormValue("smtp_port")),
		Security: strings.TrimSpace(r.FormValue("smtp_security")),
		User:     strings.TrimSpace(r.FormValue("smtp_user")),
	}
	if v.From != "" && !mail.ValidAddress(v.From) {
		return v, errText("Absenderadresse ungültig (Form name@domain.tld).")
	}
	if len(v.FromName) > 120 || strings.ContainsAny(v.FromName, "\r\n") {
		return v, errText("Absendername: bis 120 Zeichen, ohne Zeilenumbruch.")
	}
	if v.Host != "" && (len(v.Host) > 253 || strings.ContainsAny(v.Host, " /:@\\\t\r\n")) {
		return v, errText("SMTP-Server: nur der Hostname, ohne Schema, Port oder Leerzeichen.")
	}
	if v.Port != "" {
		if n, err := strconv.Atoi(v.Port); err != nil || n < 1 || n > 65535 {
			return v, errText("SMTP-Port: Zahl zwischen 1 und 65535.")
		}
	}
	switch v.Security {
	case "", "ssl", "tls", "none":
	default:
		return v, errText("Verschlüsselung: ssl, tls oder none.")
	}
	if len(v.User) > 200 || strings.ContainsAny(v.User, " \t\r\n") {
		return v, errText("SMTP-Benutzer: bis 200 Zeichen, ohne Leerzeichen.")
	}
	return v, nil
}

// mailTransport beschreibt den wirksamen Versandweg in einem Satzteil.
func mailTransport(cfg *config.Config) string {
	if cfg.SMTPHost == "" {
		return "lokales sendmail, kein SMTP-Server eingetragen"
	}
	s := fmt.Sprintf("SMTP %s:%d, %s", cfg.SMTPHost, cfg.SMTPPort, map[string]string{"ssl": "TLS ab Verbindung", "tls": "STARTTLS", "none": "unverschlüsselt"}[cfg.SMTPSecurity])
	if cfg.SMTPUser != "" {
		s += ", Anmeldung als " + cfg.SMTPUser
	} else {
		s += ", ohne Anmeldung"
	}
	return s
}
