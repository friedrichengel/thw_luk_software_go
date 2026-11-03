// Package mail: Versand von Systemmails (Passwort-Reset) - Port von lib/Mailer.php.
//
// Ist SMTP_HOST gesetzt, wird direkt per SMTP mit Login ueber den
// Mailanbieter versendet (ssl = TLS ab Verbindungsaufbau, meist Port 465;
// tls = STARTTLS, meist Port 587; none = unverschluesselt). Ohne SMTP_HOST
// faellt der Versand auf ein lokales sendmail (Postfix o.ae.) zurueck.
//
// Test: luk mail-test empfaenger@example.org
package mail

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/config"
)

const timeout = 15 * time.Second

var (
	reCRLF  = regexp.MustCompile(`[\r\n]`)
	reEmail = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	reASCII = regexp.MustCompile(`[^\x20-\x7E]`)
)

// ValidAddress prueft eine Empfaengeradresse grob (lokaler Teil @ Domain mit Punkt, kein Whitespace).
func ValidAddress(addr string) bool {
	return reEmail.MatchString(addr) && !reCRLF.MatchString(addr)
}

// Send versendet eine Textmail an to.
func Send(cfg *config.Config, to, subject, body string) error {
	if !ValidAddress(to) {
		return errors.New("Ungueltige Empfaengeradresse.")
	}
	if reCRLF.MatchString(to) || reCRLF.MatchString(subject) {
		return errors.New("Zeilenumbruch in Adresse oder Betreff.")
	}
	msg := BuildMessage(cfg, to, subject, body)
	if cfg.SMTPHost != "" {
		return sendSMTP(cfg, to, msg)
	}
	return sendSendmail(cfg, to, msg)
}

// EncodeHeader kodiert Nicht-ASCII-Headerwerte als =?UTF-8?B?...?=.
func EncodeHeader(v string) string {
	if reASCII.MatchString(v) {
		return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(v)) + "?="
	}
	return v
}

// BuildMessage baut die komplette Nachricht (Header + base64-Body, CRLF).
func BuildMessage(cfg *config.Config, to, subject, body string) string {
	domain := cfg.Host()
	idBytes := make([]byte, 16)
	_, _ = rand.Read(idBytes)
	headers := []string{
		"Date: " + time.Now().Format(time.RFC1123Z),
		"From: " + fmt.Sprintf("%s <%s>", EncodeHeader(cfg.MailFromName), cfg.MailFrom),
		"To: <" + to + ">",
		"Subject: " + EncodeHeader(subject),
		"Message-ID: <" + hex.EncodeToString(idBytes) + "@" + domain + ">",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: base64",
	}
	normalized := strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	enc := base64.StdEncoding.EncodeToString([]byte(normalized))
	var lines []string
	for i := 0; i < len(enc); i += 76 {
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		lines = append(lines, enc[i:end])
	}
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + strings.Join(lines, "\r\n")
}

// loginAuth implementiert AUTH LOGIN (fuer Server ohne PLAIN).
type loginAuth struct {
	user, pass string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !isLocal(server.Name) {
		return "", nil, errors.New("AUTH LOGIN nur ueber TLS.")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(strings.TrimSpace(string(fromServer)))
	switch {
	case strings.HasPrefix(prompt, "username"):
		return []byte(a.user), nil
	case strings.HasPrefix(prompt, "password"):
		return []byte(a.pass), nil
	}
	// Manche Server senden base64-kodierte Prompts; erst Benutzer, dann Passwort.
	if a.user != "" {
		u := a.user
		a.user = ""
		return []byte(u), nil
	}
	return []byte(a.pass), nil
}

func isLocal(name string) bool {
	return name == "localhost" || name == "127.0.0.1" || name == "::1"
}

func sendSMTP(cfg *config.Config, to, msg string) error {
	host := cfg.SMTPHost
	addr := net.JoinHostPort(host, fmt.Sprint(cfg.SMTPPort))
	dialer := &net.Dialer{Timeout: timeout}
	var conn net.Conn
	var err error
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if cfg.SMTPSecurity == "ssl" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("Verbindung zu %s fehlgeschlagen: %v", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(4 * timeout))
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("Begruessung: %v", err)
	}
	defer c.Close()
	ehlo := cfg.Host()
	if err := c.Hello(ehlo); err != nil {
		return fmt.Errorf("EHLO abgelehnt: %v", err)
	}
	if cfg.SMTPSecurity == "tls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("Server bietet kein STARTTLS.")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("TLS-Handshake fehlgeschlagen: %v", err)
		}
	}
	if cfg.SMTPUser != "" {
		ok, mechs := c.Extension("AUTH")
		m := strings.ToUpper(mechs)
		var a smtp.Auth
		switch {
		case !ok || strings.Contains(m, "PLAIN"):
			a = smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPass, host)
		case strings.Contains(m, "LOGIN"):
			a = &loginAuth{user: cfg.SMTPUser, pass: cfg.SMTPPass}
		default:
			return errors.New("Server unterstuetzt weder AUTH PLAIN noch LOGIN.")
		}
		if err := c.Auth(a); err != nil {
			return fmt.Errorf("Anmeldung fehlgeschlagen: %v", err)
		}
	}
	if err := c.Mail(cfg.MailFrom); err != nil {
		return fmt.Errorf("MAIL FROM: %v", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %v", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %v", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("DATA: %v", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("DATA-Ende: %v", err)
	}
	_ = c.Quit()
	return nil
}

func sendSendmail(cfg *config.Config, to, msg string) error {
	path, err := exec.LookPath("sendmail")
	if err != nil {
		for _, p := range []string{"/usr/sbin/sendmail", "/usr/lib/sendmail"} {
			if _, e := exec.LookPath(p); e == nil {
				path = p
				err = nil
				break
			}
		}
	}
	if err != nil || path == "" {
		return errors.New("kein SMTP_HOST und kein sendmail - SMTP_HOST in der Konfiguration setzen.")
	}
	cmd := exec.Command(path, "-t", "-i", "-f", cfg.MailFrom)
	cmd.Stdin = strings.NewReader(msg + "\r\n")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sendmail fehlgeschlagen: %v %s", err, strings.TrimSpace(out.String()))
	}
	return nil
}
