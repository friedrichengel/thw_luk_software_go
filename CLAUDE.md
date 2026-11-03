# LuK Stabsansicht – Claude Code Hinweise

## Projektüberblick

Eigenes, nicht offizielles Projekt: Web-Anwendung für die Stabsarbeit (LuK)
im THW, nicht vom THW herausgegeben. Welche Regionalstellen und Ortsverbände
eine Installation kennt, steht in `public/daten/stammdaten.json` (nicht im
Repository, siehe unten). Mehrere Stäbe
(OV, RSt, LV, Leitung) führen Einsätze mit Führungsharke, Kräfteplanung,
Lagekarte, Schadenskonten, Anfahrtsübersicht, Änderungs-/Zugriffslog,
Freigabelinks und Gesamtexport. Mehrere Personen arbeiten gleichzeitig
(Live-Sync per Polling, Versionsprüfung pro Datensatz). Fachliche
Beschreibung, Rollenmatrix und Deployment: `README.md`.

Eigenständiges Go-Programm; Datenmodell, API-Kontrakte und der komplette
Client (`public/js`, `public/css`) bilden eine Einheit, bestehende
Datenbanken werden beim Start migriert (`db.bootstrap`).

## Technik

- **Go ≥ 1.27**, ein Binary (`cmd/luk`), SQLite über `modernc.org/sqlite`
  (reines Go, kein CGO), Passwörter bcrypt (`golang.org/x/crypto`), sonst
  nur Standardbibliothek. Kein Framework, kein Build-System für das Frontend.
- **Vanilla JS/CSS** in `public/`; Bibliotheken (Leaflet, jsPDF, …) liegen
  lokal. Alles in `public/` und `assets/` wird per `embed` in das Binary
  gebaut (`embed.go` im Wurzelverzeichnis). Neue statische Dateien gehören
  dorthin; nach Änderungen neu bauen.
- Konfiguration über Umgebungsvariablen oder `config/.env` (Vorlage
  `config/.env.sample`); ohne `APP_SECRET` wird eines erzeugt und in
  `data/.app_secret` abgelegt. `config/.env` und `data/` sind gitignored.

## Verzeichnisse

```
cmd/luk/           main.go: serve, init-db, seed-admin, mail-test, healthcheck, version
embed.go           Public (public/), Assets (assets/), Schema (sql/)
public/            css/, js/, images/, fonts/, daten/ (stammdaten.json [gitignored],
                   stammdaten.example.json, changelog.json),
                   manifest.webmanifest, service-worker.js
assets/            taktische_zeichen/ + _thumbs/, geo/ – ausgeliefert unter
                   /images/taktische_zeichen[_thumbs]/ und /geo/
sql/               schema.sqlite.sql (idempotent, bei jedem Start ausgeführt)
internal/
  config/          Konfiguration (.env + Umgebung), Version/BuildStamp per ldflags
  db/              SQLite öffnen (WAL, busy_timeout, _txlock=immediate), Migrationen,
                   Q-Interface (DB oder Tx), Zeilen als map[string]any
  stab/            Rollenmatrix, Status, Ebenen – einzige Quelle (window.STAB)
  org/             Organisationen (OV/RSt/LV), Org-Admin, verschluesselte Zugaenge (STEIN/Divera)
  upstream/        direkte Abrufe externer Dienste mit Cache (NINA, LHP, RSS, Basis fuer STEIN/Divera)
  divera/          Divera-24/7-Client (Zugang der Organisation, Namenskuerzung, Caches)
  access/          Zugriffskontext (Ctx), Ereignis-Log, Zugriffslog, ClientContext
  einsatz/         Einsätze, Mitglieder, Harke (Knoten/Mitglieder/Schichten), Anfahrt,
                   Freigabelinks, Verknüpfungen, UpdateVersioned/SoftDelete
  lagekarte/ schadenskonten/ export/ kraefte/   weitere Repositories
  user/ auth/ session/ totp/ mail/ changelog/ update/
  httpx/           App, API-Helfer (Body/CSRF/Ctx/Fail), Seiten-Gates, PageHelper-Port,
                   statische Dateien, Sicherheits-Header
  api/*api/        JSON-Endpunkte (/api/*), je Paket Register(mux, app, …)
  pages/           HTML-Seiten + templates/ (html/template)
  server/          Verdrahtung aller Routen und Middleware
deploy/            luk.service (systemd), nginx-site.conf.example
docker/            Dockerfile, compose.yml, setup.sh, make-archive.sh, host/ (luk-updater)
scripts/           build.sh (Cross-Compile der Binaries)
```

## Wichtige Konventionen

- **Stammdaten** (`public/daten/stammdaten.json`): die einzige Datei mit
  Wissen ueber einen konkreten Landesverband (lv, karte, feeds, bezirke,
  regionen, ovs). Sie ist gitignored und wird beim Bauen eingebettet, wenn sie
  vorliegt; sonst gilt `stammdaten.example.json` (fiktiver LV Musterland:
  Regionen `rst_nord`/`rst_sued`/`lv_dst`, OVs 101-104, Nachbar
  `n-fremdstadt`). Laden nur ueber `kraefte.ReadStamm` (STAMM_FILE ->
  eingebettet -> Beispiel). **Tests lesen immer `kraefte.StammExample`**
  (Server-Tests ueber `cfg.StammFile`), nie die echte Datei. Keine Namen,
  Koordinaten, Feeds oder BU-IDs eines echten Landesverbands in Code, Tests,
  Vorlagen oder Doku; neue regionale Einstellungen bekommen einen Abschnitt in
  der Stammdaten-Datei (Beispieldatei und README-Tabelle nachziehen).
  Kartenmittelpunkt des Servers nur ueber `App.Center` (MAP_CENTER_* ->
  `karte` -> `config.DefaultMapCenter`), Standard-Feeds ueber
  `org.DefaultRSSFeeds()` (4-1/4-2 eingebaut, 6-1/6-2 aus `feeds`).
- **Rollen und Status nur in `internal/stab` ändern.** Server und Client
  (über `window.STAB`) lesen dieselbe Konstante.
- **Kontorollen**: `user` / `leiter` / `admin`. Admin hat keinen Einsatzzugriff;
  `App.Ctx()` und `access.Resolve()` weisen Admins ab.
- **Organisationen** (`internal/org`): Jedes Einsatz-Konto hat `users.org_id`,
  Einsaetze `einsaetze.org_id` (vom Ersteller geerbt). **Org-Admin** ist ein
  Flag (`users.org_admin`) auf einem Einsatz-Konto, keine vierte Rolle; Gate
  `App.RequireOrgAdmin`. `/admin` arbeitet mit einem Geltungsbereich (global
  oder eigene Organisation). Zugaenge zu Fremdsystemen (`org_credentials`,
  Arten in `org.Kinds`) gehoeren der Organisation, sind mit `APP_SECRET`
  verschluesselt und werden nie an den Browser ausgeliefert; der globale
  Admin sieht nur "hinterlegt ja/nein". Nach Schluesselwechsel Caches ueber
  `pages.Deps.OnCredentialChange` verwerfen. `org.EnsureDefault` laeuft bei
  jedem Start (Standard-Organisation fuer Altbestand). Kartenmittelpunkt und
  Nachrichten-Feeds je Organisation (`organisationen.map_*`, `rss_feeds`) nie
  direkt aus `cfg` lesen, sondern ueber `App.MapCenter(orgID)` /
  `App.RSSFeeds(orgID)` (eigener Wert -> Stammdaten-Standort -> Server).
- **Externe Dienste** laufen direkt ueber `internal/upstream` (gemeinsamer
  `Cache` mit TTL, Deduplizierung, Stale-Fallback); es gibt keine
  vorgeschalteten Proxy-Dienste.
  Jeder Endpunkt prueft die Sitzung, bevor Daten fliessen. RSS nur fuer Hosts
  aus `RSS_ALLOWED_HOSTS`. STEIN (`kraefte.SteinAssetsFor`) und Divera
  (`internal/divera`) nutzen den Schluessel der Organisation; Cache-Schluessel
  enthalten nie den Klartext-Schluessel. STEIN laeuft ueber den gemeinsamen
  Vorrat `steinStore` (Stand je BU, Berechtigungen je Organisation, Datei
  `data/stein_store.json`): nie pro Organisation getrennt cachen, alte Daten
  nie verwerfen, Webhook-Meldungen prozessweit je BU sammeln. Divera-Antworten von `pull/all`
  immer durch `divera.Sanitize` (Initialen).
- **Server-Einstellungen** (`internal/settings`, Tabelle `settings`): Werte, die
  der globale Admin unter „Serververwaltung › Server“ setzt, ueberlagern die
  Umgebung und werden zur Laufzeit gelesen (Kacheln ueber Resolver-Funktionen,
  Mail ueber `settings.MailConfig(db, cfg)` je Versand). Geheimnisse dort nur
  ueber `settings.SetSecret/GetSecret` (APP_SECRET). Nie `cfg` zur Laufzeit
  veraendern; neue Werte bekommen einen Resolver oder eine Kopie der Config.
- **Kacheln** (`internal/tiles`, `GET /tiles/{z}/{x}/{y}.png`): eingebauter
  Platten-Cache unter `<DATA_DIR>/tiles`, Quelle `TILE_SOURCE` (OSM-konformer
  User-Agent mit Kontakt), Browser-URL `TILE_URL` (Standard eingebaut, im
  Client `window.CURRENT_OV.tile_url`). Ohne `TILE_PUBLIC` nur mit Sitzung,
  mit `TILE_PUBLIC` CORS * und optional `TILE_TOKEN`. Nie als offener Proxy
  betreiben; Single-Flight und 512-KB-Grenze beibehalten.
- **Org-Freigabe**: `einsatz_org_members` gibt allen Einsatz-Konten einer
  Organisation Stabsrollen in einem Einsatz. Mitgliedschaft immer ueber
  `access.MemberCond` und `access.RolesFor` pruefen (vereinigen persoenliche
  und Org-Rollen), nie nur gegen `einsatz_members`. Hilfsmittel (Divera) sind
  nie Teil einer Einsatz-Freigabe.
- **Upstream-Sperren**: `upstream.Cache` pausiert nach Fehlern (2 min), nach
  401/403 (30 min) und nach 429 (1 h); STEIN zusaetzlich prozessweit max. 10
  Abrufe/min (`steinLimiter`), weil STEIN 20/min/IP erlaubt und danach die IP
  eine Stunde sperrt. Ohne Schluessel nie abrufen. Zugaenge werden mit
  `pages.Deps.CheckCredential` live geprueft ("Verbindung pruefen").
- **STEIN-Webhook** (`organisationen.stein_mode` = poll|webhook,
  `internal/api/steinhookapi`): einziger Endpunkt ohne Sitzung neben
  `/healthz`, abgesichert ueber Token im Pfad (`org.WebhookToken`) und
  `X-Secret` (Tabelle `org_webhook_secrets`, je OV eine Zeile). Nachricht nur
  protokollieren (`org.LogWebhookEvent`) und BUs vormerken
  (`kat.SteinWebhookTouch`), nie direkt abrufen; der gebuendelte Nachabruf
  mischt in den Cache (`upstream.Cache.Put`). Im Webhook-Modus TTL 30 min.
- **Divera-Recht** pro Konto (`users.divera_access`: off/read/write) vergibt
  der Org-Admin; wirksam nur mit hinterlegtem `divera_access`-Zugang
  (`divera.Resolve`). Ohne wirksamen Zugang zeigen Dashboard, Footer und
  `/divera` nichts von Divera (`App.DiveraSource`).
- **Jede Schreibaktion** läuft über ein Repository (`internal/<bereich>`), nimmt
  `q db.Q` (DB oder Transaktion) und `*access.Ctx`, prüft die `version`
  (`einsatz.UpdateVersioned`, 409 `conflict` mit aktueller Zeile), schreibt ein
  Ereignis (`access.Event`) und ruft `einsatz.TouchEinsatz`. Transaktionen über
  `db.InTx(q, …)`, damit verschachtelte Aufrufe dieselbe Transaktion nutzen.
  Niemals Tabellen direkt aus einem Handler beschreiben (Ausnahme: `update_meta`,
  `close`, `reopen`, `delete` in `einsatzapi`).
- **Fehler** sind `*apierr.Error` (Code, HTTP-Status, Extra); `App.Fail` macht
  daraus `{error: code}`. Neue Codes auch in `public/js/luk_core.js` `ERROR_TEXT`.
- **Soft-Delete** überall (`deleted_at`); Leser filtern `deleted_at IS NULL`.
- **Schema**: `sql/schema.sqlite.sql` ist die Quelle für Tabellen und Indizes
  (idempotent, läuft bei jedem Start). Neue Spalten für bestehende
  Installationen zusätzlich in die `addColumn`-Liste in `db.bootstrap`, und
  **Indizes auf solchen Spalten nur in die Index-Liste nach `addColumn`**,
  nie ins Schema: dort scheitern sie auf einer älteren Datenbank am noch
  fehlenden Feld und der Dienst startet nicht (Bad Gateway). Jede neue
  Spalte bekommt einen Test, der eine Datenbank des Vorgängerschemas öffnet.
  Zeitstempel durchgehend als TEXT im Format `datetime('now')` (UTC, `db.Now()`,
  Vergleiche mit `db.Ago()`, Parsen mit `db.ParseTime()`), nie Unix-Zahlen.
  Aufzählungsspalten bekommen eine `CHECK`-Regel passend zur Go-Konstante.
- **Ereignisse** (`einsatz_events`) sind Änderungslog, Sync-Feed und Exportbasis.
  Für neue Entitäten: `entity`-Namen im Client (`luk_core.js` `tree.applyEvent`,
  `log.js` ENTITY_LABEL) nachziehen und `after` als vollständige Zeile liefern.
  Neue Tabellen mit Versionsprüfung registrieren ihre API-Form mit
  `einsatz.RegisterRow` (für `current` im Konflikt).
- **JSON-Form** der Antworten ist Vertrag mit dem Client (Feldnamen,
  Null-Werte, leere Arrays statt null). Zeilen-Structs tragen `json`-Tags.
- **API-Kontrakte** stehen als Kopfkommentar in `internal/api/*/handler.go`.
  POST = JSON mit `csrf` (aus `window.STAB.csrf`); Antworten `{ok:true,...}`
  oder `{error:code}`. Endpunkte heissen `/api/x` (keine Seiten-Pfade mit
  Endung). Jeder Endpunkt prueft die Sitzung.
- **Seiten**: `internal/pages`, ein Template je Seite; Kopf, Fußleiste und
  `window.STAB` kommen aus `httpx` (`PwaHead`, `Header`, `Footer`, `StabScript`).
  Neue Seite = Handler mit `app.RequirePage()` + Template wie `harke`.
- **Cache-Buster**: Eigene CSS/JS-Dateien **immer** mit `?v={{.AssetVersion}}`
  einbinden; statische Dateien werden mit `Cache-Control: immutable` (7 Tage)
  ausgeliefert und vom Service Worker cache-first bedient. Ohne Login erreichbar
  sind nur `/css/`, `/js/`, `/images/`, `/fonts/`, `/geo/` (`httpx.RegisterStatic`)
  und die Login-Seiten; `public/daten/` wird **nicht** ausgeliefert (Stammdaten
  nur ueber `api/kraefte?action=stamm`). Neue statische Pfade auch in
  `STATIC_PREFIX` des Service Workers eintragen.
- **Sitzungen** liegen in der Tabelle `sessions` (JSON) und überleben Neustarts.
  Cookie `luk_session`, Leerlauf `SESSION_IDLE_MIN`.
- **Changelog**: Bei jeder nutzersichtbaren Änderung `public/daten/changelog.json`
  ergänzen (neuester Eintrag oben, `show: true` nur für Dinge, die ein Overlay
  nach dem Login rechtfertigen) und `APP_VERSION` in `config/.env.sample`
  anheben; die Version kommt per `-ldflags` ins Binary (`scripts/build.sh`,
  `docker/Dockerfile`).
- **Update-Kanal** (`internal/update`): `UPDATE_MODE=docker` legt Dateien in
  `UPDATE_DIR` ab (Host-Dienst `docker/host/luk-updater.sh` führt aus),
  `UPDATE_MODE=binary` tauscht das eigene Binary (SHA-256, optional minisign)
  und startet neu (`update.RestartFunc`, gesetzt in `cmd/luk`). Externe Dienste
  nie als `localhost` verdrahten, sondern konfigurierbar (z.B. `TILE_SOURCE`).
- **Kräftekatalog**: Typ-Normalisierung STEIN ↔ Stammdaten in
  `internal/kraefte` und – als bewusste Kopie – `public/js/kraefte_typ.js`
  (beide Stellen pflegen). STEIN-Status kommt serverseitig gecacht über
  `api/kraefte`, Clients fragen nie direkt den Proxy.
- UI-Sprache Deutsch, Datenbank-Spalten/Code Englisch/ASCII; Log-Texte der
  Ereignisse sind feste Formulierungen (Export/Import und Tests hängen daran).

## Bauen und Testen

- `go build ./... && go vet ./... && go test ./...` – Tests legen Temp-Datenbanken
  an und decken Rechte, Konflikte, Sync-Feed, Freigabelinks, Export/Import,
  Kräftekatalog, Update-Kanal und die Seiten (httptest) ab.
- Lokal starten: `go run ./cmd/luk` (oder `luk serve`), dann
  `http://127.0.0.1:8080`. Beim ersten Start wird `admin` mit Initialpasswort
  angelegt (Ausgabe im Log und `data/admin-initialpasswort.txt`).
- Release-Binaries: `scripts/build.sh` (Version aus `config/.env.sample`),
  Docker: `docker/make-archive.sh`; `bash -n` für alle Shell-Skripte vor dem Commit.
