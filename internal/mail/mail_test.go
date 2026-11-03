package mail

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/friedrichengel/thw_luk_software_go/internal/config"
)

func TestEncodeHeader(t *testing.T) {
	if EncodeHeader("Plain Text") != "Plain Text" {
		t.Fatal("ASCII darf nicht kodiert werden")
	}
	got := EncodeHeader("Grüße")
	if !strings.HasPrefix(got, "=?UTF-8?B?") || !strings.HasSuffix(got, "?=") {
		t.Fatalf("encoded: %q", got)
	}
	dec, _ := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(got, "=?UTF-8?B?"), "?="))
	if string(dec) != "Grüße" {
		t.Fatalf("roundtrip: %q", dec)
	}
}

func TestBuildMessage(t *testing.T) {
	cfg := &config.Config{BaseURL: "https://luk.example.org", MailFrom: "no-reply@example.org", MailFromName: "THW LuK Stabsansicht"}
	msg := BuildMessage(cfg, "a@example.org", "Test äöü", "Zeile 1\r\nZeile 2\rZeile 3")
	head, body, ok := strings.Cut(msg, "\r\n\r\n")
	if !ok {
		t.Fatal("kein Header/Body-Trenner")
	}
	for _, want := range []string{"From: THW LuK Stabsansicht <no-reply@example.org>", "To: <a@example.org>", "Subject: =?UTF-8?B?", "@luk.example.org>", "Content-Transfer-Encoding: base64", "MIME-Version: 1.0"} {
		if !strings.Contains(head, want) {
			t.Errorf("Header fehlt: %s\n%s", want, head)
		}
	}
	for _, line := range strings.Split(body, "\r\n") {
		if len(line) > 76 {
			t.Fatalf("Zeile zu lang: %d", len(line))
		}
	}
	dec, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body, "\r\n", ""))
	if err != nil || string(dec) != "Zeile 1\nZeile 2\nZeile 3" {
		t.Fatalf("Body: %q %v", dec, err)
	}
}

func TestValidAddress(t *testing.T) {
	if !ValidAddress("a.b@example.org") || ValidAddress("a@b") || ValidAddress("a@b.c\r\nX: y") || ValidAddress("kein-at.de") {
		t.Fatal("ValidAddress")
	}
}

func TestSendWithoutTransport(t *testing.T) {
	cfg := &config.Config{BaseURL: "http://localhost", MailFrom: "x@example.org", MailFromName: "X"}
	if err := Send(cfg, "ungueltig", "s", "b"); err == nil {
		t.Fatal("ungueltige Adresse muss scheitern")
	}
}
