package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/uuid"
)

// Gemeinsamer Chunk-Upload fuer beide Betriebsarten. Der Upload landet in
// einem tmp-Verzeichnis (<UPDATE_DIR>/tmp bzw. <DataDir>/update_tmp) als
// <id>.part mit <id>.json als Metadaten; FinishUpload prueft Groesse und
// SHA-256 und uebergibt je nach Modus an inbox/ (Docker) oder an die
// Binary-Staging-Logik (binary.go).

var (
	reArchiveName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,120}\.tar(\.gz)?$`)
	reBinaryName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,120}$`)
	reUploadID    = regexp.MustCompile(`^[a-f0-9]{16}$`)
	reSHA256      = regexp.MustCompile(`^[a-f0-9]{64}$`)
	reVersionNum  = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)
)

type uploadMeta struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Received int64  `json:"received"`
	Chunks   int64  `json:"chunks"`
	By       string `json:"by"`
	Started  string `json:"started"`
}

func e(code string, status int) *apierr.Error { return apierr.New(code, status) }

func ioErr(err error) error {
	if err == nil {
		return nil
	}
	return apierr.WithExtra("io", 400, map[string]any{"_cause": err.Error()})
}

func (c *Channel) tmpDir() string {
	if c.Mode() == "binary" {
		return filepath.Join(c.Cfg.DataDir, "update_tmp")
	}
	return c.Dir("tmp")
}

func (c *Channel) requireEnabled() error {
	if !c.Enabled() {
		return e("not_enabled", 400)
	}
	return nil
}

// Busy: laeuft gerade ein Auftrag oder wartet einer?
func (c *Channel) Busy() bool {
	if c.Mode() == "docker" && len(c.Pending()) > 0 {
		return true
	}
	s := c.StatusInfo()
	if s.State != "running" {
		return false
	}
	// Haengender Status (Host-Dienst abgestuerzt): nach 30 Minuten nicht mehr als "laeuft" werten.
	at, _ := s.At.(string)
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return false
	}
	return time.Since(t) < 30*time.Minute
}

// BeginUpload eroeffnet einen Chunk-Upload und liefert die Upload-ID.
func (c *Channel) BeginUpload(name string, size int64, sha string, user string) (string, error) {
	if err := c.requireEnabled(); err != nil {
		return "", err
	}
	nameRe := reArchiveName
	if c.Mode() == "binary" {
		nameRe = reBinaryName
	}
	if !nameRe.MatchString(name) {
		return "", e("bad_name", 400)
	}
	if size <= 0 || size > int64(c.Cfg.UpdateMaxMB)*1024*1024 {
		return "", e("too_large", 413)
	}
	if !reSHA256.MatchString(sha) {
		return "", e("bad_sha256", 400)
	}
	if c.Busy() {
		return "", e("busy", 409)
	}
	if err := os.MkdirAll(c.tmpDir(), 0o750); err != nil {
		return "", ioErr(err)
	}
	c.cleanupStale()
	id := uuid.RandomHex(8)
	meta := uploadMeta{Name: name, Size: size, SHA256: sha, By: user, Started: time.Now().Format(time.RFC3339)}
	if err := os.WriteFile(filepath.Join(c.tmpDir(), id+".part"), nil, 0o640); err != nil {
		return "", ioErr(err)
	}
	if err := c.writeMeta(id, &meta); err != nil {
		return "", err
	}
	return id, nil
}

// AppendChunk haengt einen Chunk an; index muss der naechste erwartete sein.
func (c *Channel) AppendChunk(id string, index int, r io.Reader, length int64) (received, chunks int64, err error) {
	meta, err := c.readMeta(id)
	if err != nil {
		return 0, 0, err
	}
	if int64(index) != meta.Chunks {
		return 0, 0, e("bad_index", 400)
	}
	if length <= 0 || length > ChunkMaxBytes {
		return 0, 0, e("bad_chunk", 400)
	}
	if meta.Received+length > meta.Size {
		return 0, 0, e("too_large", 413)
	}
	f, err := os.OpenFile(filepath.Join(c.tmpDir(), id+".part"), os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return 0, 0, ioErr(err)
	}
	n, err := io.Copy(f, io.LimitReader(r, length))
	cerr := f.Close()
	if err != nil || cerr != nil || n != length {
		return 0, 0, e("io", 400)
	}
	meta.Received += length
	meta.Chunks = int64(index) + 1
	if err := c.writeMeta(id, meta); err != nil {
		return 0, 0, err
	}
	return meta.Received, meta.Chunks, nil
}

// FinishUpload prueft Groesse und Pruefsumme. Docker: Archivkopf pruefen und
// samt Sidecars nach inbox/ verschieben. Binary: Signatur pruefen (wenn
// UPDATE_PUBKEY gesetzt) und als <exe>.new bereitstellen.
func (c *Channel) FinishUpload(id, minisig, user string) (string, error) {
	meta, err := c.readMeta(id)
	if err != nil {
		return "", err
	}
	part := filepath.Join(c.tmpDir(), id+".part")
	st, err := os.Stat(part)
	if err != nil || meta.Received != meta.Size || st.Size() != meta.Size {
		return "", e("incomplete", 400)
	}
	sum, err := fileSHA256(part)
	if err != nil {
		return "", ioErr(err)
	}
	if sum != meta.SHA256 {
		c.CancelUpload(id)
		return "", e("sha256_mismatch", 400)
	}
	head := make([]byte, 512)
	fh, err := os.Open(part)
	if err != nil {
		return "", ioErr(err)
	}
	n, _ := io.ReadFull(fh, head)
	fh.Close()
	head = head[:n]

	if c.Mode() == "binary" {
		return c.finishBinary(id, meta, part, head, minisig, user)
	}

	isGzip := len(head) >= 2 && head[0] == 0x1f && head[1] == 0x8b
	isTar := len(head) >= 262 && string(head[257:262]) == "ustar"
	if !isGzip && !isTar {
		c.CancelUpload(id)
		return "", e("not_an_archive", 400)
	}
	name := meta.Name
	if isGzip && !strings.HasSuffix(name, ".gz") {
		name += ".gz"
	}
	if !isGzip && strings.HasSuffix(name, ".gz") {
		name = strings.TrimSuffix(name, ".gz")
	}
	inbox := c.Dir("inbox")
	if _, err := os.Stat(filepath.Join(inbox, name)); err == nil {
		return "", e("busy", 409)
	}
	// Sidecars zuerst, Archiv zuletzt (der Host-Dienst reagiert auf "inbox nicht leer").
	if err := c.placeAtomic(name+".sha256", sum+"  "+name+"\n"); err != nil {
		return "", err
	}
	if minisig != "" {
		if err := c.placeAtomic(name+".minisig", strings.TrimRight(minisig, "\r\n")+"\n"); err != nil {
			return "", err
		}
	}
	metaJSON, _ := json.Marshal(map[string]any{"by": user, "at": time.Now().Format(time.RFC3339), "size": meta.Size})
	if err := c.placeAtomic(name+".meta", string(metaJSON)); err != nil {
		return "", err
	}
	if err := os.Rename(part, filepath.Join(inbox, name)); err != nil {
		os.Remove(filepath.Join(inbox, name+".sha256"))
		os.Remove(filepath.Join(inbox, name+".minisig"))
		os.Remove(filepath.Join(inbox, name+".meta"))
		return "", ioErr(err)
	}
	os.Remove(filepath.Join(c.tmpDir(), id+".json"))
	return name, nil
}

// CancelUpload verwirft einen laufenden Upload.
func (c *Channel) CancelUpload(id string) {
	if !reUploadID.MatchString(id) {
		return
	}
	os.Remove(filepath.Join(c.tmpDir(), id+".part"))
	os.Remove(filepath.Join(c.tmpDir(), id+".json"))
}

// Trigger legt pull.trigger bzw. rollback.trigger atomar in inbox/ ab (Docker).
// Im Binary-Modus loest rollback den Tausch mit <exe>.old aus; pull gibt es nicht.
func (c *Channel) Trigger(kind, user string) error {
	if kind != "pull" && kind != "rollback" {
		return errors.New("kind")
	}
	if err := c.requireEnabled(); err != nil {
		return err
	}
	if c.Mode() == "binary" {
		if kind == "pull" {
			return e("not_enabled", 400)
		}
		return c.Rollback(user)
	}
	if c.Busy() {
		return e("busy", 409)
	}
	payload, _ := json.Marshal(map[string]any{"by": user, "at": time.Now().Format(time.RFC3339)})
	return c.placeAtomic(kind+".trigger", string(payload))
}

// DiscardPending entfernt wartende Eintraege aus inbox/ (Docker) bzw. die
// bereitgestellte neue Datei (Binary).
func (c *Channel) DiscardPending() (int, error) {
	if err := c.requireEnabled(); err != nil {
		return 0, err
	}
	if c.Mode() == "binary" {
		return c.discardStaged(), nil
	}
	n := 0
	entries, err := os.ReadDir(c.Dir("inbox"))
	if err != nil {
		return 0, ioErr(err)
	}
	for _, en := range entries {
		if en.IsDir() {
			continue
		}
		if os.Remove(filepath.Join(c.Dir("inbox"), en.Name())) == nil {
			n++
		}
	}
	return n, nil
}

// --- intern ------------------------------------------------------------

func (c *Channel) placeAtomic(name, content string) error {
	if err := os.MkdirAll(c.tmpDir(), 0o750); err != nil {
		return ioErr(err)
	}
	tmp := filepath.Join(c.tmpDir(), uuid.RandomHex(4)+".tmp")
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return ioErr(err)
	}
	if err := os.Rename(tmp, filepath.Join(c.Dir("inbox"), name)); err != nil {
		os.Remove(tmp)
		return ioErr(err)
	}
	return nil
}

func (c *Channel) readMeta(id string) (*uploadMeta, error) {
	if !reUploadID.MatchString(id) {
		return nil, e("bad_id", 400)
	}
	b, err := os.ReadFile(filepath.Join(c.tmpDir(), id+".json"))
	if err != nil {
		return nil, e("unknown_upload", 400)
	}
	var m uploadMeta
	if json.Unmarshal(b, &m) != nil || m.Name == "" || m.Size == 0 || m.SHA256 == "" {
		return nil, e("unknown_upload", 400)
	}
	return &m, nil
}

func (c *Channel) writeMeta(id string, m *uploadMeta) error {
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(c.tmpDir(), id+".json"), b, 0o640); err != nil {
		return ioErr(err)
	}
	return nil
}

// cleanupStale raeumt abgebrochene Uploads (aelter als 6 h) auf.
func (c *Channel) cleanupStale() {
	entries, err := os.ReadDir(c.tmpDir())
	if err != nil {
		return
	}
	for _, en := range entries {
		if en.IsDir() {
			continue
		}
		if st, err := en.Info(); err == nil && time.Since(st.ModTime()) > 6*time.Hour {
			os.Remove(filepath.Join(c.tmpDir(), en.Name()))
		}
	}
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func versionFromName(name string) string {
	return reVersionNum.FindString(name)
}
