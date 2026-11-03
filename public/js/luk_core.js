// luk_core.js - gemeinsame Client-Bibliothek fuer alle Einsatz-Seiten.
//
//   LUK.api.get(api, action, params)   / LUK.api.post(api, action, body)
//   LUK.sync                           Live-Sync (Polling auf events?since=seq)
//   LUK.tree                           Tree-Modell (Knoten, Mitglieder, Schichten)
//   LUK.staerke                        Staerke-/Schicht-/Ampel-Berechnung (zentral!)
//   LUK.ui                             esc, toast, Datumsformat, Status-Badges, Druck
//
// Erwartet window.STAB (von PageHelper::printStabScript gesetzt).
(function () {
  'use strict';

  const CFG = window.STAB || {};
  const CTX = CFG.ctx || null;
  const API_URLS = Object.assign({
    einsatz: '/api/einsatz', lagekarten: '/api/lagekarten', schadenskonten: '/api/schadenskonten', kraefte: '/api/kraefte'
  }, CFG.api || {});

  // ------------------------------------------------------------------
  // API
  // ------------------------------------------------------------------
  class LukError extends Error {
    constructor(code, status, extra) {
      super(code);
      this.code = code;
      this.status = status;
      this.extra = extra || {};
    }
  }

  const ERROR_TEXT = {
    unauthorized: 'Sitzung abgelaufen. Bitte neu anmelden.',
    forbidden: 'Keine Berechtigung für diese Aktion.',
    forbidden_admin: 'Admin-Konten haben keinen Zugriff auf Einsätze.',
    invalid_vorwarn: 'Vorwarnzeit ungültig (0 bis 168 Stunden).',
    link_invalid: 'Freigabelink unbekannt, abgelaufen oder widerrufen.',
    link_self: 'Ein Einsatz kann nicht mit sich selbst verknüpft werden.',
    link_exists: 'Dieser Freigabelink ist für den Einsatz bereits eingetragen.',
    forbidden_leiter: 'Dafür ist die Kontorolle „Leiter" erforderlich.',
    einsatz_closed: 'Der Einsatz ist abgeschlossen und schreibgeschützt.',
    conflict: 'Der Datensatz wurde zwischenzeitlich geändert. Ansicht wurde aktualisiert, bitte Änderung erneut vornehmen.',
    not_found: 'Datensatz nicht gefunden (wurde er gelöscht?).',
    has_children: 'Die Führungsebene hat noch untergeordnete Einheiten. Bitte diese zuerst verschieben oder löschen.',
    has_members: 'Die Führungsstelle hat noch Mitglieder (Fachberater / interne Einheiten).',
    cycle: 'Ein Knoten kann nicht unter sich selbst verschoben werden.',
    not_fuehrung: 'Nur an Führungsstellen möglich.',
    fuehrung_no_schicht: 'Führungsstellen haben keine eigenen Schichten. Schichten gehören zu Mitgliedern oder Einheiten.',
    fuehrung_no_status: 'Führungsstellen haben keinen eigenen Anfahrtsstatus. Den führen die Einheiten und Fachberater an dieser Stelle.',
    invalid_staerke: 'Stärke bitte im Format Führer/Unterführer/Mannschaft angeben, z.B. 1/3/8.',
    invalid_url: 'Bitte eine gültige http(s)-Adresse angeben.',
    ref_assigned: 'Diese Einheit ist bereits einem anderen Schadenskonto zugeordnet.',
    csrf: 'Sicherheits-Token ungültig. Bitte Seite neu laden.',
    payload_too_large: 'Die Daten sind zu groß.',
    invalid_format: 'Die Datei ist kein LuK-Einsatz-Export.',
    unsupported_version: 'Die Export-Version wird nicht unterstützt.',
    missing_title: 'Bitte einen Titel angeben.',
    server_error: 'Serverfehler. Bitte erneut versuchen.',
    network: 'Keine Verbindung zum Server.',
    no_fields: 'Keine Änderungen zu speichern.',
    ref_not_found: 'Die verknüpfte Einheit existiert nicht (mehr).',
    invalid_ref: 'Ungültige Verknüpfung.',
    id_exists: 'Dieses Objekt existiert bereits.',
    invalid_geometry: 'Ungültige Geometrie.',
    invalid_symbol: 'Ungültiges taktisches Zeichen.',
    missing_time: 'Bitte Beginn und Ende der Schicht angeben.',
    invalid_kind: 'Ungültiger Objekttyp.',
    parent_not_found: 'Übergeordnetes Element nicht gefunden.',
    user_not_found: 'Nutzer nicht gefunden.',
    missing_name: 'Bitte einen Namen angeben.',
    invalid_date: 'Ungültiges Datum.',
    missing_file: 'Bitte eine Datei auswählen.',
    divera_disabled: 'Divera ist für dieses Konto nicht freigeschaltet (Recht oder Zugang der Organisation fehlt).',
    divera_forbidden: 'Status setzen in Divera ist für dieses Konto nicht erlaubt.',
    divera_upstream: 'Divera ist gerade nicht erreichbar.',
    divera_auth_failed: 'Divera lehnt den Schlüssel der Organisation ab. Bitte den Zugang unter „Organisation verwalten“ prüfen.',
    stein_auth_failed: 'STEIN lehnt den Schlüssel der Organisation ab. Bitte den Zugang unter „Organisation verwalten“ prüfen.',
    org_not_found: 'Organisation nicht gefunden.',
    invalid_id: 'Ungültige Kennung.',
    stein_not_configured: 'Für die Organisation ist kein STEIN-Zugang hinterlegt.',
    invalid_json: 'Die Datei enthält kein gültiges JSON.',
    not_einheit: 'Nur interne Einheiten können umgewandelt werden.',
    invalid_status: 'Ungültiger Status.',
    invalid_ebene: 'Ungültige Stabsebene.',
    invalid_kraft_key: 'Ungültiger Verweis in den Kräftekatalog.',
    invalid_bereich: 'Unbekannter Bereich.',
    invalid_coords: 'Koordinaten ungültig (Breite -90…90, Länge -180…180).',
  };

  function errorText(err) {
    if (!err) return 'Unbekannter Fehler';
    if (err instanceof LukError) return ERROR_TEXT[err.code] || ('Fehler: ' + err.code);
    return err.message || String(err);
  }

  async function request(apiKey, action, opts) {
    opts = opts || {};
    const base = API_URLS[apiKey] || apiKey;
    const url = new URL(base, window.location.origin);
    url.searchParams.set('action', action);
    if (opts.params) {
      Object.keys(opts.params).forEach(k => {
        if (opts.params[k] !== undefined && opts.params[k] !== null) url.searchParams.set(k, String(opts.params[k]));
      });
    }
    const init = { method: opts.method || 'GET', credentials: 'same-origin' };
    if (opts.body) {
      init.headers = { 'Content-Type': 'application/json' };
      const payload = Object.assign({ csrf: CFG.csrf || '' }, opts.body);
      if (CTX && CTX.uuid && payload.einsatz === undefined) payload.einsatz = CTX.uuid;
      init.body = JSON.stringify(payload);
    }
    let res;
    try {
      res = await fetch(url.toString(), init);
    } catch (e) {
      throw new LukError('network', 0, {});
    }
    let data = null;
    try { data = await res.json(); } catch (e) { data = null; }
    if (!res.ok || !data || data.error) {
      const code = (data && data.error) || ('http_' + res.status);
      const extra = data ? Object.assign({}, data) : {};
      delete extra.error;
      if (code === 'unauthorized' && !(CTX && CTX.is_share)) {
        // Sitzung weg (Logout in anderem Tab, Ablauf): sofort zum Login,
        // nicht erst Fehlermeldungen in der halb geladenen Seite zeigen.
        window.location.replace('/login?redirect=' + encodeURIComponent(location.pathname + location.search));
        // Aufrufer nicht weiterlaufen lassen (kein Toast, kein Rendern).
        await new Promise(() => {});
      }
      throw new LukError(code, res.status, extra);
    }
    return data;
  }

  const api = {
    get: (apiKey, action, params) => request(apiKey, action, { params }),
    post: (apiKey, action, body) => request(apiKey, action, { method: 'POST', body: body || {} }),
    LukError,
    errorText,
  };

  // ------------------------------------------------------------------
  // Tree-Modell
  // ------------------------------------------------------------------
  const tree = {
    nodes: new Map(),
    mitglieder: new Map(),
    schichten: new Map(),
    seq: 0,

    load(data) {
      this.nodes.clear(); this.mitglieder.clear(); this.schichten.clear();
      (data.nodes || []).forEach(n => this.nodes.set(n.id, n));
      (data.mitglieder || []).forEach(m => this.mitglieder.set(m.id, m));
      (data.schichten || []).forEach(s => this.schichten.set(s.id, s));
      if (typeof data.seq === 'number') this.seq = data.seq;
    },
    // Wendet ein Sync-Ereignis an. Liefert true, wenn sich der Tree aendert.
    applyEvent(ev) {
      const a = ev.after || {};
      if (ev.entity === 'node') {
        if (ev.action === 'delete' || a.deleted_at) {
          this.removeNode(ev.entity_id);
        } else if (a.id) {
          this.nodes.set(a.id, a);
        }
        return true;
      }
      if (ev.entity === 'mitglied') {
        if (ev.action === 'delete' || a.deleted_at) {
          this.removeMitglied(ev.entity_id);
        } else if (a.id) {
          this.mitglieder.set(a.id, a);
        }
        return true;
      }
      if (ev.entity === 'schicht') {
        if (ev.action === 'delete' || a.deleted_at) {
          this.schichten.delete(ev.entity_id);
        } else if (a.id) {
          this.schichten.set(a.id, a);
        }
        return true;
      }
      return false;
    },
    removeNode(id) {
      this.nodes.delete(id);
      Array.from(this.mitglieder.values()).filter(m => m.node_id === id).forEach(m => this.mitglieder.delete(m.id));
      Array.from(this.schichten.values()).filter(s => s.node_id === id).forEach(s => this.schichten.delete(s.id));
    },
    removeMitglied(id) {
      this.mitglieder.delete(id);
      Array.from(this.schichten.values()).filter(s => s.mitglied_id === id).forEach(s => this.schichten.delete(s.id));
    },
    upsert(row) {
      if (!row || !row.id) return;
      if (row.deleted_at) { this.nodes.delete(row.id); this.mitglieder.delete(row.id); this.schichten.delete(row.id); return; }
      if ('parent_id' in row && 'type' in row) this.nodes.set(row.id, row);
      else if ('kind' in row && 'node_id' in row) this.mitglieder.set(row.id, row);
      else if ('von' in row) this.schichten.set(row.id, row);
    },
    sortNodes(list) {
      return list.slice().sort((a, b) => (a.sort_order - b.sort_order) || String(a.id).localeCompare(String(b.id)));
    },
    roots() { return this.sortNodes(Array.from(this.nodes.values()).filter(n => !n.parent_id || !this.nodes.has(n.parent_id))); },
    children(parentId) { return this.sortNodes(Array.from(this.nodes.values()).filter(n => n.parent_id === parentId)); },
    mitgliederOf(nodeId, kind) {
      return Array.from(this.mitglieder.values())
        .filter(m => m.node_id === nodeId && (!kind || m.kind === kind))
        .sort((a, b) => (a.sort_order - b.sort_order) || String(a.id).localeCompare(String(b.id)));
    },
    schichtenOf(nodeId, mitgliedId) {
      return Array.from(this.schichten.values())
        .filter(s => s.node_id === nodeId && (mitgliedId ? s.mitglied_id === mitgliedId : !s.mitglied_id))
        .sort((a, b) => String(a.von).localeCompare(String(b.von)));
    },
    descendants(nodeId) {
      const out = [];
      const walk = id => this.children(id).forEach(c => { out.push(c); walk(c.id); });
      walk(nodeId);
      return out;
    },
    isDescendant(candidateId, ancestorId) {
      let cur = this.nodes.get(candidateId);
      let guard = 0;
      while (cur && guard++ < 1000) {
        if (cur.id === ancestorId) return true;
        cur = cur.parent_id ? this.nodes.get(cur.parent_id) : null;
      }
      return false;
    },
    path(nodeId) {
      const parts = [];
      let cur = this.nodes.get(nodeId);
      let guard = 0;
      while (cur && guard++ < 100) {
        parts.unshift(this.label(cur));
        cur = cur.parent_id ? this.nodes.get(cur.parent_id) : null;
      }
      return parts.join(' › ');
    },
    label(n) { return n ? (n.bezeichnung || '(ohne Bezeichnung)') : '?'; },
    mitgliedLabel(m) {
      if (!m) return '?';
      if (m.kind === 'fachberater') return ((m.funktion || 'Fachberater') + (m.name ? ' · ' + m.name : ''));
      return m.bezeichnung || '(ohne Bezeichnung)';
    },
    // Aufloesung einer Verknuepfung {ref_kind, ref_id} -> Anzeigeobjekt
    resolveRef(kind, id) {
      if (!kind || !id) return null;
      if (kind === 'node') {
        const n = this.nodes.get(id);
        return n ? { kind: 'node', id: n.id, row: n, label: this.label(n), funkruf: n.funkruf, ov: n.ov, status: n.status,
                     path: this.path(n.id), type: n.type } : null;
      }
      const m = this.mitglieder.get(id);
      if (!m) return null;
      const parent = this.nodes.get(m.node_id);
      return { kind: 'mitglied', id: m.id, row: m, label: this.mitgliedLabel(m), funkruf: m.funkruf, ov: m.ov, status: m.status,
               path: (parent ? this.path(parent.id) + ' › ' : '') + this.mitgliedLabel(m), type: m.kind };
    },
    // Alle Einheiten (Knoten + Mitglieder) als flache Liste fuer Auswahlfelder
    allUnits() {
      const out = [];
      const walk = (n, depth) => {
        out.push({ kind: 'node', id: n.id, label: this.label(n), depth, type: n.type, row: n });
        this.mitgliederOf(n.id).forEach(m => out.push({ kind: 'mitglied', id: m.id, label: this.mitgliedLabel(m), depth: depth + 1, type: m.kind, row: m }));
        this.children(n.id).forEach(c => walk(c, depth + 1));
      };
      this.roots().forEach(r => walk(r, 0));
      return out;
    },
  };

  // ------------------------------------------------------------------
  // Staerke / Schichten / Ampel (zentrale Logik fuer alle Seiten)
  // ------------------------------------------------------------------
  function parseStaerke(s) {
    const m = String(s || '').match(/^\s*(\d+)\s*\/\s*(\d+)\s*\/\s*(\d+)/);
    if (!m) return null;
    return [parseInt(m[1], 10), parseInt(m[2], 10), parseInt(m[3], 10)];
  }
  function fmtStaerke(arr) {
    if (!arr) return '0/0/0//0';
    return arr[0] + '/' + arr[1] + '/' + arr[2] + '//' + (arr[0] + arr[1] + arr[2]);
  }
  function add3(a, b) { a[0] += b[0]; a[1] += b[1]; a[2] += b[2]; return a; }
  function parseTime(s) {
    if (!s) return null;
    const t = Date.parse(String(s));
    return isNaN(t) ? null : t;
  }
  function shiftActiveAt(s, atMs) {
    if (!s || s.aktiv === false) return false;
    const v = parseTime(s.von), b = parseTime(s.bis);
    if (v === null || b === null) return false;
    return atMs >= v && atMs <= b;
  }
  function activeShiftStaerke(schichten, atMs) {
    const acc = [0, 0, 0];
    (schichten || []).forEach(s => {
      if (!shiftActiveAt(s, atMs)) return;
      const t = parseStaerke(s.staerke);
      if (t) add3(acc, t);
    });
    return acc;
  }
  // Aktuelle Schicht: laeuft gerade, sonst die naechste geplante.
  function currentShift(schichten, atMs) {
    const list = (schichten || []).filter(s => s && s.aktiv !== false);
    const running = list.filter(s => shiftActiveAt(s, atMs)).sort((a, b) => (parseTime(a.von) || 0) - (parseTime(b.von) || 0));
    if (running.length) return { shift: running[0], state: 'running' };
    const future = list.filter(s => (parseTime(s.von) || 0) > atMs).sort((a, b) => (parseTime(a.von) || 0) - (parseTime(b.von) || 0));
    if (future.length) return { shift: future[0], state: 'planned' };
    return { shift: null, state: 'none' };
  }
  // Eigenstaerke eines Knotens: eigene Schichten (nur Einheit) + Schichten aller Mitglieder
  function ownStaerke(nodeId, atMs) {
    const n = tree.nodes.get(nodeId);
    if (!n) return [0, 0, 0];
    const acc = [0, 0, 0];
    if (n.type !== 'fuehrung') add3(acc, activeShiftStaerke(tree.schichtenOf(nodeId, null), atMs));
    tree.mitgliederOf(nodeId).forEach(m => add3(acc, activeShiftStaerke(tree.schichtenOf(nodeId, m.id), atMs)));
    return acc;
  }
  function sumStaerke(nodeId, atMs) {
    const acc = ownStaerke(nodeId, atMs);
    tree.children(nodeId).forEach(c => add3(acc, sumStaerke(c.id, atMs)));
    return acc;
  }
  function unitStaerke(kind, id, atMs) {
    if (kind === 'node') return ownStaerke(id, atMs);
    const m = tree.mitglieder.get(id);
    if (!m) return [0, 0, 0];
    return activeShiftStaerke(tree.schichtenOf(m.node_id, m.id), atMs);
  }
  // Ampel: >=2 kuenftige Schichten gruen, 1 gelb, nur laufende rot, sonst null
  function ampelForUnit(schichten, nowMs) {
    const now = nowMs || Date.now();
    let plannedAhead = 0, hasCurrent = false;
    (schichten || []).forEach(s => {
      if (!s || s.aktiv === false) return;
      const v = parseTime(s.von), b = parseTime(s.bis);
      if (v === null || b === null) return;
      if (v > now) plannedAhead++;
      else if (b >= now) hasCurrent = true;
    });
    if (plannedAhead >= 2) return 'green';
    if (plannedAhead === 1) return 'yellow';
    if (hasCurrent) return 'red';
    return null;
  }
  function nodeAmpel(nodeId, nowMs) {
    const n = tree.nodes.get(nodeId);
    if (!n) return null;
    const colors = [];
    if (n.type === 'einheit') { const c = ampelForUnit(tree.schichtenOf(nodeId, null), nowMs); if (c) colors.push(c); }
    tree.mitgliederOf(nodeId).forEach(m => { const c = ampelForUnit(tree.schichtenOf(nodeId, m.id), nowMs); if (c) colors.push(c); });
    tree.children(nodeId).forEach(c => { const k = nodeAmpel(c.id, nowMs); if (k) colors.push(k); });
    if (colors.indexOf('red') !== -1) return 'red';
    if (colors.indexOf('yellow') !== -1) return 'yellow';
    if (colors.indexOf('green') !== -1) return 'green';
    return null;
  }
  function ampelLabel(color) {
    return color === 'green' ? 'Genug Schichten geplant'
      : color === 'yellow' ? 'Nur eine Schicht im Vorfeld'
      : color === 'red' ? 'Keine Schicht mehr geplant' : '';
  }

  // Status einer Einheit (Knoten oder Mitglied) unter Beruecksichtigung ihrer
  // Schichten. Hat mindestens eine aktive Schicht einen eigenen Status
  // (!= geplant), gilt der "staerkste" davon: vor Ort > auf Anfahrt >
  // angefordert > abgerueckt. Sonst zaehlt der Status der Einheit selbst
  // (Fuehrungsharke). derived=true heisst: Status wird ueber die Schichten
  // gepflegt (Kraefteplanung / Kraefteuebersicht), nicht an der Einheit.
  const STATUS_RANK = { vor_ort: 4, anfahrt: 3, angefordert: 2, abgerueckt: 1 };
  function unitStatus(kind, row) {
    if (!row) return { status: 'geplant', status_at: null, derived: false, shifts: [] };
    const schichten = kind === 'node' ? tree.schichtenOf(row.id, null) : tree.schichtenOf(row.node_id, row.id);
    const withStatus = schichten.filter(s => s.aktiv !== false && s.status && s.status !== 'geplant');
    if (!withStatus.length) return { status: row.status, status_at: row.status_at, derived: false, shifts: [] };
    let best = null;
    withStatus.forEach(s => { if (!best || (STATUS_RANK[s.status] || 0) > (STATUS_RANK[best.status] || 0)) best = s; });
    return { status: best.status, status_at: best.status_at, derived: true, shifts: withStatus };
  }
  // Einheit (Knoten oder Mitglied), zu der eine Schicht gehoert.
  function shiftUnit(s) {
    if (!s) return null;
    if (s.mitglied_id) { const m = tree.mitglieder.get(s.mitglied_id); return m ? { kind: 'mitglied', row: m, label: tree.mitgliedLabel(m), parentId: m.node_id } : null; }
    const n = tree.nodes.get(s.node_id);
    return n ? { kind: 'node', row: n, label: tree.label(n), parentId: n.parent_id } : null;
  }
  // Schichten, die noch "geplant" sind, aber innerhalb der Vorwarnzeit
  // beginnen (oder schon begonnen haben und noch nicht vorbei sind).
  function dueShifts(nowMs, vorwarnMs) {
    const out = [];
    tree.schichten.forEach(s => {
      if (s.aktiv === false || (s.status && s.status !== 'geplant')) return;
      const v = parseTime(s.von), b = parseTime(s.bis);
      if (v === null || b === null || b < nowMs) return;
      if (v - nowMs <= vorwarnMs) out.push(s);
    });
    return out.sort((a, b) => (parseTime(a.von) || 0) - (parseTime(b.von) || 0));
  }

  const staerke = { parseStaerke, fmtStaerke, parseTime, shiftActiveAt, activeShiftStaerke, currentShift, ownStaerke, sumStaerke, unitStaerke, ampelForUnit, nodeAmpel, ampelLabel, unitStatus, shiftUnit, dueShifts };

  // ------------------------------------------------------------------
  // Live-Sync
  // ------------------------------------------------------------------
  const sync = {
    seq: 0,
    timer: null,
    handlers: [],
    running: false,
    interval: CFG.sync_interval_ms || 7000,
    backoff: 0,
    lastOk: 0,
    onEvent(fn) { this.handlers.push(fn); },
    start(seq) {
      if (typeof seq === 'number') this.seq = seq;
      this.running = true;
      this.schedule(this.interval);
      document.addEventListener('visibilitychange', () => { if (!document.hidden && this.running) this.poll(); });
    },
    stop() { this.running = false; if (this.timer) clearTimeout(this.timer); },
    schedule(ms) {
      if (this.timer) clearTimeout(this.timer);
      if (!this.running) return;
      this.timer = setTimeout(() => this.poll(), ms);
    },
    async poll() {
      if (!this.running || !CTX || !CTX.uuid) return;
      if (document.hidden) { this.schedule(this.interval * 3); return; }
      try {
        const data = await api.get('einsatz', 'events', { einsatz: CTX.uuid, since: this.seq });
        this.backoff = 0;
        this.lastOk = Date.now();
        const events = data.events || [];
        let treeChanged = false;
        events.forEach(ev => {
          if (tree.applyEvent(ev)) treeChanged = true;
        });
        if (typeof data.seq === 'number') this.seq = Math.max(this.seq, data.seq);
        tree.seq = this.seq;
        if (CTX && typeof data.is_open === 'boolean' && data.is_open !== CTX.is_open) {
          ui.toast(data.is_open ? 'Der Einsatz wurde wieder geöffnet. Seite wird neu geladen.' : 'Der Einsatz wurde abgeschlossen. Seite wird neu geladen.', 'warn', 4000);
          setTimeout(() => window.location.reload(), 1500);
          return;
        }
        if (events.length) {
          this.handlers.forEach(fn => { try { fn(events, treeChanged); } catch (e) { console.error('sync handler', e); } });
        }
        setIndicator('ok');
      } catch (e) {
        this.backoff = Math.min(5, this.backoff + 1);
        setIndicator('error');
        if (e instanceof LukError && (e.code === 'forbidden' || e.code === 'not_found')) {
          ui.toast('Der Zugriff auf diesen Einsatz ist nicht mehr möglich.', 'error', 6000);
          this.stop();
          return;
        }
      }
      this.schedule(this.interval * (1 + this.backoff));
    },
    // Nach eigener Schreibaktion sofort nachziehen (andere Clients bekommen es per Poll)
    bump(seq) { if (typeof seq === 'number' && seq > this.seq) { this.seq = seq; tree.seq = seq; } },
  };

  function setIndicator(state) {
    const el = document.getElementById('luk-sync-indicator');
    if (!el) return;
    el.className = 'luk-sync luk-sync-' + state;
    el.title = state === 'ok' ? 'Live-Sync aktiv (zuletzt ' + new Date().toLocaleTimeString('de-DE') + ')' : 'Live-Sync gestört, versuche erneut…';
  }

  // ------------------------------------------------------------------
  // UI-Helfer
  // ------------------------------------------------------------------
  function esc(s) {
    const d = document.createElement('div');
    d.textContent = String(s == null ? '' : s);
    return d.innerHTML;
  }
  function toast(msg, kind, ttlMs) {
    let host = document.getElementById('luk-toast-host');
    if (!host) {
      host = document.createElement('div');
      host.id = 'luk-toast-host';
      document.body.appendChild(host);
    }
    const t = document.createElement('div');
    t.className = 'luk-toast luk-toast-' + (kind || 'info');
    t.textContent = msg;
    host.appendChild(t);
    const ttl = ttlMs || (kind === 'error' ? 6000 : 2500);
    setTimeout(() => { t.classList.add('luk-toast-out'); setTimeout(() => t.remove(), 300); }, ttl);
    return t;
  }
  function fmtDateTime(s) {
    const t = parseTime(s);
    if (t === null) return s ? String(s) : '–';
    return new Date(t).toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'short' });
  }
  function fmtDate(s) {
    const t = parseTime(s);
    if (t === null) return s ? String(s) : '–';
    return new Date(t).toLocaleDateString('de-DE', { dateStyle: 'medium' });
  }
  // Server-Zeitstempel sind UTC ("YYYY-MM-DD HH:MM:SS") -> lokal anzeigen
  function fmtServerTs(s) {
    if (!s) return '–';
    const iso = String(s).replace(' ', 'T') + (String(s).endsWith('Z') ? '' : 'Z');
    const t = Date.parse(iso);
    if (isNaN(t)) return String(s);
    return new Date(t).toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'medium' });
  }
  function toLocalIso(d) {
    const pad = n => String(n).padStart(2, '0');
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) + 'T' + pad(d.getHours()) + ':' + pad(d.getMinutes());
  }
  function statusLabel(st) { return (CFG.status && CFG.status[st]) || st || ''; }
  function statusBadge(st) {
    const span = document.createElement('span');
    span.className = 'luk-status luk-status-' + (st || 'geplant');
    span.textContent = statusLabel(st);
    return span;
  }
  function roleShort(r) { return (CFG.roles && CFG.roles[r] && CFG.roles[r].short) || r; }
  function roleLabel(r) { return (CFG.roles && CFG.roles[r] && CFG.roles[r].label) || r; }
  function ampelDot(color) {
    if (!color) return null;
    const dot = document.createElement('span');
    dot.className = 'luk-ampel luk-ampel-' + color;
    dot.title = ampelLabel(color);
    return dot;
  }
  function uid(prefix) {
    const b = new Uint8Array(16);
    crypto.getRandomValues(b);
    b[6] = (b[6] & 0x0f) | 0x40;
    b[8] = (b[8] & 0x3f) | 0x80;
    const h = Array.from(b, x => x.toString(16).padStart(2, '0')).join('');
    return h.slice(0, 8) + '-' + h.slice(8, 12) + '-' + h.slice(12, 16) + '-' + h.slice(16, 20) + '-' + h.slice(20);
  }
  // Einheitlicher Konflikt-Handler: Zeile aus err.extra.current uebernehmen
  function handleConflict(err, applyCurrent) {
    if (err instanceof LukError && err.code === 'conflict') {
      if (err.extra && err.extra.current && typeof applyCurrent === 'function') applyCurrent(err.extra.current);
      toast(ERROR_TEXT.conflict, 'warn', 5000);
      return true;
    }
    return false;
  }
  function can(cap) { return !!(CTX && CTX.can && CTX.can[cap]); }
  function readonlyReason() {
    if (!CTX) return '';
    if (CTX.is_share) return 'Leseansicht über Freigabelink';
    if (!CTX.is_open) return 'Einsatz abgeschlossen (schreibgeschützt)';
    return 'Für diese Seite nur Leserecht (Rollen: ' + (CTX.roles || []).map(roleShort).join(', ') + ')';
  }
  // Druck ueber den Browser-Druckdialog ("Als PDF speichern").
  function print(title, opts) {
    opts = opts || {};
    document.querySelectorAll('.luk-print-header').forEach(n => n.remove());
    const h = document.createElement('div');
    h.className = 'luk-print-header';
    let meta = [];
    if (CTX) {
      meta.push(CTX.title);
      if (CTX.stab_name) meta.push(CTX.stab_name);
      if (CTX.ebene_label) meta.push(CTX.ebene_label);
    }
    if (opts.showStand !== false) meta.push('Stand: ' + new Date().toLocaleString('de-DE', { dateStyle: 'medium', timeStyle: 'short' }));
    h.innerHTML = '<h1>' + esc(title) + '</h1><div class="luk-print-meta">' + esc(meta.join(' · ')) + '</div>';
    document.body.insertBefore(h, document.body.firstChild);
    document.body.classList.add('luk-printing');
    if (opts.beforePrint) opts.beforePrint();
    const cleanup = () => {
      document.body.classList.remove('luk-printing');
      h.remove();
      if (opts.afterPrint) opts.afterPrint();
      window.removeEventListener('afterprint', cleanup);
    };
    window.addEventListener('afterprint', cleanup);
    setTimeout(() => window.print(), 80);
  }
  function showReadonlyBanner() {
    if (!CTX) return;
    const el = document.getElementById('luk-readonly-banner');
    if (!el) return;
    const text = readonlyReason();
    el.textContent = text;
    el.hidden = false;
  }

  const ui = { esc, toast, fmtDateTime, fmtDate, fmtServerTs, toLocalIso, statusLabel, statusBadge, roleShort, roleLabel, ampelDot, uid, handleConflict, can, readonlyReason, print, showReadonlyBanner, errorText };

  // ------------------------------------------------------------------
  // Kraeftekatalog (LUK.kraefte): Stammdaten aller THW-Einheiten, STEIN-
  // Live-Status und Bindung in offenen Einsaetzen (api/kraefte).
  // Rangfolge fuer Vorschlaege (zentral, alle Seiten):
  //   Bereich: eigen (0) < angrenzende RSt (1) < eigener LV (2) < Nachbar-LV (3)
  //   Verfuegbarkeit: frei (0) < bedingt (1) < unbekannt/keine Live-Daten (2)
  //                   < gebunden (3) < nicht einsatzbereit (4)
  //   danach Entfernung zum Einsatzort.
  // Freitext bleibt ueberall moeglich: der Picker haengt sich nur an ein
  // normales Textfeld und fuellt bei Auswahl weitere Felder.
  // ------------------------------------------------------------------
  const AVAIL = {
    frei:      { rank: 0, label: 'einsatzbereit',          color: '#2ecc71' },
    bedingt:   { rank: 1, label: 'bedingt einsatzbereit',  color: '#f1c40f' },
    unbekannt: { rank: 2, label: 'keine Live-Daten',       color: '#8a2be2' },
    gebunden:  { rank: 3, label: 'im Einsatz',             color: '#003399' },
    nicht:     { rank: 4, label: 'nicht einsatzbereit',    color: '#e74c3c' },
  };
  const BEREICH_LABEL = ['Eigener Bereich', 'Angrenzende Regionalstelle', 'Eigener Landesverband', 'Nachbar-Landesverband'];

  function haversineKm(lat1, lng1, lat2, lng2) {
    const toRad = d => d * Math.PI / 180;
    const R = 6371;
    const dLat = toRad(lat2 - lat1), dLng = toRad(lng2 - lng1);
    const a = Math.sin(dLat / 2) ** 2 + Math.cos(toRad(lat1)) * Math.cos(toRad(lat2)) * Math.sin(dLng / 2) ** 2;
    return R * 2 * Math.atan2(Math.sqrt(a), Math.sqrt(1 - a));
  }

  const kraefte = {
    data: null,
    loadedAt: 0,
    ttlMs: 60000,
    _loading: null,
    _handlers: [],
    byKey: new Map(),      // kraft_key -> { einheit, ov }
    regionen: new Map(),   // key -> region

    available() { return !!(CTX && CTX.uuid); },
    onLoad(fn) { this._handlers.push(fn); if (this.data) { try { fn(this.data); } catch (e) { console.error(e); } } },

    // Katalog laden (gecacht, 60 s). Liefert null, wenn nicht verfuegbar.
    async load(force) {
      if (!this.available()) return null;
      if (!force && this.data && Date.now() - this.loadedAt < this.ttlMs) return this.data;
      if (this._loading) return this._loading;
      this._loading = (async () => {
        try {
          const res = await api.get('kraefte', 'katalog', { einsatz: CTX.uuid });
          this._index(res.katalog || null);
          this.loadedAt = Date.now();
          this._handlers.forEach(fn => { try { fn(this.data); } catch (e) { console.error('kraefte handler', e); } });
        } catch (e) {
          console.warn('Kräftekatalog nicht verfügbar:', errorText(e));
        } finally {
          this._loading = null;
        }
        return this.data;
      })();
      return this._loading;
    },
    _index(data) {
      this.data = data;
      this.byKey = new Map();
      this.regionen = new Map();
      if (!data) return;
      (data.regionen || []).forEach(r => this.regionen.set(r.key, r));
      (data.ovs || []).forEach(ov => (ov.einheiten || []).forEach(e => this.byKey.set(e.key, { einheit: e, ov })));
    },
    lookup(key) { return key ? (this.byKey.get(key) || null) : null; },
    // Lesbarer Name "FGr N 2 · Musterstadt"; ohne Katalog nur der Schluessel
    label(key, opts) {
      const l = this.lookup(key);
      if (!l) return key || '';
      const e = l.einheit;
      const base = e.short + (e.nr ? ' ' + e.nr : '');
      return (opts && opts.short) ? base : base + ' · ' + l.ov.name;
    },
    region(ov) { return ov ? this.regionen.get(ov.region) || null : null; },

    // Bereichsrang 0..3 (siehe oben)
    bereichRank(ov) {
      if (!ov) return 2;
      const reg = this.region(ov);
      if (reg && reg.extern) return 3;
      const bk = (this.data && this.data.bereich_key) || '';
      if (!bk) return 0;
      if (bk.indexOf('ov:') === 0) {
        const ovKey = bk.slice(3);
        if (ov.key === ovKey) return 0;
        const own = this.data.ovs.find(o => o.key === ovKey);
        if (own && own.region === ov.region) return 1;
        return 2;
      }
      if (ov.region === bk) return 0;
      const ownReg = this.regionen.get(bk);
      if (ownReg && (ownReg.nachbarn || []).indexOf(ov.region) !== -1) return 1;
      return 2;
    },
    bereichLabel(rank) { return BEREICH_LABEL[rank] || ''; },

    // Entfernung Heimatstandort -> Einsatzort (oder Kartenzentrum) in km
    distKm(ov) {
      if (!ov || ov.lat == null || ov.lng == null) return null;
      const ort = this.data && this.data.ort;
      let lat = ort && ort.lat != null ? ort.lat : null, lng = ort && ort.lng != null ? ort.lng : null;
      if (lat === null) { const mc = CFG.map_center || {}; lat = mc.lat; lng = mc.lng; }
      if (lat == null || lng == null) return null;
      return haversineKm(ov.lat, ov.lng, lat, lng);
    },
    hasOrt() { return !!(this.data && this.data.ort && this.data.ort.lat != null); },
    fmtDist(km) {
      if (km === null || km === undefined) return '';
      return (km < 10 ? km.toFixed(1) : Math.round(km)) + ' km';
    },
    // Grobe Fahrzeit (Luftlinie * 1.3, 65 km/h)
    fmtEta(km) {
      if (km === null || km === undefined) return '';
      const min = Math.round(km * 1.3 / 65 * 60);
      if (min < 60) return '~' + min + ' min';
      return '~' + Math.floor(min / 60) + ' h ' + (min % 60) + ' min';
    },

    // Bindungen, die zum Zeitpunkt atMs (ms) relevant sind
    activeBindungen(einheit, atMs) {
      const list = (einheit && einheit.bindungen) || [];
      if (!atMs) return list;
      return list.filter(b => {
        if (b.kind !== 'schicht' || !b.von || !b.bis) return true;
        const v = parseTime(b.von), e = parseTime(b.bis);
        if (v === null || e === null) return true;
        return v <= atMs && e >= atMs;
      });
    },
    // Verfuegbarkeitsklasse: {cls, rank, label, color, text, bind[], stein}
    avail(einheit, atMs) {
      const st = einheit && einheit.stein;
      const bind = this.activeBindungen(einheit, atMs);
      let cls = 'unbekannt';
      let text = AVAIL.unbekannt.label;
      if (st && st.status) {
        if (st.status === 'ready') { cls = 'frei'; text = AVAIL.frei.label; }
        else if (st.status === 'semiready') { cls = 'bedingt'; text = AVAIL.bedingt.label; }
        else if (st.status === 'inuse') { cls = 'gebunden'; text = 'laut STEIN im Einsatz'; }
        else if (st.status === 'notready') { cls = 'nicht'; text = AVAIL.nicht.label; }
        else if (st.status === 'maint') { cls = 'nicht'; text = 'in der Werkstatt'; }
        if (st.ev && (cls === 'frei' || cls === 'bedingt')) { cls = 'bedingt'; text += ' · Einsatzvorbehalt'; }
        if (typeof st.age_h === 'number' && st.age_h > 24 && cls !== 'unbekannt') {
          text += ' · STEIN-Stand ' + (st.age_h >= 48 ? Math.round(st.age_h / 24) + ' d' : st.age_h + ' h') + ' alt';
        }
      }
      if (bind.length) {
        cls = 'gebunden';
        text = bind.map(b => this.bindText(b)).filter((v, i, a) => a.indexOf(v) === i).join('; ');
      }
      const a = AVAIL[cls];
      return { cls, rank: a.rank, label: a.label, color: a.color, text, bind, stein: st || null };
    },
    bindText(b) {
      if (!b) return '';
      const where = b.own ? 'in diesem Einsatz' : (b.einsatz_title ? 'im Einsatz „' + b.einsatz_title + '“' : 'in anderem Einsatz (' + (b.stab_name || b.ebene_label || 'Stab') + ')');
      const when = b.kind === 'schicht' && b.von ? ' · Schicht ab ' + fmtDateTime(b.von) : '';
      const st = b.status && b.status !== 'geplant' ? ' · ' + statusLabel(b.status) : '';
      return where + when + st;
    },
    // Sortierschluessel
    rank(entry) {
      return entry.bereich * 10 + entry.avail.rank;
    },
    // Kandidatenliste: opts {typ, query, atMs, limit, exclude:Set, typOnly:boolean}
    candidates(opts) {
      opts = opts || {};
      if (!this.data) return [];
      const q = (opts.query || '').trim().toLowerCase();
      const words = q ? q.split(/\s+/).filter(Boolean) : [];
      const out = [];
      this.data.ovs.forEach(ov => {
        (ov.einheiten || []).forEach(e => {
          if (opts.typOnly && opts.typ && e.typ !== opts.typ) return;
          if (opts.exclude && opts.exclude.has(e.key)) return;
          if (words.length) {
            const hay = (e.short + ' ' + e.label + ' ' + (e.nr || '') + ' ' + ov.name + ' ' + (ov.landkreis || '') + ' ' + e.typ).toLowerCase();
            if (!words.every(w => hay.indexOf(w) !== -1)) return;
          }
          const avail = this.avail(e, opts.atMs);
          const dist = this.distKm(ov);
          out.push({ key: e.key, einheit: e, ov, avail, bereich: this.bereichRank(ov), dist, typMatch: !!(opts.typ && e.typ === opts.typ) });
        });
      });
      out.sort((a, b) => {
        // Passender Typ zuerst (wenn nicht ohnehin gefiltert), dann Rang, dann Entfernung
        if (a.typMatch !== b.typMatch) return a.typMatch ? -1 : 1;
        const r = this.rank(a) - this.rank(b);
        if (r) return r;
        const da = a.dist === null ? 1e9 : a.dist, db = b.dist === null ? 1e9 : b.dist;
        if (da !== db) return da - db;
        return a.ov.name.localeCompare(b.ov.name, 'de') || a.einheit.short.localeCompare(b.einheit.short, 'de');
      });
      return typeof opts.limit === 'number' ? out.slice(0, opts.limit) : out;
    },
    sectionLabel(entry) {
      return BEREICH_LABEL[entry.bereich] + ' · ' + (entry.avail.cls === 'gebunden' ? 'bereits im Einsatz' : entry.avail.label);
    },
    // Vorbelegung fuer Formularfelder aus einem Katalog-Eintrag
    fill(entry) {
      const e = entry.einheit, st = e.stein || {};
      return {
        kraft_key: e.key,
        bezeichnung: e.short + (e.nr ? ' ' + e.nr : ''),
        ov: entry.ov.name,
        funkruf: st.radio || '',
      };
    },
    // Taktisches Zeichen zum Typ (relativ zum Katalog) oder null
    symbolFor(key) {
      const l = this.lookup(key);
      const typ = l ? l.einheit.typ : null;
      const map = this.data && this.data.typen;
      return typ && map && map[typ] && map[typ].symbol ? map[typ].symbol : null;
    },
    // Typschluessel einer Katalog-Einheit (fuer "passende zuerst")
    typOf(key) { const l = this.lookup(key); return l ? l.einheit.typ : null; },
    // Zusammenfassung fuer den eigenen Bereich: {frei, bedingt, unbekannt, gebunden, nicht, gesamt}
    summary(atMs, bereichMax) {
      const out = { frei: 0, bedingt: 0, unbekannt: 0, gebunden: 0, nicht: 0, gesamt: 0 };
      if (!this.data) return out;
      const max = typeof bereichMax === 'number' ? bereichMax : 0;
      this.data.ovs.forEach(ov => {
        if (this.bereichRank(ov) > max) return;
        (ov.einheiten || []).forEach(e => { out[this.avail(e, atMs).cls]++; out.gesamt++; });
      });
      return out;
    },

    // --- UI-Bausteine -------------------------------------------------
    dot(avail, title) {
      const s = document.createElement('span');
      s.className = 'luk-kp-dot luk-kp-dot-' + avail.cls;
      s.title = title || avail.text;
      return s;
    },
    // Chip "● FGr N · Musterstadt" fuer eine gesetzte Katalog-Einheit.
    // opts: {atMs, onRemove, short}
    chip(key, opts) {
      opts = opts || {};
      const l = this.lookup(key);
      const chip = document.createElement('span');
      chip.className = 'luk-kp-chip';
      if (!l) {
        chip.classList.add('luk-kp-chip-unknown');
        chip.textContent = this.data ? 'Katalog: ' + key + ' (unbekannt)' : 'Katalog: ' + key;
        chip.title = this.data ? 'Dieser Verweis ist im Kräftekatalog nicht (mehr) bekannt.' : 'Kräftekatalog noch nicht geladen';
      } else {
        const a = this.avail(l.einheit, opts.atMs);
        chip.appendChild(this.dot(a));
        const t = document.createElement('span');
        t.textContent = this.label(key, { short: !!opts.short });
        chip.appendChild(t);
        const info = [a.text];
        const reg = this.region(l.ov);
        if (reg) info.push(reg.kurz || reg.name);
        const d = this.distKm(l.ov);
        if (d !== null) info.push(this.fmtDist(d) + (this.hasOrt() ? ' zum Einsatzort' : ' zum Kartenzentrum'));
        if (l.einheit.stein && l.einheit.stein.radio) info.push('Funk: ' + l.einheit.stein.radio);
        chip.title = info.join('\n');
      }
      if (opts.onRemove) {
        const x = document.createElement('button');
        x.type = 'button';
        x.className = 'luk-kp-chip-x';
        x.title = 'Verweis in den Katalog lösen (Freitext bleibt erhalten)';
        x.textContent = '×';
        x.addEventListener('click', e => { e.preventDefault(); e.stopPropagation(); opts.onRemove(); });
        chip.appendChild(x);
      }
      return chip;
    },

    /**
     * Picker an ein Textfeld haengen. Das Feld bleibt ein normales Freitextfeld;
     * bei Auswahl ruft der Picker opts.onPick(entry) auf.
     * opts: { typ(): string|null  Typschluessel fuer "passende zuerst" / Filter,
     *         atMs(): number       Bewertungszeitpunkt,
     *         exclude(): Set,      Schluessel, die nicht angeboten werden,
     *         onPick(entry),       Auswahl
     *         minChars: 0 }
     */
    picker(input, opts) {
      if (!input || input._lukPicker) return input ? input._lukPicker : null;
      opts = opts || {};
      const self = this;
      const wrap = document.createElement('span');
      wrap.className = 'luk-kp-wrap';
      input.parentNode.insertBefore(wrap, input);
      wrap.appendChild(input);
      const pop = document.createElement('div');
      pop.className = 'luk-kp-pop';
      pop.hidden = true;
      wrap.appendChild(pop);
      input.setAttribute('autocomplete', 'off');
      let items = [];
      let hi = -1;
      let typOnly = true;
      let blurTimer = null;

      function render() {
        pop.innerHTML = '';
        hi = -1;
        if (!self.data) {
          pop.innerHTML = '<div class="luk-kp-empty">' + (self.available() ? 'Kräftekatalog wird geladen…' : 'Kräftekatalog nicht verfügbar.') + '</div>';
          if (self.available()) self.load().then(() => { if (!pop.hidden) render(); });
          return;
        }
        const typ = typeof opts.typ === 'function' ? opts.typ() : (opts.typ || null);
        const atMs = typeof opts.atMs === 'function' ? opts.atMs() : opts.atMs;
        const q = input.value;
        const head = document.createElement('div');
        head.className = 'luk-kp-head';
        const info = document.createElement('span');
        info.className = 'luk-muted';
        const stein = self.data.stein || {};
        info.textContent = stein.ok ? ('STEIN-Stand ' + (stein.stand ? fmtDateTime(stein.stand) : '–') + (stein.stale ? ' (veraltet)' : '')) : (stein.aktiv === false ? 'STEIN-Daten für diesen Einsatz abgeschaltet' : 'ohne STEIN-Live-Daten');
        head.appendChild(info);
        if (typ) {
          const tl = self.data.typen && self.data.typen[typ] ? self.data.typen[typ].short : typ;
          const tgl = document.createElement('button');
          tgl.type = 'button';
          tgl.className = 'luk-kp-filter' + (typOnly ? ' luk-kp-filter-on' : '');
          tgl.textContent = 'nur ' + tl;
          tgl.title = typOnly ? 'Alle Typen anzeigen' : 'Nur Einheiten vom Typ ' + tl;
          tgl.addEventListener('mousedown', e => { e.preventDefault(); typOnly = !typOnly; render(); });
          head.appendChild(tgl);
        }
        pop.appendChild(head);
        items = self.candidates({ typ, typOnly: !!typ && typOnly, query: q, atMs, exclude: typeof opts.exclude === 'function' ? opts.exclude() : opts.exclude, limit: q.trim() ? 80 : 40 });
        if (!items.length) {
          const e = document.createElement('div');
          e.className = 'luk-kp-empty';
          e.textContent = 'Keine passende Einheit im Katalog. Der eingegebene Text wird als Freitext übernommen.';
          pop.appendChild(e);
          return;
        }
        let lastSec = null;
        items.forEach((it, idx) => {
          const sec = self.sectionLabel(it);
          if (sec !== lastSec) {
            const h = document.createElement('div');
            h.className = 'luk-kp-sec luk-kp-sec-' + it.avail.cls;
            h.textContent = sec;
            pop.appendChild(h);
            lastSec = sec;
          }
          const row = document.createElement('div');
          row.className = 'luk-kp-row';
          row.setAttribute('data-idx', String(idx));
          row.appendChild(self.dot(it.avail));
          const main = document.createElement('span');
          main.className = 'luk-kp-main';
          main.textContent = it.einheit.short + (it.einheit.nr ? ' ' + it.einheit.nr : '') + ' · ' + it.ov.name;
          row.appendChild(main);
          const meta = document.createElement('span');
          meta.className = 'luk-kp-meta';
          const parts = [];
          const reg = self.region(it.ov);
          if (reg) parts.push(reg.kurz || reg.name);
          if (it.dist !== null) parts.push(self.fmtDist(it.dist));
          if (it.avail.cls === 'gebunden' || it.avail.cls === 'bedingt' || it.avail.cls === 'nicht') parts.push(it.avail.text);
          meta.textContent = parts.join(' · ');
          row.appendChild(meta);
          row.title = it.einheit.label + '\n' + it.avail.text;
          row.addEventListener('mousedown', e => { e.preventDefault(); pick(idx); });
          pop.appendChild(row);
        });
        const foot = document.createElement('div');
        foot.className = 'luk-kp-foot luk-muted';
        foot.textContent = 'Auswahl übernimmt Bezeichnung, OV und Katalog-Verweis. Ohne Auswahl bleibt der Text als Freitext.';
        pop.appendChild(foot);
      }
      function show() { if (pop.hidden) { pop.hidden = false; } render(); }
      function hide() { pop.hidden = true; }
      function setHi(n) {
        const rows = pop.querySelectorAll('.luk-kp-row');
        if (!rows.length) return;
        hi = Math.max(0, Math.min(rows.length - 1, n));
        rows.forEach((r, i) => r.classList.toggle('luk-kp-row-hi', i === hi));
        rows[hi].scrollIntoView({ block: 'nearest' });
      }
      function pick(idx) {
        const it = items[idx];
        if (!it) return;
        hide();
        if (opts.onPick) opts.onPick(it);
      }
      input.addEventListener('focus', () => { if (opts.openOnFocus !== false) show(); });
      input.addEventListener('input', () => { show(); });
      input.addEventListener('blur', () => { blurTimer = setTimeout(hide, 150); });
      input.addEventListener('keydown', e => {
        if (pop.hidden) { if (e.key === 'ArrowDown') { show(); e.preventDefault(); } return; }
        if (e.key === 'ArrowDown') { setHi(hi + 1); e.preventDefault(); }
        else if (e.key === 'ArrowUp') { setHi(hi - 1); e.preventDefault(); }
        else if (e.key === 'Enter') { if (hi >= 0) { e.preventDefault(); pick(hi); } else hide(); }
        else if (e.key === 'Escape') { hide(); e.stopPropagation(); }
      });
      const handle = { show, hide, render, destroy() { if (blurTimer) clearTimeout(blurTimer); pop.remove(); } };
      input._lukPicker = handle;
      return handle;
    },
  };

  window.LUK = { CFG, CTX, api, tree, staerke, sync, ui, kraefte, LukError };
})();
