// anfahrt.js - Kraefteuebersicht: vier Spalten (angefordert, auf Anfahrt,
// eingetroffen, Heimfahrt) mit Weiterschalten des Status. Live aus dem Tree.
//
// Datenbasis:
// - Hat eine Einheit (Knoten oder Mitglied) Schichten mit eigenem Status
//   (!= geplant), erscheint je Schicht eine Karte. So stehen bei einer
//   Abloesung zwei Karten derselben Einheit nebeneinander (kommende Schicht
//   "eingetroffen", alte Schicht "Heimfahrt").
// - Sonst zaehlt der Status der Einheit selbst, wie er in der Fuehrungsharke
//   gesetzt wird (reicht fuer Einsaetze mit einer Schicht).
// - Schichten, die noch "geplant" sind, aber innerhalb der Vorwarnzeit des
//   Einsatzes beginnen, werden oben als "faellig" gelistet.
(function () {
  'use strict';
  const L = window.LUK;
  const CTX = L.CTX;
  const T = L.tree;
  const S = L.staerke;
  const ui = L.ui;
  const api = L.api;
  const K = L.kraefte;
  if (!CTX) return;
  const canStatus = ui.can('status');

  // Kraeftekatalog: Heimatstandort -> Entfernung/Fahrzeit zum Einsatzort,
  // Verfuegbarkeit laut STEIN. Schluessel der Schicht geht vor dem der Einheit.
  function katalogLine(kraftKey) {
    if (!kraftKey || !K || !K.lookup(kraftKey)) return '';
    const l = K.lookup(kraftKey);
    const a = K.avail(l.einheit);
    const d = K.distKm(l.ov);
    let html = '<div class="af-card-line af-katalog"><span class="luk-kp-dot luk-kp-dot-' + a.cls + '" title="' + ui.esc(a.text) + '"></span> ' + ui.esc(K.label(kraftKey));
    if (d !== null) html += ' <span class="luk-muted">· ' + ui.esc(K.fmtDist(d)) + ' / ' + ui.esc(K.fmtEta(d)) + (K.hasOrt() ? '' : ' (bis Kartenzentrum, Einsatzort in der Verwaltung setzen)') + '</span>';
    return html + '</div>';
  }

  // Spalten der Uebersicht: Status-Schluessel -> Anzeige + moegliche Folgeaktionen
  const COLUMNS = [
    { status: 'angefordert', title: 'Angefordert',  next: [['anfahrt', 'Auf Anfahrt', true], ['vor_ort', 'Eingetroffen', false]] },
    { status: 'anfahrt',     title: 'Auf Anfahrt',  next: [['vor_ort', 'Eingetroffen', true], ['angefordert', 'Zurück: angefordert', false]] },
    { status: 'vor_ort',     title: 'Eingetroffen', next: [['abgerueckt', 'Heimfahrt', true], ['anfahrt', 'Zurück: Anfahrt', false]] },
    { status: 'abgerueckt',  title: 'Heimfahrt',    next: [['vor_ort', 'Zurück: vor Ort', false], ['geplant', 'Abschließen (geplant)', false]] },
  ];
  // Heimfahrt-Spalte: standardmaessig nur die letzten 24 h, sonst waechst sie endlos.
  const HEIMFAHRT_WINDOW_MS = 24 * 3600 * 1000;
  const vorwarnMs = () => (typeof CTX.vorwarn_min === 'number' ? CTX.vorwarn_min : 180) * 60000;

  const $board = document.getElementById('af-board');
  const $summary = document.getElementById('af-summary');
  const $due = document.getElementById('af-due');
  const $showAll = document.getElementById('af-show-all');

  async function load() {
    try {
      const data = await api.get('einsatz', 'tree', { einsatz: CTX.uuid });
      T.load(data.tree);
      render();
      L.sync.start(T.seq);
      if (K) { K.onLoad(() => render()); K.load(); }
    } catch (e) { ui.toast('Laden fehlgeschlagen: ' + ui.errorText(e), 'error'); }
  }
  L.sync.onEvent((events, treeChanged) => { if (treeChanged) render(); });

  function sinceMs(row) {
    return row.status_at ? Date.parse(String(row.status_at).replace(' ', 'T') + 'Z') : null;
  }

  function shiftText(s) {
    return 'Schicht ' + ui.fmtDateTime(s.von) + ' – ' + ui.fmtDateTime(s.bis)
      + (s.staerke ? ' · ' + s.staerke : '') + (s.name ? ' · ' + s.name : '');
  }

  /**
   * Alle Kraefte nach Status gruppiert: {status: [item, ...]}.
   * item = { kind: 'node'|'mitglied'|'schicht', row, unit:{kind,row,label}, label, path, since }
   */
  function collect() {
    const now = Date.now();
    const showAll = $showAll.checked;
    const groups = {};
    COLUMNS.forEach(c => { groups[c.status] = []; });
    const push = (kind, row, unit, path) => {
      const st = row.status;
      if (!groups[st]) return;
      const since = sinceMs(row);
      if (st === 'abgerueckt' && !showAll && since && now - since > HEIMFAHRT_WINDOW_MS) return;
      groups[st].push({ kind, row, unit, label: unit.label, path, since });
    };
    const takeUnit = (kind, row, label, parentId) => {
      const unit = { kind, row, label };
      const path = T.path(parentId);
      const us = S.unitStatus(kind, row);
      if (us.derived) {
        us.shifts.forEach(s => push('schicht', s, unit, path));
      } else {
        push(kind, row, unit, path);
      }
    };
    // Fuehrungsstellen (TEL, EAL, ...) ruecken nicht selbst an - den Status
    // fuehren die Einheiten und Fachberater an der Stelle.
    T.nodes.forEach(n => { if (n.type !== 'fuehrung') takeUnit('node', n, T.label(n), n.parent_id); });
    T.mitglieder.forEach(m => takeUnit('mitglied', m, T.mitgliedLabel(m), m.node_id));
    Object.values(groups).forEach(list => list.sort((a, b) => (a.since || 0) - (b.since || 0)));
    return groups;
  }

  function fmtDuration(ms) {
    if (ms === null || isNaN(ms)) return '';
    const min = Math.round(Math.abs(ms) / 60000);
    if (min < 60) return min + ' min';
    const h = Math.floor(min / 60);
    if (h < 24) return h + ' h ' + (min % 60) + ' min';
    return Math.floor(h / 24) + ' d ' + (h % 24) + ' h';
  }

  function render() {
    const groups = collect();
    const now = Date.now();
    renderDue(now);

    $summary.innerHTML = COLUMNS.map(c =>
      '<div class="af-tile af-tile-' + c.status + '"><div class="af-tile-num">' + groups[c.status].length + '</div>'
      + '<div class="af-tile-label">' + ui.esc(c.title) + '</div></div>').join('');

    $board.innerHTML = '';
    // Die vier Spalten stehen immer, auch wenn sie leer sind.
    COLUMNS.forEach(c => {
      const col = document.createElement('section');
      col.className = 'af-col af-col-' + c.status;
      const head = document.createElement('h3');
      head.className = 'af-col-title';
      head.innerHTML = ui.esc(c.title) + ' <span class="af-col-count">' + groups[c.status].length + '</span>';
      col.appendChild(head);
      const body = document.createElement('div');
      body.className = 'af-col-body';
      if (!groups[c.status].length) {
        const e = document.createElement('div');
        e.className = 'af-col-empty';
        e.textContent = 'keine';
        body.appendChild(e);
      }
      groups[c.status].forEach(it => body.appendChild(card(it, c, now)));
      col.appendChild(body);
      $board.appendChild(col);
    });
    const total = COLUMNS.reduce((n, c) => n + groups[c.status].length, 0);
    if (!total) {
      const hint = document.createElement('div');
      hint.className = 'af-board-hint luk-muted';
      hint.textContent = 'Aktuell sind keine Kräfte angefordert, auf Anfahrt, vor Ort oder auf Heimfahrt. Der Status wird in der Führungsharke, der Kräfteplanung oder hier gesetzt.';
      $board.appendChild(hint);
    }
  }

  // Faellige Schichten: noch geplant, Beginn innerhalb der Vorwarnzeit
  function renderDue(now) {
    const due = S.dueShifts(now, vorwarnMs());
    if (!due.length) { $due.hidden = true; $due.innerHTML = ''; return; }
    $due.hidden = false;
    $due.innerHTML = '<div class="af-due-title">Demnächst fällig: ' + due.length + ' Schicht' + (due.length === 1 ? '' : 'en')
      + ' noch nicht angefordert <span class="luk-muted">(Vorwarnzeit ' + ui.esc(fmtDuration(vorwarnMs())) + ', einstellbar in der Verwaltung)</span></div>';
    due.forEach(s => {
      const u = S.shiftUnit(s);
      if (!u) return;
      const von = S.parseTime(s.von);
      const row = document.createElement('div');
      row.className = 'af-due-row';
      const overdue = von !== null && von <= now;
      // Anfahrtsdauer laut Kraeftekatalog: zu knapp, wenn Beginn naeher liegt als die geschaetzte Fahrzeit
      const kk = s.kraft_key || (u.row && u.row.kraft_key) || '';
      let eta = '';
      if (kk && K && K.lookup(kk)) {
        const d = K.distKm(K.lookup(kk).ov);
        if (d !== null) {
          const etaMin = Math.round(d * 1.3 / 65 * 60);
          const knapp = !overdue && (von - now) < etaMin * 60000;
          eta = ' <span class="luk-muted' + (knapp ? ' af-due-over' : '') + '" title="Luftlinie ' + ui.esc(K.fmtDist(d)) + ' vom Heimatstandort">Anfahrt ' + ui.esc(K.fmtEta(d)) + (knapp ? ' – knapp!' : '') + '</span>';
        }
      }
      row.innerHTML = '<strong>' + ui.esc(u.label) + '</strong>'
        + '<span class="luk-muted">' + ui.esc(shiftText(s)) + '</span>'
        + '<span class="af-due-when' + (overdue ? ' af-due-over' : '') + '">'
        + (overdue ? 'Beginn vor ' + fmtDuration(now - von) : 'Beginn in ' + fmtDuration(von - now)) + eta + '</span>';
      if (canStatus) {
        const acts = document.createElement('div');
        acts.className = 'af-due-actions luk-no-print';
        acts.appendChild(shiftBtn(s, u.label, 'angefordert', 'Anfordern', true));
        acts.appendChild(shiftBtn(s, u.label, 'anfahrt', 'Auf Anfahrt', false));
        row.appendChild(acts);
      }
      $due.appendChild(row);
    });
  }

  function card(it, col, now) {
    const el = document.createElement('article');
    el.className = 'af-card af-card-' + it.row.status;
    const unitRow = it.unit.row;

    let html = '<div class="af-card-head"><strong>' + ui.esc(it.label) + '</strong></div>';
    if (it.path) html += '<div class="af-path">' + ui.esc(it.path) + '</div>';
    if (it.kind === 'schicht') {
      html += '<div><span class="af-card-shift">' + ui.esc(shiftText(it.row)) + '</span></div>';
      const kontakt = [it.row.funkruf || unitRow.funkruf, it.row.ov || unitRow.ov].filter(Boolean).join(' / ');
      const tel = it.row.telefon || unitRow.telefon;
      if (kontakt || tel) html += '<div class="af-card-line">' + ui.esc(kontakt) + (tel ? (kontakt ? ' · ' : '') + ui.esc(tel) : '') + '</div>';
      html += katalogLine(it.row.kraft_key || unitRow.kraft_key);
      if (it.row.note) html += '<div class="af-card-line luk-muted">' + ui.esc(it.row.note) + '</div>';
    } else {
      const schichten = it.kind === 'node' ? T.schichtenOf(unitRow.id, null) : T.schichtenOf(unitRow.node_id, unitRow.id);
      const cs = S.currentShift(schichten, now);
      const planned = cs.shift
        ? (cs.shift.staerke || '') + (cs.shift.name ? ' · ' + cs.shift.name : '')
          + (cs.state === 'planned' ? ' (ab ' + ui.fmtDateTime(cs.shift.von) + ')' : '')
        : '';
      const stNow = it.kind === 'node' ? S.ownStaerke(unitRow.id, now) : S.unitStaerke('mitglied', unitRow.id, now);
      const hasNow = stNow[0] || stNow[1] || stNow[2];
      const kontakt = [unitRow.funkruf, unitRow.ov].filter(Boolean).join(' / ');
      if (kontakt || unitRow.telefon) {
        html += '<div class="af-card-line">' + ui.esc(kontakt) + (unitRow.telefon ? (kontakt ? ' · ' : '') + ui.esc(unitRow.telefon) : '') + '</div>';
      }
      if (planned || hasNow) {
        html += '<div class="af-card-line luk-muted">Stärke: ' + ui.esc(planned || '–') + (hasNow ? ' · aktiv ' + ui.esc(S.fmtStaerke(stNow)) : '') + '</div>';
      }
      html += katalogLine((cs.shift && cs.shift.kraft_key) || unitRow.kraft_key);
      if (unitRow.note) html += '<div class="af-card-line luk-muted">' + ui.esc(unitRow.note) + '</div>';
    }
    html += '<div class="af-since">' + (it.since
      ? 'seit ' + ui.esc(ui.fmtServerTs(it.row.status_at)) + ' <span class="luk-muted">(' + fmtDuration(now - it.since) + ')</span>'
      : '<span class="luk-muted">ohne Zeitstempel</span>') + '</div>';
    el.innerHTML = html;

    if (canStatus) {
      const acts = document.createElement('div');
      acts.className = 'af-actions luk-no-print';
      col.next.forEach(([status, label, primary]) => {
        acts.appendChild(it.kind === 'schicht'
          ? shiftBtn(it.row, it.label, status, label, primary)
          : unitBtn(it, status, label, primary));
      });
      el.appendChild(acts);
    }
    return el;
  }

  function makeBtn(label, primary, onClick) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'luk-btn luk-btn-small' + (primary ? ' luk-btn-primary' : '');
    b.textContent = label;
    b.addEventListener('click', onClick);
    return b;
  }

  function unitBtn(it, status, label, primary) {
    return makeBtn(label, primary, async () => {
      try {
        const res = await api.post('einsatz', it.kind === 'node' ? 'node_status' : 'mitglied_status', { id: it.row.id, version: it.row.version, status });
        T.upsert(res.node || res.mitglied);
        ui.toast(it.label + ': ' + ui.statusLabel(status), 'ok');
        render();
        L.sync.poll();
      } catch (e) {
        if (!ui.handleConflict(e, cur => T.upsert(cur))) ui.toast(ui.errorText(e), 'error');
        render();
      }
    });
  }

  function shiftBtn(s, unitLabel, status, label, primary) {
    return makeBtn(label, primary, async () => {
      try {
        const res = await api.post('einsatz', 'schicht_status', { id: s.id, version: s.version, status });
        T.upsert(res.schicht);
        ui.toast(unitLabel + ' (Schicht): ' + ui.statusLabel(status), 'ok');
        render();
        L.sync.poll();
      } catch (e) {
        if (!ui.handleConflict(e, cur => T.upsert(cur))) ui.toast(ui.errorText(e), 'error');
        render();
      }
    });
  }

  $showAll.addEventListener('change', render);
  document.getElementById('af-print').addEventListener('click', () => ui.print('Kräfteübersicht'));
  if (!canStatus) ui.showReadonlyBanner();
  setInterval(() => { if (!document.hidden) { render(); if (K) K.load(); } }, 60000);
  load();
})();
