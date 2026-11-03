#!/usr/bin/env bash
#
# THW LuK Stabsansicht (Go) - Image bauen und als Archiv ablegen (Arbeitsplatz
# mit Docker Desktop oder Server mit Docker). Ergebnis in dist/:
#   luk-image-<version>.tar.gz          docker save, gzip
#   luk-image-<version>.tar.gz.sha256   Pruefsumme
#   luk-image-<version>.tar.gz.minisig  Signatur (nur mit --sign)
#
#   docker/make-archive.sh                    bauen + Archiv
#   docker/make-archive.sh --no-archive       nur bauen (lokal testen)
#   docker/make-archive.sh --sign ~/.minisign/luk.key   zusaetzlich signieren
#   docker/make-archive.sh --push             zusaetzlich nach ghcr.io pushen (docker login vorher)
#
# Signaturschluessel einmalig erzeugen (ohne Passwort, damit auch die
# GitHub-Action damit signieren kann):
#   minisign -G -W -p luk.pub -s luk.key
# Den Inhalt der zweiten Zeile von luk.pub als UPDATE_PUBKEY in setup.sh eintragen.
#
# Fuer den Betrieb ohne Docker (ein Binary) siehe scripts/build.sh.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO="${REPO:-ghcr.io/friedrichengel/thw_luk_software_go}"
OUT="${OUT:-$ROOT/dist}"
SIGN_KEY=""; ARCHIVE=1; PUSH=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        --sign) SIGN_KEY="${2:?Schluesseldatei fehlt}"; shift 2 ;;
        --no-archive) ARCHIVE=0; shift ;;
        --push) PUSH=1; shift ;;
        -h|--help) sed -n '3,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "Unbekannte Option: $1" >&2; exit 2 ;;
    esac
done

VERSION="$(grep -E '^APP_VERSION=' "$ROOT/config/.env.sample" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]' || true)"
YEAR="$(grep -E '^APP_COPYRIGHT_YEAR=' "$ROOT/config/.env.sample" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]' || true)"
SHA="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo local)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
[[ -n $VERSION ]] || { echo "APP_VERSION in config/.env.sample fehlt." >&2; exit 1; }

echo "==> Build $REPO:$VERSION ($SHA)"
# --pull: Basis-Images (golang, alpine) immer frisch holen, damit
# Sicherheitsupdates nicht am lokalen Cache haengen.
docker build --pull -f "$ROOT/docker/Dockerfile" \
    --build-arg "APP_VERSION=$VERSION" --build-arg "APP_COPYRIGHT_YEAR=${YEAR:-2026}" \
    --build-arg "GIT_SHA=$SHA" --build-arg "BUILD_DATE=$DATE" \
    -t "$REPO:$VERSION" -t "$REPO:latest" "$ROOT"

if [[ $PUSH == 1 ]]; then
    echo "==> Push"
    docker push "$REPO:$VERSION"
    docker push "$REPO:latest"
fi

[[ $ARCHIVE == 1 ]] || exit 0
mkdir -p "$OUT"
FILE="$OUT/luk-image-$VERSION.tar.gz"
echo "==> Archiv $FILE"
docker save "$REPO:$VERSION" "$REPO:latest" | gzip -6 > "$FILE"
(cd "$OUT" && sha256sum "$(basename "$FILE")" > "$(basename "$FILE").sha256")
if [[ -n $SIGN_KEY ]]; then
    command -v minisign >/dev/null || { echo "minisign fehlt." >&2; exit 1; }
    minisign -S -W -s "$SIGN_KEY" -m "$FILE" -t "luk-image $VERSION $SHA"
    echo "    signiert: $FILE.minisig"
fi
ls -lh "$FILE"*
echo "Auf den Server: scp $FILE* user@server:~/  und dort  sudo bash setup.sh load ~/$(basename "$FILE")"
