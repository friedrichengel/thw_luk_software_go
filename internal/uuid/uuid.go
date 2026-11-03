// Package uuid erzeugt UUIDv4 und prueft client-generierte IDs.
package uuid

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
)

var (
	reValid    = regexp.MustCompile(`^[a-fA-F0-9-]{8,64}$`)
	reClientID = regexp.MustCompile(`^[a-zA-Z0-9_-]{4,64}$`)
)

// V4 liefert eine zufaellige UUID (Version 4).
func V4() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// Valid prueft UUID-artige IDs (auch client-generiert) auf ein sicheres Format.
func Valid(s string) bool {
	return reValid.MatchString(s)
}

// ValidClientID: Client-IDs duerfen nur aus [a-z0-9_-] bestehen (4-64 Zeichen).
func ValidClientID(s string) bool {
	return reClientID.MatchString(s)
}

// RandomHex liefert n Zufallsbytes hex-kodiert (2n Zeichen).
func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
