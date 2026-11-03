package update

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/friedrichengel/thw_luk_software_go/internal/config"
)

// binaryStatus liest <DataDir>/update_status.json, das der Binary-Updater
// schreibt (siehe binary.go). Ohne Datei: unknown.
func binaryStatus(cfg *config.Config) Status {
	b, err := os.ReadFile(filepath.Join(cfg.DataDir, "update_status.json"))
	if err != nil {
		return Status{State: "unknown", ImageVersion: cfg.Version}
	}
	var s Status
	if json.Unmarshal(b, &s) != nil || s.State == "" {
		return Status{State: "unknown", ImageVersion: cfg.Version}
	}
	return s
}
