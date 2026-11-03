// Package apierr definiert den Fehlertyp der JSON-APIs: ein kurzer Code
// (z.B. "conflict", "not_found"), ein HTTP-Status und optionale Zusatzdaten
// (bei conflict die aktuelle Zeile).
package apierr

import "fmt"

type Error struct {
	Code   string
	Status int
	Extra  map[string]any
}

func (e *Error) Error() string { return e.Code }

// New erzeugt einen API-Fehler.
func New(code string, status int) *Error {
	return &Error{Code: code, Status: status}
}

// WithExtra haengt Zusatzdaten an (z.B. {"current": row}).
func WithExtra(code string, status int, extra map[string]any) *Error {
	return &Error{Code: code, Status: status, Extra: extra}
}

// Conflict: 409 mit aktueller Zeile.
func Conflict(current any) *Error {
	return &Error{Code: "conflict", Status: 409, Extra: map[string]any{"current": current}}
}

// Is prueft, ob err ein API-Fehler mit diesem Code ist.
func Is(err error, code string) bool {
	if e, ok := err.(*Error); ok {
		return e.Code == code
	}
	return false
}

// Code liefert den API-Code eines Fehlers oder "" fuer andere Fehler.
func Code(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return ""
}

// Wrap verpackt einen beliebigen Fehler als server_error (Ursache bleibt im Text).
func Wrap(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*Error); ok {
		return err
	}
	return fmt.Errorf("server_error: %w", err)
}
