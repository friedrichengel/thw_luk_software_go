#!/usr/bin/env bash
#
# THW LuK Stabsansicht (Go) - Docker-Setup (Erstinstallation, Neukonfiguration, Update)
#
# Einzige Datei, die auf den Server kopiert werden muss. Alles Weitere
# (compose.yml, Host-Dienst) holt sie aus dem Image.
#
#   sudo bash setup.sh                     Assistent: installieren oder neu konfigurieren
#   sudo bash setup.sh --archive DATEI     Assistent, Image aus Archiv statt Registry
#   sudo bash setup.sh load DATEI          Update aus Archiv (luk-image-<version>.tar.gz)
#   sudo bash setup.sh update              Update aus der Registry
#   sudo bash setup.sh rollback            vorheriges Image wiederherstellen
#   sudo bash setup.sh status              Stand anzeigen
#   sudo bash setup.sh logs                Container-Protokoll verfolgen
#   sudo bash setup.sh mail-test ADRESSE   Testmail ueber die konfigurierten Einstellungen
#
# Zwei Bezugswege fuer das Image (das Repo ist privat):
#   Registry  ghcr.io - braucht einen GitHub-Token mit "read:packages"
#             (Settings > Developer settings > Fine-grained tokens), wird
#             einmalig per docker login hinterlegt.
#   Archiv    luk-image-<version>.tar.gz (+ .sha256, optional .minisig) aus
#             dem GitHub-Actions-Build oder docker/make-archive.sh; per scp
#             neben dieses Skript legen. Kein GitHub-Zugriff vom Server noetig.
# Spaetere Updates laufen ueber dieselben Wege oder ueber die Admin-Seite
# "System-Update" (Archiv hochladen / aus Registry ziehen).
#
# Umgebungsvariablen: INSTALL_DIR (Standard /opt/luk), NONINTERACTIVE=1
# (Standardwerte bzw. vorhandene .env ohne Rueckfragen uebernehmen).
set -euo pipefail

DEFAULT_IMAGE="ghcr.io/friedrichengel/thw_luk_software_go:latest"
INSTALL_DIR="${INSTALL_DIR:-/opt/luk}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NONINTERACTIVE="${NONINTERACTIVE:-0}"
ARCHIVE=""

log()  { printf '\n\033[1;34m==> %s\033[0m\n' "$*"; }
info() { printf '    %s\n' "$*"; }
warn() { printf '\033[1;33mWARNUNG: %s\033[0m\n' "$*" >&2; }
die()  { printf '\033[1;31mFEHLER: %s\033[0m\n' "$*" >&2; exit 1; }

# ask VAR "Frage" "Standard"  - leere Eingabe uebernimmt den Standard
ask() {
    local var="$1" prompt="$2" def="${3:-}" val
    if [[ $NONINTERACTIVE == 1 ]]; then printf -v "$var" '%s' "$def"; return; fi
    read -r -p "$prompt${def:+ [$def]}: " val </dev/tty || true
    printf -v "$var" '%s' "${val:-$def}"
}
# ask_secret VAR "Frage" "bisheriger Wert" - Eingabe unsichtbar, leer = behalten
ask_secret() {
    local var="$1" prompt="$2" def="${3:-}" val
    if [[ $NONINTERACTIVE == 1 ]]; then printf -v "$var" '%s' "$def"; return; fi
    read -r -s -p "$prompt${def:+ [unveraendert lassen: Enter]}: " val </dev/tty || true
    echo
    printf -v "$var" '%s' "${val:-$def}"
}
# ask_yn "Frage" j|n  -> Rueckgabe 0 = ja
ask_yn() {
    local prompt="$1" def="${2:-n}" val
    if [[ $NONINTERACTIVE == 1 ]]; then [[ $def == j ]]; return; fi
    read -r -p "$prompt [$([[ $def == j ]] && echo J/n || echo j/N)]: " val </dev/tty || true
    val="${val:-$def}"
    [[ $val =~ ^[jJyY] ]]
}

# .env-Werte einfach gequotet: KEY='wert' - Compose liest sie woertlich, keine
# Interpolation. Enthaelt der Wert ein Hochkomma, doppelt gequotet mit \" und
# \\ als Escapes und $$ fuer $ (Compose-Interpolation).
env_quote() {
    local s="$1"
    if [[ $s != *\'* ]]; then printf "'%s'" "$s"; return; fi
    s="${s//\\/\\\\}"; s="${s//\"/\\\"}"; s="${s//\$/\$\$}"; printf '"%s"' "$s"
}
env_unquote() {
    local v="$1"
    if [[ $v == \'*\' ]]; then v="${v:1:-1}"
    elif [[ $v == \"*\" ]]; then v="${v:1:-1}"; v="${v//\\\"/\"}"; v="${v//\\\\/\\}"; v="${v//\$\$/\$}"; fi
    printf '%s' "$v"
}
load_env() {
    [[ -f "$INSTALL_DIR/.env" ]] || return 0
    local line k v
    while IFS= read -r line; do
        [[ $line =~ ^[A-Z_][A-Z0-9_]*= ]] || continue
        k="${line%%=*}"; v="$(env_unquote "${line#*=}")"
        printf -v "CFG_$k" '%s' "$v"
    done < "$INSTALL_DIR/.env"
}
cfg() { local n="CFG_$1"; printf '%s' "${!n:-${2:-}}"; }

need_root() { [[ $EUID -eq 0 ]] || die "Bitte mit sudo/als root ausfuehren."; }

need_docker() {
    if command -v docker >/dev/null && docker compose version >/dev/null 2>&1; then return; fi
    warn "Docker (mit Compose-Plugin) ist nicht installiert."
    command -v curl >/dev/null || die "curl fehlt - bitte Docker manuell installieren (https://docs.docker.com/engine/install/)."
    ask_yn "Docker jetzt ueber get.docker.com installieren?" j || die "Ohne Docker geht es nicht weiter."
    curl -fsSL https://get.docker.com | sh
    systemctl enable --now docker >/dev/null 2>&1 || true
    docker compose version >/dev/null 2>&1 || die "Docker Compose fehlt weiterhin."
}

registry_reachable() {
    local code
    code="$(curl -s -m 6 -o /dev/null -w '%{http_code}' https://ghcr.io/v2/ 2>/dev/null || echo 000)"
    [[ $code != 000 ]]
}

# docker load mit Pruefsummen-Check; setzt LOADED auf die geladene Referenz.
load_archive() {
    local file="$1" out
    [[ -f $file ]] || die "Archiv nicht gefunden: $file"
    if [[ -f "$file.sha256" ]]; then
        info "Pruefsumme wird geprueft ..."
        (cd "$(dirname "$file")" && sha256sum -c --quiet "$(basename "$file").sha256") || die "Pruefsumme stimmt nicht: $file"
    else
        warn "Keine .sha256-Datei neben dem Archiv - Pruefsumme wird nicht geprueft."
    fi
    info "docker load (dauert einen Moment) ..."
    out="$(docker load -i "$file")" || die "docker load fehlgeschlagen."
    LOADED="$(grep -oE 'Loaded image: .*' <<<"$out" | head -1 | sed 's/^Loaded image: //' || true)"
    [[ -n $LOADED ]] || die "Archiv enthaelt kein getaggtes Image."
    info "Geladen: $LOADED"
}

pull_image() {
    local image="$1" user token
    if docker pull "$image" 2>/dev/null; then return; fi
    echo
    info "Pull fehlgeschlagen - das Image ist vermutlich privat."
    info "Benoetigt wird ein GitHub-Token mit der Berechtigung read:packages."
    ask user "GitHub-Benutzername" "$(cfg GHCR_USER)"
    ask_secret token "GitHub-Token"
    [[ -n $user && -n $token ]] || die "Ohne Zugangsdaten kein Zugriff auf $image."
    printf '%s' "$token" | docker login ghcr.io -u "$user" --password-stdin || die "docker login fehlgeschlagen."
    CFG_GHCR_USER="$user"
    docker pull "$image" || die "docker pull $image fehlgeschlagen."
}

# compose.yml und Host-Dienst aus dem Image nach $INSTALL_DIR/dist holen
extract_dist() {
    local image="$1" cid
    cid="$(docker create "$image")"
    rm -rf "$INSTALL_DIR/dist.new"; mkdir -p "$INSTALL_DIR/dist.new"
    docker cp "$cid:/app/docker/." "$INSTALL_DIR/dist.new/" >/dev/null
    docker rm "$cid" >/dev/null
    rm -rf "$INSTALL_DIR/dist"; mv "$INSTALL_DIR/dist.new" "$INSTALL_DIR/dist"
    [[ -f "$INSTALL_DIR/dist/compose.yml" ]] || die "Image enthaelt kein docker/compose.yml - zu alt?"
}

install_host_service() {
    local d="$INSTALL_DIR/dist/host"
    install -m 755 "$d/luk-updater.sh" /usr/local/bin/luk-updater
    if [[ -d /run/systemd/system ]] && command -v systemctl >/dev/null; then
        sed "s#@INSTALL_DIR@#$INSTALL_DIR#g" "$d/luk-updater.service" > /etc/systemd/system/luk-updater.service
        sed "s#@INSTALL_DIR@#$INSTALL_DIR#g" "$d/luk-updater.path"    > /etc/systemd/system/luk-updater.path
        systemctl daemon-reload
        systemctl enable --now luk-updater.path >/dev/null
        info "Host-Dienst luk-updater installiert (systemd: luk-updater.path)."
    else
        warn "Kein systemd - die Admin-Seite 'System-Update' kann Auftraege nur ablegen; 'luk-updater run' dann manuell starten."
    fi
    if [[ -n "$(cfg UPDATE_PUBKEY)" ]] && ! command -v minisign >/dev/null; then
        if command -v apt-get >/dev/null && ask_yn "UPDATE_PUBKEY ist gesetzt, minisign fehlt. Jetzt installieren (apt)?" j; then
            apt-get install -y -qq minisign >/dev/null || warn "minisign konnte nicht installiert werden."
        fi
    fi
}

wait_healthy() {
    local t=0 st
    while (( t < 120 )); do
        st="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' luk-app 2>/dev/null || true)"
        [[ $st == healthy ]] && return 0
        [[ $st == exited || $st == dead ]] && return 1
        sleep 3; t=$((t + 3))
    done
    return 1
}

show_initial_password() {
    local f="$INSTALL_DIR/data/admin-initialpasswort.txt" t=0
    while (( t < 30 )) && [[ ! -s $f ]]; do sleep 2; t=$((t + 2)); done
    if [[ -s $f ]]; then
        echo
        printf '\033[1;32m    Admin-Konto "admin" angelegt. Initialpasswort: %s\033[0m\n' "$(cat "$f")"
        info "Beim ersten Login ist ein Passwortwechsel Pflicht. Die Datei wird jetzt geloescht."
        rm -f "$f"
    fi
}

# ---------------------------------------------------------------------------
wizard() {
    need_root
    need_docker
    log "Installationsverzeichnis"
    ask INSTALL_DIR "Verzeichnis fuer .env, Daten und Updates" "$INSTALL_DIR"
    mkdir -p "$INSTALL_DIR/data" "$INSTALL_DIR/updates/inbox" "$INSTALL_DIR/updates/tmp" "$INSTALL_DIR/updates/archive"
    load_env
    [[ -n "$(cfg IMAGE)" ]] && info "Vorhandene Konfiguration gefunden - Werte werden als Vorgabe angeboten."

    # ---- Image beschaffen --------------------------------------------------
    log "Image beschaffen"
    local image choice found="" LOADED=""
    [[ -z $ARCHIVE ]] && found="$(ls -1t "$SCRIPT_DIR"/luk-image-*.tar.gz "$SCRIPT_DIR"/luk-image-*.tar 2>/dev/null | head -1 || true)"
    if [[ -n $ARCHIVE ]]; then
        choice=2
    else
        local reach=n; registry_reachable && reach=j
        info "Registry ghcr.io erreichbar: $([[ $reach == j ]] && echo ja || echo nein)"
        [[ -n $found ]] && info "Archiv gefunden: $found"
        echo "    1) Registry (ghcr.io) - Updates spaeter per Knopfdruck"
        echo "    2) Archiv (luk-image-*.tar.gz) - ohne GitHub-Zugriff vom Server"
        local def=1; { [[ $reach == n ]] || [[ -n $found && -z "$(cfg IMAGE)" ]]; } && def=2
        ask choice "Bezugsweg" "$def"
    fi
    if [[ $choice == 2 ]]; then
        ask ARCHIVE "Pfad zum Image-Archiv" "${ARCHIVE:-$found}"
        load_archive "$ARCHIVE"
        image="$(cfg IMAGE "$LOADED")"
        if [[ $image != "$LOADED" ]]; then docker tag "$LOADED" "$image"; fi
    else
        ask image "Image" "$(cfg IMAGE "$DEFAULT_IMAGE")"
        pull_image "$image"
    fi
    CFG_IMAGE="$image"
    extract_dist "$image"

    # ---- Konfiguration -----------------------------------------------------
    log "Konfiguration (Enter uebernimmt den Vorschlag)"
    local v
    ask v "Oeffentliche Adresse der Anwendung (APP_BASE_URL)" "$(cfg APP_BASE_URL https://luk.example.org)"; CFG_APP_BASE_URL="${v%/}"
    ask v "Anzeigename (APP_NAME)" "$(cfg APP_NAME 'THW LuK')"; CFG_APP_NAME="$v"
    echo
    info "Der Container lauscht nur auf HTTP. Davor gehoert ein Reverse-Proxy mit TLS"
    info "oder ein Cloudflare-Tunnel (dann braucht der Server keinen offenen Port)."
    ask v "Bind-Adresse des Containers (HTTP_BIND)" "$(cfg HTTP_BIND 127.0.0.1:8080)"; CFG_HTTP_BIND="$v"
    ask_secret v "Cloudflare-Tunnel-Token (leer = kein Tunnel)" "$(cfg CLOUDFLARE_TUNNEL_TOKEN)"; CFG_CLOUDFLARE_TUNNEL_TOKEN="$v"
    CFG_COMPOSE_PROFILES=""; [[ -n $v ]] && CFG_COMPOSE_PROFILES="cloudflare"

    echo
    local host="${CFG_APP_BASE_URL#*://}"; host="${host%%/*}"; host="${host%%:*}"
    ask v "Mail-Absender (MAIL_FROM)" "$(cfg MAIL_FROM "no-reply@$host")"; CFG_MAIL_FROM="$v"
    ask v "Absendername (MAIL_FROM_NAME)" "$(cfg MAIL_FROM_NAME 'THW LuK Stabsansicht')"; CFG_MAIL_FROM_NAME="$v"
    ask v "SMTP-Server (leer = kein Mailversand, Passwort-Reset dann ohne Mail)" "$(cfg SMTP_HOST)"; CFG_SMTP_HOST="$v"
    if [[ -n $CFG_SMTP_HOST ]]; then
        ask v "SMTP-Port" "$(cfg SMTP_PORT 587)"; CFG_SMTP_PORT="$v"
        ask v "SMTP-Verschluesselung (tls = STARTTLS, ssl, none)" "$(cfg SMTP_SECURITY "$([[ $CFG_SMTP_PORT == 465 ]] && echo ssl || echo tls)")"; CFG_SMTP_SECURITY="$v"
        ask v "SMTP-Benutzer" "$(cfg SMTP_USER "$CFG_MAIL_FROM")"; CFG_SMTP_USER="$v"
        ask_secret v "SMTP-Passwort" "$(cfg SMTP_PASS)"; CFG_SMTP_PASS="$v"
    fi

    echo
    info "Hilfsmittel (RSS, NINA, LHP) ruft das Programm direkt ab; STEIN- und Divera-"
    info "Schluessel hinterlegen die Org-Admins spaeter in der Anwendung (Organisation verwalten)."
    echo
    info "Kacheln: Die Installation cacht OpenStreetMap-Kacheln selbst (data/tiles). Laufen am Standort"
    info "mehrere Installationen, soll nur EINE bei OSM anfragen: die anderen nehmen diese als Quelle"
    info "(TILE_SOURCE auf deren /tiles/... zeigen, dort TILE_PUBLIC=1 und ggf. TILE_TOKEN setzen)."
    ask v "Kachel-URL fuer den Browser (TILE_URL, Standard = eingebauter Cache)" "$(cfg TILE_URL '/tiles/{z}/{x}/{y}.png')"; CFG_TILE_URL="$v"
    ask v "Quelle des eingebauten Caches (TILE_SOURCE)" "$(cfg TILE_SOURCE 'https://tile.openstreetmap.org/{z}/{x}/{y}.png')"; CFG_TILE_SOURCE="$v"
    ask v "Kachel-Endpunkt fuer andere Installationen freigeben? (TILE_PUBLIC 0/1)" "$(cfg TILE_PUBLIC 0)"; CFG_TILE_PUBLIC="$v"
    if [[ $CFG_TILE_PUBLIC == 1 ]]; then
        ask_secret v "Token fuer fremde Zugriffe (TILE_TOKEN, leer = ohne)" "$(cfg TILE_TOKEN)"; CFG_TILE_TOKEN="$v"
    fi
    ask v "Obergrenze Kachel-Cache in MB (TILE_CACHE_MAX_MB)" "$(cfg TILE_CACHE_MAX_MB 2000)"; CFG_TILE_CACHE_MAX_MB="$v"


    echo
    info "Kartenzentrum: leer lassen = Abschnitt \"karte\" der eingebetteten Stammdaten."
    ask v "Kartenzentrum Breitengrad (MAP_CENTER_LAT)" "$(cfg MAP_CENTER_LAT '')"; CFG_MAP_CENTER_LAT="$v"
    ask v "Kartenzentrum Laengengrad (MAP_CENTER_LNG)" "$(cfg MAP_CENTER_LNG '')"; CFG_MAP_CENTER_LNG="$v"
    ask v "Kartenzentrum Name (MAP_CENTER_NAME)" "$(cfg MAP_CENTER_NAME '')"; CFG_MAP_CENTER_NAME="$v"

    echo
    info "Optional: minisign-Public-Key des Build-Rechners. Gesetzt = der Host-Dienst"
    info "laedt nur signierte Archive (empfohlen, sobald Archive per Website kommen)."
    ask v "UPDATE_PUBKEY (eine Zeile aus minisign.pub, leer = keine Signaturpflicht)" "$(cfg UPDATE_PUBKEY)"; CFG_UPDATE_PUBKEY="$v"

    if [[ -z "$(cfg APP_SECRET)" ]]; then
        CFG_APP_SECRET="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
        info "APP_SECRET erzeugt."
    fi

    # ---- Dateien schreiben -------------------------------------------------
    log "Dateien nach $INSTALL_DIR schreiben"
    {
        echo "# THW LuK Stabsansicht - erzeugt von setup.sh am $(date '+%F %T'). Erneut ausfuehren zum Aendern."
        for k in IMAGE HTTP_BIND COMPOSE_PROFILES CLOUDFLARE_TUNNEL_TOKEN APP_SECRET APP_BASE_URL APP_NAME \
                 MAIL_FROM MAIL_FROM_NAME SMTP_HOST SMTP_PORT SMTP_SECURITY SMTP_USER SMTP_PASS \
                 TILE_URL TILE_SOURCE TILE_PUBLIC TILE_TOKEN TILE_CACHE_MAX_MB \
                 MAP_CENTER_LAT MAP_CENTER_LNG MAP_CENTER_NAME SYNC_INTERVAL_MS SESSION_IDLE_MIN TRUSTED_PROXIES UPDATE_MAX_MB \
                 UPDATE_PUBKEY GHCR_USER; do
            echo "$k=$(env_quote "$(cfg "$k")")"
        done
    } > "$INSTALL_DIR/.env.new"
    chmod 600 "$INSTALL_DIR/.env.new"
    mv -f "$INSTALL_DIR/.env.new" "$INSTALL_DIR/.env"
    cp -f "$INSTALL_DIR/dist/compose.yml" "$INSTALL_DIR/docker-compose.yml"
    info ".env und docker-compose.yml geschrieben."
    install_host_service

    # ---- Starten -----------------------------------------------------------
    log "Container starten"
    docker compose --project-directory "$INSTALL_DIR" up -d --remove-orphans
    if wait_healthy; then
        info "Container laeuft und ist gesund."
    else
        warn "Container meldet sich nicht gesund. Protokoll: docker compose --project-directory $INSTALL_DIR logs"
    fi
    show_initial_password

    log "Fertig"
    info "Anwendung:        $CFG_APP_BASE_URL  (Container: http://$CFG_HTTP_BIND)"
    info "Verzeichnis:      $INSTALL_DIR  (.env, data/, updates/)"
    if [[ -n $CFG_CLOUDFLARE_TUNNEL_TOKEN ]]; then
        info "Cloudflare:       Public Hostname im Tunnel auf  http://app:80  zeigen lassen."
    else
        info "Reverse-Proxy:    Beispiel in $INSTALL_DIR/dist/host/nginx-proxy.conf.example"
    fi
    info "Mail pruefen:     sudo bash setup.sh mail-test empfaenger@example.org"
    info "Update (Archiv):  sudo bash setup.sh load luk-image-<version>.tar.gz"
    info "Update (Registry): sudo bash setup.sh update   - oder im Browser: Admin > System-Update"
    info "Neu konfigurieren: sudo bash setup.sh"
}

# ---------------------------------------------------------------------------
updater() {
    need_root
    [[ -x /usr/local/bin/luk-updater ]] || die "Host-Dienst nicht installiert - zuerst 'sudo bash setup.sh' ausfuehren."
    LUK_INSTALL_DIR="$INSTALL_DIR" /usr/local/bin/luk-updater "$@"
}

cmd="${1:-}"; [[ $# -gt 0 ]] && shift
case "$cmd" in
    ""|setup|install) wizard ;;
    --archive|-a)     ARCHIVE="${1:?Pfad zum Archiv fehlt}"; shift; wizard ;;
    load)
        f="${1:?Archiv fehlt: setup.sh load DATEI}"
        f="$(readlink -f "$f")"
        updater load "$f"
        show_initial_password
        ;;
    update|pull)      updater pull ;;
    rollback)         updater rollback ;;
    status)           updater status ;;
    logs)             need_root; docker compose --project-directory "$INSTALL_DIR" logs -f --tail 100 ;;
    mail-test)        need_root; docker exec luk-app /app/luk mail-test "${1:?Empfaenger fehlt: setup.sh mail-test ADRESSE}" ;;
    -h|--help|help)   sed -n '3,28p' "$0" | sed 's/^# \{0,1\}//' ;;
    *)                die "Unbekannter Befehl: $cmd (siehe setup.sh --help)" ;;
esac
