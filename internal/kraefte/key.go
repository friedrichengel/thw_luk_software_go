package kraefte

import (
	"regexp"
	"strconv"
	"unicode/utf8"
)

// KraftKeyMax ist die Hoechstlaenge eines kraft_key.
const KraftKeyMax = 80

var reKey = regexp.MustCompile(`^([a-z0-9-]+):([a-z0-9]+)(?:#(\d))?$`)

// ParsedKey ist ein zerlegter kraft_key "<ov>:<typ>[#nr]".
type ParsedKey struct {
	OV  string
	Typ string
	Nr  *int
}

// ParseKey zerlegt einen kraft_key; nil bei ungueltigem Format.
func ParseKey(key string) *ParsedKey {
	m := reKey.FindStringSubmatch(key)
	if m == nil {
		return nil
	}
	p := &ParsedKey{OV: m[1], Typ: m[2]}
	if m[3] != "" {
		n, _ := strconv.Atoi(m[3])
		p.Nr = &n
	}
	return p
}

// ValidKey: leer oder gueltiges Format bis KraftKeyMax Zeichen.
func ValidKey(key string) bool {
	return key == "" || (utf8.RuneCountInString(key) <= KraftKeyMax && ParseKey(key) != nil)
}
