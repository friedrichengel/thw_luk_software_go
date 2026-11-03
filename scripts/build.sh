#!/usr/bin/env bash
#
# THW LuK Stabsansicht (Go) - Binaries fuer alle Plattformen bauen (Betrieb
# ohne Docker: ein Programm plus Datenverzeichnis). Ergebnis in dist/:
#   luk-<version>-linux-amd64, luk-<version>-linux-arm64,
#   luk-<version>-windows-amd64.exe, luk-<version>-darwin-arm64
#   je mit .sha256 und - mit --sign - .minisig
#
#   scripts/build.sh                      alle Plattformen
#   scripts/build.sh --only linux/amd64   nur eine (GOOS/GOARCH)
#   scripts/build.sh --sign luk.key       zusaetzlich mit minisign signieren
#   scripts/build.sh --keep 3             aeltere Builds behalten (Standard: nur
#                                         die aktuelle Version bleibt in dist/)
#
# Nach dem Bauen werden aeltere Binaries (luk-<version>-<os>-<arch> samt
# .sha256/.minisig) aus dist/ entfernt; es bleiben die --keep juengsten
# Versionen. Image-Archive (luk-image-*) bleiben unberuehrt.
#
# Die Dateien laesst sich ueber Admin > System-Update hochladen (UPDATE_MODE=binary):
# der Server prueft Pruefsumme und Signatur (UPDATE_PUBKEY), tauscht sich
# selbst aus und startet neu. Version aus config/.env.sample (APP_VERSION).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${OUT:-$ROOT/dist}"
SIGN_KEY=""
KEEP="${KEEP:-1}"
TARGETS=("linux/amd64" "linux/arm64" "windows/amd64" "darwin/arm64")
while [[ $# -gt 0 ]]; do
    case "$1" in
        --sign) SIGN_KEY="${2:?Schluesseldatei fehlt}"; shift 2 ;;
        --only) TARGETS=("${2:?GOOS/GOARCH fehlt}"); shift 2 ;;
        --keep) KEEP="${2:?Anzahl fehlt}"; shift 2 ;;
        -h|--help) sed -n '3,22p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "Unbekannte Option: $1" >&2; exit 2 ;;
    esac
done

VERSION="$(grep -E '^APP_VERSION=' "$ROOT/config/.env.sample" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]' || true)"
VERSION="${VERSION:-dev}"
SHA="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo local)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
PKG="github.com/friedrichengel/thw_luk_software_go/internal/config"
LDFLAGS="-s -w -X $PKG.Version=$VERSION -X $PKG.BuildStamp=$SHA -X $PKG.BuildDate=$DATE"

command -v go >/dev/null || { echo "go fehlt (https://go.dev/dl/)." >&2; exit 1; }
mkdir -p "$OUT"
for t in "${TARGETS[@]}"; do
    goos="${t%/*}"; goarch="${t#*/}"
    name="luk-$VERSION-$goos-$goarch"
    [[ $goos == windows ]] && name="$name.exe"
    echo "==> $t -> $OUT/$name"
    (cd "$ROOT" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/$name" ./cmd/luk)
    (cd "$OUT" && sha256sum "$name" > "$name.sha256")
    if [[ -n $SIGN_KEY ]]; then
        command -v minisign >/dev/null || { echo "minisign fehlt." >&2; exit 1; }
        minisign -S -W -s "$SIGN_KEY" -m "$OUT/$name" -t "luk $VERSION $SHA $t"
    fi
done

# ---- Aeltere Builds aufraeumen ------------------------------------------
# Versionen aus den Dateinamen sammeln (luk-<version>-<os>-<arch>...), nach
# juengster Aenderung sortieren; die aktuelle Version zaehlt immer als juengste.
if [[ $KEEP =~ ^[0-9]+$ && $KEEP -ge 1 ]]; then
    declare -A newest=()
    for f in "$OUT"/luk-*-*-*; do
        [[ -f $f ]] || continue
        base="$(basename "$f")"
        [[ $base == luk-image-* ]] && continue
        v="${base#luk-}"; v="${v%%-*}"
        [[ -n $v ]] || continue
        if [[ $v == "$VERSION" ]]; then
            newest[$v]=9999999999
        else
            m="$(stat -c %Y "$f" 2>/dev/null || stat -f %m "$f" 2>/dev/null || echo 0)"
            [[ ${newest[$v]:-0} -lt $m ]] && newest[$v]="$m"
        fi
    done
    i=0
    for v in $(for k in "${!newest[@]}"; do echo "${newest[$k]} $k"; done | sort -rn | awk '{print $2}'); do
        i=$((i + 1))
        [[ $i -le $KEEP ]] && continue
        echo "==> entferne aeltere Version $v aus $OUT"
        rm -f "$OUT"/luk-"$v"-*
    done
fi

ls -lh "$OUT"/luk-"$VERSION"-*
