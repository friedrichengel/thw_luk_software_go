// Package auth: Login, Zwei-Faktor, Kontorollen und Rate-Limits.
//
// Kontorollen (users.role):
//
//	user    darf sich einloggen und an Einsaetzen mitarbeiten, fuer die er
//	        freigegeben wurde
//	leiter  wie user, darf zusaetzlich Einsaetze anlegen und verwalten
//	admin   Serververwaltung (/admin): Nutzer, Organisationen, Server. Hat KEINEN Zugriff auf Einsaetze.
//
// Hilfsmittel (Dashboard, STEINboard, STEINmap) stehen allen eingeloggten
// Nutzern offen. Die Sitzungsschluessel entsprechen der PHP-Fassung
// (uid, username, role, must_change_password, csrf, 2fa_pending).
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/session"
	"github.com/friedrichengel/thw_luk_software_go/internal/totp"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
	"github.com/friedrichengel/thw_luk_software_go/internal/uuid"
)

const (
	LoginOK          = "ok"
	Login2FA         = "2fa"
	LoginFailed      = "failed"
	LoginRateLimited = "rate_limited"

	loginWindow      = 900 // 15 Minuten
	loginMaxIP       = 10
	loginMaxUser     = 5
	tfaWindow        = 900
	tfaMax           = 5
	pwResetWindow    = 1800
	pwResetMax       = 3
	pwResetMaxIP     = 10
	hookWindow       = 900 // STEIN-Webhook: abgelehnte Aufrufe je IP
	hookMaxFail      = 20
	PasswordMinChars = 10
)

type Auth struct {
	Cfg *config.Config
	DB  *db.DB
}

// --- Sitzung ---------------------------------------------------------------

func UID(s *session.Session) int64 { return s.GetInt64("uid") }

func Role(s *session.Session) string { return s.GetString("role") }

func Username(s *session.Session) string { return s.GetString("username") }

func IsLoggedIn(s *session.Session) bool { return UID(s) > 0 }

func IsAdmin(s *session.Session) bool { return Role(s) == "admin" }

func IsLeiter(s *session.Session) bool { return Role(s) == "leiter" }

// IsEinsatzUser: darf an Einsaetzen mitarbeiten (user oder leiter, nicht admin).
func IsEinsatzUser(s *session.Session) bool {
	r := Role(s)
	return r == "user" || r == "leiter"
}

func MustChangePassword(s *session.Session) bool { return s.GetBool("must_change_password") }

// HomeURL: Ziel nach dem Login je nach Kontorolle.
func HomeURL(s *session.Session) string {
	if IsAdmin(s) {
		return "/admin"
	}
	return "/einsatz"
}

// LoginTarget: Ziel nach erfolgreicher Anmeldung. redirect ist der (bereits
// als intern geprüfte) Pfad, den der Nutzer vor dem Login aufrufen wollte.
// Admin-Konten haben keinen Einsatzzugriff: eine Weiterleitung auf das
// Dashboard oder eine Einsatzseite (z.B. PWA-Start /einsatz) liefe sonst
// direkt in "Zugriff verweigert"; sie landen stattdessen auf der
// Serververwaltung.
func LoginTarget(s *session.Session, redirect string) string {
	if redirect == "" {
		return HomeURL(s)
	}
	if IsAdmin(s) {
		for _, ok := range []string{"/admin", "/settings"} {
			if redirect == ok || strings.HasPrefix(redirect, ok+"?") || strings.HasPrefix(redirect, ok+"_") || strings.HasPrefix(redirect, ok+"/") {
				return redirect
			}
		}
		return HomeURL(s)
	}
	return redirect
}

// CSRFToken liefert das Token der Sitzung (legt es bei Bedarf an).
func CSRFToken(s *session.Session) string {
	t := s.GetString("csrf")
	if t == "" {
		t = uuid.RandomHex(32)
		s.Set("csrf", t)
	}
	return t
}

// CSRFCheck prueft ein Token zeitkonstant.
func CSRFCheck(s *session.Session, token string) bool {
	have := s.GetString("csrf")
	return token != "" && have != "" && hmac.Equal([]byte(have), []byte(token))
}

// User laedt den Nutzer der Sitzung (nil, wenn nicht angemeldet).
func (a *Auth) User(s *session.Session) *user.User {
	uid := UID(s)
	if uid <= 0 {
		return nil
	}
	u, _ := user.FindByID(a.DB, uid)
	return u
}

// UserLabel: Anzeigename des eingeloggten Nutzers (fuer Log-Eintraege).
func (a *Auth) UserLabel(s *session.Session) string {
	u := a.User(s)
	if u == nil {
		return ""
	}
	return user.Label(u)
}

// --- Login -----------------------------------------------------------------

func (a *Auth) Login(s *session.Session, r *http.Request, identifier, password string) (string, error) {
	ipHash := a.IPHash(r)
	limited, err := a.isRateLimited(ipHash)
	if err != nil {
		return "", err
	}
	if !limited {
		limited, err = a.isUserRateLimited(identifier, ipHash)
		if err != nil {
			return "", err
		}
	}
	if limited {
		return LoginRateLimited, nil
	}
	u, err := user.FindByUsernameOrEmail(a.DB, identifier)
	if err != nil {
		return "", err
	}
	if u == nil || !user.VerifyPassword(u.PasswordHash, password) {
		if err := a.recordFailedLogin(ipHash, identifier); err != nil {
			return "", err
		}
		return LoginFailed, nil
	}
	if err := a.clearLoginAttempts(ipHash, identifier); err != nil {
		return "", err
	}
	s.Delete("2fa_pending")
	if u.HasTwoFactor() {
		s.Set("2fa_pending", map[string]any{"uid": u.ID, "username": u.Username, "role": u.Role})
		s.Delete("uid")
		s.Delete("username")
		s.Delete("role")
		return Login2FA, nil
	}
	a.setSessionForUser(s, u)
	return LoginOK, nil
}

func (a *Auth) CompleteTwoFactor(s *session.Session, code string) (bool, error) {
	pending := s.GetMap("2fa_pending")
	if pending == nil {
		return false, nil
	}
	uid := toInt64(pending["uid"])
	if uid <= 0 {
		return false, nil
	}
	limited, err := a.isTfaRateLimited(uid)
	if err != nil || limited {
		return false, err
	}
	u, err := user.FindByID(a.DB, uid)
	if err != nil {
		return false, err
	}
	if u == nil || !u.HasTwoFactor() {
		s.Delete("2fa_pending")
		return false, nil
	}
	counter, ok := totp.VerifyCodeReturnCounter(*u.TwoFactorSecret, code, u.TwoFactorLastCounter, 1, time.Now())
	if !ok {
		return false, a.recordFailedTfa(uid)
	}
	if err := user.SetTwoFactorLastCounter(a.DB, uid, counter); err != nil {
		return false, err
	}
	if err := a.clearTfaAttempts(uid); err != nil {
		return false, err
	}
	a.setSessionForUser(s, u)
	s.Delete("2fa_pending")
	return true, nil
}

func (a *Auth) setSessionForUser(s *session.Session, u *user.User) {
	s.Regenerate()
	s.Delete("csrf")
	s.Set("uid", u.ID)
	s.Set("username", u.Username)
	s.Set("role", user.NormalizeRole(u.Role))
	s.Set("must_change_password", u.MustChangePassword)
	// Freigabe-Sessions eines vorherigen Besuchers nicht mitschleppen.
	s.Delete("share")
}

// Pending2FAUsername liefert den Benutzernamen einer wartenden 2FA-Anmeldung.
func Pending2FAUsername(s *session.Session) string {
	if p := s.GetMap("2fa_pending"); p != nil {
		if v, ok := p["username"].(string); ok {
			return v
		}
	}
	return ""
}

func Logout(s *session.Session) {
	s.Destroy()
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	}
	return 0
}

// --- Client-IP -------------------------------------------------------------

// ClientIP liefert die Adresse des Clients; hinter vertrauten Proxies die
// erste Adresse aus X-Forwarded-For.
func (a *Auth) ClientIP(r *http.Request) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	remote = strings.Trim(remote, "[]")
	if remote == "" {
		remote = "0.0.0.0"
	}
	if !a.Cfg.IsTrustedProxy(remote) {
		return remote
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return remote
	}
	first := strings.TrimSpace(strings.Split(xff, ",")[0])
	if net.ParseIP(first) != nil {
		return first
	}
	return remote
}

// IsHTTPS: TLS direkt oder X-Forwarded-Proto eines vertrauten Proxys.
func (a *Auth) IsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	// APP_BASE_URL mit https: der Betrieb hinter TLS ist erklaert, das Cookie
	// bekommt Secure auch ohne TRUSTED_PROXIES.
	if strings.HasPrefix(strings.ToLower(a.Cfg.BaseURL), "https://") {
		return true
	}
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	if a.Cfg.IsTrustedProxy(strings.Trim(remote, "[]")) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return false
}

// IPHash: pseudonymisierte Client-IP (HMAC mit APP_SECRET).
func (a *Auth) IPHash(r *http.Request) string {
	return a.hmac(a.ClientIP(r))
}

func (a *Auth) hmac(s string) string {
	m := hmac.New(sha256.New, []byte(a.Cfg.Secret))
	m.Write([]byte(s))
	return hex.EncodeToString(m.Sum(nil))
}

func (a *Auth) userKey(identifier string) string {
	return a.hmac(strings.ToLower(strings.TrimSpace(identifier)))
}

// --- Rate-Limiting fuer Login ---------------------------------------------

func (a *Auth) isRateLimited(ipHash string) (bool, error) {
	n, err := db.QueryInt(a.DB, "SELECT COUNT(*) FROM login_attempts WHERE ip_hash = ? AND created_at >= ?", ipHash, db.Ago(loginWindow*time.Second))
	return n >= loginMaxIP, err
}

// isUserRateLimited zaehlt Fehlversuche je Konto und IP: Fremde koennen ein
// Konto nicht sperren, indem sie nur den Benutzernamen kennen; die IP-Grenze
// (isRateLimited) begrenzt das Durchprobieren trotzdem.
func (a *Auth) isUserRateLimited(identifier, ipHash string) (bool, error) {
	n, err := db.QueryInt(a.DB, "SELECT COUNT(*) FROM login_attempts WHERE username = ? AND ip_hash = ? AND created_at >= ?", a.userKey(identifier), ipHash, db.Ago(loginWindow*time.Second))
	return n >= loginMaxUser, err
}

func (a *Auth) recordFailedLogin(ipHash, identifier string) error {
	if _, err := a.DB.Exec("INSERT INTO login_attempts (ip_hash, username, created_at) VALUES (?, ?, ?)", ipHash, a.userKey(identifier), db.Now()); err != nil {
		return err
	}
	_, err := a.DB.Exec("DELETE FROM login_attempts WHERE created_at < ?", db.Ago(4*loginWindow*time.Second))
	return err
}

func (a *Auth) clearLoginAttempts(ipHash, identifier string) error {
	_, err := a.DB.Exec("DELETE FROM login_attempts WHERE ip_hash = ? OR username = ?", ipHash, a.userKey(identifier))
	return err
}

// --- 2FA Throttling --------------------------------------------------------

func (a *Auth) isTfaRateLimited(uid int64) (bool, error) {
	n, err := db.QueryInt(a.DB, "SELECT COUNT(*) FROM tfa_attempts WHERE user_id = ? AND created_at >= ?", uid, db.Ago(tfaWindow*time.Second))
	return n >= tfaMax, err
}

func (a *Auth) recordFailedTfa(uid int64) error {
	if _, err := a.DB.Exec("INSERT INTO tfa_attempts (user_id, created_at) VALUES (?, ?)", uid, db.Now()); err != nil {
		return err
	}
	_, err := a.DB.Exec("DELETE FROM tfa_attempts WHERE created_at < ?", db.Ago(4*tfaWindow*time.Second))
	return err
}

func (a *Auth) clearTfaAttempts(uid int64) error {
	_, err := a.DB.Exec("DELETE FROM tfa_attempts WHERE user_id = ?", uid)
	return err
}

// --- STEIN-Webhook: abgelehnte Aufrufe je IP -------------------------------

// IsWebhookRateLimited: zu viele abgelehnte Webhook-Aufrufe (unbekannter
// Token oder falsches X-Secret) von dieser IP im Zeitfenster. Gueltige
// Aufrufe zaehlen nicht, STEIN sperrt sich also nicht selbst aus.
func (a *Auth) IsWebhookRateLimited(ipHash string) (bool, error) {
	n, err := db.QueryInt(a.DB, "SELECT COUNT(*) FROM login_attempts WHERE username = 'webhook' AND ip_hash = ? AND created_at >= ?", ipHash, db.Ago(hookWindow*time.Second))
	return n >= hookMaxFail, err
}

// RecordWebhookFailure zaehlt einen abgelehnten Aufruf; true genau dann, wenn
// damit die Grenze erreicht ist (eine Log-Zeile je Sperre).
func (a *Auth) RecordWebhookFailure(ipHash string) (bool, error) {
	if _, err := a.DB.Exec("INSERT INTO login_attempts (ip_hash, username, created_at) VALUES (?, 'webhook', ?)", ipHash, db.Now()); err != nil {
		return false, err
	}
	n, err := db.QueryInt(a.DB, "SELECT COUNT(*) FROM login_attempts WHERE username = 'webhook' AND ip_hash = ? AND created_at >= ?", ipHash, db.Ago(hookWindow*time.Second))
	return n == hookMaxFail, err
}

// --- Password-Reset Throttling --------------------------------------------

// IsPasswordResetRateLimited: je E-Mail (Mailbomb) und je IP (Massenversand
// an viele Adressen) begrenzt.
func (a *Auth) IsPasswordResetRateLimited(email, ipHash string) (bool, error) {
	n, err := db.QueryInt(a.DB, "SELECT COUNT(*) FROM login_attempts WHERE username = ? AND ip_hash = 'pwreset' AND created_at >= ?",
		a.userKey(email), db.Ago(pwResetWindow*time.Second))
	if err != nil || n >= pwResetMax {
		return true, err
	}
	n, err = db.QueryInt(a.DB, "SELECT COUNT(*) FROM login_attempts WHERE username = 'pwreset' AND ip_hash = ? AND created_at >= ?",
		ipHash, db.Ago(pwResetWindow*time.Second))
	return n >= pwResetMaxIP, err
}

func (a *Auth) RecordPasswordResetRequest(email, ipHash string) error {
	now := db.Now()
	if _, err := a.DB.Exec("INSERT INTO login_attempts (ip_hash, username, created_at) VALUES (?, ?, ?)", "pwreset", a.userKey(email), now); err != nil {
		return err
	}
	_, err := a.DB.Exec("INSERT INTO login_attempts (ip_hash, username, created_at) VALUES (?, ?, ?)", ipHash, "pwreset", now)
	return err
}
