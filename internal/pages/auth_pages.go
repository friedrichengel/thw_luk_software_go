package pages

import (
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/auth"
	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
	"github.com/friedrichengel/thw_luk_software_go/internal/session"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

// csrfOrFail prueft das Formular-Token wie CSRF::check (400 + Text).
func (p *Pages) csrfOrFail(w http.ResponseWriter, r *http.Request) bool {
	if !auth.CSRFCheck(p.app.Sess(r), r.FormValue("csrf")) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Invalid CSRF token"))
		return false
	}
	return true
}

// validEmail entspricht filter_var(..., FILTER_VALIDATE_EMAIL) in guter Naeherung.
func validEmail(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\r\n<>") {
		return false
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s {
		return false
	}
	at := strings.LastIndex(s, "@")
	return at > 0 && strings.Contains(s[at+1:], ".")
}

// ---------------------------------------------------------------------------
// Login

type loginData struct {
	base
	Error    string
	Redirect string
}

func (p *Pages) login(w http.ResponseWriter, r *http.Request) {
	s := p.app.Sess(r)
	// Bereits eingeloggt? Direkt weiter (Admin -> Serververwaltung, sonst Einsaetze)
	if auth.IsLoggedIn(s) {
		httpx.Redirect(w, r, auth.HomeURL(s))
		return
	}
	redirect := httpx.Query(r, "redirect")
	if r.Method == http.MethodPost {
		if v := r.FormValue("redirect"); redirect == "" {
			redirect = v
		}
	}
	// Nur interne Pfade zulassen (kein //evil.com, kein CRLF)
	redirect = httpx.SafeRedirect(redirect)

	d := loginData{Redirect: redirect}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		if !p.csrfOrFail(w, r) {
			return
		}
		u := strings.TrimSpace(r.FormValue("username"))
		pw := r.FormValue("password")
		result, err := p.app.Auth.Login(s, r, u, pw)
		if err != nil {
			p.app.Log.Printf("login: %v", err)
			http.Error(w, "Serverfehler", http.StatusInternalServerError)
			return
		}
		switch result {
		case auth.LoginOK:
			httpx.Redirect(w, r, auth.LoginTarget(s, redirect))
			return
		case auth.Login2FA:
			to := "/twofactor"
			if redirect != "" {
				to += "?redirect=" + url.QueryEscape(redirect)
			}
			httpx.Redirect(w, r, to)
			return
		case auth.LoginRateLimited:
			status = http.StatusTooManyRequests
			d.Error = "Zu viele fehlgeschlagene Login-Versuche. Bitte in 15 Minuten erneut versuchen."
		default:
			d.Error = "Ungültiger Benutzername / E-Mail oder Passwort."
		}
	}
	d.AppName = p.app.Cfg.Name
	d.AssetVersion = p.app.Cfg.AssetVersion
	d.PwaHead = p.app.PwaHead(r)
	d.CSRF = auth.CSRFToken(s)
	p.renderStatus(w, status, "login.html", d)
}

// ---------------------------------------------------------------------------
// Logout

// logout beendet die Sitzung nur per POST mit CSRF-Token; ein GET (auch von
// einer fremden Seite ausgeloest) zeigt nur die Bestaetigungsseite.
func (p *Pages) logout(w http.ResponseWriter, r *http.Request) {
	s := p.app.Sess(r)
	if !auth.IsLoggedIn(s) || (r.Method == http.MethodPost && auth.CSRFCheck(s, r.FormValue("csrf"))) {
		auth.Logout(s)
		p.render(w, "logout.html", nil)
		return
	}
	p.render(w, "logout_confirm.html", map[string]any{"CSRF": auth.CSRFToken(s), "AssetVersion": p.app.Cfg.AssetVersion})
}

// ---------------------------------------------------------------------------
// Zwei-Faktor

type twofactorData struct {
	base
	PendingUser string
	Error       string
	Redirect    string
}

func (p *Pages) twofactor(w http.ResponseWriter, r *http.Request) {
	s := p.app.Sess(r)
	if s.GetMap("2fa_pending") == nil {
		httpx.Redirect(w, r, "/login")
		return
	}
	// Standardziel: Einstellungsseite (immer erreichbar, auch ohne Seitenrechte).
	const defaultTarget = "/settings"
	redirect := httpx.Query(r, "redirect")
	if redirect == "" && r.Method == http.MethodPost {
		redirect = r.FormValue("redirect")
	}
	if redirect == "" {
		redirect = defaultTarget
	}
	if httpx.SafeRedirect(redirect) == "" {
		redirect = defaultTarget
	}
	d := twofactorData{PendingUser: auth.Pending2FAUsername(s), Redirect: redirect}
	if r.Method == http.MethodPost {
		if !p.csrfOrFail(w, r) {
			return
		}
		if r.FormValue("cancel") != "" {
			auth.Logout(s)
			httpx.Redirect(w, r, "/login")
			return
		}
		code := strings.Join(strings.Fields(r.FormValue("code")), "")
		ok, err := p.app.Auth.CompleteTwoFactor(s, code)
		if err != nil {
			p.app.Log.Printf("twofactor: %v", err)
			http.Error(w, "Serverfehler", http.StatusInternalServerError)
			return
		}
		if ok {
			httpx.Redirect(w, r, auth.LoginTarget(s, redirect))
			return
		}
		d.Error = "Der Code war ungueltig. Bitte erneut versuchen."
	}
	d.AppName = p.app.Cfg.Name
	d.AssetVersion = p.app.Cfg.AssetVersion
	d.PwaHead = p.app.PwaHead(r)
	d.CSRF = auth.CSRFToken(s)
	p.render(w, "twofactor.html", d)
}

// ---------------------------------------------------------------------------
// Passwort vergessen

type forgotData struct {
	base
	Info  string
	Error string
}

func (p *Pages) forgotPassword(w http.ResponseWriter, r *http.Request) {
	s := p.app.Sess(r)
	d := forgotData{}
	if r.Method == http.MethodPost {
		if !p.csrfOrFail(w, r) {
			return
		}
		email := strings.TrimSpace(r.FormValue("email"))
		const generic = "Wenn die E-Mail-Adresse bekannt ist, wurde ein Link zum Zuruecksetzen versendet."
		if email == "" || !validEmail(email) {
			d.Error = "Bitte eine gueltige E-Mail-Adresse angeben."
		} else if limited, _ := p.app.Auth.IsPasswordResetRateLimited(email, p.app.Auth.IPHash(r)); limited {
			// Anti-Mailbomb: maximal wenige Anforderungen pro E-Mail im
			// Zeitfenster. Antwort bleibt generisch, damit kein Hinweis
			// entsteht, ob die Adresse existiert.
			d.Info = generic
		} else {
			u, _ := user.FindByEmail(p.app.DB, email)
			// Aus Sicherheitsgruenden immer die gleiche Meldung
			if u != nil {
				token, err := user.CreatePasswordResetToken(p.app.DB, u.ID, time.Hour)
				if err == nil && p.deps.SendMail != nil {
					name := u.DisplayNameStr()
					if name == "" {
						name = u.Username
					}
					resetURL := strings.TrimRight(p.app.Cfg.BaseURL, "/") + "/reset_password?token=" + token
					body := "Hallo " + name + ",\n\n" +
						"fuer dein Konto auf " + p.app.Cfg.Host() + " wurde eine Passwort-Zuruecksetzung angefordert.\n" +
						"Dieser Link ist 60 Minuten gueltig:\n\n" +
						resetURL + "\n\n" +
						"Falls du das nicht warst, kannst du diese Mail ignorieren.\n\n" +
						"-- THW Dashboard\n"
					if err := p.deps.SendMail(u.EmailStr(), "Passwort zuruecksetzen - "+p.app.Cfg.Name, body); err != nil {
						p.app.Log.Printf("forgot_password: Mail an %s: %v", u.EmailStr(), err)
					}
				} else if err != nil {
					p.app.Log.Printf("forgot_password: token: %v", err)
				}
			}
			_ = p.app.Auth.RecordPasswordResetRequest(email, p.app.Auth.IPHash(r))
			d.Info = generic
		}
	}
	d.AppName = p.app.Cfg.Name
	d.AssetVersion = p.app.Cfg.AssetVersion
	d.PwaHead = p.app.PwaHead(r)
	d.CSRF = auth.CSRFToken(s)
	p.render(w, "forgot_password.html", d)
}

// ---------------------------------------------------------------------------
// Neues Passwort (Reset-Link)

var reResetToken = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

type resetData struct {
	base
	Token string
	Done  bool
	Error string
}

func (p *Pages) resetPassword(w http.ResponseWriter, r *http.Request) {
	s := p.app.Sess(r)
	token := strings.TrimSpace(httpx.Query(r, "token"))
	if token == "" && r.Method == http.MethodPost {
		token = strings.TrimSpace(r.FormValue("token"))
	}
	if !reResetToken.MatchString(token) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Ungueltiger Link."))
		return
	}
	d := resetData{Token: token}
	if r.Method == http.MethodPost {
		if !p.csrfOrFail(w, r) {
			return
		}
		p1 := r.FormValue("password")
		p2 := r.FormValue("password2")
		switch {
		case len(p1) < auth.PasswordMinChars:
			d.Error = "Das Passwort muss mindestens 10 Zeichen lang sein."
		case p1 != p2:
			d.Error = "Die Passwoerter stimmen nicht ueberein."
		default:
			u, err := user.ConsumePasswordResetToken(p.app.DB, token)
			if err != nil {
				p.app.Log.Printf("reset_password: %v", err)
			}
			if u == nil {
				d.Error = "Der Link ist abgelaufen oder ungueltig. Bitte neuen Link anfordern."
			} else if err := user.UpdatePassword(p.app.DB, u.ID, p1, true); err != nil {
				p.app.Log.Printf("reset_password: %v", err)
				d.Error = "Das Passwort konnte nicht gespeichert werden."
			} else {
				_ = session.DeleteForUser(p.app.DB, u.ID)
				d.Done = true
			}
		}
	}
	d.AppName = p.app.Cfg.Name
	d.AssetVersion = p.app.Cfg.AssetVersion
	d.PwaHead = p.app.PwaHead(r)
	d.CSRF = auth.CSRFToken(s)
	p.render(w, "reset_password.html", d)
}
