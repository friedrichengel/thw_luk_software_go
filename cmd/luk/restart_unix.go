//go:build !windows

package main

import (
	"os"
	"syscall"

	"github.com/friedrichengel/thw_luk_software_go/internal/update"
)

// restartSelf ersetzt den laufenden Prozess durch das (neue) Binary mit
// denselben Argumenten und derselben Umgebung. Der Listener ist zu diesem
// Zeitpunkt geschlossen, das neue Programm bindet den Port erneut.
// Gestartet wird der Installationspfad (beim Prozessstart festgehalten),
// nicht /proc/self/exe - das zeigt nach dem Austausch auf luk.old.
func restartSelf() error {
	exe, err := update.ExecutablePath()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}
