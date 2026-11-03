#!/bin/sh
# THW LuK Stabsansicht - Container-Entrypoint (laeuft als root, wechselt dann
# zu "luk"). Einzige Aufgabe: die Bind-Mounts gehoeren beim ersten Start root,
# der Dienst laeuft aber unprivilegiert - also Rechte setzen und wechseln.
# Alles andere (Verzeichnisse, APP_SECRET, Datenbank, erster Admin mit
# Initialpasswort in data/admin-initialpasswort.txt) macht `luk serve` selbst.
set -eu

DATA="${DATA_DIR:-/app/data}"
UPD="${UPDATE_DIR:-/app/updates}"

mkdir -p "$DATA" "$UPD/inbox" "$UPD/tmp" "$UPD/archive"
# status.json/updater.log schreibt der Host-Dienst als root und laesst sie
# lesbar - die fassen wir nicht an, nur das Verzeichnis und die Inbox.
chown luk:luk "$DATA" "$UPD" "$UPD/inbox" "$UPD/tmp" "$UPD/archive"
chown -R luk:luk "$DATA"
chmod 750 "$DATA"

if [ "$(id -u)" = "0" ]; then
    exec su-exec luk /app/luk "$@"
fi
exec /app/luk "$@"
