package settings

import (
	"strconv"
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/org"
)

// Schluessel der Mail-Einstellungen (Serververwaltung > Server). Jeder Wert
// ueberlagert die gleichnamige Umgebungsvariable; "" heisst "aus der Umgebung".
const (
	MailFrom     = "mail_from"      // Absenderadresse (MAIL_FROM)
	MailFromName = "mail_from_name" // Absendername (MAIL_FROM_NAME)
	SMTPHost     = "smtp_host"      // SMTP_HOST ("" = Umgebung; dort leer = sendmail)
	SMTPPort     = "smtp_port"      // SMTP_PORT
	SMTPSecurity = "smtp_security"  // SMTP_SECURITY: ssl | tls | none
	SMTPUser     = "smtp_user"      // SMTP_USER
	SMTPPass     = "smtp_pass"      // SMTP_PASS, verschluesselt mit APP_SECRET (org.Encrypt)
)

// SetSecret speichert einen Wert verschluesselt (AES-GCM, Schluessel aus
// APP_SECRET wie bei den Org-Zugaengen). "" loescht den Eintrag.
func SetSecret(q db.Q, key, plain, secret string, by int64) error {
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return Set(q, key, "", by)
	}
	enc, err := org.Encrypt(secret, plain)
	if err != nil {
		return err
	}
	return Set(q, key, enc, by)
}

// GetSecret entschluesselt einen mit SetSecret abgelegten Wert; "" wenn nicht
// gesetzt oder (nach Wechsel von APP_SECRET) nicht mehr lesbar.
func GetSecret(q db.Q, key, secret string) string {
	enc := Get(q, key)
	if enc == "" {
		return ""
	}
	plain, err := org.Decrypt(secret, enc)
	if err != nil {
		return ""
	}
	return plain
}

// MailValues: die Mail-Einstellungen als Formularwerte. Fuer die gespeicherten
// Werte (Mail) und fuer die Umgebung (Platzhalter) dieselbe Form; das
// Passwort erscheint nur als "gesetzt ja/nein".
type MailValues struct {
	From     string
	FromName string
	Host     string
	Port     string
	Security string
	User     string
	PassSet  bool
}

// Mail liest die in der Oberflaeche gespeicherten Mail-Einstellungen.
func Mail(q db.Q) MailValues {
	return MailValues{
		From:     Get(q, MailFrom),
		FromName: Get(q, MailFromName),
		Host:     Get(q, SMTPHost),
		Port:     Get(q, SMTPPort),
		Security: Get(q, SMTPSecurity),
		User:     Get(q, SMTPUser),
		PassSet:  Get(q, SMTPPass) != "",
	}
}

// MailEnv liefert die Werte der Umgebung in derselben Form.
func MailEnv(cfg *config.Config) MailValues {
	return MailValues{
		From:     cfg.MailFrom,
		FromName: cfg.MailFromName,
		Host:     cfg.SMTPHost,
		Port:     strconv.Itoa(cfg.SMTPPort),
		Security: cfg.SMTPSecurity,
		User:     cfg.SMTPUser,
		PassSet:  cfg.SMTPPass != "",
	}
}

// MailConfig liefert eine Kopie der Konfiguration, in der die gespeicherten
// Mail-Einstellungen die Umgebung ueberlagern. Wird bei jedem Versand neu
// gebildet, damit Aenderungen in der Oberflaeche sofort gelten.
func MailConfig(q db.Q, cfg *config.Config) *config.Config {
	c := *cfg
	return ApplyMail(&c, Mail(q), GetSecret(q, SMTPPass, cfg.Secret))
}

// ApplyMail ueberlagert c mit den gesetzten Werten aus v (und dem Passwort pass,
// wenn v.PassSet) und liefert c zurueck. Ohne ausdrueckliche Verschluesselung
// folgt sie dem Port: 465 = ssl, sonst STARTTLS.
func ApplyMail(c *config.Config, v MailValues, pass string) *config.Config {
	if v.From != "" {
		c.MailFrom = v.From
	}
	if v.FromName != "" {
		c.MailFromName = v.FromName
	}
	if v.Host != "" {
		c.SMTPHost = v.Host
	}
	if v.Port != "" {
		if n, err := strconv.Atoi(v.Port); err == nil && n > 0 && n <= 65535 {
			c.SMTPPort = n
			if v.Security == "" {
				c.SMTPSecurity = "tls"
				if n == 465 {
					c.SMTPSecurity = "ssl"
				}
			}
		}
	}
	if v.Security != "" {
		c.SMTPSecurity = v.Security
	}
	if v.User != "" {
		c.SMTPUser = v.User
	}
	if v.PassSet && pass != "" {
		c.SMTPPass = pass
	}
	return c
}
