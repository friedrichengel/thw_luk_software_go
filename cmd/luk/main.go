// luk - THW LuK Stabsansicht als ein einzelnes Programm.
//
//	luk serve                 Webserver starten (Standard, wenn kein Befehl angegeben)
//	luk init-db               Datenbank anlegen/migrieren
//	luk seed-admin [...]      ersten Admin anlegen oder Passwort zuruecksetzen
//	luk mail-test ADRESSE     Testmail ueber die konfigurierten Einstellungen
//	luk healthcheck [URL]     Health-Check (Exit 0 = gesund), fuer Docker HEALTHCHECK
//	luk version               Version und Build ausgeben
//
// Konfiguration: Umgebungsvariablen oder config/.env (siehe config/.env.sample).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/kraefte"
	"github.com/friedrichengel/thw_luk_software_go/internal/mail"
	"github.com/friedrichengel/thw_luk_software_go/internal/org"
	"github.com/friedrichengel/thw_luk_software_go/internal/server"
	"github.com/friedrichengel/thw_luk_software_go/internal/settings"
	"github.com/friedrichengel/thw_luk_software_go/internal/update"
	"github.com/friedrichengel/thw_luk_software_go/internal/user"
)

func main() {
	log.SetFlags(log.LstdFlags)
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = runServe(args)
	case "init-db":
		err = runInitDB()
	case "seed-admin":
		err = runSeedAdmin(args)
	case "mail-test":
		err = runMailTest(args)
	case "healthcheck":
		err = runHealthcheck(args)
	case "version", "-v", "--version":
		fmt.Printf("luk %s (%s %s)\n", versionString(), config.BuildStamp, config.BuildDate)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "Unbekannter Befehl: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fehler: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`luk - THW LuK Stabsansicht

  luk serve [-listen ADDR]   Webserver starten (Standard)
  luk init-db                Datenbank anlegen/migrieren
  luk seed-admin [--user NAME] [--reset] [--if-empty] [--password-file PFAD]
  luk mail-test ADRESSE      Testmail senden
  luk healthcheck [URL]      Health-Check, Exit 0 = gesund
  luk version

Konfiguration ueber Umgebungsvariablen oder config/.env (siehe config/.env.sample).`)
}

func versionString() string {
	if config.Version != "" {
		return config.Version
	}
	return "dev"
}

func openDB(cfg *config.Config) (*db.DB, error) {
	return db.Open(cfg.DBPath, luk.Schema)
}

// ---------------------------------------------------------------------------

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "", "Adresse, z.B. 127.0.0.1:8080 (Standard: LISTEN_ADDR)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.ListenAddr = *listen
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("Datenverzeichnis %s: %w", cfg.DataDir, err)
	}
	if cfg.UpdateMode == "docker" && cfg.UpdateDir != "" {
		for _, d := range []string{"inbox", "tmp", "archive"} {
			_ = os.MkdirAll(filepath.Join(cfg.UpdateDir, d), 0o770)
		}
	}
	database, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer database.Close()

	// Erster Start ohne Nutzer: Admin anlegen, Initialpasswort ausgeben und
	// in data/admin-initialpasswort.txt ablegen (setup.sh zeigt es an und loescht es).
	if n, _ := user.Count(database); n == 0 {
		pw, err := seedAdmin(database, "admin", false)
		if err != nil {
			return err
		}
		if pw != "" {
			f := filepath.Join(cfg.DataDir, "admin-initialpasswort.txt")
			if err := os.WriteFile(f, []byte(pw+"\n"), 0o600); err == nil {
				log.Printf("Admin-Konto \"admin\" angelegt. Initialpasswort: %s (auch in %s)", pw, f)
			} else {
				log.Printf("Admin-Konto \"admin\" angelegt. Initialpasswort: %s", pw)
			}
		}
	}

	srv, err := server.New(cfg, database, log.Default())
	if err != nil {
		return err
	}
	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler,
		ReadHeaderTimeout: 20 * time.Second,
		ReadTimeout:       10 * time.Minute, // grosse Uploads (Import, Update)
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 * 1024,
	}
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("lauschen auf %s: %w", cfg.ListenAddr, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	restart := make(chan struct{}, 1)
	update.RestartFunc = func() {
		select {
		case restart <- struct{}{}:
		default:
		}
	}

	envInfo := cfg.EnvFile
	if envInfo == "" {
		envInfo = "nur Umgebungsvariablen"
	}
	log.Printf("THW LuK v%s bereit auf http://%s - %s (Konfiguration: %s, Daten: %s)", versionString(), cfg.ListenAddr, cfg.BaseURL, envInfo, cfg.DataDir)
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()

	doRestart := false
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Printf("Beende ...")
	case <-restart:
		log.Printf("Neustart nach Update ...")
		doRestart = true
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	database.Close()
	if doRestart {
		return restartSelf()
	}
	return nil
}

// ---------------------------------------------------------------------------

func runInitDB() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	database, err := openDB(cfg)
	if err != nil {
		return err
	}
	// Organisationsmodell: Altbestand einer Standard-Organisation zuordnen (wie
	// beim Start; Name aus MAP_CENTER_NAME, sonst aus den Stammdaten).
	name := cfg.MapCenterName
	if raw, _, err := kraefte.ReadStamm(luk.Public, cfg.StammFile); err == nil {
		name = kraefte.New(cfg, raw).Center(cfg).Name
	}
	if err := org.EnsureDefault(database, name); err != nil {
		database.Close()
		return err
	}
	database.Close()
	fmt.Printf("DB initialisiert: %s\n", cfg.DBPath)
	return nil
}

// seedAdmin legt den Admin an (oder setzt sein Passwort zurueck) und liefert das Passwort.
func seedAdmin(database *db.DB, username string, reset bool) (string, error) {
	existing, err := user.FindByUsername(database, username)
	if err != nil {
		return "", err
	}
	pw := user.GeneratePassword(14)
	if existing != nil && !reset {
		fmt.Printf("Nutzer '%s' existiert bereits (Rolle: %s). Mit --reset Passwort zuruecksetzen.\n", username, existing.Role)
		return "", nil
	}
	if existing != nil {
		if err := user.UpdatePassword(database, existing.ID, pw, false); err != nil {
			return "", err
		}
		if err := user.SetMustChangePassword(database, existing.ID, true); err != nil {
			return "", err
		}
		if err := user.SetRole(database, existing.ID, "admin"); err != nil {
			return "", err
		}
		fmt.Printf("Passwort fuer '%s' zurueckgesetzt.\n", username)
	} else {
		dn := "Administrator"
		if _, err := user.Create(database, username, pw, nil, &dn, "admin", true); err != nil {
			return "", err
		}
		fmt.Printf("Admin '%s' angelegt.\n", username)
	}
	return pw, nil
}

func runSeedAdmin(args []string) error {
	fs := flag.NewFlagSet("seed-admin", flag.ContinueOnError)
	username := fs.String("user", "admin", "Benutzername")
	reset := fs.Bool("reset", false, "Passwort des Admins zuruecksetzen")
	ifEmpty := fs.Bool("if-empty", false, "nur, wenn noch gar kein Nutzer existiert")
	pwFile := fs.String("password-file", "", "Initialpasswort zusaetzlich in Datei (0600) schreiben")
	// Die Schreibweise --user=NAME wird von flag ebenfalls verstanden.
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*username) == "" {
		return errors.New("ungueltiger Benutzername")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	database, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer database.Close()
	if *ifEmpty {
		if n, _ := user.Count(database); n > 0 {
			fmt.Println("Nutzer vorhanden, kein Admin angelegt (--if-empty).")
			return nil
		}
	}
	pw, err := seedAdmin(database, *username, *reset)
	if err != nil || pw == "" {
		return err
	}
	fmt.Printf("Initialpasswort: %s\nBitte sicher uebergeben - beim ersten Login ist ein Passwortwechsel Pflicht.\n", pw)
	if *pwFile != "" {
		if err := os.WriteFile(*pwFile, []byte(pw+"\n"), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "Passwortdatei %s konnte nicht geschrieben werden: %v\n", *pwFile, err)
		} else {
			fmt.Printf("Initialpasswort auch in %s abgelegt.\n", *pwFile)
		}
	}
	return nil
}

func runMailTest(args []string) error {
	if len(args) < 1 || !strings.Contains(args[0], "@") {
		return errors.New("Aufruf: luk mail-test empfaenger@example.org")
	}
	to := args[0]
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Einstellungen aus der Serververwaltung (Tabelle settings) ueberlagern die Umgebung
	if database, err := openDB(cfg); err == nil {
		cfg = settings.MailConfig(database, cfg)
		database.Close()
		fmt.Println("Konfiguration (Umgebung, ueberlagert durch Serververwaltung > Server):")
	} else {
		fmt.Println("Konfiguration (nur Umgebung, Datenbank nicht lesbar):")
	}
	if cfg.SMTPHost != "" {
		fmt.Printf("  Transport : SMTP %s:%d (%s)\n", cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPSecurity)
		u := cfg.SMTPUser
		if u == "" {
			u = "(ohne Login)"
		}
		fmt.Printf("  Benutzer  : %s\n", u)
		p := "(leer)"
		if cfg.SMTPPass != "" {
			p = "********"
		}
		fmt.Printf("  Passwort  : %s\n", p)
	} else {
		fmt.Println("  Transport : sendmail - SMTP_HOST ist nicht gesetzt")
	}
	fmt.Printf("  Absender  : %s <%s>\n", cfg.MailFromName, cfg.MailFrom)
	fmt.Printf("  Empfaenger: %s\n\n", to)
	body := "Dies ist eine Testmail der " + cfg.Name + " Stabsansicht.\n\n" +
		"Gesendet am " + time.Now().Format("02.01.2006 15:04:05") + " von " + cfg.BaseURL + "\n" +
		"Umlaute-Test: aeoeue -> äöü ß\n"
	if err := mail.Send(cfg, to, "Testmail - "+cfg.Name, body); err != nil {
		return fmt.Errorf("Mailversand: %w", err)
	}
	fmt.Println("OK - Mail wurde vom Server angenommen. Bitte Posteingang (und Spam-Ordner) pruefen.")
	return nil
}

func runHealthcheck(args []string) error {
	url := ""
	if len(args) > 0 {
		url = args[0]
	}
	if url == "" {
		addr := os.Getenv("LISTEN_ADDR")
		if addr == "" {
			addr = "127.0.0.1:8080"
		}
		if strings.HasPrefix(addr, ":") {
			addr = "127.0.0.1" + addr
		}
		if strings.HasPrefix(addr, "0.0.0.0:") {
			addr = "127.0.0.1" + addr[len("0.0.0.0"):]
		}
		url = "http://" + addr + "/healthz"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"ok":true`) {
		return fmt.Errorf("ungesund: %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	fmt.Println("ok")
	return nil
}
