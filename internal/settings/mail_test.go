package settings_test

import (
	"path/filepath"
	"testing"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/settings"
)

// Mail-Einstellungen der Oberflaeche ueberlagern die Umgebung; das Passwort
// liegt verschluesselt in der Tabelle und ist nur mit APP_SECRET lesbar.
func TestMailConfig(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.sqlite"), luk.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cfg := &config.Config{Secret: "s3cret", MailFrom: "env@example.org", MailFromName: "Env", SMTPHost: "", SMTPPort: 587, SMTPSecurity: "tls"}

	// nichts gesetzt: Umgebung unveraendert, Original nicht angefasst
	eff := settings.MailConfig(d, cfg)
	if eff.MailFrom != "env@example.org" || eff.SMTPHost != "" || eff == cfg {
		t.Fatalf("ohne Einstellungen: %+v", eff)
	}

	settings.Set(d, settings.SMTPHost, "smtp.example.org", 1)
	settings.Set(d, settings.SMTPPort, "465", 1)
	settings.Set(d, settings.SMTPUser, "login", 1)
	if err := settings.SetSecret(d, settings.SMTPPass, "geheim", cfg.Secret, 1); err != nil {
		t.Fatal(err)
	}
	if raw := settings.Get(d, settings.SMTPPass); raw == "" || raw == "geheim" {
		t.Fatalf("Passwort nicht verschluesselt abgelegt: %q", raw)
	}
	eff = settings.MailConfig(d, cfg)
	if eff.SMTPHost != "smtp.example.org" || eff.SMTPPort != 465 || eff.SMTPSecurity != "ssl" || eff.SMTPUser != "login" || eff.SMTPPass != "geheim" {
		t.Fatalf("ueberlagert: %+v", eff)
	}
	if eff.MailFrom != "env@example.org" || cfg.SMTPHost != "" || cfg.SMTPPass != "" {
		t.Fatalf("Umgebung muss fuer leere Felder gelten und cfg unveraendert bleiben: %+v / %+v", eff, cfg)
	}
	// ausdrueckliche Verschluesselung schlaegt die Port-Regel
	settings.Set(d, settings.SMTPSecurity, "none", 1)
	if eff = settings.MailConfig(d, cfg); eff.SMTPSecurity != "none" {
		t.Fatalf("security: %q", eff.SMTPSecurity)
	}
	// Formularwerte: Passwort nur gesetzt, wenn PassSet
	v := settings.Mail(d)
	if !v.PassSet || v.Port != "465" || v.Security != "none" {
		t.Fatalf("Mail(): %+v", v)
	}
	other := &config.Config{Secret: "anderes"}
	if settings.GetSecret(d, settings.SMTPPass, other.Secret) != "" {
		t.Fatal("Passwort mit fremdem Secret lesbar")
	}
	// Loeschen stellt die Umgebung wieder her
	settings.SetSecret(d, settings.SMTPPass, "", cfg.Secret, 1)
	settings.Set(d, settings.SMTPHost, "", 1)
	if eff = settings.MailConfig(d, cfg); eff.SMTPHost != "" || eff.SMTPPass != "" {
		t.Fatalf("nach Loeschen: %+v", eff)
	}
}
