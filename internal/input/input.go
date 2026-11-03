// Package input: Helfer zum Lesen von JSON-/Formularwerten (map[string]any)
// mit Laengenbegrenzung und Typ-Toleranz, wie Api::str / Api::int in PHP.
package input

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/uuid"
)

var reInt = regexp.MustCompile(`^-?\d+$`)

// Str: getrimmter Text, auf max Zeichen (Runen) gekuerzt; Arrays/Objekte -> "".
func Str(v any, max int) string {
	return Trunc(strings.TrimSpace(Raw(v)), max)
}

// StrRaw: Text ohne Trim, auf max Zeichen gekuerzt.
func StrRaw(v any, max int) string {
	return Trunc(Raw(v), max)
}

// Raw wandelt Skalare in Text (bool -> "1"/"", Zahlen wie PHP).
func Raw(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "1"
		}
		return ""
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	default:
		return ""
	}
}

// Trunc kuerzt auf n Runen.
func Trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// Int: Ganzzahl aus Zahl oder numerischem Text, sonst def.
func Int(v any, def int) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	case string:
		if reInt.MatchString(x) {
			n, err := strconv.Atoi(x)
			if err == nil {
				return n
			}
		}
	}
	return def
}

// Int64 wie Int fuer IDs.
func Int64(v any, def int64) int64 {
	return int64(Int(v, int(def)))
}

// Version prueft eine Versionsnummer (>= 0), sonst missing_version.
func Version(v any) (int, error) {
	n := Int(v, -1)
	if n < 0 {
		return 0, apierr.New("missing_version", 400)
	}
	return n, nil
}

// IsNumeric meldet, ob v eine Zahl oder ein numerischer Text ist.
func IsNumeric(v any) bool {
	switch x := v.(type) {
	case float64, int, int64:
		return true
	case string:
		_, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return err == nil && strings.TrimSpace(x) != ""
	}
	return false
}

// Float liest eine Zahl (0 wenn nicht numerisch).
func Float(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	}
	return 0
}

// Truthy wie PHP !empty($v) && $v !== 'false' && $v !== '0'.
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	case string:
		return x != "" && x != "0" && x != "false"
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// Empty wie PHP empty().
func Empty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case float64:
		return x == 0
	case int:
		return x == 0
	case int64:
		return x == 0
	case string:
		return x == "" || x == "0"
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// IDOrNew: client-generierte oder serverseitige ID.
func IDOrNew(v any) string {
	s := ""
	if x, ok := v.(string); ok {
		s = strings.TrimSpace(x)
	}
	if s != "" && uuid.ValidClientID(s) {
		return strings.ToLower(s)
	}
	return uuid.V4()
}

// RequireID verlangt eine gueltige Client-ID.
func RequireID(v any) (string, error) {
	s := ""
	if x, ok := v.(string); ok {
		s = strings.TrimSpace(x)
	}
	if !uuid.ValidClientID(s) {
		return "", apierr.New("invalid_id", 400)
	}
	return s, nil
}

// Has meldet, ob der Schluessel im Objekt vorkommt (auch mit null).
func Has(m map[string]any, k string) bool {
	_, ok := m[k]
	return ok
}

// Map liefert ein verschachteltes Objekt oder nil.
func Map(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// List liefert ein Array oder nil.
func List(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}
