# LuK Stabsansicht

Eigenes, privates Projekt: eine **Stabsansicht** für die Stabsarbeit
(Lage und Kommunikation, LuK) im THW. Die Software ist keine offizielle
Anwendung des THW. Welche Regionalstellen und Ortsverbände eine Installation
kennt, steht in einer Stammdaten-Datei, die nicht Teil des Repositories ist
(siehe „Stammdaten“). Mehrere Stäbe
(OV, RSt, LV, Leitung) führen ihre Einsätze mit Führungsharke, Kräfteplanung,
Lagekarte, Schadenskonten und Anfahrtsübersicht; alle Änderungen landen in
einem Änderungslog, Lesezugriffe im Zugriffslog. Mehrere Personen können
gleichzeitig arbeiten (Live-Sync, Versionsprüfung pro Datensatz).

Eckpunkte:

- **Ein Programm statt Laufzeitumgebung.** `luk` ist ein einzelnes Binary
  (Go), das Webserver, Seiten, API, statische Dateien (CSS, JS, taktische
  Zeichen, GeoJSON) und das Datenbankschema enthält. Kein Interpreter, keine
  weiteren Pakete, SQLite als Datenbank. Läuft auf Linux (amd64/arm64),
  Windows und macOS.
- **Vanilla-Client.** Der Browser-Client besteht aus eigenem JavaScript und
  CSS ohne Build-System; Bibliotheken (Leaflet, jsPDF, …) liegen lokal bei.
- **Drei Betriebsarten:** lokal auf einem PC (eine Datei starten), auf einem
  Server mit systemd und Reverse-Proxy, oder als Docker-Container mit dem
  bekannten `setup.sh`-Assistenten.
- **Updates ohne Internet** in beiden Server-Betriebsarten über die
  Admin-Seite „System-Update“: Docker lädt ein Image-Archiv hoch (Host-Dienst
  wie bisher), ohne Docker lädt der Admin ein neues Binary hoch, das nach
  Prüfsumme und optionaler minisign-Signatur eingetauscht wird.
- Sitzungen liegen in der Datenbank und überleben Neustarts.

## Rollenmodell

### Kontorollen (`users.role`, Verwaltung über `/admin`)

| Rolle    | Darf |
|----------|------|
| `user`   | anmelden, an Einsätzen mitarbeiten, für die eine Stabsrolle vergeben wurde |
| `leiter` | wie `user`, zusätzlich Einsätze anlegen/importieren, Mitglieder und Rollen vergeben, Freigabelinks, Abschluss, Export |
| `admin`  | Nutzerkonten anlegen/ändern (Benutzername, Anzeigename, E-Mail, Kontorolle, Passwort-Reset, 2FA zurücksetzen), Organisationen anlegen und Konten zuordnen, System-Update. **Kein Zugriff auf Einsätze, sieht keine Zugänge (Schlüssel) der Organisationen.** |

### Organisationen und Org-Admins (`organisationen`, `users.org_id`, `users.org_admin`)

Jedes Einsatz-Konto gehört zu einer Organisation (Ortsverband, Regionalstelle,
Landesverband, Leitung). Zugänge zu Fremdsystemen gehören der Organisation,
nicht dem Server: STEIN API-Schlüssel, Divera Access-Key und optional Divera
System-Key (`org_credentials`, AES-GCM-verschlüsselt mit einem aus
`APP_SECRET` abgeleiteten Schlüssel; nur die letzten vier Zeichen sind
sichtbar). **Org-Admin** ist ein Flag auf einem `user`-/`leiter`-Konto: Es
verwaltet unter „Organisation verwalten“ (`/admin`) die Konten der eigenen
Organisation (anlegen, Rolle user/leiter, Passwort-Reset, 2FA, löschen),
vergibt das Divera-Recht (`users.divera_access`: kein / lesen / lesen und
Status setzen) und hinterlegt die Zugänge. Einsätze tragen die Organisation
ihres Erstellers (`einsaetze.org_id`) und beziehen darüber den STEIN-Status.
Beim ersten Start mit diesem Schema wird eine Standard-Organisation
(Name aus `MAP_CENTER_NAME`, sonst aus dem Abschnitt `karte` der Stammdaten)
angelegt und allen vorhandenen Einsatz-Konten und Einsätzen zugeordnet.
Weitere Organisationen übernimmt der Admin im Reiter „Organisationen“ aus den
Stammdaten (`stammdaten.json`: Landesverband, Regionalstellen, Ortsverbände;
einzeln auswählbar oder alle), der Bezug steht in `organisationen.stamm_key`.

**Freigabe an Organisationen:** Der Ersteller eines Einsatzes kann unter
„Verwaltung“ neben einzelnen Nutzern auch ganze Organisationen eintragen
(`einsatz_org_members`), auch fremde Ortsverbände oder Regionalstellen. Alle
Einsatz-Konten dieser Organisation erhalten dann die eingetragenen Stabsrollen,
zusätzlich zu persönlich vergebenen. Hilfsmittel wie Divera sind davon
ausgenommen, sie hängen immer am eigenen Konto und dessen Organisation.

Hilfsmittel (Dashboard, STEINboard, STEINmap) stehen allen Einsatz-Konten
offen. **Divera** (Tab „Divera“ im Dashboard oben links, DiveraMap unter
`/divera`, `api/divera`) erscheint nur für Konten mit Divera-Recht, deren
Organisation einen Divera-Zugang hinterlegt hat; sonst ist davon nichts zu
sehen. Die Personalübersicht (`pull/all`) wird serverseitig auf Initialen
gekürzt, der Access-Key maskiert.


Je Organisation stellt der Org-Admin im Reiter „Einstellungen“ außerdem den
**Kartenmittelpunkt** (Dashboard-Karten, neue Lagekarten; Standard ist der
Standort aus den Stammdaten, sonst `MAP_CENTER_*` bzw. der Abschnitt `karte`
der Stammdaten) und die **Nachrichten-Feeds** der vier Dashboard-Kacheln ein
(Standard: Tagesschau und GDACS, dazu die regionalen Quellen aus dem Abschnitt
`feeds` der Stammdaten; eigene RSS/Atom-Adressen und Reitertitel je Kachel).
Hosts der Standard-Feeds und eigener Feeds sind für den RSS-Abruf zusätzlich
zu `RSS_ALLOWED_HOSTS` erlaubt. Eigene Feeds dürfen nicht auf localhost oder
private Netze zeigen (auch nicht über einen Namen, der dorthin auflöst);
`RSS_ALLOW_PRIVATE=1` hebt das für Feed-Server im eigenen Netz auf.
### Stabsrollen je Einsatz (`internal/stab`)

Der Ersteller eines Einsatzes vergibt pro Nutzer beliebig viele Rollen.
Die Schreibrechte folgen aus der Matrix (eine Konstante, leicht anpassbar):

| Rolle | Führungsharke | Kräfteplanung | Lagekarte | Schadenskonten | Log |
|---|:-:|:-:|:-:|:-:|:-:|
| Leiter Stab | ✔ | ✔ | ✔ | ✔ | ✔ |
| S1 / Unterstützung S1 | ✔ | ✔ | | | |
| S2 / Unterstützung S2 | ✔ | | ✔ | ✔ | |
| S3 / Unterstützung S3 | ✔ | | | ✔ | |
| Lagekarte | ✔ | | ✔ | ✔ | |
| S4, S5, S6, ETB, Sichter (+ Unterstützung) | lesen | lesen | lesen | lesen | |

Der **Ersteller** hat immer die Verwaltungsrechte (Mitglieder, Freigabelinks,
Abschluss, Löschen, Export, Log) und bekommt beim Anlegen automatisch
„Leiter Stab“ (kann entfernt werden). Status „angefordert / auf Anfahrt /
vor Ort“ darf setzen, wer Harke- oder Kräfte-Recht hat. Den Status führen
immer die Einheiten und Fachberater – eine Führungsstelle (TEL, EAL,
Abschnittsleitung) rückt nicht selbst an und hat deshalb keinen.

## Seiten

Alle Seiten sind unter dem Pfad ohne Endung erreichbar (`/harke`). Ohne
Login sind ausschließlich
Login, Passwort-vergessen, Health-Check und die statischen Assets (`/css/`,
`/js/`, `/images/`, `/fonts/`, `/geo/`) erreichbar; `public/daten/` wird nicht
ausgeliefert.

| Seite | Zweck |
|---|---|
| `/einsatz` | Einsatzübersicht, Anlegen (Leiter), Import |
| `/harke?einsatz=U` | Führungsharke (zentrale Seite): Führungsstellen, Einheiten, Fachberater, interne Einheiten, Status, externe Freigabelinks an Führungsebenen |
| `/kraefteplanung?einsatz=U` | Schichten und Stammdaten je Einheit |
| `/lagekarte?einsatz=U[&karte=ID]` | Lagekarten des Einsatzes (mehrere); Objekte können mit Einheiten der Harke verknüpft werden |
| `/schadenskonten?einsatz=U` | Schadenskonten mit verknüpften Einheiten (live aufgelöst) und Fremdeinheiten |
| `/anfahrt?einsatz=U` | Kräfteübersicht: angefordert / auf Anfahrt / eingetroffen / Heimfahrt, fällige Schichten (Vorwarnzeit je Einsatz) |
| `/log?einsatz=U` | Änderungslog (wer/wann/was, vorher/nachher) und Zugriffslog, druckbar |
| `/verwaltung?einsatz=U` | Einsatzdaten, Mitglieder/Rollen, Freigabelinks, Verknüpfungen, Snapshots, Export, Abschluss, Löschen |
| `/view?t=TOKEN` | Freigabelink eintragen (Login Pflicht, kein Direktzugriff) |
| `/`, `/stein`, `/steinmap` | Hilfsmittel |
| `/admin` | Serververwaltung (Admin: Nutzer, Organisationen, Server); Org-Admins verwalten dort ihre Organisation |
| `/admin_update` | System-Update (Admin): Archiv bzw. Binary hochladen, Rückfall |
| `/healthz` | Health-Check ohne Login (`{"ok":true}`), für Container und Reverse-Proxy |
| `/settings` | Profil, Passwort, 2FA, „Was ist neu“ |

## Stammdaten (Landesverband, Regionalstellen, Ortsverbände)

Alles, was eine Installation über ihren Landesverband weiß, steht in **einer**
Datei: `public/daten/stammdaten.json`. Sie liegt **nicht im Repository**
(`.gitignore`); dort gibt es nur `public/daten/stammdaten.example.json` mit
einem fiktiven „LV Musterland“, die den Aufbau zeigt und zugleich als
Rückfall dient. Beim Bauen bettet `go build` die echte Datei ein, wenn sie
auf dem Rechner vorliegt (`embed.go`, `all:public`), sonst die Beispieldatei;
der Server protokolliert beim Start, welche Quelle er nutzt. Alternativ nennt
`STAMM_FILE` eine Datei auf dem Rechner, die zur Laufzeit gelesen wird und
vor der eingebetteten gilt (Docker: Datei einhängen und `STAMM_FILE` setzen).

Abschnitte der Datei:

| Abschnitt | Inhalt |
|---|---|
| `lv` | eigener Landesverband (`key`, `name`, `kurz`) |
| `karte` | Kartenmittelpunkt des Servers (`lat`, `lng`, `name`), wenn `MAP_CENTER_*` nicht gesetzt ist; der Name ist zugleich der Name der Standard-Organisation |
| `feeds` | regionale Nachrichten-Kacheln des Dashboards (`slot` 6-1/6-2, `title`, `urls`); 4-1/4-2 sind fest Tagesschau und GDACS |
| `bezirke` | optional: ein Landkreis, der in Bezirke gegliedert ist (`landkreis`, `geo` = Pfad unter `/geo/`, `titel`, `prefix` im OV-Namen, `attribution`); STEINmap zeigt die Ebene und ordnet Punkte dem Bezirks-OV zu |
| `regionen` | Regionalstellen und Nachbar-Landesverbände (`key`, `name`, `kurz`, `live` = STEIN-Daten vorhanden, `extern` = fremder LV, `nachbarn`) |
| `ovs` | Ortsverbände und Dienststellen mit STEIN-BU-ID (`buid`), `art` (ov/rst/lv), Koordinaten, `landkreis` (Name wie in `assets/geo/landkreise_simplify20.geojson`) und `einheiten` (`key` = `<ov>:<typ>[#<nr>]`); Nachbar-OVs ohne STEIN mit `key` `n-…` und `extern: true` |

Die Datei wird nicht als statische Datei ausgeliefert; STEINboard, STEINmap
und die Einsatz-Seiten laden sie nach dem Login über
`/api/kraefte?action=stamm`. Tests laufen immer gegen die Beispieldatei, nie
gegen die echte (sie muss ohne sie bestehen). Die Geodaten unter
`assets/geo/` (Landkreise, Gemeinden, ggf. Bezirke) müssen zum Gebiet der
Stammdaten passen und liegen im Repository.

## Kräftekatalog (STEIN-Daten und Verfügbarkeit)

`internal/kraefte` mischt zu den Stammdaten den STEIN-Live-Status (direkt von
`stein.app` mit dem STEIN-Schlüssel der Organisation des Einsatzes) und die
Bindung in offenen Einsätzen.

**STEIN-Vorrat:** Der Server hält einen gemeinsamen Stand je Ortsverband für
alle Organisationen und merkt sich je Organisation, für welche Ortsverbände ihr
Schlüssel Daten liefert. Eine Organisation fragt nur dann bei STEIN an, wenn
einer ihrer Ortsverbände älter als die Frist ist (2 min, im Webhook-Modus
30 min), und zwar alle Einheiten des Landesverbands in einer Anfrage; haben
zwölf Organisationen denselben Landesverband im Blick, geht pro Frist eine
Anfrage statt zwölf. Spätestens stündlich fragt jede Organisation einmal selbst
nach, damit neue Berechtigungen sichtbar werden. Niemand erhält Daten zu
Ortsverbänden, für die der eigene Schlüssel nie Zugriff hatte. Alte Daten
verfallen nicht: Ist STEIN nicht erreichbar, bleibt der letzte Stand mit
Zeitstempel und wird als veraltet markiert, auch über einen Neustart hinweg
(`data/stein_store.json`). STEIN erlaubt 20 Anfragen pro Minute und IP und
sperrt die IP bei Überschreitung eine Stunde; der Prozess bleibt unter
10 Abrufen pro Minute und pausiert nach Ablehnung (401/403: 30 min, 429: 1 h)
oder Netzfehlern (2 min). Ohne hinterlegten Schlüssel findet kein Abruf statt.
Webhook-Meldungen werden prozessweit je Ortsverband gesammelt: dieselbe
STEIN-Ereignis-ID wird nur einmal verarbeitet, egal über wie viele
Organisationen sie eintrifft, und ein gemeldeter Ortsverband wird einmal
nachgeholt.

**Kachel-Cache (`/tiles/{z}/{x}/{y}.png`, `internal/tiles`):** Jede Installation
cacht OpenStreetMap-Kacheln selbst unter `data/tiles` (30 Tage frisch, ein
Upstream-Abruf je Kachel, 512 KB je Kachel, atomar geschrieben, Obergrenze
`TILE_CACHE_MAX_MB`). `TILE_URL` ist die URL für den Browser (Standard der
eingebaute Cache), `TILE_SOURCE` die Quelle für fehlende Kacheln (Standard
OSM mit Kennung und Kontaktadresse im User-Agent). Laufen an einem Standort
mehrere Installationen hinter derselben öffentlichen IP, zeigt `TILE_SOURCE`
der übrigen auf den Cache einer Installation, die mit `TILE_PUBLIC=1` (und
optional `TILE_TOKEN`, Zugriff per `?key=` oder Header `X-Tile-Key`)
freigegeben ist, oder auf einen zentralen Kachel-Dienst. Der globale Admin
stellt Quelle, Freigabe und Token im Reiter „Server“ der Serververwaltung
ein (mit „Testabruf“ gegen die eingetragene Quelle); diese Werte liegen in
der Tabelle `settings` und gehen der Umgebung vor. So fragt
je Standort nur eine Stelle bei OSM an (Tile Usage Policy). Ohne `TILE_PUBLIC`
liefert der Endpunkt nur an angemeldete Nutzer, das Sitzungs-Cookie kommt bei
Bildern gleicher Herkunft automatisch mit.

**STEIN-Webhook (je Organisation wählbar, Reiter „Zugänge“ > „STEIN-Abruf“):**
Im Modus „Webhook“ meldet STEIN Änderungen an
`POST /api/stein/webhook/<token>` (Token je Organisation aus `APP_SECRET`
abgeleitet, Header `X-Secret` gegen die hinterlegten Webhook-Secrets der
Ortsverbände geprüft, eine Zeile je Ortsverband in `org_webhook_secrets`).
Die Nachricht enthält nur Verweise; der Server merkt die betroffenen
Ortsverbände vor, holt sie nach 8 s gebündelt in einer Anfrage mit dem STEIN-
Schlüssel nach und mischt sie in die gecachte Live-Menge. Der volle Abruf
läuft dann nur noch alle 30 Minuten. Eingegangene Ereignisse (Zeit, Umgebung,
Typ, Aktion, Asset, Ortsverband) stehen 7 Tage zur Kontrolle in der
Oberfläche; Duplikate werden über `eventId` erkannt. Der Webhook braucht eine
von STEIN aus erreichbare Adresse (`APP_BASE_URL`), lokale Installationen
bleiben beim Abruf-Modus.
Knoten, Mitglieder und Schichten tragen optional `kraft_key` (z.B. `103:fgrn`);
Bezeichnung, Funkruf und OV bleiben Freitext. Bindungslogik: Schichtstatus
vor Einheitenstatus, abgerückte Einheiten binden nicht; Nichtmitglieder sehen
nur „in anderem Einsatz (Stab …)“.

## Changelog „Was ist neu“

`public/daten/changelog.json` enthält die Änderungen je Version (neueste
zuerst). Der Versionsstempel im Footer und der Abschnitt unter Einstellungen
öffnen den Dialog; nach dem Login erscheint er automatisch, wenn es seit der
zuletzt bestätigten Version (`users.changelog_seen`) Einträge mit
`"show": true` gibt. Bei jedem Release: Eintrag ergänzen und `APP_VERSION`
in `config/.env.sample` anheben (die Version kommt per `-ldflags` ins Binary).

## Datenmodell (`sql/schema.sqlite.sql`)

Eine Zeile je Objekt mit `version` (optimistic locking `UPDATE … WHERE id=?
AND version=?` → 409 `conflict`) und Soft-Delete `deleted_at`: `einsaetze`, `einsatz_members`, `einsatz_nodes`,
`einsatz_mitglieder`, `einsatz_schichten`, `schadenskonten` (+ `_refs`,
`_fremd`), `lagekarten`, `lagekarte_abschnitte`, `lagekarte_features`,
`einsatz_events` (Änderungslog, Sync-Feed, Exportbasis), `einsatz_access_log`,
`einsatz_share_links`, `einsatz_links`, `einsatz_snapshots`, `users`,
`password_resets`, `login_attempts`, `tfa_attempts`. Neu: `sessions`
(serverseitige Sitzungen als JSON). Das Schema wird bei jedem Start
idempotent ausgeführt, fehlende Spalten werden nachgezogen.

Live-Sync: Clients pollen alle `SYNC_INTERVAL_MS` (Standard 7 s) die neuen
Ereignisse und wenden sie lokal an. Lesen ist dank SQLite-WAL nie blockiert.

## API (`/api/*`, nur mit Sitzung)

- `einsatz` – Einsätze, Mitglieder, Harke (Knoten/Mitglieder/Schichten), Status, Events, Log, Anfahrt, Freigabelinks, Verknüpfungen, Export/Import, Snapshots
- `lagekarten` – Karten, Abschnitte, Kartenobjekte
- `schadenskonten` – Schadenskonten, Verknüpfungen, Fremdeinheiten
- `kraefte` – Kräftekatalog: Stammdaten, STEIN-Status (gecacht), Bindungen (nur lesend)
- `proxy` – Session-geschützter Zugang zu NINA/MOWAS, Hochwasserportal, RSS (nur erlaubte Hosts), STEIN (Schlüssel der Organisation), Adresssuche (Nominatim) und Routing (OSRM); der Server ruft die Dienste direkt ab (`internal/upstream`, Cache mit Stale-Fallback, Adresssuche und Routing gebremst auf eine Anfrage je Sekunde)
- `divera` – Divera 24/7 (Personalübersicht, Fahrzeugstatus, Einsätze, PDF, Status setzen) mit dem Zugang der Organisation; nur mit Divera-Recht
- `/tiles/{z}/{x}/{y}.png` – eingebauter Kachel-Cache (kein `/api`-Pfad; Sitzung oder `TILE_PUBLIC`)
- `changelog` – „Was ist neu“
- `update` – System-Update (Admin): Chunk-Upload, Trigger, Status

Die Kontrakte stehen als Kommentar am Anfang von `internal/api/*/handler.go`.
POST-Aufrufe erwarten JSON mit `csrf` (Token aus `window.STAB.csrf`).

## Export / Import

`GET /api/einsatz?action=export&einsatz=U` liefert den kompletten
Einsatz als JSON (`format: luk-einsatz`), inklusive gelöschter Zeilen,
Ereignis- und Zugriffslog. Ein Leiter kann die Datei über die
Einsatzübersicht importieren: Es entsteht ein **neuer** Einsatz mit neuen
IDs, Mitglieder werden über den Benutzernamen gemappt, die Historie wird
als „importiert“ markiert übernommen.

## Verzeichnisstruktur

```
thw_luk_software_go/
├── cmd/luk/              main.go (serve, init-db, seed-admin, mail-test, healthcheck, version)
├── embed.go              bettet public/, assets/ und sql/ in das Binary ein
├── public/               css/ js/ images/ fonts/ daten/ manifest.webmanifest service-worker.js
│                         daten/stammdaten.json (eigene, nicht im Repo), stammdaten.example.json
├── assets/               taktische_zeichen/ taktische_zeichen_thumbs/ geo/
├── sql/schema.sqlite.sql
├── internal/             config db stab access einsatz lagekarte schadenskonten export
│                         kraefte user auth session totp mail changelog update httpx
│                         api/{einsatzapi,lagekartenapi,schadenskontenapi,kraefteapi,
│                              changelogapi,proxyapi,updateapi} pages server
├── config/.env.sample
├── deploy/               luk.service (systemd), nginx-site.conf.example
├── docker/               Dockerfile compose.yml setup.sh make-archive.sh host/ (luk-updater)
└── scripts/build.sh      Cross-Compile der Release-Binaries
```

## Installation

### A) Lokal auf einem PC (ohne Docker)

1. Passendes Binary aus `dist/` (siehe „Bauen“) oder aus dem Release
   herunterladen: `luk-windows-amd64.exe`, `luk-linux-amd64`,
   `luk-linux-arm64`, `luk-darwin-arm64`.
2. In einen Ordner legen und starten (Doppelklick oder `./luk serve`).
   Beim ersten Start entstehen `data/` (Datenbank, Secret) und das Konto
   `admin`; das Initialpasswort steht im Konsolenfenster und in
   `data/admin-initialpasswort.txt`.
3. Browser: `http://127.0.0.1:8080`. Anmelden, Passwort ändern, Nutzer anlegen
   (mindestens ein Konto mit Rolle „Leiter“).

Optional eine `config/.env` (Vorlage `config/.env.sample`) neben das Binary
legen, z.B. für `APP_NAME`, `MAP_CENTER_*` oder `LISTEN_ADDR=0.0.0.0:8080`,
wenn andere Geräte im Netz zugreifen sollen. Mail-Versand (Absender, SMTP) und
Kartenkacheln stellt der Admin unter „Serververwaltung › Server“ ein; eine
`.env` ist dafür nicht nötig. Updates auf einem PC: neues
Binary über die alte Datei kopieren und neu starten – oder `UPDATE_MODE=binary`
setzen und die Admin-Seite nutzen.

### B) Server ohne Docker (systemd + Reverse-Proxy)

```bash
sudo useradd -r -s /usr/sbin/nologin -d /opt/luk luk
sudo mkdir -p /opt/luk/config /opt/luk/data
sudo cp luk-linux-amd64 /opt/luk/luk && sudo chmod 755 /opt/luk/luk
sudo cp config/.env.sample /opt/luk/config/.env     # APP_BASE_URL, MAIL_*, SMTP_*, TRUSTED_PROXIES=127.0.0.1
sudo chown -R luk:luk /opt/luk && sudo chmod 600 /opt/luk/config/.env
sudo cp deploy/luk.service /etc/systemd/system/luk.service
sudo systemctl enable --now luk
sudo journalctl -u luk -n 20      # Initialpasswort des Admin-Kontos
```

Davor ein Reverse-Proxy mit TLS (`deploy/nginx-site.conf.example`, Zertifikat
z.B. per `certbot --nginx`) oder ein Cloudflare-Tunnel auf `http://127.0.0.1:8080`.
Mailversand prüfen: `sudo -u luk /opt/luk/luk mail-test empfaenger@example.org`.
Die Unit setzt `UPDATE_MODE=binary`, damit die Admin-Seite „System-Update“
neue Binaries annimmt (siehe „Updates“).

Bei einer Neuinstallation entstehen Datenbank, Secret und Admin-Konto beim
ersten Start.

### C) Docker

Ein Image (`docker/Dockerfile`, Alpine + Binary, läuft als Nutzer `luk`),
konfiguriert ausschließlich über Umgebungsvariablen, Daten auf zwei
Bind-Mounts (`data/`, `updates/`). Nichts muss in Dateien eingetragen werden –
`docker/setup.sh` fragt alles ab.

Voraussetzungen und Ablauf wie bisher: Linux-Server mit systemd und Docker
(Compose-Plugin; `setup.sh` bietet die Installation an), eine Domain oder ein
Cloudflare-Tunnel, das Image aus der Registry
`ghcr.io/friedrichengel/thw_luk_software_go` (Token mit `read:packages`) oder
als Archiv `luk-image-<version>.tar.gz` (+ `.sha256`, optional `.minisig`)
aus `docker/make-archive.sh` bzw. dem Actions-Build.

```bash
scp docker/setup.sh user@server:~/
scp luk-image-3.0.0.tar.gz luk-image-3.0.0.tar.gz.sha256 user@server:~/   # Archiv-Weg
sudo bash ~/setup.sh
```

Der Assistent fragt Installationsverzeichnis (`/opt/luk`), Bezugsweg,
`APP_BASE_URL`, `APP_NAME`, `HTTP_BIND` (Standard `127.0.0.1:8080`), optional
Cloudflare-Tunnel-Token, Mail/SMTP, Kachel-URL, Kartenzentrum und `UPDATE_PUBKEY`, schreibt `.env`
und `docker-compose.yml`,
installiert den Host-Dienst `luk-updater`, startet den Container und zeigt
einmalig das Initialpasswort des Admin-Kontos. Danach Reverse-Proxy
(`/opt/luk/dist/host/nginx-proxy.conf.example`) oder Tunnel auf `http://app:80`.

```
/opt/luk/
  .env                 Konfiguration (Secrets, 0600)
  docker-compose.yml   aus dem Image (nicht editieren; Anpassungen in docker-compose.override.yml)
  dist/                compose.yml + Host-Dienst aus dem laufenden Image
  data/                auth.sqlite (+ -wal/-shm), .app_secret, stein_cache.json
  updates/             inbox/ (Aufträge), archive/, status.json, updater.log
```

Backup = `data/` und `.env`. Konsistente DB-Kopie im laufenden Betrieb:
`sqlite3 /opt/luk/data/auth.sqlite "VACUUM INTO '/opt/luk/data/backup.sqlite'"`
(oder den Container kurz stoppen). Logs: `sudo bash setup.sh logs`,
`/opt/luk/updates/updater.log`, `journalctl -u luk-updater`.

## Updates

**Ohne Docker (`UPDATE_MODE=binary`):** Admin > **System-Update** nimmt das
neue Binary für die Plattform des Servers entgegen (`luk-linux-amd64` aus
`scripts/build.sh`), in 8-MB-Teilen mit Prüfsumme (im Browser berechnet,
serverseitig gegengeprüft). Ist `UPDATE_PUBKEY` gesetzt, muss zusätzlich die
`.minisig`-Datei mitgegeben werden und die Signatur passen. „Anwenden“
tauscht die Programmdatei (`luk` → `luk.old`), schreibt
`data/update_status.json` und startet das Programm neu; die Anwendung ist
dabei wenige Sekunden nicht erreichbar, Sitzungen bleiben erhalten. „Vorherigen
Stand wiederherstellen“ tauscht `luk.old` zurück. Auf der Konsole geht es auch
von Hand: neues Binary kopieren, `systemctl restart luk`.

**Docker (`UPDATE_MODE=docker`, im Image vorbelegt):** drei gleichwertige
Wege, alle über den Host-Dienst:

```bash
sudo bash setup.sh load ~/luk-image-3.1.0.tar.gz   # Archiv (+ .sha256/.minisig daneben)
sudo bash setup.sh update                          # Registry (docker pull)
sudo bash setup.sh rollback                        # vorheriges Image zurück
sudo bash setup.sh status
```

Im Browser: Admin > **System-Update** lädt ein Archiv hoch oder löst „aus
Registry ziehen“ bzw. „Rückfall“ aus. Die Web-App hat keinen Docker-Zugriff;
sie legt nur Dateien in `updates/inbox/` ab (`internal/update`), der
Host-Dienst `docker/host/luk-updater.sh` prüft SHA-256 und optional die
minisign-Signatur, lehnt Images mit fremdem Namen ab, sichert das laufende
Image als `:previous`, startet neu und wartet auf den Health-Check; bleibt
der Container ungesund, dreht er automatisch zurück.

**Signierte Updates** (empfohlen, sobald Dateien per Website kommen):
`minisign -G -W -p luk.pub -s luk.key`; zweite Zeile von `luk.pub` als
`UPDATE_PUBKEY` eintragen. `scripts/build.sh --sign luk.key` und
`docker/make-archive.sh --sign luk.key` erzeugen die `.minisig`-Dateien; in
GitHub Actions als Secret `MINISIGN_SECRET_KEY`. Ohne Schlüssel wird jede
Datei mit gültiger Prüfsumme angenommen und eine Warnung protokolliert.

## Bauen und Testen

```bash
go build ./... && go vet ./... && go test ./...     # Tests mit Temp-Datenbanken
go run ./cmd/luk                                   # lokal starten (127.0.0.1:8080)
scripts/build.sh [--sign luk.key]                  # dist/luk-<os>-<arch>[.exe] + .sha256 (+ .minisig)
docker/make-archive.sh [--sign luk.key] [--push]   # Image + dist/luk-image-<version>.tar.gz
```

Die Version kommt aus `APP_VERSION` in `config/.env.sample` und wird per
`-ldflags -X …/internal/config.Version=…` in das Binary geschrieben
(`luk version`). Für die Entwicklung reicht `go run`; es braucht nur Go,
keine C-Toolchain (SQLite ist in reinem Go eingebunden). Vor dem Bauen eines
Release die eigene `public/daten/stammdaten.json` in den Arbeitsbaum legen
(siehe „Stammdaten“); ohne sie enthält das Binary nur den Beispiel-LV.

## Fehlersuche

- Programm startet nicht: Ausgabe auf der Konsole bzw. `journalctl -u luk`;
  häufigste Ursachen sind ein belegter Port (`LISTEN_ADDR`) oder fehlende
  Schreibrechte auf `data/`.
- Admin-Passwort vergessen: `luk seed-admin --reset` (setzt `admin` zurück,
  Passwortwechsel beim nächsten Login Pflicht).
- Hinter einem Proxy stimmen Client-IPs oder `Secure`-Cookies nicht:
  `TRUSTED_PROXIES` auf die Proxy-Adresse setzen, Proxy muss
  `X-Forwarded-For` und `X-Forwarded-Proto` senden.
- Hilfsmittel leer: der Server braucht ausgehendes HTTPS zu
  `warnung.bund.de`, `api.hochwasserzentralen.de`, den Nachrichten-Feeds,
  `stein.app` und `app.divera247.com`; Fehler stehen im Log (`upstream ...`).
- STEIN/Divera ohne Daten: Org-Admin prüft unter „Organisation verwalten“ >
  „Zugänge“ mit „Verbindung prüfen“, ob der Schlüssel angenommen wird; Divera
  zusätzlich das Recht des Kontos im Reiter „Nutzer“. Nach einer Ablehnung
  pausiert der Abruf 30 Minuten (`upstream ... Zugang abgelehnt` im Log).
  STEIN antwortet nur deutschen IP-Adressen (sonst 404).
- Admin-Seite meldet „Server antwortet nicht“: während eines Updates normal;
  sonst `update_status.json` / `updater.log` ansehen.

## Verwendete Komponenten Dritter

Alle Bibliotheken liegen im Repository bzw. in `go.mod`; es gibt keine
Laufzeitabhängigkeiten außerhalb des Binaries.

### Go-Module (`go.mod`)

| Modul | Version | Lizenz | Autor / Quelle |
|---|---|---|---|
| Go-Standardbibliothek | Go 1.27 | BSD-3-Clause | The Go Authors, https://go.dev |
| `golang.org/x/crypto` (bcrypt) | v0.58.0 | BSD-3-Clause | The Go Authors, https://github.com/golang/crypto |
| `modernc.org/sqlite` (SQLite in Go, kein CGO) | v1.61.0 | BSD-3-Clause | Jan Mercl, https://gitlab.com/cznic/sqlite (SQLite selbst: Public Domain, https://sqlite.org) |
| `modernc.org/libc`, `modernc.org/mathutil`, `modernc.org/memory` (indirekt) | v1.79.0, v1.7.1, v1.12.1 | BSD-3-Clause | Jan Mercl, https://gitlab.com/cznic |
| `github.com/google/uuid` (indirekt) | v1.6.0 | BSD-3-Clause | Google, https://github.com/google/uuid |
| `github.com/dustin/go-humanize` (indirekt) | v1.1.0 | MIT | Dustin Sallings, https://github.com/dustin/go-humanize |
| `github.com/mattn/go-isatty` (indirekt) | v0.0.24 | MIT | Yasuhiro Matsumoto, https://github.com/mattn/go-isatty |
| `github.com/ncruces/go-strftime` (indirekt) | v1.1.0 | MIT | Nuno Cruces, https://github.com/ncruces/go-strftime |
| `github.com/remyoudompheng/bigfft` (indirekt) | 2023-01-29 | BSD-3-Clause | Rémy Oudompheng, https://github.com/remyoudompheng/bigfft |
| `golang.org/x/sys` (indirekt) | v0.49.0 | BSD-3-Clause | The Go Authors, https://github.com/golang/sys |

### Browser-Bibliotheken (`public/js`, `public/css`, `public/fonts`)

| Bibliothek | Version | Lizenz | Autor / Quelle |
|---|---|---|---|
| Leaflet | 1.2.0 | BSD-2-Clause | Vladimir Agafonkin, https://github.com/Leaflet/Leaflet |
| Leaflet.draw | 1.0.4 | MIT | Jacob Toye und Leaflet-Mitwirkende, https://github.com/Leaflet/Leaflet.draw |
| Leaflet.pattern | Stand 2015 (ohne Versionsnummer) | MIT | Tyler Eastman, https://github.com/teastman/Leaflet.pattern |
| jQuery | 3.7.1 | MIT | OpenJS Foundation, https://github.com/jquery/jquery |
| jsPDF | 2.5.1 | MIT | James Hall, yWorks GmbH und Mitwirkende, https://github.com/parallax/jsPDF |
| html2canvas | 1.4.1 | MIT | Niklas von Hertzen, https://github.com/niklasvh/html2canvas |
| pdf-lib | im Bundle nicht ausgewiesen (1.x) | MIT | Andrew Dillon, https://github.com/Hopding/pdf-lib |
| ExcelJS | 4.4.0 | MIT | Guyon Roche und Mitwirkende, https://github.com/exceljs/exceljs |
| qrcode-generator | 1.4.4 | MIT | Kazuhiko Arase, https://github.com/kazuhikoarase/qrcode-generator (QR-Code der 2FA-Einrichtung, lokal im Browser) |
| Font Awesome Free | 6.7.2 | Icons CC BY 4.0, Schriften SIL OFL 1.1, Code MIT | Fonticons, Inc., https://github.com/FortAwesome/Font-Awesome |

### Daten (`assets/`, `public/daten/`)

| Daten | Lizenz | Quelle |
|---|---|---|
| Verwaltungsgrenzen Landkreise und Gemeinden (`assets/geo/*_simplify20*.geojson`) | Datenlizenz Deutschland – Namensnennung 2.0 | © GeoBasis-DE / BKG 2013 (Daten verändert), https://gdz.bkg.bund.de |
| Berliner Bezirksgrenzen (`assets/geo/berlin_bezirke.geojson`) | CC BY | Amt für Statistik Berlin-Brandenburg, bereitgestellt über https://github.com/funkeinteraktiv/Berlin-Geodaten (Berliner Morgenpost); auf den Bezirksnamen reduziert |
| Taktische Zeichen (`assets/taktische_zeichen/`, SVG) | CC BY 4.0 | Jonas Köritz, „Taktische Zeichen für Hilfsorganisationen als Vektorgrafiken“, https://github.com/jonas-koeritz/Taktische-Zeichen |
| Beispiel-Stammdaten (`public/daten/stammdaten.example.json`) | Bestandteil dieses Projekts | fiktiver Landesverband; die echte `stammdaten.json` liegt nicht im Repository |

### Dienste zur Laufzeit (nicht im Repository)

| Dienst | Nutzung | Bedingungen |
|---|---|---|
| OpenStreetMap-Kacheln | Karten (über den eingebauten Kachel-Cache) | © OpenStreetMap-Mitwirkende, ODbL; Tile Usage Policy, https://www.openstreetmap.org/copyright |
| NINA/MOWAS (BBK) | Warnmeldungen im Dashboard | nach den Nutzungsbedingungen des BBK |
| Länderübergreifendes Hochwasserportal (LHP) | Hochwasserkarte | CC BY 4.0, Quelle www.hochwasserzentralen.de |
| Deutscher Wetterdienst | Waldbrandgefahrenindex-Karte | Open Data des DWD, CC BY 4.0 |
| Nominatim (nominatim.openstreetmap.org) | Adresssuche in STEINmap und bei der Ortswahl der Organisation (über den Server, max. 1 Anfrage/s, gecacht) | © OpenStreetMap-Mitwirkende, ODbL; Nominatim Usage Policy, https://operations.osmfoundation.org/policies/nominatim/ |
| OSRM-Demo-Server (router.project-osrm.org) | Fahrtroute zum Marker in STEINmap (über den Server, max. 1 Anfrage/s, gecacht) | OSRM-Demo-Server, nur leichte Nutzung, https://project-osrm.org |
| Nachrichten-Feeds (tagesschau.de, GDACS, regionale Quellen aus den Stammdaten) | Ticker im Dashboard | nach den Bedingungen der jeweiligen Anbieter; GDACS-Feed gemeinfrei |
| STEIN (stein.app) | Live-Status der Einheiten | mit Schlüssel der Organisation, 20 Anfragen/min je IP |
| Divera 24/7 | Fahrzeugstatus, Einsätze, Personalübersicht | mit Schlüssel der Organisation nach den Divera-Bedingungen |

## Lizenz

Der Code dieses Projekts steht unter der [MIT-Lizenz](LICENSE). Die oben
aufgeführten Komponenten Dritter (Go-Module, JavaScript-Bibliotheken,
Schriften, taktische Zeichen, Geodaten) behalten ihre jeweiligen Lizenzen;
bei Weitergabe sind deren Bedingungen (Namensnennung, Lizenztexte) einzuhalten.

## Hinweis

Die Software ist nicht vom THW herausgegeben und nicht mit dem THW abgestimmt.
