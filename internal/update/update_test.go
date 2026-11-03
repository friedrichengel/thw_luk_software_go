package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/config"
)

func dockerChannel(t *testing.T) *Channel {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"inbox", "tmp", "archive"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{UpdateDir: dir, UpdateMode: "docker", Image: "ghcr.io/example/luk:latest", UpdateMaxMB: 500, DataDir: t.TempDir(), Version: "9.9.9"}
	return New(cfg)
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func wantCode(t *testing.T, err error, code string, msg string) {
	t.Helper()
	if !apierr.Is(err, code) {
		t.Fatalf("%s: erwartet %s, bekommen %v", msg, code, err)
	}
}

func TestDockerChannel(t *testing.T) {
	c := dockerChannel(t)
	if !c.Enabled() || c.Mode() != "docker" || c.Info()["image"] != "ghcr.io/example/luk:latest" {
		t.Fatal("UpdateChannel nicht aktiv")
	}
	blob := append([]byte{0x1f, 0x8b}, make([]byte, 3000)...)
	rand.Read(blob[2:])
	id, err := c.BeginUpload("luk-image-9.9.9.tar.gz", int64(len(blob)), sha(blob), "Tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.AppendChunk(id, 0, bytes.NewReader(blob[:2000]), 2000); err != nil {
		t.Fatal(err)
	}
	_, _, err = c.AppendChunk(id, 0, bytes.NewReader(blob[:2000]), 2000)
	wantCode(t, err, "bad_index", "Chunk doppelt")
	_, err = c.FinishUpload(id, "", "Tester")
	wantCode(t, err, "incomplete", "unvollstaendig")
	if _, _, err := c.AppendChunk(id, 1, bytes.NewReader(blob[2000:]), int64(len(blob)-2000)); err != nil {
		t.Fatal(err)
	}
	name, err := c.FinishUpload(id, "untrusted comment: test\nSIG\n", "Tester")
	if err != nil || name != "luk-image-9.9.9.tar.gz" {
		t.Fatalf("finish: %v %s", err, name)
	}
	inbox := c.Dir("inbox")
	got, _ := os.ReadFile(filepath.Join(inbox, name))
	if !bytes.Equal(got, blob) {
		t.Fatal("Inhalt in inbox/ weicht ab")
	}
	sc, _ := os.ReadFile(filepath.Join(inbox, name+".sha256"))
	if strings.TrimSpace(string(sc)) != sha(blob)+"  "+name {
		t.Fatalf("sha256-Sidecar: %q", sc)
	}
	if _, err := os.Stat(filepath.Join(inbox, name+".minisig")); err != nil {
		t.Fatal("minisig fehlt")
	}
	var meta map[string]any
	mb, _ := os.ReadFile(filepath.Join(inbox, name+".meta"))
	if json.Unmarshal(mb, &meta) != nil || meta["by"] != "Tester" {
		t.Fatalf("meta: %s", mb)
	}
	if len(c.Pending()) != 1 || !c.Busy() {
		t.Fatal("inbox belegt -> busy erwartet")
	}
	wantCode(t, c.Trigger("pull", "Tester"), "busy", "Trigger bei busy")
	n, err := c.DiscardPending()
	if err != nil || n != 4 || c.Busy() {
		t.Fatalf("discard: %v %d busy=%v", err, n, c.Busy())
	}
	if err := c.Trigger("rollback", "Tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(inbox, "rollback.trigger")); err != nil {
		t.Fatal("rollback.trigger fehlt")
	}
	c.DiscardPending()

	id2, err := c.BeginUpload("x.tar.gz", 10, strings.Repeat("a", 64), "Tester")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.AppendChunk(id2, 0, strings.NewReader("plaintext!"), 10); err != nil {
		t.Fatal(err)
	}
	_, err = c.FinishUpload(id2, "", "Tester")
	wantCode(t, err, "sha256_mismatch", "falsche Pruefsumme")
	if _, err := os.Stat(filepath.Join(c.Dir("tmp"), id2+".part")); err == nil {
		t.Fatal("Rest nach sha256_mismatch nicht geloescht")
	}
	_, err = c.BeginUpload("../evil.tar.gz", 10, strings.Repeat("a", 64), "Tester")
	wantCode(t, err, "bad_name", "Dateiname")
	_, err = c.BeginUpload("x.tar", 600*1024*1024, strings.Repeat("a", 64), "Tester")
	wantCode(t, err, "too_large", "zu gross")

	// kein Archivkopf
	plain := []byte("just text, no archive header, no gzip")
	id3, _ := c.BeginUpload("y.tar.gz", int64(len(plain)), sha(plain), "Tester")
	c.AppendChunk(id3, 0, bytes.NewReader(plain), int64(len(plain)))
	_, err = c.FinishUpload(id3, "", "Tester")
	wantCode(t, err, "not_an_archive", "kein Archiv")
}

func TestMinisign(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyID := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	pubLine := MinisignPubKeyLine(pub, keyID)
	msg := []byte("hello luk binary")
	for _, pre := range []bool{false, true} {
		sig := SignMinisign(priv, keyID, msg, "luk-image 9.9.9 abc", pre)
		if err := VerifyMinisign(pubLine, msg, sig); err != nil {
			t.Fatalf("prehashed=%v: %v", pre, err)
		}
		if err := VerifyMinisign("untrusted comment: minisign public key\n"+pubLine+"\n", msg, sig); err != nil {
			t.Fatalf("pub mit Kommentarzeile: %v", err)
		}
		if err := VerifyMinisign(pubLine, append(msg, 'x'), sig); err == nil {
			t.Fatal("manipulierte Nachricht akzeptiert")
		}
		// manipulierter trusted comment
		bad := strings.Replace(sig, "trusted comment: luk-image", "trusted comment: evil", 1)
		if err := VerifyMinisign(pubLine, msg, bad); err == nil {
			t.Fatal("manipulierter trusted comment akzeptiert")
		}
	}
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	sig := SignMinisign(priv, keyID, msg, "t", true)
	if err := VerifyMinisign(MinisignPubKeyLine(otherPub, keyID), msg, sig); err == nil {
		t.Fatal("fremder Schluessel akzeptiert")
	}
	if err := VerifyMinisign(MinisignPubKeyLine(pub, []byte{9, 9, 9, 9, 9, 9, 9, 9}), msg, sig); err == nil {
		t.Fatal("andere key id akzeptiert")
	}
}

func TestBinaryStaging(t *testing.T) {
	data := t.TempDir()
	exeDir := t.TempDir()
	fakeExe := filepath.Join(exeDir, "luk")
	os.WriteFile(fakeExe, []byte("old"), 0o755)
	orig := ExecutablePath
	ExecutablePath = func() (string, error) { return fakeExe, nil }
	t.Cleanup(func() { ExecutablePath = orig })

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	keyID := []byte{8, 7, 6, 5, 4, 3, 2, 1}
	cfg := &config.Config{UpdateMode: "binary", DataDir: data, UpdateMaxMB: 500, Version: "3.0.0", UpdatePubKey: MinisignPubKeyLine(pub, keyID)}
	c := New(cfg)
	if c.Mode() != "binary" || !c.Enabled() {
		t.Fatal("binary mode")
	}
	bin := append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 500)...)
	rand.Read(bin[4:])
	// ohne Signatur -> abgelehnt
	id, err := c.BeginUpload("luk-3.0.1-linux-amd64", int64(len(bin)), sha(bin), "Admin")
	if err != nil {
		t.Fatal(err)
	}
	c.AppendChunk(id, 0, bytes.NewReader(bin), int64(len(bin)))
	_, err = c.FinishUpload(id, "", "Admin")
	wantCode(t, err, "signature_missing", "Signatur Pflicht")
	// falsche Signatur
	id, _ = c.BeginUpload("luk-3.0.1-linux-amd64", int64(len(bin)), sha(bin), "Admin")
	c.AppendChunk(id, 0, bytes.NewReader(bin), int64(len(bin)))
	_, err = c.FinishUpload(id, SignMinisign(priv, keyID, []byte("other"), "x", true), "Admin")
	wantCode(t, err, "signature_invalid", "falsche Signatur")
	// gueltig
	id, _ = c.BeginUpload("luk-3.0.1-linux-amd64", int64(len(bin)), sha(bin), "Admin")
	c.AppendChunk(id, 0, bytes.NewReader(bin), int64(len(bin)))
	name, err := c.FinishUpload(id, SignMinisign(priv, keyID, bin, "luk 3.0.1", true), "Admin")
	if err != nil || name != "luk-3.0.1-linux-amd64" {
		t.Fatalf("finish binary: %v", err)
	}
	staged, err := os.ReadFile(fakeExe + ".new")
	if err != nil || !bytes.Equal(staged, bin) {
		t.Fatal(".new fehlt oder falsch")
	}
	p := c.Pending()
	if len(p) != 1 || p[0].Name != name || p[0].Size != int64(len(bin)) {
		t.Fatalf("pending: %+v", p)
	}
	st := c.StatusInfo()
	if st.State != "idle" || st.ImageVersion != "3.0.1" {
		t.Fatalf("status: %+v", st)
	}
	if c.Info()["mode"] != "binary" || !strings.Contains(c.LogTail(10), "bereitgestellt") {
		t.Fatal("info/log")
	}
	// kein Binary-Kopf
	txt := []byte("#!/bin/sh\necho hi\n")
	id, _ = c.BeginUpload("luk.sh", int64(len(txt)), sha(txt), "Admin")
	c.AppendChunk(id, 0, bytes.NewReader(txt), int64(len(txt)))
	_, err = c.FinishUpload(id, SignMinisign(priv, keyID, txt, "t", true), "Admin")
	wantCode(t, err, "not_a_binary", "kein Binary")
	// Rollback ohne .old
	wantCode(t, c.Rollback("Admin"), "no_previous", "kein .old")
	n, _ := c.DiscardPending()
	if n != 2 || len(c.Pending()) != 0 {
		t.Fatalf("discard staged: %d", n)
	}
	// Pull gibt es im Binary-Modus nicht
	wantCode(t, c.Trigger("pull", "Admin"), "not_enabled", "pull im Binary-Modus")
}
