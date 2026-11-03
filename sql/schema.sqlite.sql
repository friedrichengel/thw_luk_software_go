-- LuK Stabsansicht - SQLite Schema
-- Wird bei jedem Start ausgefuehrt (db.Open): nur CREATE ... IF NOT EXISTS,
-- also idempotent. Spalten, die spaeter dazukamen, zieht db.bootstrap per
-- ALTER TABLE nach. Indizes stehen hier - AUSSER Indizes auf nachgezogenen
-- Spalten: die legt db.bootstrap nach addColumn an, weil sie auf einer
-- aelteren Datenbank sonst am noch fehlenden Feld scheitern.
--
-- Zeitstempel sind durchgehend TEXT im Format datetime('now') (UTC,
-- "YYYY-MM-DD HH:MM:SS", db.Now()). Aufzaehlungen tragen CHECK-Regeln; die
-- fachlichen Werte stammen aus internal/stab (Rollen, Status, Ebenen) bzw.
-- internal/user (Kontorollen, Divera-Recht) und internal/org (Zugangsarten,
-- STEIN-Modus).
--
-- Grundprinzipien:
--   * Eine Zeile pro fachlichem Objekt (Knoten, Mitglied, Schicht, Karten-
--     objekt, Schadenskonto, ...) mit eigener Versionsnummer. Schreibzugriffe
--     laufen immer als "UPDATE ... WHERE id=? AND version=?" (optimistic
--     locking). So kann ein Stab gleichzeitig an verschiedenen Objekten
--     arbeiten, ohne sich gegenseitig zu ueberschreiben.
--   * Soft-Delete (deleted_at) statt physischem Loeschen, damit das Log und
--     verknuepfte Objekte (Lagekarte, Schadenskonten) weiter auf den Datensatz
--     verweisen koennen.
--   * einsatz_events ist Aenderungslog, Live-Sync-Feed und Exportbasis in
--     einem. einsatz_access_log protokolliert Lesezugriffe (Nutzer und
--     Freigabelinks).

PRAGMA foreign_keys = ON;

-- ---------------------------------------------------------------------------
-- Nutzer / Login
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    email TEXT UNIQUE COLLATE NOCASE,
    display_name TEXT,
    -- user   = darf sich einloggen und an freigegebenen Einsaetzen mitarbeiten
    -- leiter = wie user, darf zusaetzlich Einsaetze anlegen/verwalten
    -- admin  = Serververwaltung, KEIN Zugriff auf Einsaetze
    role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user','leiter','admin')),
    two_factor_enabled INTEGER NOT NULL DEFAULT 0,
    two_factor_secret TEXT,
    two_factor_last_counter INTEGER NOT NULL DEFAULT 0,
    must_change_password INTEGER NOT NULL DEFAULT 1,
    -- Changelog "Was ist neu": zuletzt bestaetigte Version (public/daten/changelog.json)
    changelog_seen TEXT NOT NULL DEFAULT '',
    -- Organisation (OV/RSt/LV), der das Konto angehoert; NULL nur bei Admins
    -- und Altbestand ohne Zuordnung. Org-Admins verwalten Nutzer, Rechte und
    -- Zugaenge (STEIN/Divera) ihrer eigenen Organisation.
    org_id INTEGER REFERENCES organisationen (id) ON DELETE SET NULL,
    org_admin INTEGER NOT NULL DEFAULT 0,
    -- Divera-Recht: off | read | write (Status setzen); wirkt nur mit
    -- hinterlegtem Divera-Schluessel der Organisation
    divera_access TEXT NOT NULL DEFAULT 'off' CHECK (divera_access IN ('off','read','write')),
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Organisationen: Ortsverband, Regionalstelle, Landesverband, Leitung.
-- Schluessel fuer Fremdsysteme (STEIN, Divera) gehoeren zur Organisation,
-- nicht zum Server - jede Einheit hat ihre eigenen.
CREATE TABLE IF NOT EXISTS organisationen (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    kurz TEXT NOT NULL DEFAULT '',
    ebene TEXT NOT NULL DEFAULT 'ov' CHECK (ebene IN ('ov','rst','lv','leitung')),
    -- STEIN-Abruf: poll = alle 2 min die Live-Menge holen (Standard);
    -- webhook = STEIN meldet Aenderungen an /api/stein/webhook/<token>,
    -- der Server holt gebuendelt nach, voller Abruf nur alle 30 min.
    stein_mode TEXT NOT NULL DEFAULT 'poll' CHECK (stein_mode IN ('poll','webhook')),
    -- Bezug zu den Stammdaten (stammdaten.json): "lv:<key>", Regionalstellen-
    -- Key (rst_...) oder "ov:<key>"; leer = frei angelegt.
    stamm_key TEXT NOT NULL DEFAULT '',
    -- Divera-Gruppen (IDs als JSON-Array), die im Dashboard NICHT gezeigt werden
    -- (z.B. Sammelgruppen, die Personen doppelt zaehlen wuerden)
    divera_hidden_groups TEXT NOT NULL DEFAULT '[]',
    -- Eigener Kartenmittelpunkt (Dashboard-Karten, neue Lagekarten); NULL =
    -- Standort aus den Stammdaten, sonst MAP_CENTER_* des Servers
    map_lat REAL,
    map_lng REAL,
    map_name TEXT NOT NULL DEFAULT '',
    -- Eigene Nachrichten-Feeds der Dashboard-Kacheln als JSON-Array
    -- [{slot,title,urls}] ; '' = eingebaute Feeds
    rss_feeds TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
-- idx_organisationen_stamm entsteht in db.bootstrap nach dem Nachziehen der Spalte.

-- Eingegangene STEIN-Webhook-Ereignisse (Nachweis fuer den Org-Admin, Duplikat-
-- Erkennung). Nur die Verweise aus der Nachricht, keine Inhalte.
CREATE TABLE IF NOT EXISTS stein_webhook_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id INTEGER NOT NULL,
    received_at TEXT NOT NULL DEFAULT (datetime('now')),
    event_id TEXT NOT NULL DEFAULT '',
    environment TEXT NOT NULL DEFAULT '',
    item_type TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL DEFAULT '',
    item_id TEXT NOT NULL DEFAULT '',
    parent_id TEXT NOT NULL DEFAULT '',
    secret_label TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (org_id) REFERENCES organisationen (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_stein_webhook_events_org ON stein_webhook_events (org_id, received_at);

-- Zugaenge einer Organisation (kind: stein | divera_access | divera_system).
-- value_enc ist AES-GCM-verschluesselt mit einem aus APP_SECRET abgeleiteten
-- Schluessel; hint zeigt die letzten Zeichen zur Wiedererkennung. Der Wert
-- wird nie an den Browser ausgeliefert.
CREATE TABLE IF NOT EXISTS org_credentials (
    org_id INTEGER NOT NULL,
    kind TEXT NOT NULL,
    value_enc TEXT NOT NULL,
    hint TEXT NOT NULL DEFAULT '',
    set_by INTEGER,
    set_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (org_id, kind),
    FOREIGN KEY (org_id) REFERENCES organisationen (id) ON DELETE CASCADE,
    FOREIGN KEY (set_by) REFERENCES users (id) ON DELETE SET NULL
);

-- STEIN-Webhook-Secrets einer Organisation: STEIN vergibt das X-Secret je
-- Ortsverband, deshalb eine Zeile pro Ortsverband (label = Bezeichnung).
-- Verschluesselung und hint wie bei org_credentials.
CREATE TABLE IF NOT EXISTS org_webhook_secrets (
    org_id INTEGER NOT NULL,
    label TEXT NOT NULL COLLATE NOCASE,
    value_enc TEXT NOT NULL,
    hint TEXT NOT NULL DEFAULT '',
    set_by INTEGER,
    set_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (org_id, label),
    FOREIGN KEY (org_id) REFERENCES organisationen (id) ON DELETE CASCADE,
    FOREIGN KEY (set_by) REFERENCES users (id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS password_resets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_password_resets_user ON password_resets (user_id);

-- Fehlversuche fuer das Rate-Limiting (Login je IP/Konto, Passwort-Reset mit
-- ip_hash 'pwreset'). Fluechtige Daten, werden laufend abgeraeumt.
CREATE TABLE IF NOT EXISTS login_attempts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ip_hash TEXT NOT NULL,
    username TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_login_attempts_ip ON login_attempts (ip_hash, created_at);
CREATE INDEX IF NOT EXISTS idx_login_attempts_user ON login_attempts (username, created_at);

CREATE TABLE IF NOT EXISTS tfa_attempts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_tfa_attempts_user ON tfa_attempts (user_id, created_at);

-- ---------------------------------------------------------------------------
-- Einsatz (Akte)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS einsaetze (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    uuid TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    -- Stabsebene, vom Ersteller beim Anlegen gewaehlt
    ebene TEXT NOT NULL DEFAULT 'ov' CHECK (ebene IN ('ov','rst','lv','leitung')),
    stab_name TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    owner_id INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed')),
    -- Vorwarnzeit in Minuten: Schichten, die innerhalb dieser Zeit beginnen
    -- und noch "geplant" sind, erscheinen in der Kraefteuebersicht als faellig.
    vorwarn_min INTEGER NOT NULL DEFAULT 180,
    -- Kraeftekatalog: eigener Bereich (Regionalstelle oder "ov:<key>", leer = ganzer LV),
    -- Einsatzort fuer Entfernungen, STEIN-Live-Daten verwenden (Uebungen: aus)
    bereich_key TEXT NOT NULL DEFAULT '',
    ort_name TEXT NOT NULL DEFAULT '',
    ort_lat REAL,
    ort_lng REAL,
    stein_aktiv INTEGER NOT NULL DEFAULT 1,
    -- Organisation des Einsatzes (beim Anlegen die des Erstellers): bestimmt
    -- den STEIN-Zugang, mit dem der Kraeftekatalog den Live-Status holt.
    org_id INTEGER REFERENCES organisationen (id) ON DELETE SET NULL,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    closed_at TEXT,
    closed_by INTEGER,
    deleted_at TEXT,
    deleted_by INTEGER,
    FOREIGN KEY (owner_id) REFERENCES users (id) ON DELETE RESTRICT,
    FOREIGN KEY (closed_by) REFERENCES users (id) ON DELETE SET NULL,
    FOREIGN KEY (deleted_by) REFERENCES users (id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_einsaetze_owner ON einsaetze (owner_id);
CREATE INDEX IF NOT EXISTS idx_einsaetze_status ON einsaetze (status, deleted_at);

-- Stabsrollen je Einsatz und Nutzer. Eine Zeile pro Rolle, ein Nutzer kann
-- mehrere Rollen haben. Rollen-Keys siehe internal/stab.
CREATE TABLE IF NOT EXISTS einsatz_members (
    einsatz_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    role TEXT NOT NULL,
    added_by INTEGER,
    added_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (einsatz_id, user_id, role),
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    FOREIGN KEY (added_by) REFERENCES users (id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_einsatz_members_user ON einsatz_members (user_id);

-- Freigabe an ganze Organisationen: alle Einsatz-Konten der Organisation
-- erhalten die eingetragenen Stabsrollen (zusaetzlich zu persoenlichen
-- Rollen aus einsatz_members). Eine Zeile pro Rolle.
CREATE TABLE IF NOT EXISTS einsatz_org_members (
    einsatz_id INTEGER NOT NULL,
    org_id INTEGER NOT NULL,
    role TEXT NOT NULL,
    added_by INTEGER,
    added_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (einsatz_id, org_id, role),
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE,
    FOREIGN KEY (org_id) REFERENCES organisationen (id) ON DELETE CASCADE,
    FOREIGN KEY (added_by) REFERENCES users (id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_einsatz_org_members_org ON einsatz_org_members (org_id);

-- ---------------------------------------------------------------------------
-- Fuehrungsharke: Knoten, Mitglieder, Schichten
-- ---------------------------------------------------------------------------

-- Knoten der Fuehrungsharke (Fuehrungsstelle oder Einheit). parent_id NULL =
-- Wurzelknoten. sort_order ordnet Geschwister.
CREATE TABLE IF NOT EXISTS einsatz_nodes (
    id TEXT PRIMARY KEY,
    einsatz_id INTEGER NOT NULL,
    parent_id TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    type TEXT NOT NULL DEFAULT 'einheit' CHECK (type IN ('fuehrung','einheit')),
    bezeichnung TEXT NOT NULL DEFAULT '',
    funkruf TEXT NOT NULL DEFAULT '',
    ov TEXT NOT NULL DEFAULT '',
    -- Verweis in den Kraeftekatalog (internal/kraefte), leer = Freitext
    kraft_key TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    -- Anforderungs-/Anfahrtsstatus (siehe internal/stab, Status)
    status TEXT NOT NULL DEFAULT 'geplant'
        CHECK (status IN ('geplant','angefordert','anfahrt','vor_ort','abgerueckt')),
    status_at TEXT,
    -- Externer Freigabelink eines anderen Stabes, der hinter dieser
    -- Fuehrungsebene hinterlegt ist (optional)
    extern_link_url TEXT NOT NULL DEFAULT '',
    extern_link_label TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    created_by INTEGER,
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_by INTEGER,
    deleted_at TEXT,
    deleted_by INTEGER,
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_einsatz_nodes_einsatz ON einsatz_nodes (einsatz_id, deleted_at);
CREATE INDEX IF NOT EXISTS idx_einsatz_nodes_parent ON einsatz_nodes (parent_id);
CREATE INDEX IF NOT EXISTS idx_einsatz_nodes_kraft ON einsatz_nodes (kraft_key);

-- Mitglieder einer Fuehrungsstelle: Fachberater oder interne Einheit
-- ("an dieser Stelle", nicht als eigener Knoten in der Hierarchie).
CREATE TABLE IF NOT EXISTS einsatz_mitglieder (
    id TEXT PRIMARY KEY,
    einsatz_id INTEGER NOT NULL,
    node_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('fachberater','einheit')),
    sort_order INTEGER NOT NULL DEFAULT 0,
    funktion TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    bezeichnung TEXT NOT NULL DEFAULT '',
    funkruf TEXT NOT NULL DEFAULT '',
    telefon TEXT NOT NULL DEFAULT '',
    ov TEXT NOT NULL DEFAULT '',
    kraft_key TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'geplant'
        CHECK (status IN ('geplant','angefordert','anfahrt','vor_ort','abgerueckt')),
    status_at TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    created_by INTEGER,
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_by INTEGER,
    deleted_at TEXT,
    deleted_by INTEGER,
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES einsatz_nodes (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_einsatz_mitglieder_node ON einsatz_mitglieder (node_id, deleted_at);
CREATE INDEX IF NOT EXISTS idx_einsatz_mitglieder_einsatz ON einsatz_mitglieder (einsatz_id, deleted_at);
CREATE INDEX IF NOT EXISTS idx_einsatz_mitglieder_kraft ON einsatz_mitglieder (kraft_key);

-- Schichten (Kraefteplanung). Haengen an einem Knoten (Einheit) oder an
-- einem Mitglied (Fachberater / interne Einheit) eines Fuehrungsknotens.
CREATE TABLE IF NOT EXISTS einsatz_schichten (
    id TEXT PRIMARY KEY,
    einsatz_id INTEGER NOT NULL,
    node_id TEXT NOT NULL,
    mitglied_id TEXT,
    von TEXT NOT NULL DEFAULT '',
    bis TEXT NOT NULL DEFAULT '',
    staerke TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    telefon TEXT NOT NULL DEFAULT '',
    funkruf TEXT NOT NULL DEFAULT '',
    ov TEXT NOT NULL DEFAULT '',
    -- Einheit aus dem Kraeftekatalog, die diese Schicht stellt (Abloesung durch andere Einheit moeglich)
    kraft_key TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    aktiv INTEGER NOT NULL DEFAULT 1,
    -- Eigener Anforderungs-/Anfahrtsstatus je Schicht (Abloesung: die
    -- kommende Schicht kann eintreffen, waehrend die alte auf Heimfahrt ist)
    status TEXT NOT NULL DEFAULT 'geplant'
        CHECK (status IN ('geplant','angefordert','anfahrt','vor_ort','abgerueckt')),
    status_at TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    created_by INTEGER,
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_by INTEGER,
    deleted_at TEXT,
    deleted_by INTEGER,
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES einsatz_nodes (id) ON DELETE CASCADE,
    FOREIGN KEY (mitglied_id) REFERENCES einsatz_mitglieder (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_einsatz_schichten_node ON einsatz_schichten (node_id, deleted_at);
CREATE INDEX IF NOT EXISTS idx_einsatz_schichten_einsatz ON einsatz_schichten (einsatz_id, deleted_at);
CREATE INDEX IF NOT EXISTS idx_einsatz_schichten_kraft ON einsatz_schichten (kraft_key);

-- ---------------------------------------------------------------------------
-- Schadenskonten
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS schadenskonten (
    id TEXT PRIMARY KEY,
    einsatz_id INTEGER NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    -- {gefahren:[{file,label,custom?}], schaeden:[...], massnahmen:[...]}
    taktische_zeichen TEXT NOT NULL DEFAULT '{}',
    sort_order INTEGER NOT NULL DEFAULT 0,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    created_by INTEGER,
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_by INTEGER,
    deleted_at TEXT,
    deleted_by INTEGER,
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_schadenskonten_einsatz ON schadenskonten (einsatz_id, deleted_at, sort_order);

-- Verknuepfung Schadenskonto -> Einheit aus der Fuehrungsharke (Knoten oder
-- Mitglied). Werte (Bezeichnung, Staerke, Besetzung) werden zur Laufzeit
-- aus der Harke/Kraefteplanung aufgeloest, nie kopiert.
CREATE TABLE IF NOT EXISTS schadenskonten_refs (
    schadenskonto_id TEXT NOT NULL,
    einsatz_id INTEGER NOT NULL,
    ref_kind TEXT NOT NULL CHECK (ref_kind IN ('node','mitglied')),
    ref_id TEXT NOT NULL,
    added_by INTEGER,
    added_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (schadenskonto_id, ref_kind, ref_id),
    FOREIGN KEY (schadenskonto_id) REFERENCES schadenskonten (id) ON DELETE CASCADE,
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_schadenskonten_refs_ref ON schadenskonten_refs (einsatz_id, ref_kind, ref_id);

-- Fremdeinheiten (FW/Polizei/RD/BW/...) ohne Bezug zur Harke, mit eigener
-- Staerkeangabe.
CREATE TABLE IF NOT EXISTS schadenskonten_fremd (
    id TEXT PRIMARY KEY,
    schadenskonto_id TEXT NOT NULL,
    einsatz_id INTEGER NOT NULL,
    kind TEXT NOT NULL DEFAULT 'sonstige',
    bezeichnung TEXT NOT NULL DEFAULT '',
    funkruf TEXT NOT NULL DEFAULT '',
    ov TEXT NOT NULL DEFAULT '',
    staerke TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    created_by INTEGER,
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_by INTEGER,
    deleted_at TEXT,
    deleted_by INTEGER,
    FOREIGN KEY (schadenskonto_id) REFERENCES schadenskonten (id) ON DELETE CASCADE,
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_schadenskonten_fremd_sk ON schadenskonten_fremd (schadenskonto_id, deleted_at);

-- ---------------------------------------------------------------------------
-- Lagekarten
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS lagekarten (
    id TEXT PRIMARY KEY,
    einsatz_id INTEGER NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    center_lat REAL,
    center_lng REAL,
    zoom INTEGER,
    basemap TEXT NOT NULL DEFAULT 'osm',
    sort_order INTEGER NOT NULL DEFAULT 0,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    created_by INTEGER,
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_by INTEGER,
    deleted_at TEXT,
    deleted_by INTEGER,
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_lagekarten_einsatz ON lagekarten (einsatz_id, deleted_at);

-- Einsatzabschnitte (Layer) einer Lagekarte. nr 0 = "Grundkarte" ist
-- implizit und wird nicht gespeichert.
CREATE TABLE IF NOT EXISTS lagekarte_abschnitte (
    lagekarte_id TEXT NOT NULL,
    nr INTEGER NOT NULL,
    einsatz_id INTEGER NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    created_by INTEGER,
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_by INTEGER,
    deleted_at TEXT,
    deleted_by INTEGER,
    PRIMARY KEY (lagekarte_id, nr),
    FOREIGN KEY (lagekarte_id) REFERENCES lagekarten (id) ON DELETE CASCADE,
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE
);

-- Kartenobjekte: taktische Zeichen (marker), Flaechen, Linien, Rechtecke,
-- Kreise, Pfeile, Texte. geometry und props sind JSON; abschnitte ist ein
-- JSON-Array der Abschnitts-Nummern. ref_kind/ref_id verknuepfen optional
-- mit einer Einheit aus der Harke (Werte werden live aufgeloest).
CREATE TABLE IF NOT EXISTS lagekarte_features (
    id TEXT PRIMARY KEY,
    lagekarte_id TEXT NOT NULL,
    einsatz_id INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('marker','polygon','polyline','rectangle','circle','arrow','text')),
    geometry TEXT NOT NULL DEFAULT '{}',
    props TEXT NOT NULL DEFAULT '{}',
    abschnitte TEXT NOT NULL DEFAULT '[]',
    ref_kind TEXT CHECK (ref_kind IS NULL OR ref_kind IN ('node','mitglied')),
    ref_id TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    created_by INTEGER,
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_by INTEGER,
    deleted_at TEXT,
    deleted_by INTEGER,
    FOREIGN KEY (lagekarte_id) REFERENCES lagekarten (id) ON DELETE CASCADE,
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_lagekarte_features_karte ON lagekarte_features (lagekarte_id, deleted_at);
CREATE INDEX IF NOT EXISTS idx_lagekarte_features_ref ON lagekarte_features (einsatz_id, ref_kind, ref_id);

-- ---------------------------------------------------------------------------
-- Ereignis-Log, Zugriffslog, Freigabelinks, Snapshots
-- ---------------------------------------------------------------------------

-- Jede Schreibaktion erzeugt genau ein Ereignis. seq ist global monoton
-- steigend und dient den Clients als Sync-Cursor (events?since=seq).
CREATE TABLE IF NOT EXISTS einsatz_events (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    einsatz_id INTEGER NOT NULL,
    ts TEXT NOT NULL DEFAULT (datetime('now')),
    user_id INTEGER,
    -- Anzeigename zum Zeitpunkt des Ereignisses (bleibt bei Export/Import
    -- und nach Loeschung des Nutzerkontos erhalten)
    user_label TEXT NOT NULL DEFAULT '',
    entity TEXT NOT NULL,       -- einsatz|member|org_member|node|mitglied|schicht|schadenskonto|sk_ref|sk_fremd|lagekarte|abschnitt|feature|share_link|einsatz_link|snapshot
    entity_id TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,       -- insert|update|delete|status|move|close|reopen|import|...
    summary TEXT NOT NULL DEFAULT '',
    before_json TEXT,
    after_json TEXT,
    imported INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_einsatz_events_einsatz ON einsatz_events (einsatz_id, seq);

-- Lesezugriffe (Seitenaufrufe) durch Nutzer und Freigabelinks.
CREATE TABLE IF NOT EXISTS einsatz_access_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    einsatz_id INTEGER NOT NULL,
    ts TEXT NOT NULL DEFAULT (datetime('now')),
    user_id INTEGER,
    user_label TEXT NOT NULL DEFAULT '',
    share_link_id INTEGER,
    ip_hash TEXT NOT NULL DEFAULT '',
    page TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_einsatz_access_log_einsatz ON einsatz_access_log (einsatz_id, ts);

-- Freigabelinks: Grundlage fuer den lesenden Zugriff anderer Staebe auf den
-- GESAMTEN Einsatz. Der Link selbst gibt KEINEN Zugriff (kein anonymer
-- Direktaufruf); er wird vom Eigentuemer eines anderen Einsatzes in dessen
-- Verwaltung eingetragen (einsatz_links). Es wird nur der Hash des Tokens
-- gespeichert.
CREATE TABLE IF NOT EXISTS einsatz_share_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    einsatz_id INTEGER NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL DEFAULT '',
    created_by INTEGER,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    expires_at TEXT,
    revoked_at TEXT,
    revoked_by INTEGER,
    last_used_at TEXT,
    use_count INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_einsatz_share_links_einsatz ON einsatz_share_links (einsatz_id);

-- Verknuepfungen: Einsatz B (einsatz_id) nutzt den Freigabelink eines
-- Einsatzes A (share_link_id). Beim Eintragen sofort 'approved' (die
-- Weitergabe des Links ist die Zustimmung des Erstellers). Der Eigentuemer
-- von A kann einzelnen Einsaetzen den Zugriff entziehen ('rejected') und
-- wieder freigeben. Nur 'approved' gibt den Mitgliedern von B Lesezugriff.
-- (Ein frueher vorgesehener Zustand 'pending' wird nicht mehr vergeben.)
CREATE TABLE IF NOT EXISTS einsatz_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    einsatz_id INTEGER NOT NULL,
    share_link_id INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'approved' CHECK (status IN ('approved','rejected')),
    label TEXT NOT NULL DEFAULT '',
    requested_by INTEGER,
    requested_at TEXT NOT NULL DEFAULT (datetime('now')),
    decided_by INTEGER,
    decided_at TEXT,
    removed_at TEXT,
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE,
    FOREIGN KEY (share_link_id) REFERENCES einsatz_share_links (id) ON DELETE CASCADE,
    FOREIGN KEY (requested_by) REFERENCES users (id) ON DELETE SET NULL,
    FOREIGN KEY (decided_by) REFERENCES users (id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_einsatz_links_einsatz ON einsatz_links (einsatz_id, removed_at);
CREATE INDEX IF NOT EXISTS idx_einsatz_links_share ON einsatz_links (share_link_id, status, removed_at);

-- Vollstaendige Export-Snapshots (z.B. beim Abschluss).
CREATE TABLE IF NOT EXISTS einsatz_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    einsatz_id INTEGER NOT NULL,
    kind TEXT NOT NULL DEFAULT 'manual',   -- close|manual|import
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    created_by INTEGER,
    export_json TEXT NOT NULL,
    FOREIGN KEY (einsatz_id) REFERENCES einsaetze (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_einsatz_snapshots_einsatz ON einsatz_snapshots (einsatz_id, created_at);

-- ---------------------------------------------------------------------------
-- Server-Einstellungen des globalen Admins (Admin > Server): Schluessel/Wert,
-- gehen den Umgebungsvariablen vor (z.B. tile_source, tile_public, tile_token).
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_by INTEGER
);

-- ---------------------------------------------------------------------------
-- Sitzungen (Go-Fassung): serverseitig in der Datenbank, damit sie einen
-- Neustart des Prozesses (Update) ueberleben. data ist JSON.
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    data TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_sessions_last_seen ON sessions (last_seen);
