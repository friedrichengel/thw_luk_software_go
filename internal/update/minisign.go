package update

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// minisign-Format (https://jedisct1.github.io/minisign/):
//
//	Public Key:  base64("Ed" | key_id(8) | pk(32))  - zweite Zeile von minisign.pub
//	Signatur:    untrusted comment: ...
//	             base64(sigalg(2) | key_id(8) | sig(64))      sigalg "Ed" = ueber die Nachricht,
//	                                                          "ED" = ueber blake2b-512(Nachricht)
//	             trusted comment: ...
//	             base64(global_sig(64))  = ed25519 ueber sig(64) | trusted_comment

var ErrSignature = errors.New("Signatur ungueltig")

type minisignPubKey struct {
	keyID []byte
	key   ed25519.PublicKey
}

func parseMinisignPubKey(s string) (*minisignPubKey, error) {
	s = strings.TrimSpace(s)
	// erlaubt auch den ganzen Inhalt von minisign.pub
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "untrusted comment:") {
			continue
		}
		s = line
		break
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(raw) != 42 || string(raw[:2]) != "Ed" {
		return nil, errors.New("Public Key hat kein minisign-Format")
	}
	return &minisignPubKey{keyID: raw[2:10], key: ed25519.PublicKey(raw[10:42])}, nil
}

// VerifyMinisign prueft eine minisign-Signaturdatei (Inhalt von .minisig)
// ueber message mit dem Public Key (zweite Zeile von minisign.pub).
func VerifyMinisign(pubKeyB64 string, message []byte, sigFile string) error {
	pk, err := parseMinisignPubKey(pubKeyB64)
	if err != nil {
		return err
	}
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(sigFile, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimRight(l, " \t"))
		}
	}
	if len(lines) < 2 {
		return errors.New("Signaturdatei unvollstaendig")
	}
	if !strings.HasPrefix(lines[0], "untrusted comment:") {
		return errors.New("Signaturdatei hat kein minisign-Format")
	}
	sigRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(sigRaw) != 74 {
		return errors.New("Signatur hat kein minisign-Format")
	}
	alg := string(sigRaw[:2])
	keyID := sigRaw[2:10]
	sig := sigRaw[10:74]
	if !bytes.Equal(keyID, pk.keyID) {
		return errors.New("Signatur stammt von einem anderen Schluessel")
	}
	var signed []byte
	switch alg {
	case "Ed":
		signed = message
	case "ED":
		h := blake2b.Sum512(message)
		signed = h[:]
	default:
		return errors.New("unbekannter Signaturalgorithmus")
	}
	if !ed25519.Verify(pk.key, signed, sig) {
		return ErrSignature
	}
	// Trusted comment + globale Signatur (optional, aber wenn vorhanden pruefen)
	if len(lines) >= 4 && strings.HasPrefix(lines[2], "trusted comment:") {
		tc := strings.TrimPrefix(lines[2], "trusted comment:")
		tc = strings.TrimPrefix(tc, " ")
		gsig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[3]))
		if err != nil || len(gsig) != 64 {
			return errors.New("globale Signatur hat kein minisign-Format")
		}
		if !ed25519.Verify(pk.key, append(append([]byte{}, sig...), []byte(tc)...), gsig) {
			return errors.New("globale Signatur (trusted comment) ungueltig")
		}
	}
	return nil
}

// SignMinisign erzeugt eine Signaturdatei im minisign-Format (fuer Tests und
// scripts; prehashed = "ED").
func SignMinisign(priv ed25519.PrivateKey, keyID []byte, message []byte, trustedComment string, prehashed bool) string {
	alg := "Ed"
	signed := message
	if prehashed {
		alg = "ED"
		h := blake2b.Sum512(message)
		signed = h[:]
	}
	sig := ed25519.Sign(priv, signed)
	raw := append([]byte(alg), keyID...)
	raw = append(raw, sig...)
	gsig := ed25519.Sign(priv, append(append([]byte{}, sig...), []byte(trustedComment)...))
	return "untrusted comment: signature from minisign secret key\n" +
		base64.StdEncoding.EncodeToString(raw) + "\n" +
		"trusted comment: " + trustedComment + "\n" +
		base64.StdEncoding.EncodeToString(gsig) + "\n"
}

// MinisignPubKeyLine baut die Public-Key-Zeile (zweite Zeile von minisign.pub).
func MinisignPubKeyLine(pub ed25519.PublicKey, keyID []byte) string {
	raw := append([]byte("Ed"), keyID...)
	raw = append(raw, pub...)
	return base64.StdEncoding.EncodeToString(raw)
}
