package einsatz

// Verknuepfungen zwischen Einsaetzen ueber Freigabelinks.
//
// Einsatz A erstellt einen Freigabelink (einsatz_share_links) und gibt ihn
// an einen anderen Stab weiter. Der Eigentuemer von Einsatz B traegt den Link
// ein -> Zeile in einsatz_links, sofort approved: Die Weitergabe des Links
// ist die Zustimmung des Erstellers. Alle Mitglieder von B erhalten lesenden
// Zugriff auf A. Der Link selbst gibt keinen Zugriff; er dient nur zum
// Eintragen (kein anonymer Direktaufruf, kein Erraten).
//
// Der Eigentuemer von A sieht in seiner Verwaltung, welche Einsaetze den Link
// nutzen, und kann einzelnen den Zugriff entziehen (rejected, wieder
// freigebbar) oder den Link ganz widerrufen. Beides wirkt sofort.

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/friedrichengel/thw_luk_software_go/internal/access"
	"github.com/friedrichengel/thw_luk_software_go/internal/apierr"
	"github.com/friedrichengel/thw_luk_software_go/internal/db"
	"github.com/friedrichengel/thw_luk_software_go/internal/stab"
)

const linkBaseSelect = `SELECT k.*, l.einsatz_id AS share_einsatz_id, l.label AS share_label, l.revoked_at AS share_revoked_at, l.expires_at AS share_expires_at,
	a.uuid AS a_uuid, a.title AS a_title, a.stab_name AS a_stab, a.ebene AS a_ebene, a.status AS a_status, a.deleted_at AS a_deleted_at,
	b.uuid AS b_uuid, b.title AS b_title, b.stab_name AS b_stab, b.ebene AS b_ebene, b.status AS b_status, b.deleted_at AS b_deleted_at,
	ur.username AS req_username, ur.display_name AS req_display,
	ud.username AS dec_username, ud.display_name AS dec_display
	FROM einsatz_links k
	JOIN einsatz_share_links l ON l.id = k.share_link_id
	JOIN einsaetze a ON a.id = l.einsatz_id
	JOIN einsaetze b ON b.id = k.einsatz_id
	LEFT JOIN users ur ON ur.id = k.requested_by
	LEFT JOIN users ud ON ud.id = k.decided_by `

var (
	reTokenURL = regexp.MustCompile(`(?i)[?&]t=([a-f0-9]{32,128})`)
	reToken    = regexp.MustCompile(`(?i)^[a-f0-9]{32,128}$`)
)

// ExtractToken loest das Token aus einer eingefuegten URL oder einem rohen Code.
func ExtractToken(in string) string {
	in = strings.TrimSpace(in)
	if m := reTokenURL.FindStringSubmatch(in); m != nil {
		return strings.ToLower(m[1])
	}
	if reToken.MatchString(in) {
		return strings.ToLower(in)
	}
	return ""
}

// ShareLinkInfo: gueltiger Freigabelink zu einem Token inkl. Daten von Einsatz A.
type ShareLinkInfo struct {
	ID        int64
	EinsatzID int64
	Label     string
	ExpiresAt *string
	UUID      string
	Title     string
	StabName  string
	Ebene     string
	Status    string
}

// ShareLinkByToken: gueltiger Freigabelink (nicht widerrufen, nicht abgelaufen) oder nil.
func ShareLinkByToken(q db.Q, secret, token string) (*ShareLinkInfo, error) {
	if !reToken.MatchString(token) {
		return nil, nil
	}
	row, err := db.QueryMap(q, `SELECT l.id, l.einsatz_id, l.label, l.expires_at, l.revoked_at, e.uuid, e.title, e.stab_name, e.ebene, e.status
		FROM einsatz_share_links l JOIN einsaetze e ON e.id = l.einsatz_id
		WHERE l.token_hash = ? AND e.deleted_at IS NULL LIMIT 1`, access.HashToken(secret, token))
	if err != nil || row == nil {
		return nil, err
	}
	if !db.IsNull(row, "revoked_at") {
		return nil, nil
	}
	if exp := db.StrPtr(row, "expires_at"); exp != nil && *exp != "" {
		if t, ok := ParseDBTime(*exp); ok && t.Before(time.Now()) {
			return nil, nil
		}
	}
	return &ShareLinkInfo{
		ID: db.Int(row, "id"), EinsatzID: db.Int(row, "einsatz_id"), Label: db.Str(row, "label"), ExpiresAt: db.StrPtr(row, "expires_at"),
		UUID: db.Str(row, "uuid"), Title: db.Str(row, "title"), StabName: db.Str(row, "stab_name"), Ebene: db.Str(row, "ebene"), Status: db.Str(row, "status"),
	}, nil
}

// LinkEinsatz beschreibt Einsatz A bzw. B einer Verknuepfung.
type LinkEinsatz struct {
	UUID       string `json:"uuid"`
	Title      string `json:"title"`
	StabName   string `json:"stab_name"`
	Ebene      string `json:"ebene"`
	EbeneLabel string `json:"ebene_label"`
	Status     string `json:"status"`
}

type Link struct {
	ID          int64       `json:"id"`
	Status      string      `json:"status"`
	Label       string      `json:"label"`
	ShareLinkID int64       `json:"share_link_id"`
	ShareLabel  string      `json:"share_label"`
	LinkActive  bool        `json:"link_active"`
	Effective   bool        `json:"effective"`
	EinsatzA    LinkEinsatz `json:"einsatz_a"`
	EinsatzB    LinkEinsatz `json:"einsatz_b"`
	RequestedBy string      `json:"requested_by"`
	RequestedAt string      `json:"requested_at"`
	DecidedBy   string      `json:"decided_by"`
	DecidedAt   *string     `json:"decided_at"`
	// intern
	einsatzID      int64
	shareEinsatzID int64
	removedAt      *string
}

func mapLink(r map[string]any) *Link {
	active := linkActive(db.StrPtr(r, "share_revoked_at"), db.StrPtr(r, "share_expires_at"))
	label := func(display, username string) string {
		if strings.TrimSpace(display) != "" {
			return display
		}
		return username
	}
	return &Link{
		ID: db.Int(r, "id"), Status: db.Str(r, "status"), Label: db.Str(r, "label"),
		ShareLinkID: db.Int(r, "share_link_id"), ShareLabel: db.Str(r, "share_label"), LinkActive: active,
		Effective:   db.Str(r, "status") == "approved" && active && db.IsNull(r, "a_deleted_at") && db.IsNull(r, "b_deleted_at"),
		EinsatzA:    LinkEinsatz{UUID: db.Str(r, "a_uuid"), Title: db.Str(r, "a_title"), StabName: db.Str(r, "a_stab"), Ebene: db.Str(r, "a_ebene"), EbeneLabel: stab.EbeneLabel(db.Str(r, "a_ebene")), Status: db.Str(r, "a_status")},
		EinsatzB:    LinkEinsatz{UUID: db.Str(r, "b_uuid"), Title: db.Str(r, "b_title"), StabName: db.Str(r, "b_stab"), Ebene: db.Str(r, "b_ebene"), EbeneLabel: stab.EbeneLabel(db.Str(r, "b_ebene")), Status: db.Str(r, "b_status")},
		RequestedBy: label(db.Str(r, "req_display"), db.Str(r, "req_username")), RequestedAt: db.Str(r, "requested_at"),
		DecidedBy: label(db.Str(r, "dec_display"), db.Str(r, "dec_username")), DecidedAt: db.StrPtr(r, "decided_at"),
		einsatzID: db.Int(r, "einsatz_id"), shareEinsatzID: db.Int(r, "share_einsatz_id"), removedAt: db.StrPtr(r, "removed_at"),
	}
}

func linkRaw(q db.Q, id int64) (*Link, error) {
	row, err := db.QueryMap(q, linkBaseSelect+"WHERE k.id = ? LIMIT 1", id)
	if err != nil || row == nil {
		return nil, err
	}
	return mapLink(row), nil
}

func LinkRow(q db.Q, id int64) (*Link, error) {
	l, err := linkRaw(q, id)
	if err != nil {
		return nil, err
	}
	if l == nil {
		return nil, apierr.New("not_found", 404)
	}
	return l, nil
}

// LinkRequest: Einsatz B (ctx, Eigentuemer) traegt den Freigabelink von A ein -> sofort wirksam.
func LinkRequest(q db.Q, c *access.Ctx, secret, token, label string) (*Link, error) {
	link, err := ShareLinkByToken(q, secret, token)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, apierr.New("link_invalid", 404)
	}
	bID := c.Einsatz.ID
	if link.EinsatzID == bID {
		return nil, apierr.New("link_self", 409)
	}
	ex, err := db.QueryMap(q, "SELECT id FROM einsatz_links WHERE einsatz_id = ? AND share_link_id = ? AND removed_at IS NULL LIMIT 1", bID, link.ID)
	if err != nil {
		return nil, err
	}
	if ex != nil {
		return nil, apierr.New("link_exists", 409)
	}
	label = access.Truncate(strings.TrimSpace(label), 120)
	now := db.Now()
	var out *Link
	err = db.InTx(q, func(q db.Q) error {
		res, err := q.Exec("INSERT INTO einsatz_links (einsatz_id, share_link_id, status, label, requested_by, requested_at, decided_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			bID, link.ID, "approved", label, db.NullIfZero(c.UID), now, now)
		if err != nil {
			return err
		}
		id := db.LastID(res)
		if _, err := access.Event(q, c, "einsatz_link", fmt.Sprint(id), "insert",
			"Einsatz „"+link.Title+"\" verknüpft (Freigabelink „"+link.Label+"\"), Lesezugriff für alle Mitglieder",
			nil, map[string]any{"id": id, "status": "approved", "einsatz": link.Title}); err != nil {
			return err
		}
		if _, err := access.EventFor(q, c, link.EinsatzID, "einsatz_link", fmt.Sprint(id), "insert",
			"Einsatz „"+c.Einsatz.Title+"\" nutzt Freigabelink „"+link.Label+"\" (Lesezugriff)",
			nil, map[string]any{"id": id, "status": "approved", "einsatz": c.Einsatz.Title}); err != nil {
			return err
		}
		out, err = LinkRow(q, id)
		return err
	})
	return out, err
}

// LinkDecide: Eigentuemer von A entzieht einem verknuepften Einsatz den Zugriff (approve=false) oder gibt ihn wieder frei.
func LinkDecide(q db.Q, cA *access.Ctx, id int64, approve bool) (*Link, error) {
	row, err := linkRaw(q, id)
	if err != nil {
		return nil, err
	}
	if row == nil || row.shareEinsatzID != cA.Einsatz.ID || row.removedAt != nil {
		return nil, apierr.New("not_found", 404)
	}
	status := "rejected"
	txt := "entzogen"
	if approve {
		status = "approved"
		txt = "wieder freigegeben"
	}
	err = db.InTx(q, func(q db.Q) error {
		if _, err := q.Exec("UPDATE einsatz_links SET status = ?, decided_by = ?, decided_at = ? WHERE id = ?", status, db.NullIfZero(cA.UID), db.Now(), id); err != nil {
			return err
		}
		if _, err := access.Event(q, cA, "einsatz_link", fmt.Sprint(id), "update",
			"Lesezugriff für „"+row.EinsatzB.Title+"\" "+txt,
			map[string]any{"status": row.Status}, map[string]any{"id": id, "status": status, "einsatz": row.EinsatzB.Title}); err != nil {
			return err
		}
		_, err := access.EventFor(q, cA, row.einsatzID, "einsatz_link", fmt.Sprint(id), "update",
			"Lesezugriff auf „"+row.EinsatzA.Title+"\" vom Ersteller "+txt,
			map[string]any{"status": row.Status}, map[string]any{"id": id, "status": status, "einsatz": row.EinsatzA.Title})
		return err
	})
	if err != nil {
		return nil, err
	}
	return LinkRow(q, id)
}

// LinkRemove: Eigentuemer von B entfernt die Verknuepfung.
func LinkRemove(q db.Q, cB *access.Ctx, id int64) error {
	row, err := linkRaw(q, id)
	if err != nil {
		return err
	}
	if row == nil || row.einsatzID != cB.Einsatz.ID {
		return apierr.New("not_found", 404)
	}
	if row.removedAt != nil {
		return nil
	}
	return db.InTx(q, func(q db.Q) error {
		if _, err := q.Exec("UPDATE einsatz_links SET removed_at = ? WHERE id = ?", db.Now(), id); err != nil {
			return err
		}
		if _, err := access.Event(q, cB, "einsatz_link", fmt.Sprint(id), "delete",
			"Verknüpfung mit „"+row.EinsatzA.Title+"\" entfernt", map[string]any{"status": row.Status}, map[string]any{"id": id, "removed": true}); err != nil {
			return err
		}
		_, err := access.EventFor(q, cB, row.shareEinsatzID, "einsatz_link", fmt.Sprint(id), "delete",
			"Verknüpfung von „"+row.EinsatzB.Title+"\" entfernt", map[string]any{"status": row.Status}, map[string]any{"id": id, "removed": true})
		return err
	})
}

func mapLinks(rows []map[string]any) []*Link {
	out := []*Link{}
	for _, r := range rows {
		out = append(out, mapLink(r))
	}
	return out
}

// LinksOutgoing (Einsatz B): alle Verknuepfungen zu fremden Einsaetzen.
func LinksOutgoing(q db.Q, bID int64) ([]*Link, error) {
	rows, err := db.QueryMaps(q, linkBaseSelect+"WHERE k.einsatz_id = ? AND k.removed_at IS NULL ORDER BY k.id DESC", bID)
	if err != nil {
		return nil, err
	}
	return mapLinks(rows), nil
}

// LinksIncoming (Einsatz A): Anfragen und Freigaben je Freigabelink.
func LinksIncoming(q db.Q, aID int64) ([]*Link, error) {
	rows, err := db.QueryMaps(q, linkBaseSelect+"WHERE l.einsatz_id = ? AND k.removed_at IS NULL ORDER BY k.id DESC", aID)
	if err != nil {
		return nil, err
	}
	return mapLinks(rows), nil
}

// SharedEinsatz: fremder Einsatz in der Einsatzliste, freigegeben ueber Verknuepfungen.
type SharedEinsatz struct {
	UUID       string              `json:"uuid"`
	Title      string              `json:"title"`
	StabName   string              `json:"stab_name"`
	Ebene      string              `json:"ebene"`
	EbeneLabel string              `json:"ebene_label"`
	Status     string              `json:"status"`
	Via        []access.ViaEinsatz `json:"via"`
}

// SharedForUser: fremde Einsaetze, die dem Nutzer ueber Verknuepfungen freigegeben sind.
func SharedForUser(q db.Q, uid int64) ([]SharedEinsatz, error) {
	out := []SharedEinsatz{}
	if uid <= 0 {
		return out, nil
	}
	rows, err := db.QueryMaps(q, linkBaseSelect+`WHERE k.status = 'approved' AND k.removed_at IS NULL
		AND l.revoked_at IS NULL AND (l.expires_at IS NULL OR l.expires_at > ?)
		AND a.deleted_at IS NULL AND b.deleted_at IS NULL
		AND (b.owner_id = ? OR `+access.MemberCond("b.id", "?", "?")+`)
		ORDER BY a.title, k.id`, db.Now(), uid, uid, uid)
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for _, r := range rows {
		key := db.Str(r, "a_uuid")
		i, ok := idx[key]
		if !ok {
			out = append(out, SharedEinsatz{UUID: key, Title: db.Str(r, "a_title"), StabName: db.Str(r, "a_stab"), Ebene: db.Str(r, "a_ebene"),
				EbeneLabel: stab.EbeneLabel(db.Str(r, "a_ebene")), Status: db.Str(r, "a_status"), Via: []access.ViaEinsatz{}})
			i = len(out) - 1
			idx[key] = i
		}
		out[i].Via = append(out[i].Via, access.ViaEinsatz{UUID: db.Str(r, "b_uuid"), Title: db.Str(r, "b_title")})
	}
	return out, nil
}
