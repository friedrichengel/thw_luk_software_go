package pages

import (
	"net/http"
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/auth"
	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
	"github.com/friedrichengel/thw_luk_software_go/internal/org"
	"github.com/friedrichengel/thw_luk_software_go/internal/totp"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

type settingsData struct {
	base
	Username        string
	RoleLabel       string
	OrgName         string
	OrgAdmin        bool
	DisplayName     string
	Email           string
	Forced          bool
	Redirect        string // Ziel nach erzwungenem Passwortwechsel
	Notice          string
	Error           string
	TwoFactorActive bool
	SetupSecret     string
	FormattedSecret string
	OtpURI          string
	DisplayVersion  string
}

func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (p *Pages) settings(w http.ResponseWriter, r *http.Request) {
	if !p.app.RequireLogin(w, r) {
		return
	}
	s := p.app.Sess(r)
	u := p.app.Auth.User(s)
	if u == nil {
		httpx.Redirect(w, r, "/login")
		return
	}
	d := settingsData{}
	forcedParam := httpx.Query(r, "forced")
	d.Forced = (forcedParam != "" && forcedParam != "0") || u.MustChangePassword
	// Ursprüngliches Ziel (von RequireLogin mitgegeben, im Formular durchgereicht)
	d.Redirect = httpx.SafeRedirect(httpx.Query(r, "redirect"))
	if r.Method == http.MethodPost && d.Redirect == "" {
		d.Redirect = httpx.SafeRedirect(r.FormValue("redirect"))
	}
	wasForced := u.MustChangePassword

	if r.Method == http.MethodPost {
		if !p.csrfOrFail(w, r) {
			return
		}
		action := r.FormValue("action")
		err := func() error {
			switch action {
			case "update_profile":
				email := strings.TrimSpace(r.FormValue("email"))
				display := strings.TrimSpace(r.FormValue("display_name"))
				if email != "" && !validEmail(email) {
					return errText("Bitte eine gültige E-Mail angeben.")
				}
				if email != "" {
					other, _ := user.FindByEmail(p.app.DB, email)
					if other != nil && other.ID != u.ID {
						return errText("Diese E-Mail-Adresse wird bereits verwendet.")
					}
				}
				if err := user.UpdateEmail(p.app.DB, u.ID, strOrNil(email)); err != nil {
					return err
				}
				if err := user.UpdateDisplayName(p.app.DB, u.ID, strOrNil(display)); err != nil {
					return err
				}
				d.Notice = "Profil aktualisiert."
			case "change_password":
				current := r.FormValue("current_password")
				new1 := r.FormValue("new_password")
				new2 := r.FormValue("new_password2")
				full, _ := user.FindByUsername(p.app.DB, u.Username)
				if full == nil || !user.VerifyPassword(full.PasswordHash, current) {
					return errText("Aktuelles Passwort ist falsch.")
				}
				if len(new1) < auth.PasswordMinChars {
					return errText("Das neue Passwort muss mindestens 10 Zeichen lang sein.")
				}
				if new1 != new2 {
					return errText("Die neuen Passwörter stimmen nicht überein.")
				}
				if err := user.UpdatePassword(p.app.DB, u.ID, new1, true); err != nil {
					return err
				}
				s.Set("must_change_password", false)
				if wasForced {
					// Erzwungener Wechsel (Initialpasswort): direkt dorthin weiter,
					// wo der Nutzer hinwollte (sonst Startseite der Rolle), statt
					// auf der Einstellungsseite zu bleiben.
					httpx.Redirect(w, r, auth.LoginTarget(s, d.Redirect))
					return errRedirected
				}
				d.Notice = "Passwort geändert."
				d.Forced = false
			case "2fa_start":
				s.Set("2fa_setup_secret", totp.GenerateSecret(20))
				d.Notice = "2FA-Setup gestartet – bitte QR-Code scannen und Code bestätigen."
			case "2fa_enable":
				secret := s.GetString("2fa_setup_secret")
				code := strings.Join(strings.Fields(r.FormValue("code")), "")
				if secret == "" {
					return errText("Kein 2FA-Setup aktiv. Bitte erneut starten.")
				}
				if !totp.VerifyCode(secret, code) {
					return errText("Der Code ist ungültig. Bitte erneut versuchen.")
				}
				if err := user.EnableTwoFactor(p.app.DB, u.ID, secret); err != nil {
					return err
				}
				s.Delete("2fa_setup_secret")
				d.Notice = "Zwei-Faktor-Authentifizierung aktiviert."
			case "2fa_cancel":
				s.Delete("2fa_setup_secret")
				d.Notice = "2FA-Setup abgebrochen."
			case "2fa_disable":
				current := r.FormValue("current_password")
				full, _ := user.FindByUsername(p.app.DB, u.Username)
				if full == nil || !user.VerifyPassword(full.PasswordHash, current) {
					return errText("Aktuelles Passwort ist falsch.")
				}
				if err := user.DisableTwoFactor(p.app.DB, u.ID); err != nil {
					return err
				}
				s.Delete("2fa_setup_secret")
				d.Notice = "Zwei-Faktor-Authentifizierung deaktiviert."
			}
			return nil
		}()
		if err == errRedirected {
			return
		}
		if err != nil {
			d.Error = err.Error()
		}
		if fresh := p.app.Auth.User(s); fresh != nil {
			u = fresh
		}
	}

	d.AppName = p.app.Cfg.Name
	d.AssetVersion = p.app.Cfg.AssetVersion
	d.PwaHead = p.app.PwaHead(r)
	d.Header = p.app.Header(nil, "Einstellungen")
	d.Footer = p.app.Footer(r, nil, "Einstellungen", "")
	d.CSRF = auth.CSRFToken(s)
	d.Username = u.Username
	d.RoleLabel = user.RoleLabelOf(u.Role)
	if o, _ := org.FindByID(p.app.DB, u.OrgIDValue()); o != nil {
		d.OrgName = o.Name
		d.OrgAdmin = u.IsOrgAdmin()
	}
	d.DisplayName = u.DisplayNameStr()
	d.Email = u.EmailStr()
	d.TwoFactorActive = u.TwoFactorEnabled
	d.SetupSecret = s.GetString("2fa_setup_secret")
	if d.SetupSecret != "" {
		d.OtpURI = totp.BuildOtpAuthURI(d.SetupSecret, u.Username, p.app.Cfg.Name)
		d.FormattedSecret = totp.FormatSecret(d.SetupSecret)
	}
	d.DisplayVersion = p.app.Changelog.DisplayVersion()
	p.render(w, "settings.html", d)
}

// errRedirected: die Aktion hat bereits umgeleitet, nichts mehr rendern.
var errRedirected = errText("redirected")

// errText: fachliche Fehlermeldung fuer die Seite (wie RuntimeException in PHP).
type errText string

func (e errText) Error() string { return string(e) }
