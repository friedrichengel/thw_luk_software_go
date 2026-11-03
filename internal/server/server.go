// Package server verdrahtet alle Teile zu einem http.Handler: statische
// Dateien, Seiten, JSON-APIs, Sitzungen und Sicherheits-Header.
package server

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	luk "github.com/friedrichengel/thw_luk_software_go"
	"github.com/friedrichengel/thw_luk_software_go/internal/api/changelogapi"
	"github.com/friedrichengel/thw_luk_software_go/internal/api/diveraapi"
	"github.com/friedrichengel/thw_luk_software_go/internal/api/einsatzapi"
	"github.com/friedrichengel/thw_luk_software_go/internal/api/kraefteapi"
	"github.com/friedrichengel/thw_luk_software_go/internal/api/lagekartenapi"
	"github.com/friedrichengel/thw_luk_software_go/internal/api/proxyapi"
	"github.com/friedrichengel/thw_luk_software_go/internal/api/schadenskontenapi"
	"github.com/friedrichengel/thw_luk_software_go/internal/api/steinhookapi"
	"github.com/friedrichengel/thw_luk_software_go/internal/api/updateapi"
	"github.com/friedrichengel/thw_luk_software_go/internal/auth"
	"github.com/friedrichengel/thw_luk_software_go/internal/changelog"
	"github.com/friedrichengel/thw_luk_software_go/internal/config"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/divera"
	"github.com/friedrichengel/thw_luk_software_go/internal/httpx"
	"github.com/friedrichengel/thw_luk_software_go/internal/kraefte"
	"github.com/friedrichengel/thw_luk_software_go/internal/mail"
	"github.com/friedrichengel/thw_luk_software_go/internal/org"
	"github.com/friedrichengel/thw_luk_software_go/internal/pages"
	"github.com/friedrichengel/thw_luk_software_go/internal/session"
	"github.com/friedrichengel/thw_luk_software_go/internal/settings"
	"github.com/friedrichengel/thw_luk_software_go/internal/tiles"
	"github.com/friedrichengel/thw_luk_software_go/internal/update"
	"github.com/friedrichengel/thw_luk_software_go/internal/upstream"
)

// Server haelt App und Handler zusammen.
type Server struct {
	App     *httpx.App
	Handler http.Handler
	Katalog *kraefte.Katalog
}

// New baut die App mit allen Diensten und Routen.
func New(cfg *config.Config, database *db.DB, logger *log.Logger) (*Server, error) {
	if logger == nil {
		logger = log.Default()
	}
	clRaw, _ := fs.ReadFile(luk.Public, "public/daten/changelog.json")
	// Stammdaten: STAMM_FILE, sonst die eingebettete Datei der Installation,
	// sonst die Beispieldatei des Repositories.
	stammRaw, stammSrc, err := kraefte.ReadStamm(luk.Public, cfg.StammFile)
	if err != nil {
		return nil, err
	}
	kat := kraefte.New(cfg, stammRaw)
	if stammSrc == kraefte.StammExample {
		logger.Printf("stammdaten: %s (Beispieldaten; eigene Datei nach %s legen oder STAMM_FILE setzen)", stammSrc, kraefte.StammFile)
	} else {
		logger.Printf("stammdaten: %s (%d Ortsverbaende, Stand %v)", stammSrc, len(kat.OVs()), kat.Stamm()["stand"])
	}
	// Regionale Standards aus den Stammdaten: Kartenmittelpunkt, Nachrichten-Feeds.
	center := kat.Center(cfg)
	org.SetDefaultFeeds(org.FeedsFromStamm(kat.Feeds()))

	app := &httpx.App{
		Cfg:       cfg,
		DB:        database,
		Auth:      &auth.Auth{Cfg: cfg, DB: database},
		Changelog: changelog.New(clRaw, cfg.Version),
		Update:    update.New(cfg),
		Center:    center,
		Log:       logger,
	}
	app.Sessions = &session.Store{DB: database, Idle: time.Duration(cfg.SessionIdleMin) * time.Minute, Secure: app.Auth.IsHTTPS}
	app.Feeds = &upstream.Feeds{Cfg: cfg, Cache: &upstream.Cache{}}
	app.Divera = divera.New(cfg)
	// Nach einem Binary-Update: "running" im Status auf "ok" setzen.
	app.Update.MarkStarted()
	// Organisationsmodell: Altbestand einer Standard-Organisation zuordnen.
	if err := org.EnsureDefault(database, center.Name); err != nil {
		return nil, err
	}
	kat.SteinMode = func(orgID int64) string { return org.SteinModeOf(database, orgID) }
	// Standort einer Organisation aus den Stammdaten (nur OVs mit stamm_key "ov:<key>").
	app.OrgHome = func(orgID int64) map[string]any {
		o, err := org.FindByID(database, orgID)
		if err != nil || o == nil || !strings.HasPrefix(o.StammKey, "ov:") {
			return nil
		}
		ov := kat.OV(strings.TrimPrefix(o.StammKey, "ov:"))
		if ov == nil {
			return nil
		}
		lat, okLat := ov["lat"].(float64)
		lng, okLng := ov["lng"].(float64)
		if !okLat || !okLng {
			return nil
		}
		name, _ := ov["name"].(string)
		return map[string]any{"lat": lat, "lng": lng, "name": name}
	}
	kat.SteinKey = func(orgID int64) string {
		key, err := org.Credential(database, orgID, org.KindStein, cfg.Secret)
		if err != nil {
			logger.Printf("stein: Schluessel der Organisation %d: %v", orgID, err)
			return ""
		}
		return key
	}

	mux := http.NewServeMux()
	public, err := fs.Sub(luk.Public, "public")
	if err != nil {
		return nil, err
	}
	assets, err := fs.Sub(luk.Assets, "assets")
	if err != nil {
		return nil, err
	}
	httpx.RegisterStatic(mux, public, assets)
	pageDeps := pages.Deps{
		// Mail-Einstellungen aus der Serververwaltung gehen der Umgebung vor (je Versand neu gelesen).
		SendMail: func(to, subject, body string) error {
			return mail.Send(settings.MailConfig(database, cfg), to, subject, body)
		},
		OnCredentialChange: func(orgID int64, kind string) {
			if kind == org.KindStein {
				kat.ForgetStein(orgID)
			}
		},
		Stamm: func() (map[string]any, []map[string]any, []map[string]any) {
			lv, _ := kat.Stamm()["lv"].(map[string]any)
			return lv, kat.Regionen(), kat.OVs()
		},
		SeedOrganisationen: func(only map[string]bool) (int, int, error) {
			var res org.SeedResult
			err := database.Tx(func(q db.Q) error {
				lv, _ := kat.Stamm()["lv"].(map[string]any)
				var err error
				res, err = org.SeedFromStamm(q, lv, kat.Regionen(), kat.OVs(), only)
				return err
			})
			return res.Created, res.Existing, err
		},
		DiveraGroups: func(orgID int64) ([]divera.Group, error) {
			access, err := org.Credential(database, orgID, org.KindDiveraAccess, cfg.Secret)
			if err != nil || strings.TrimSpace(access) == "" {
				return nil, errors.New("kein Divera-Zugangsschlüssel hinterlegt")
			}
			system, _ := org.Credential(database, orgID, org.KindDiveraSystem, cfg.Secret)
			if strings.TrimSpace(system) == "" {
				system = access
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			return app.Divera.Groups(ctx, &divera.Source{OrgID: orgID, AccessKey: strings.TrimSpace(access), SystemKey: strings.TrimSpace(system)})
		},
		CheckCredential: func(orgID int64, kind string) (string, error) {
			key, err := org.Credential(database, orgID, kind, cfg.Secret)
			if err != nil {
				return "", err
			}
			if key == "" {
				return "", errors.New("kein Schlüssel hinterlegt")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			if kind == org.KindStein {
				return kat.SteinCheck(ctx, key)
			}
			return app.Divera.CheckKey(ctx, kind, key)
		},
	}
	einsatzapi.Register(mux, app, kat)
	lagekartenapi.Register(mux, app)
	schadenskontenapi.Register(mux, app)
	kraefteapi.Register(mux, app, kat)
	changelogapi.Register(mux, app)
	proxyapi.Register(mux, app, kat)
	diveraapi.Register(mux, app)
	steinhookapi.Register(mux, app, kat)
	updateapi.Register(mux, app)
	// Eingebauter Kachel-Cache (/tiles/...): nur mit Sitzung, ausser TILE_PUBLIC.
	tileSvc := tiles.New(cfg, logger)
	tileSvc.IsLoggedIn = func(r *http.Request) bool { return auth.IsLoggedIn(app.Sess(r)) }
	// Admin > Server geht der Umgebung vor (Kachelquelle, Freigabe, Token).
	tileSvc.SourceFn = func() string { return settings.Get(database, settings.TileSource) }
	tileSvc.PublicFn = func() bool {
		if v, set := settings.Bool(database, settings.TilePublic); set {
			return v
		}
		return cfg.TilePublic
	}
	tileSvc.TokenFn = func() string {
		if v := settings.Get(database, settings.TileToken); v != "" {
			return v
		}
		return cfg.TileToken
	}
	tileSvc.Register(mux)
	pageDeps.TileTest = func(source string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return tileSvc.Check(ctx, source)
	}
	pageDeps.TileState = func() (source string, public bool, token string) {
		return tileSvc.Source(), tileSvc.Public(), tileSvc.Token()
	}
	pages.Register(mux, app, pageDeps)

	var h http.Handler = mux
	h = app.Sessions.Middleware(h)
	// Externe Kachel-URL in der CSP erlauben (Bildquelle und fetch fuer den
	// Karten-PDF-Export); leer bei eingebautem Kachel-Cache.
	h = httpx.SecurityHeadersWith(cfg.TileOrigin())(h)
	h = recoverer(logger, h)
	return &Server{App: app, Handler: h, Katalog: kat}, nil
}

// recoverer faengt Panics und antwortet mit 500 statt die Verbindung zu kappen.
func recoverer(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Printf("panic %s %s: %v", r.Method, r.URL.Path, rec)
				http.Error(w, "Serverfehler", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
