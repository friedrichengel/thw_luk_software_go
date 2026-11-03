//go:build windows

package main

import (
	"os"
	"os/exec"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/update"
)

// restartSelf startet das (neue) Binary als eigenen Prozess mit denselben
// Argumenten und beendet den laufenden. Windows kennt kein exec(2); das
// Fenster bleibt am neuen Prozess, der Port ist nach dem Shutdown frei.
func restartSelf() error {
	exe, err := update.ExecutablePath()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	os.Exit(0)
	return nil
}
