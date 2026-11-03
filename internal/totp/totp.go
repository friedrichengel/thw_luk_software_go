// Package totp: zeitbasierte Einmalpasswoerter (RFC 6238) mit Base32-Secrets,
// otpauth-URI fuer Authenticator-Apps und Replay-Schutz ueber den zuletzt
// verbrauchten Zaehler.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	interval = 30
	digits   = 6
)

var (
	enc         = base32.StdEncoding.WithPadding(base32.NoPadding)
	reNotBase32 = regexp.MustCompile(`[^A-Za-z2-7]`)
	reDigits    = regexp.MustCompile(`^\d{6}$`)
	reSpace     = regexp.MustCompile(`\s+`)
)

func GenerateSecret(bytes int) string {
	if bytes < 10 {
		bytes = 10
	}
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return enc.EncodeToString(b)
}

func normalize(secret string) string {
	return strings.ToUpper(reNotBase32.ReplaceAllString(secret, ""))
}

func BuildOtpAuthURI(secret, accountName, issuer string) string {
	label := url.PathEscape(issuer + ":" + accountName)
	return fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=%s&period=%d",
		label, normalize(secret), url.QueryEscape(issuer), interval)
}

// VerifyCode prueft einen Code im Zeitfenster +-1 Intervall.
func VerifyCode(secret, code string) bool {
	_, ok := VerifyCodeReturnCounter(secret, code, 0, 1, time.Now())
	return ok
}

// VerifyCodeReturnCounter liefert den verbrauchten Zaehler. Zaehler <=
// minCounter sind tabu (Replay-Schutz), wenn der letzte verifizierte
// Zaehler persistiert wird.
func VerifyCodeReturnCounter(secret, code string, minCounter int64, window int, now time.Time) (int64, bool) {
	key, err := enc.DecodeString(normalize(secret))
	if err != nil || len(key) == 0 {
		return 0, false
	}
	code = strings.TrimSpace(reSpace.ReplaceAllString(code, ""))
	if !reDigits.MatchString(code) {
		return 0, false
	}
	counter := now.Unix() / interval
	for off := -int64(window); off <= int64(window); off++ {
		cand := counter + off
		if cand < 0 || cand <= minCounter {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(hotp(key, cand)), []byte(code)) == 1 {
			return cand, true
		}
	}
	return 0, false
}

// FormatSecret gruppiert das Secret in Viererbloecke fuer die Anzeige.
func FormatSecret(secret string) string {
	n := normalize(secret)
	var parts []string
	for i := 0; i < len(n); i += 4 {
		end := i + 4
		if end > len(n) {
			end = len(n)
		}
		parts = append(parts, n[i:end])
	}
	return strings.Join(parts, " ")
}

func hotp(key []byte, counter int64) string {
	buf := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		buf[i] = byte(counter & 0xff)
		counter >>= 8
	}
	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	h := mac.Sum(nil)
	off := h[len(h)-1] & 0x0f
	v := (uint32(h[off])&0x7f)<<24 | uint32(h[off+1])<<16 | uint32(h[off+2])<<8 | uint32(h[off+3])
	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, v%mod)
}
