package update

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
)

// Binary-Modus: das laufende Programm tauscht sich selbst aus.
//
//	<exe>.new   bereitgestelltes neues Binary (nach FinishUpload)
//	<exe>.old   vorheriger Stand (nach ApplyBinary; Rollback tauscht zurueck)
//	<DataDir>/update_staged.json   Metadaten der bereitgestellten Datei
//	<DataDir>/update_status.json   Stand fuer die Admin-Seite (Status-Struktur)
//	<DataDir>/update.log           Protokoll
//
// Nach dem Tausch startet RestartFunc das Programm neu (setzt der Server:
// Linux/macOS exec in-place, Windows neuer Prozess + Exit). Ohne Hook bleibt
// der Stand "ok" mit dem Hinweis, dass ein Neustart noetig ist.

// RestartFunc wird vom Server gesetzt und startet das (ausgetauschte) Binary neu.
var RestartFunc func()

// ExecutablePath liefert den Installationspfad des laufenden Binaries
// (Symlinks aufgeloest). Der Pfad wird EINMAL beim Prozessstart bestimmt und
// danach festgehalten: /proc/self/exe folgt einer Umbenennung der laufenden
// Datei, und nach ApplyBinary (luk -> luk.old) wuerde os.Executable() sonst
// auf luk.old zeigen - der Neustart startete dann das alte Programm und ein
// weiteres Update verschob die Dateien zu luk.old.old.
var ExecutablePath = func() (string, error) {
	exeOnce.Do(func() {
		p, err := os.Executable()
		if err != nil {
			exeErr = err
			return
		}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		exePath = p
	})
	return exePath, exeErr
}

var (
	exeOnce sync.Once
	exePath string
	exeErr  error
)

func init() {
	// Pfad sofort festhalten, bevor ein Update die Datei umbenennen kann.
	_, _ = ExecutablePath()
}

type stagedMeta struct {
	Name   string `json:"name"`
	By     string `json:"by"`
	At     string `json:"at"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Signed bool   `json:"signed"`
}

func (c *Channel) stagedFile() string { return filepath.Join(c.Cfg.DataDir, "update_staged.json") }

func (c *Channel) readStaged() *stagedMeta {
	b, err := os.ReadFile(c.stagedFile())
	if err != nil {
		return nil
	}
	var m stagedMeta
	if json.Unmarshal(b, &m) != nil || m.Name == "" {
		return nil
	}
	return &m
}

// pendingBinary: die bereitgestellte neue Datei, falls vorhanden.
func (c *Channel) pendingBinary() []PendingItem {
	out := []PendingItem{}
	m := c.readStaged()
	if m == nil {
		return out
	}
	exe, err := ExecutablePath()
	if err != nil {
		return out
	}
	st, err := os.Stat(exe + ".new")
	if err != nil {
		return out
	}
	out = append(out, PendingItem{Name: m.Name, Size: st.Size(), Mtime: st.ModTime().Format(time.RFC3339)})
	return out
}

func (c *Channel) discardStaged() int {
	n := 0
	if exe, err := ExecutablePath(); err == nil {
		if os.Remove(exe+".new") == nil {
			n++
		}
	}
	if os.Remove(c.stagedFile()) == nil {
		n++
	}
	return n
}

func isExecutableHeader(head []byte) bool {
	switch {
	case len(head) >= 4 && bytes.Equal(head[:4], []byte{0x7f, 'E', 'L', 'F'}): // Linux
		return true
	case len(head) >= 2 && head[0] == 'M' && head[1] == 'Z': // Windows PE
		return true
	case len(head) >= 4 && (bytes.Equal(head[:4], []byte{0xcf, 0xfa, 0xed, 0xfe}) || bytes.Equal(head[:4], []byte{0xca, 0xfe, 0xba, 0xbe})): // macOS
		return true
	}
	return false
}

// finishBinary prueft Kopf und Signatur und stellt die Datei als <exe>.new bereit.
func (c *Channel) finishBinary(id string, meta *uploadMeta, part string, head []byte, minisig, user string) (string, error) {
	if !isExecutableHeader(head) {
		c.CancelUpload(id)
		return "", e("not_a_binary", 400)
	}
	signed := false
	if c.Cfg.UpdatePubKey != "" {
		if minisig == "" {
			c.CancelUpload(id)
			return "", e("signature_missing", 400)
		}
		data, err := os.ReadFile(part)
		if err != nil {
			return "", ioErr(err)
		}
		if err := VerifyMinisign(c.Cfg.UpdatePubKey, data, minisig); err != nil {
			c.CancelUpload(id)
			c.logf("Signatur fuer %s ungueltig: %v", meta.Name, err)
			return "", apierr.WithExtra("signature_invalid", 400, map[string]any{"detail": err.Error()})
		}
		signed = true
	}
	exe, err := ExecutablePath()
	if err != nil {
		return "", ioErr(err)
	}
	target := exe + ".new"
	os.Remove(target)
	if err := os.Rename(part, target); err != nil {
		// anderes Dateisystem: kopieren
		if cerr := copyFile(part, target); cerr != nil {
			return "", ioErr(cerr)
		}
		os.Remove(part)
	}
	if err := os.Chmod(target, 0o755); err != nil && runtime.GOOS != "windows" {
		return "", ioErr(err)
	}
	os.Remove(filepath.Join(c.tmpDir(), id+".json"))
	sm := stagedMeta{Name: meta.Name, By: user, At: time.Now().Format(time.RFC3339), Size: meta.Size, SHA256: meta.SHA256, Signed: signed}
	b, _ := json.Marshal(sm)
	if err := os.WriteFile(c.stagedFile(), b, 0o640); err != nil {
		return "", ioErr(err)
	}
	c.logf("Binary %s (%d Bytes, sha256 %s, signiert=%v) bereitgestellt von %s", meta.Name, meta.Size, meta.SHA256[:12], signed, user)
	c.writeBinaryStatus("idle", "Bereitgestellt", "Neues Binary "+meta.Name+" wartet auf Einspielen", versionFromName(meta.Name))
	return meta.Name, nil
}

// ApplyBinary prueft die bereitgestellte Datei (Startprobe mit "version"),
// tauscht sie gegen das laufende Binary und startet neu.
func (c *Channel) ApplyBinary(name, user string) error {
	if c.Mode() != "binary" {
		return e("not_enabled", 400)
	}
	if c.Busy() {
		return e("busy", 409)
	}
	m := c.readStaged()
	exe, err := ExecutablePath()
	if err != nil {
		return ioErr(err)
	}
	if m == nil || (name != "" && m.Name != name) {
		return e("unknown_upload", 400)
	}
	newPath := exe + ".new"
	if _, err := os.Stat(newPath); err != nil {
		return e("unknown_upload", 400)
	}
	ver := versionFromName(m.Name)
	c.writeBinaryStatus("running", "Pruefen", "Neues Binary wird geprueft", ver)
	// Startprobe: das neue Binary muss "version" beantworten koennen.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, perr := exec.CommandContext(ctx, newPath, "version").CombinedOutput()
	if perr != nil {
		c.logf("Startprobe von %s fehlgeschlagen: %v %s", m.Name, perr, bytes.TrimSpace(out))
		c.writeBinaryStatus("error", "Pruefen", "Neues Binary startet nicht ("+perr.Error()+") - verworfen", ver)
		c.discardStaged()
		return apierr.WithExtra("binary_invalid", 400, map[string]any{"detail": string(bytes.TrimSpace(out))})
	}
	c.logf("Startprobe ok: %s", bytes.TrimSpace(out))
	c.writeBinaryStatus("running", "Austausch", "Binary wird ausgetauscht", ver)
	oldPath := exe + ".old"
	os.Remove(oldPath)
	if err := os.Rename(exe, oldPath); err != nil {
		c.writeBinaryStatus("error", "Austausch", "Altes Binary konnte nicht umbenannt werden: "+err.Error(), ver)
		return ioErr(err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		_ = os.Rename(oldPath, exe)
		c.writeBinaryStatus("error", "Austausch", "Neues Binary konnte nicht aktiviert werden: "+err.Error(), ver)
		return ioErr(err)
	}
	os.Remove(c.stagedFile())
	c.logf("Binary %s eingespielt von %s (vorheriger Stand: %s)", m.Name, user, oldPath)
	c.restart("Update auf "+m.Name+" eingespielt", ver)
	return nil
}

// Rollback tauscht <exe>.old zurueck und startet neu (Binary-Modus).
func (c *Channel) Rollback(user string) error {
	if c.Mode() != "binary" {
		return e("not_enabled", 400)
	}
	if c.Busy() {
		return e("busy", 409)
	}
	exe, err := ExecutablePath()
	if err != nil {
		return ioErr(err)
	}
	oldPath := exe + ".old"
	if _, err := os.Stat(oldPath); err != nil {
		return e("no_previous", 400)
	}
	c.writeBinaryStatus("running", "Rueckfall", "Vorheriges Binary wird aktiviert", "")
	tmp := exe + ".rollback"
	os.Remove(tmp)
	if err := os.Rename(exe, tmp); err != nil {
		c.writeBinaryStatus("error", "Rueckfall", err.Error(), "")
		return ioErr(err)
	}
	if err := os.Rename(oldPath, exe); err != nil {
		_ = os.Rename(tmp, exe)
		c.writeBinaryStatus("error", "Rueckfall", err.Error(), "")
		return ioErr(err)
	}
	_ = os.Rename(tmp, oldPath)
	c.logf("Rueckfall auf vorheriges Binary durch %s", user)
	c.restart("Vorheriger Stand wiederhergestellt", "")
	return nil
}

func (c *Channel) restart(msg, ver string) {
	if RestartFunc == nil {
		c.writeBinaryStatus("ok", "Neustart", msg+" - Neustart des Dienstes erforderlich", ver)
		return
	}
	c.writeBinaryStatus("running", "Neustart", msg+", Dienst startet neu", ver)
	go func() {
		time.Sleep(1500 * time.Millisecond)
		RestartFunc()
	}()
}

// MarkStarted schreibt nach einem Neustart den Stand "ok" (vom Server beim Start aufgerufen).
func (c *Channel) MarkStarted() {
	if c.Mode() != "binary" {
		return
	}
	s := binaryStatus(c.Cfg)
	if s.State == "running" {
		msg := "Neustart abgeschlossen, laufende Version v" + c.Cfg.Version
		if c.Cfg.Deployed != "" {
			msg += " (Build " + c.Cfg.Deployed + ")"
		}
		c.writeBinaryStatus("ok", s.Step, msg, c.Cfg.Version)
	}
}

func (c *Channel) writeBinaryStatus(state, step, msg, ver string) {
	prev := false
	if exe, err := ExecutablePath(); err == nil {
		if _, err := os.Stat(exe + ".old"); err == nil {
			prev = true
		}
	}
	if ver == "" {
		ver = c.Cfg.Version
	}
	s := Status{State: state, Step: step, Message: msg, Job: step, Image: "luk (Binary)", ImageVersion: ver, PreviousAvailable: prev, At: time.Now().Format(time.RFC3339)}
	b, _ := json.Marshal(s)
	_ = os.MkdirAll(c.Cfg.DataDir, 0o750)
	tmp := filepath.Join(c.Cfg.DataDir, "update_status.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err == nil {
		_ = os.Rename(tmp, filepath.Join(c.Cfg.DataDir, "update_status.json"))
	}
	c.logf("[%s] %s: %s", state, step, msg)
}

func (c *Channel) logf(format string, a ...any) {
	line := time.Now().Format("2006-01-02 15:04:05") + " " + fmt.Sprintf(format, a...)
	f, err := os.OpenFile(filepath.Join(c.Cfg.DataDir, "update.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Print("update: " + line)
		return
	}
	f.WriteString(line + "\n")
	f.Close()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := out.ReadFrom(in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
