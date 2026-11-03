package einsatz

import (
	"fmt"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/uuid"
)

// ParseDBTime liest "YYYY-MM-DD HH:MM:SS" (UTC) oder RFC3339; ok=false bei Fehler.
func ParseDBTime(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ShareLink ist ein Freigabelink in der Verwaltung (ohne Token).
type ShareLink struct {
	ID         int64   `json:"id"`
	Label      string  `json:"label"`
	CreatedBy  string  `json:"created_by"`
	CreatedAt  string  `json:"created_at"`
	ExpiresAt  *string `json:"expires_at"`
	RevokedAt  *string `json:"revoked_at"`
	LastUsedAt *string `json:"last_used_at"`
	UseCount   int     `json:"use_count"`
	Active     bool    `json:"active"`
}

func linkActive(revoked, expires *string) bool {
	if revoked != nil && *revoked != "" {
		return false
	}
	if expires == nil || *expires == "" {
		return true
	}
	t, ok := ParseDBTime(*expires)
	return !ok || !t.Before(time.Now())
}

func ShareLinks(q db.Q, einsatzID int64) ([]ShareLink, error) {
	rows, err := db.QueryMaps(q, `SELECT l.*, u.username, u.display_name FROM einsatz_share_links l LEFT JOIN users u ON u.id = l.created_by
		WHERE l.einsatz_id = ? ORDER BY l.id DESC`, einsatzID)
	if err != nil {
		return nil, err
	}
	out := []ShareLink{}
	for _, r := range rows {
		cb := db.Str(r, "display_name")
		if cb == "" {
			cb = db.Str(r, "username")
		}
		out = append(out, ShareLink{
			ID: db.Int(r, "id"), Label: db.Str(r, "label"), CreatedBy: cb, CreatedAt: db.Str(r, "created_at"),
			ExpiresAt: db.StrPtr(r, "expires_at"), RevokedAt: db.StrPtr(r, "revoked_at"), LastUsedAt: db.StrPtr(r, "last_used_at"),
			UseCount: int(db.Int(r, "use_count")), Active: linkActive(db.StrPtr(r, "revoked_at"), db.StrPtr(r, "expires_at")),
		})
	}
	return out, nil
}

type NewShareLink struct {
	ID    int64  `json:"id"`
	Token string `json:"token"`
	URL   string `json:"url"`
}

// CreateShareLink legt einen Freigabelink an (nur der Hash des Tokens wird gespeichert).
func CreateShareLink(q db.Q, c *access.Ctx, secret, baseURL, label string, expiresAt *string) (*NewShareLink, error) {
	einsatzID := c.Einsatz.ID
	token := uuid.RandomHex(24)
	now := db.Now()
	var out *NewShareLink
	err := db.InTx(q, func(q db.Q) error {
		var exp any
		if expiresAt != nil {
			exp = *expiresAt
		}
		res, err := q.Exec("INSERT INTO einsatz_share_links (einsatz_id, token_hash, label, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
			einsatzID, access.HashToken(secret, token), label, c.UID, now, exp)
		if err != nil {
			return err
		}
		id := db.LastID(res)
		summary := "Freigabelink „" + label + "\" erstellt"
		if expiresAt != nil {
			summary += " (gültig bis " + *expiresAt + ")"
		}
		if _, err := access.Event(q, c, "share_link", fmt.Sprint(id), "insert", summary, nil,
			map[string]any{"id": id, "label": label, "expires_at": exp}); err != nil {
			return err
		}
		out = &NewShareLink{ID: id, Token: token, URL: baseURL + "/view?t=" + token}
		return nil
	})
	return out, err
}

func RevokeShareLink(q db.Q, c *access.Ctx, id int64) error {
	einsatzID := c.Einsatz.ID
	row, err := db.QueryMap(q, "SELECT * FROM einsatz_share_links WHERE id = ? AND einsatz_id = ? LIMIT 1", id, einsatzID)
	if err != nil {
		return err
	}
	if row == nil {
		return apierr.New("not_found", 404)
	}
	if !db.IsNull(row, "revoked_at") {
		return nil
	}
	return db.InTx(q, func(q db.Q) error {
		if _, err := q.Exec("UPDATE einsatz_share_links SET revoked_at = ?, revoked_by = ? WHERE id = ?", db.Now(), c.UID, id); err != nil {
			return err
		}
		_, err := access.Event(q, c, "share_link", fmt.Sprint(id), "delete", "Freigabelink „"+db.Str(row, "label")+"\" widerrufen",
			map[string]any{"label": db.Str(row, "label")}, map[string]any{"id": id, "revoked": true})
		return err
	})
}
