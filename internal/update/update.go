// Package update: Update-Kanal der Anwendung.
//
// Zwei Betriebsarten (UPDATE_MODE):
//
//	docker  Die App fuehrt Updates nie selbst aus - sie hat keinen Docker-
//	        Zugriff. Sie legt lediglich Dateien in UPDATE_DIR ab, die der
//	        Host-Dienst luk-updater (docker/host/luk-updater.sh) verarbeitet:
//
//	          updates/tmp/<id>.part        laufender Chunk-Upload (nur die App)
//	          updates/tmp/<id>.json        Metadaten zum Upload
//	          updates/inbox/<name>.tar.gz  fertiges Image-Archiv (docker save | gzip)
//	          updates/inbox/<name>.sha256  Pruefsumme ("<hex>  <name>")
//	          updates/inbox/<name>.minisig optionale minisign-Signatur
//	          updates/inbox/<name>.meta    wer/wann hochgeladen (JSON)
//	          updates/inbox/pull.trigger   "Image aus Registry ziehen"
//	          updates/inbox/rollback.trigger "vorheriges Image wiederherstellen"
//	          updates/status.json          Rueckmeldung des Host-Dienstes (nur lesen)
//	          updates/updater.log          Protokoll des Host-Dienstes (nur lesen)
//
//	binary  Betrieb ohne Docker (ein Binary): die Admin-Seite nimmt ein neues
//	        Binary entgegen, prueft SHA-256 und - wenn UPDATE_PUBKEY gesetzt
//	        ist - die minisign-Signatur, tauscht die eigene Datei aus und
//	        startet sich neu (siehe binary.go).
//
// Diese Datei enthaelt den lesenden Teil (Status, wartende Auftraege,
// Protokoll), den die Admin-Seite und die Fussleiste brauchen.
package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/config"
)

const (
	ChunkMaxBytes = 16 * 1024 * 1024
	ChunkBytes    = 8 * 1024 * 1024
)

var reSidecar = regexp.MustCompile(`\.(sha256|minisig|meta)$`)

type Channel struct {
	Cfg *config.Config
}

func New(cfg *config.Config) *Channel {
	return &Channel{Cfg: cfg}
}

// Mode: "docker", "binary" oder "" (kein Update-Kanal).
func (c *Channel) Mode() string {
	if c == nil || c.Cfg == nil {
		return ""
	}
	switch c.Cfg.UpdateMode {
	case "docker":
		if c.dockerEnabled() {
			return "docker"
		}
		return ""
	case "binary":
		return "binary"
	}
	return ""
}

func (c *Channel) dockerEnabled() bool {
	if c.Cfg.UpdateDir == "" {
		return false
	}
	inbox := c.Dir("inbox")
	st, err := os.Stat(inbox)
	if err != nil || !st.IsDir() {
		return false
	}
	// beschreibbar?
	f, err := os.CreateTemp(inbox, ".probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// Enabled: Admin-Seite System-Update verfuegbar.
func (c *Channel) Enabled() bool {
	return c.Mode() != ""
}

func (c *Channel) Dir(sub string) string {
	if sub == "" {
		return c.Cfg.UpdateDir
	}
	return filepath.Join(c.Cfg.UpdateDir, sub)
}

type Status struct {
	State             string `json:"state"`
	Step              string `json:"step"`
	Message           string `json:"message"`
	Job               string `json:"job"`
	Image             string `json:"image"`
	ImageID           string `json:"image_id"`
	ImageVersion      string `json:"image_version"`
	PreviousAvailable bool   `json:"previous_available"`
	At                any    `json:"at"`
}

type PendingItem struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mtime string `json:"mtime"`
}

// Info: alles, was die Admin-Seite zum Rendern braucht.
func (c *Channel) Info() map[string]any {
	return map[string]any{
		"enabled":       c.Enabled(),
		"mode":          c.Mode(),
		"image":         c.Cfg.Image,
		"version":       c.Cfg.Version,
		"asset_version": c.Cfg.AssetVersion,
		"deployed":      c.Cfg.Deployed,
		"max_mb":        c.Cfg.UpdateMaxMB,
		"chunk_bytes":   ChunkBytes,
		"status":        c.StatusInfo(),
		"pending":       c.Pending(),
		"log":           c.LogTail(40),
		"signed":        c.Cfg.UpdatePubKey != "",
	}
}

// StatusInfo: letzte Rueckmeldung des Host-Dienstes (Docker) bzw. des
// Binary-Updaters.
func (c *Channel) StatusInfo() Status {
	if c.Mode() == "binary" {
		return binaryStatus(c.Cfg)
	}
	f := c.Dir("status.json")
	b, err := os.ReadFile(f)
	if err != nil {
		return Status{State: "unknown"}
	}
	var s Status
	if json.Unmarshal(b, &s) != nil {
		return Status{State: "unknown"}
	}
	if s.State == "" {
		s.State = "unknown"
	}
	return s
}

// Pending: Eintraege in inbox/, die noch auf den Host-Dienst warten.
func (c *Channel) Pending() []PendingItem {
	out := []PendingItem{}
	if c.Mode() == "binary" {
		return c.pendingBinary()
	}
	if c.Mode() != "docker" {
		return out
	}
	entries, err := os.ReadDir(c.Dir("inbox"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || reSidecar.MatchString(e.Name()) {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, PendingItem{Name: e.Name(), Size: st.Size(), Mtime: st.ModTime().Format(time.RFC3339)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LogTail liefert die letzten Zeilen des Updater-Protokolls.
func (c *Channel) LogTail(lines int) string {
	var f string
	if c.Mode() == "binary" {
		f = filepath.Join(c.Cfg.DataDir, "update.log")
	} else {
		f = c.Dir("updater.log")
	}
	st, err := os.Stat(f)
	if err != nil {
		return ""
	}
	fh, err := os.Open(f)
	if err != nil {
		return ""
	}
	defer fh.Close()
	size := st.Size()
	chunk := size
	if chunk > 64*1024 {
		chunk = 64 * 1024
	}
	buf := make([]byte, chunk)
	if _, err := fh.ReadAt(buf, size-chunk); err != nil && chunk > 0 {
		return ""
	}
	arr := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if chunk < size && len(arr) > 0 {
		arr = arr[1:] // angeschnittene erste Zeile
	}
	if len(arr) > lines {
		arr = arr[len(arr)-lines:]
	}
	return strings.Join(arr, "\n")
}

// DeployedStamp fuer die Anzeige.
func (c *Channel) DeployedStamp() string {
	return c.Cfg.Deployed
}
