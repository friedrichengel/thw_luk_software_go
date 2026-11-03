// Package luk buendelt die statischen Dateien der Anwendung in das Binary:
// public/ (CSS, JS, Bilder, Fonts, Stammdaten, Manifest, Service Worker),
// assets/ (taktische Zeichen, GeoJSON) und das SQLite-Schema. Dadurch
// besteht eine Installation aus genau einer Datei plus Datenverzeichnis.
package luk

import "embed"

//go:embed all:public
var Public embed.FS

//go:embed all:assets
var Assets embed.FS

//go:embed sql/schema.sqlite.sql
var Schema string
