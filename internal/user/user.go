// Package user: Nutzerkonten (users), Passwort-Hashing (bcrypt, kompatibel
// zu PHP password_hash) und Passwort-Reset-Tokens.
package user

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/friedrichengel/thw_luk_software_go/internal/db"
)

var Roles = []string{"user", "leiter", "admin"}

type RoleLabel struct{ Key, Label string }

// RoleLabels in Anzeige-Reihenfolge.
var RoleLabels = []RoleLabel{{"user", "User"}, {"leiter", "Leiter"}, {"admin", "Admin"}}

func RoleLabelOf(role string) string {
	for _, r := range RoleLabels {
		if r.Key == role {
			return r.Label
		}
	}
	return role
}

func NormalizeRole(role string) string {
	for _, r := range Roles {
		if r == role {
			return role
		}
	}
	return "user"
}

type User struct {
	ID                   int64
	Username             string
	PasswordHash         string
	Email                *string
	DisplayName          *string
	Role                 string
	TwoFactorEnabled     bool
	TwoFactorSecret      *string
	TwoFactorLastCounter int64
	MustChangePassword   bool
	ChangelogSeen        string
	OrgID                *int64 // Organisation (nil: nicht zugeordnet, z.B. Admin)
	OrgAdmin             bool   // verwaltet Nutzer, Rechte und Zugaenge der eigenen Organisation
	DiveraAccess         string // off | read | write
	CreatedAt            string
	UpdatedAt            string
}

// DiveraLevels: Stufen des Divera-Rechts in Anzeige-Reihenfolge.
var DiveraLevels = []RoleLabel{{"off", "kein Divera"}, {"read", "Divera lesen"}, {"write", "Divera lesen und Status setzen"}}

// NormalizeDivera liefert eine gueltige Stufe (Standard off).
func NormalizeDivera(level string) string {
	for _, l := range DiveraLevels {
		if l.Key == level {
			return level
		}
	}
	return "off"
}

// DiveraLabelOf: Anzeigetext einer Divera-Stufe.
func DiveraLabelOf(level string) string {
	for _, l := range DiveraLevels {
		if l.Key == level {
			return l.Label
		}
	}
	return level
}

func fromMap(m map[string]any) *User {
	if m == nil {
		return nil
	}
	return &User{
		ID:                   db.Int(m, "id"),
		Username:             db.Str(m, "username"),
		PasswordHash:         db.Str(m, "password_hash"),
		Email:                db.StrPtr(m, "email"),
		DisplayName:          db.StrPtr(m, "display_name"),
		Role:                 NormalizeRole(db.Str(m, "role")),
		TwoFactorEnabled:     db.Int(m, "two_factor_enabled") == 1,
		TwoFactorSecret:      db.StrPtr(m, "two_factor_secret"),
		TwoFactorLastCounter: db.Int(m, "two_factor_last_counter"),
		MustChangePassword:   db.Int(m, "must_change_password") == 1,
		ChangelogSeen:        db.Str(m, "changelog_seen"),
		OrgID:                db.IntPtr(m, "org_id"),
		OrgAdmin:             db.Int(m, "org_admin") == 1,
		DiveraAccess:         NormalizeDivera(db.Str(m, "divera_access")),
		CreatedAt:            db.Str(m, "created_at"),
		UpdatedAt:            db.Str(m, "updated_at"),
	}
}

// EmailStr liefert die E-Mail oder "".
func (u *User) EmailStr() string {
	if u == nil || u.Email == nil {
		return ""
	}
	return *u.Email
}

// DisplayNameStr liefert den Anzeigenamen oder "".
func (u *User) DisplayNameStr() string {
	if u == nil || u.DisplayName == nil {
		return ""
	}
	return *u.DisplayName
}

// HasTwoFactor: 2FA aktiv und Secret vorhanden.
func (u *User) HasTwoFactor() bool {
	return u != nil && u.TwoFactorEnabled && u.TwoFactorSecret != nil && *u.TwoFactorSecret != ""
}

// IsEinsatzUser: user oder leiter (nicht admin).
func (u *User) IsEinsatzUser() bool {
	return u != nil && (u.Role == "user" || u.Role == "leiter")
}

// OrgIDValue: Organisations-ID oder 0.
func (u *User) OrgIDValue() int64 {
	if u == nil || u.OrgID == nil {
		return 0
	}
	return *u.OrgID
}

// IsOrgAdmin: Einsatz-Konto mit Org-Admin-Flag und zugeordneter Organisation.
func (u *User) IsOrgAdmin() bool {
	return u.IsEinsatzUser() && u.OrgAdmin && u.OrgIDValue() > 0
}

// Label: Anzeigename, sonst Benutzername, sonst "?".
func Label(u *User) string {
	if u == nil {
		return "?"
	}
	if dn := strings.TrimSpace(u.DisplayNameStr()); dn != "" {
		return dn
	}
	if u.Username != "" {
		return u.Username
	}
	return "?"
}

func FindByID(q db.Q, id int64) (*User, error) {
	m, err := db.QueryMap(q, "SELECT * FROM users WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return fromMap(m), nil
}

func FindByUsername(q db.Q, username string) (*User, error) {
	m, err := db.QueryMap(q, "SELECT * FROM users WHERE username = ?", username)
	if err != nil {
		return nil, err
	}
	return fromMap(m), nil
}

func FindByUsernameOrEmail(q db.Q, identifier string) (*User, error) {
	m, err := db.QueryMap(q, "SELECT * FROM users WHERE username = ? OR (email IS NOT NULL AND email = ?) LIMIT 1", identifier, identifier)
	if err != nil {
		return nil, err
	}
	return fromMap(m), nil
}

func FindByEmail(q db.Q, email string) (*User, error) {
	m, err := db.QueryMap(q, "SELECT * FROM users WHERE email = ? LIMIT 1", email)
	if err != nil {
		return nil, err
	}
	return fromMap(m), nil
}

func All(q db.Q) ([]*User, error) {
	rows, err := db.QueryMaps(q, "SELECT * FROM users ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	out := make([]*User, 0, len(rows))
	for _, m := range rows {
		out = append(out, fromMap(m))
	}
	return out, nil
}

// AllInOrg: alle Konten einer Organisation (fuer Org-Admins).
func AllInOrg(q db.Q, orgID int64) ([]*User, error) {
	rows, err := db.QueryMaps(q, "SELECT * FROM users WHERE org_id = ? ORDER BY id ASC", orgID)
	if err != nil {
		return nil, err
	}
	out := make([]*User, 0, len(rows))
	for _, m := range rows {
		out = append(out, fromMap(m))
	}
	return out, nil
}

// AllEinsatzUsers: Nutzer, die an Einsaetzen mitarbeiten koennen (user + leiter).
func AllEinsatzUsers(q db.Q) ([]*User, error) {
	rows, err := db.QueryMaps(q, "SELECT * FROM users WHERE role IN ('user','leiter') ORDER BY LOWER(COALESCE(display_name, username)) ASC")
	if err != nil {
		return nil, err
	}
	out := make([]*User, 0, len(rows))
	for _, m := range rows {
		out = append(out, fromMap(m))
	}
	return out, nil
}

func Count(q db.Q) (int64, error) {
	return db.QueryInt(q, "SELECT COUNT(*) FROM users")
}

// HashPassword erzeugt einen bcrypt-Hash (Kosten 10 wie PHP PASSWORD_DEFAULT).
func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), 10)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// VerifyPassword prueft ein Passwort gegen einen bcrypt-Hash ($2y$ aus PHP eingeschlossen).
func VerifyPassword(hash, plain string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

func nullStr(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

func Create(q db.Q, username, plainPassword string, email, displayName *string, role string, mustChange bool) (int64, error) {
	hash, err := HashPassword(plainPassword)
	if err != nil {
		return 0, err
	}
	mc := 0
	if mustChange {
		mc = 1
	}
	res, err := q.Exec(`INSERT INTO users (username, password_hash, email, display_name, role,
		two_factor_enabled, two_factor_secret, must_change_password, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, NULL, ?, datetime('now'), datetime('now'))`,
		username, hash, nullStr(email), nullStr(displayName), NormalizeRole(role), mc)
	if err != nil {
		return 0, err
	}
	return db.LastID(res), nil
}

func UpdatePassword(q db.Q, id int64, plain string, clearMustChange bool) error {
	hash, err := HashPassword(plain)
	if err != nil {
		return err
	}
	if clearMustChange {
		_, err = q.Exec("UPDATE users SET password_hash = ?, must_change_password = 0, updated_at = datetime('now') WHERE id = ?", hash, id)
	} else {
		_, err = q.Exec("UPDATE users SET password_hash = ?, updated_at = datetime('now') WHERE id = ?", hash, id)
	}
	return err
}

func UpdateUsername(q db.Q, id int64, username string) error {
	_, err := q.Exec("UPDATE users SET username = ?, updated_at = datetime('now') WHERE id = ?", username, id)
	return err
}

func UpdateEmail(q db.Q, id int64, email *string) error {
	_, err := q.Exec("UPDATE users SET email = ?, updated_at = datetime('now') WHERE id = ?", nullStr(email), id)
	return err
}

func UpdateDisplayName(q db.Q, id int64, displayName *string) error {
	_, err := q.Exec("UPDATE users SET display_name = ?, updated_at = datetime('now') WHERE id = ?", nullStr(displayName), id)
	return err
}

func SetRole(q db.Q, id int64, role string) error {
	_, err := q.Exec("UPDATE users SET role = ?, updated_at = datetime('now') WHERE id = ?", NormalizeRole(role), id)
	return err
}

func CountAdmins(q db.Q) (int64, error) {
	return db.QueryInt(q, "SELECT COUNT(*) FROM users WHERE role = 'admin'")
}

// SetOrg ordnet ein Konto einer Organisation zu (nil = keine).
func SetOrg(q db.Q, id int64, orgID *int64) error {
	var v any
	if orgID != nil && *orgID > 0 {
		v = *orgID
	}
	_, err := q.Exec("UPDATE users SET org_id = ?, updated_at = datetime('now') WHERE id = ?", v, id)
	return err
}

// SetOrgAdmin setzt das Org-Admin-Flag.
func SetOrgAdmin(q db.Q, id int64, on bool) error {
	v := 0
	if on {
		v = 1
	}
	_, err := q.Exec("UPDATE users SET org_admin = ?, updated_at = datetime('now') WHERE id = ?", v, id)
	return err
}

// SetDiveraAccess setzt das Divera-Recht (off/read/write).
func SetDiveraAccess(q db.Q, id int64, level string) error {
	_, err := q.Exec("UPDATE users SET divera_access = ?, updated_at = datetime('now') WHERE id = ?", NormalizeDivera(level), id)
	return err
}

// CountOrgAdmins: Org-Admins einer Organisation.
func CountOrgAdmins(q db.Q, orgID int64) (int64, error) {
	return db.QueryInt(q, "SELECT COUNT(*) FROM users WHERE org_id = ? AND org_admin = 1 AND role IN ('user','leiter')", orgID)
}

var reBase32 = regexp.MustCompile(`[^A-Za-z2-7]`)

func EnableTwoFactor(q db.Q, id int64, secret string) error {
	normalized := strings.ToUpper(reBase32.ReplaceAllString(secret, ""))
	_, err := q.Exec("UPDATE users SET two_factor_secret = ?, two_factor_enabled = 1, updated_at = datetime('now') WHERE id = ?", normalized, id)
	return err
}

func DisableTwoFactor(q db.Q, id int64) error {
	_, err := q.Exec("UPDATE users SET two_factor_secret = NULL, two_factor_enabled = 0, two_factor_last_counter = 0, updated_at = datetime('now') WHERE id = ?", id)
	return err
}

func SetTwoFactorLastCounter(q db.Q, id int64, counter int64) error {
	_, err := q.Exec("UPDATE users SET two_factor_last_counter = ?, updated_at = datetime('now') WHERE id = ?", counter, id)
	return err
}

func SetMustChangePassword(q db.Q, id int64, must bool) error {
	v := 0
	if must {
		v = 1
	}
	_, err := q.Exec("UPDATE users SET must_change_password = ?, updated_at = datetime('now') WHERE id = ?", v, id)
	return err
}

func Delete(q db.Q, id int64) error {
	_, err := q.Exec("DELETE FROM users WHERE id = ?", id)
	return err
}

// OwnsEinsaetze meldet, ob der Nutzer Ersteller von Einsaetzen ist (FK RESTRICT).
func OwnsEinsaetze(q db.Q, id int64) (bool, error) {
	n, err := db.QueryInt(q, "SELECT COUNT(*) FROM einsaetze WHERE owner_id = ?", id)
	return n > 0, err
}

// GeneratePassword: zufaelliges Initialpasswort (14 Zeichen, ohne verwechselbare Zeichen).
func GeneratePassword(length int) string {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!%&+-?"
	if length <= 0 {
		length = 14
	}
	out := make([]byte, length)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out)
}

// ----- Passwort-Reset-Tokens -----

func CreatePasswordResetToken(q db.Q, userID int64, ttl time.Duration) (string, error) {
	if _, err := q.Exec("DELETE FROM password_resets WHERE user_id = ?", userID); err != nil {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	expires := time.Now().Add(ttl).UTC().Format("2006-01-02 15:04:05")
	_, err := q.Exec("INSERT INTO password_resets (user_id, token_hash, expires_at, created_at) VALUES (?, ?, ?, datetime('now'))",
		userID, hex.EncodeToString(sum[:]), expires)
	return token, err
}

// ConsumePasswordResetToken loest ein Token ein (einmalig) und liefert den Nutzer oder nil.
func ConsumePasswordResetToken(q db.Q, token string) (*User, error) {
	sum := sha256.Sum256([]byte(token))
	row, err := db.QueryMap(q, "SELECT id, user_id, expires_at FROM password_resets WHERE token_hash = ? LIMIT 1", hex.EncodeToString(sum[:]))
	if err != nil || row == nil {
		return nil, err
	}
	if _, err := q.Exec("DELETE FROM password_resets WHERE id = ?", db.Int(row, "id")); err != nil {
		return nil, err
	}
	exp, perr := time.ParseInLocation("2006-01-02 15:04:05", db.Str(row, "expires_at"), time.UTC)
	if perr != nil || exp.Before(time.Now()) {
		return nil, nil
	}
	return FindByID(q, db.Int(row, "user_id"))
}
