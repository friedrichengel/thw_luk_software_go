#!/usr/bin/env bash
#
# luk-updater - Host-Dienst fuer Updates des THW-LuK-Containers (Go-Fassung).
#
# Der Container hat keinen Docker-Zugriff. Die Web-App (Admin > System-Update)
# legt nur Dateien in <INSTALL_DIR>/updates/inbox/ ab; dieses Skript laeuft als
# root auf dem Host, wird von der systemd-Path-Unit luk-updater.path gestartet,
# sobald inbox/ nicht leer ist, und meldet seinen Stand in updates/status.json
# und updates/updater.log zurueck.
#
#   <name>.tar.gz [+ .sha256, .minisig, .meta]  Image-Archiv (docker save)
#   pull.trigger                                Image aus der Registry ziehen
#   rollback.trigger                            vorheriges Image wiederherstellen
#
# Manuell (docker/setup.sh ruft diese Befehle auf):
#   luk-updater run              inbox/ abarbeiten (Standard)
#   luk-updater load DATEI       Archiv einspielen (Pruefsumme wird ggf. erzeugt)
#   luk-updater pull             Image aus der Registry ziehen und neu starten
#   luk-updater rollback         vorheriges Image wiederherstellen
#   luk-updater status           Stand anzeigen
#
# Konfiguration aus <INSTALL_DIR>/.env: IMAGE (Pflicht), UPDATE_PUBKEY
# (minisign-Public-Key; gesetzt = Archive ohne gueltige Signatur werden
# abgelehnt). INSTALL_DIR kommt aus LUK_INSTALL_DIR (Unit) oder ist /opt/luk.
#
# Ablauf eines Updates: aktuelles Image als <repo>:previous sichern, neues
# Image auf IMAGE taggen, compose.yml und dieses Skript aus dem Image
# (/app/docker) erneuern, docker compose up -d, auf "healthy" warten (der
# Container prueft sich selbst mit `luk healthcheck`). Bleibt der Container
# ungesund, wird automatisch auf :previous zurueckgedreht.
set -euo pipefail

INSTALL_DIR="${LUK_INSTALL_DIR:-/opt/luk}"
ENV_FILE="$INSTALL_DIR/.env"
UPD="$INSTALL_DIR/updates"
INBOX="$UPD/inbox"
ARCHIVE="$UPD/archive"
STATUS="$UPD/status.json"
LOG="$UPD/updater.log"
LOCK="$UPD/.lock"
CONTAINER="luk-app"
KEEP_ARCHIVES=2
HEALTH_TIMEOUT=150
SELF=/usr/local/bin/luk-updater

log()  { printf '%s %s\n' "$(date '+%F %T')" "$*" | tee -a "$LOG" >&2; chmod 644 "$LOG" 2>/dev/null || true; }
die()  { log "FEHLER: $*"; exit 1; }

# Wert aus .env: KEY='wert' woertlich, KEY="wert" mit \" \\ und $$ als Escapes (siehe setup.sh).
getenv() {
    local v
    v="$(grep -E "^$1=" "$ENV_FILE" 2>/dev/null | head -1 | cut -d= -f2- || true)"
    if [[ $v == \'*\' ]]; then v="${v:1:-1}"
    elif [[ $v == \"*\" ]]; then v="${v:1:-1}"; v="${v//\\\"/\"}"; v="${v//\\\\/\\}"; v="${v//\$\$/\$}"; fi
    printf '%s' "$v"
}

# Repository-Anteil eines Image-Namens ("ghcr.io/x/y:tag" -> "ghcr.io/x/y").
repo_of() {
    local ref="$1" base
    base="${ref##*/}"
    if [[ $base == *:* ]]; then printf '%s' "${ref%:*}"; else printf '%s' "$ref"; fi
}

json_escape() { local s="${1//\\/\\\\}"; s="${s//\"/\\\"}"; s="${s//$'\n'/ }"; printf '%s' "$s"; }

image_id()      { docker image inspect --format '{{.Id}}' "$1" 2>/dev/null || true; }
image_version() { docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.version"}}' "$1" 2>/dev/null || true; }

# status <state> <job> <message>  -> updates/status.json (lesbar fuer die App)
status() {
    local prev=false
    [[ -n "$(image_id "$PREV")" ]] && prev=true
    mkdir -p "$UPD"
    printf '{"state":"%s","job":"%s","step":"%s","message":"%s","image":"%s","image_id":"%s","image_version":"%s","previous_available":%s,"at":"%s"}\n' \
        "$1" "$(json_escape "$2")" "$(json_escape "$2")" "$(json_escape "$3")" \
        "$(json_escape "$IMAGE")" "$(image_id "$IMAGE" | cut -c8-19)" "$(json_escape "$(image_version "$IMAGE")")" \
        "$prev" "$(date -Is)" > "$STATUS.tmp"
    chmod 644 "$STATUS.tmp"
    mv -f "$STATUS.tmp" "$STATUS"
    log "[$1] $2: $3"
}

compose() { docker compose --project-directory "$INSTALL_DIR" "$@"; }

wait_healthy() {
    local t=0 st
    while (( t < HEALTH_TIMEOUT )); do
        st="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$CONTAINER" 2>/dev/null || true)"
        [[ $st == healthy ]] && return 0
        [[ $st == exited || $st == dead ]] && return 1
        sleep 3; t=$((t + 3))
    done
    return 1
}

# compose.yml und dieses Skript aus dem (neuen) Image erneuern.
refresh_dist() {
    local cid tmp
    tmp="$(mktemp -d)"
    cid="$(docker create "$IMAGE")" || { rm -rf "$tmp"; return 1; }
    docker cp "$cid:/app/docker/." "$tmp/" >/dev/null
    docker rm "$cid" >/dev/null
    rm -rf "$INSTALL_DIR/dist.new"
    mv "$tmp" "$INSTALL_DIR/dist.new"
    rm -rf "$INSTALL_DIR/dist"
    mv "$INSTALL_DIR/dist.new" "$INSTALL_DIR/dist"
    cp "$INSTALL_DIR/dist/compose.yml" "$INSTALL_DIR/docker-compose.yml"
    # Sich selbst atomar erneuern: neuer Inode, der laufende Prozess liest den alten weiter.
    if [[ -f "$INSTALL_DIR/dist/host/luk-updater.sh" ]]; then
        install -m 755 "$INSTALL_DIR/dist/host/luk-updater.sh" "$SELF.new" && mv -f "$SELF.new" "$SELF"
    fi
}

# apply_image <neues Image (Ref oder ID)> <ID des bisherigen Images oder leer> <Job>
apply_image() {
    local new="$1" cur="$2" job="$3" newid
    newid="$(image_id "$new")"
    [[ -n $newid ]] || { status error "$job" "Image $new nicht gefunden"; return 1; }
    if [[ -n $cur && $cur == "$newid" ]]; then
        status ok "$job" "Bereits aktuell (v$(image_version "$IMAGE"))"
        return 0
    fi
    [[ -n $cur ]] && docker tag "$cur" "$PREV"
    docker tag "$newid" "$IMAGE"
    status running "$job" "Dateien aus dem Image uebernehmen"
    refresh_dist || log "WARNUNG: compose.yml/luk-updater konnten nicht aus dem Image erneuert werden"
    status running "$job" "Container wird neu gestartet (v$(image_version "$IMAGE"))"
    if compose up -d --remove-orphans >>"$LOG" 2>&1 && wait_healthy; then
        status ok "$job" "Update auf v$(image_version "$IMAGE") erfolgreich"
        docker image prune -f >/dev/null 2>&1 || true
        return 0
    fi
    log "Container nach Update nicht gesund:"
    docker logs --tail 30 "$CONTAINER" >>"$LOG" 2>&1 || true
    if [[ -n $cur ]]; then
        docker tag "$cur" "$IMAGE"
        refresh_dist || true
        compose up -d --remove-orphans >>"$LOG" 2>&1 || true
        wait_healthy || true
        status error "$job" "Neues Image nicht gesund - vorheriger Stand (v$(image_version "$IMAGE")) wiederhergestellt"
    else
        status error "$job" "Container nicht gesund (siehe Protokoll)"
    fi
    return 1
}

do_pull() {
    local job="Registry" cur
    cur="$(image_id "$IMAGE")"
    status running "$job" "docker pull $IMAGE"
    if ! docker pull "$IMAGE" >>"$LOG" 2>&1; then
        status error "$job" "docker pull fehlgeschlagen - Registry nicht erreichbar oder nicht angemeldet (docker login ghcr.io)"
        return 1
    fi
    apply_image "$IMAGE" "$cur" "$job"
}

do_rollback() {
    local job="Rueckfall" cur previd
    previd="$(image_id "$PREV")"
    [[ -n $previd ]] || { status error "$job" "Kein vorheriges Image vorhanden"; return 1; }
    cur="$(image_id "$IMAGE")"
    status running "$job" "Vorheriges Image wird aktiviert"
    docker tag "$previd" "$IMAGE"
    refresh_dist || true
    if compose up -d --remove-orphans >>"$LOG" 2>&1 && wait_healthy; then
        # Tausch: der eben ersetzte Stand wird zum neuen "previous".
        [[ -n $cur ]] && docker tag "$cur" "$PREV"
        status ok "$job" "Zurueck auf v$(image_version "$IMAGE")"
        return 0
    fi
    status error "$job" "Container nach Rueckfall nicht gesund (siehe Protokoll)"
    return 1
}

# process_archive <Datei in inbox/>
process_archive() {
    local file="$1" name job by="" out loaded lrepo cur
    name="$(basename "$file")"
    job="Archiv $name"
    [[ -f "$file.meta" ]] && by="$(grep -oE '"by":"[^"]*"' "$file.meta" | cut -d'"' -f4 || true)"
    status running "$job" "Archiv wird geprueft${by:+ (hochgeladen von $by)}"

    if [[ -f "$file.sha256" ]]; then
        (cd "$INBOX" && sha256sum -c --quiet "$name.sha256" >/dev/null 2>&1) \
            || { status error "$job" "Pruefsumme stimmt nicht"; return 1; }
    else
        status error "$job" "Pruefsumme ($name.sha256) fehlt"; return 1
    fi
    if [[ -n $PUBKEY ]]; then
        command -v minisign >/dev/null || { status error "$job" "minisign nicht installiert (apt install minisign)"; return 1; }
        [[ -f "$file.minisig" ]] || { status error "$job" "Signatur fehlt - UPDATE_PUBKEY ist gesetzt, daher Pflicht"; return 1; }
        minisign -Vq -P "$PUBKEY" -x "$file.minisig" -m "$file" >/dev/null 2>&1 \
            || { status error "$job" "Signatur ungueltig - Archiv abgelehnt"; return 1; }
        log "Signatur gueltig"
    else
        log "WARNUNG: kein UPDATE_PUBKEY in .env - Archiv wird ohne Signaturpruefung geladen"
    fi

    status running "$job" "docker load"
    if ! out="$(docker load -i "$file" 2>&1)"; then
        status error "$job" "docker load: ${out##*$'\n'}"; return 1
    fi
    loaded="$(grep -oE 'Loaded image: .*' <<<"$out" | head -1 | sed 's/^Loaded image: //' || true)"
    [[ -n $loaded ]] || { status error "$job" "Archiv enthaelt kein getaggtes Image"; return 1; }
    lrepo="$(repo_of "$loaded")"
    if [[ $lrepo != "$REPO" ]]; then
        docker rmi "$loaded" >/dev/null 2>&1 || true
        status error "$job" "Image $loaded passt nicht zu $IMAGE - abgelehnt"; return 1
    fi
    cur="$(image_id "$IMAGE")"
    apply_image "$loaded" "$cur" "$job"
}

# Archiv samt Sidecars nach archive/ verschieben, alte Archive aufraeumen.
archive_files() {
    local name="$1" ts f
    ts="$(date +%Y%m%d-%H%M%S)"
    for f in "$INBOX/$name" "$INBOX/$name".sha256 "$INBOX/$name".minisig "$INBOX/$name".meta; do
        [[ -e $f ]] && mv -f "$f" "$ARCHIVE/$ts-$(basename "$f")"
    done
    # nur die juengsten KEEP_ARCHIVES Archive behalten
    ls -1t "$ARCHIVE"/*.tar "$ARCHIVE"/*.tar.gz "$ARCHIVE"/*.tgz 2>/dev/null | tail -n +$((KEEP_ARCHIVES + 1)) \
        | while read -r f; do rm -f "$f" "$f.sha256" "$f.minisig" "$f.meta"; done
}

run_inbox() {
    local f n
    shopt -s nullglob
    for f in "$INBOX"/*; do
        n="$(basename "$f")"
        case "$n" in
            *.sha256|*.minisig|*.meta|*.part|*.tmp) continue ;;
            pull.trigger)     rm -f "$f"; do_pull || true ;;
            rollback.trigger) rm -f "$f"; do_rollback || true ;;
            *.tar|*.tar.gz|*.tgz) process_archive "$f" || true; archive_files "$n" ;;
            *) log "Unbekannte Datei $n verworfen"; mv -f "$f" "$ARCHIVE/failed-$n" ;;
        esac
    done
    # Verwaiste Sidecars (Archiv kam nie an) nach 2 Minuten entfernen; solange
    # inbox/ nicht leer ist, wuerde die Path-Unit sonst dauernd neu starten.
    local now; now="$(date +%s)"
    for f in "$INBOX"/*.sha256 "$INBOX"/*.minisig "$INBOX"/*.meta; do
        [[ -e "${f%.*}" ]] && continue
        if (( now - $(stat -c %Y "$f") > 120 )); then rm -f "$f"; else sleep 5; fi
    done
}

# ---------------------------------------------------------------------------
cmd="${1:-run}"; shift || true
[[ $EUID -eq 0 ]] || die "Bitte als root ausfuehren."
[[ -f $ENV_FILE ]] || die "$ENV_FILE fehlt - zuerst docker/setup.sh ausfuehren."
mkdir -p "$INBOX" "$ARCHIVE"
IMAGE="$(getenv IMAGE)"
PUBKEY="$(getenv UPDATE_PUBKEY)"
[[ -n $IMAGE ]] || die "IMAGE in $ENV_FILE fehlt."
REPO="$(repo_of "$IMAGE")"
PREV="$REPO:previous"

case "$cmd" in
    run)
        exec 9>"$LOCK"
        flock -n 9 || { echo "luk-updater laeuft bereits."; exit 0; }
        run_inbox
        ;;
    load)
        file="${1:-}"
        [[ -f $file ]] || die "Archiv nicht gefunden: $file"
        exec 9>"$LOCK"; flock -w 600 9 || die "luk-updater belegt."
        name="$(basename "$file")"
        cp -f "$file" "$INBOX/$name.part"
        if [[ -f "$file.sha256" ]]; then cp -f "$file.sha256" "$INBOX/$name.sha256"
        else (cd "$(dirname "$file")" && sha256sum "$name") > "$INBOX/$name.sha256"; fi
        [[ -f "$file.minisig" ]] && cp -f "$file.minisig" "$INBOX/$name.minisig"
        printf '{"by":"shell","at":"%s"}\n' "$(date -Is)" > "$INBOX/$name.meta"
        mv -f "$INBOX/$name.part" "$INBOX/$name"
        run_inbox
        ;;
    pull)
        exec 9>"$LOCK"; flock -w 600 9 || die "luk-updater belegt."
        do_pull
        ;;
    rollback)
        exec 9>"$LOCK"; flock -w 600 9 || die "luk-updater belegt."
        do_rollback
        ;;
    status)
        echo "Image:     $IMAGE ($(image_id "$IMAGE" | cut -c8-19)) v$(image_version "$IMAGE")"
        echo "Vorherig:  $([[ -n "$(image_id "$PREV")" ]] && echo "v$(image_version "$PREV")" || echo "keins")"
        echo "Container: $(docker inspect --format '{{.State.Status}}{{if .State.Health}} / {{.State.Health.Status}}{{end}}' "$CONTAINER" 2>/dev/null || echo "nicht vorhanden")"
        [[ -f $STATUS ]] && { echo "Letzter Auftrag: $(cat "$STATUS")"; }
        ls -1 "$INBOX" 2>/dev/null | sed 's/^/Wartet:    /' || true
        ;;
    *)
        sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
        exit 2
        ;;
esac
