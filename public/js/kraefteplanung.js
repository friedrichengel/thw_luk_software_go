// kraefteplanung.js - Schichten, Stammdaten und Status zu den Einheiten der
// Fuehrungsharke. Arbeitet auf dem gemeinsamen Tree-Modell (LUK.tree);
// jede Aenderung ist ein eigener API-Aufruf mit Versionspruefung, andere
// Clients bekommen sie per Live-Sync (LUK.sync).
//
// Schichten gibt es nur an Einheits-Knoten und an Mitgliedern (Fachberater /
// interne Einheiten) von Fuehrungsstellen. Fuehrungsstellen selbst haben
// keine Schichten; ihre Staerke ergibt sich aus Mitgliedern und Unterbaum.
(function () {
  'use strict';

  const L = window.LUK;
  if (!L || !L.CTX) return;
  const CTX = L.CTX;
  const T = L.tree;
  const S = L.staerke;
  const ui = L.ui;
  const api = L.api;
  const K = L.kraefte;

  const canKraefte = ui.can('kraefte');
  const canStatus = ui.can('status');

  // ---- State ----
  let perspectiveId = '';
  let useCustomTime = false;
  let customTime = '';
  let pendingRender = false;          // Sync-Aenderung waehrend Dialog offen
  const collapsed = new Set();        // eingeklappte <details> (Schluessel, s. detailsKey)
  let availTyp = '';                  // Filter "Verfuegbare Kraefte"
  let availQuery = '';

  // Kontext fuer die Dialoge
  let shiftDlg = { mode: 'create', id: null, version: 0, nodeId: '', mitgliedId: null, kraftKey: '' };
  let metaDlg = { kind: 'node', id: '', version: 0, kraftKey: '' };

  // ---- DOM ----
  const $perspList = document.getElementById('kp-perspective-list');
  const $current = document.getElementById('kp-current-perspective');
  const $currentPath = document.getElementById('kp-current-path');
  const $content = document.getElementById('kp-content');
  const $refresh = document.getElementById('kp-refresh-btn');
  const $useTime = document.getElementById('kp-use-custom-time');
  const $timeRow = document.getElementById('kp-custom-time-row');
  const $time = document.getElementById('kp-custom-time');
  const $staerke = document.getElementById('kp-staerke-overview');
  const $einsatzStaerke = document.getElementById('kp-einsatz-staerke');
  const $printBtn = document.getElementById('kp-print-btn');

  // Schicht-Dialog
  const $sDialog = document.getElementById('kp-shift-dialog');
  const $siTitle = document.getElementById('kp-si-title');
  const $siTarget = document.getElementById('kp-si-target');
  const $siVon = document.getElementById('kp-si-von');
  const $siBis = document.getElementById('kp-si-bis');
  const $siEndBis = document.getElementById('kp-si-end-bis');
  const $siEndDauer = document.getElementById('kp-si-end-dauer');
  const $siDauerH = document.getElementById('kp-si-dauer-h');
  const $siDauerM = document.getElementById('kp-si-dauer-m');
  const $siStaerke = document.getElementById('kp-si-staerke');
  const $siName = document.getElementById('kp-si-name');
  const $siTelefon = document.getElementById('kp-si-telefon');
  const $siFunkruf = document.getElementById('kp-si-funkruf');
  const $siOv = document.getElementById('kp-si-ov');
  const $siNote = document.getElementById('kp-si-note');
  const $siAktiv = document.getElementById('kp-si-aktiv');
  const $siSave = document.getElementById('kp-si-save');
  const $siCancel = document.getElementById('kp-si-cancel');
  const $siKraftSuch = document.getElementById('kp-si-kraft-such');
  const $siKraft = document.getElementById('kp-si-kraft');
  const $miKraft = document.getElementById('kp-mi-kraft');
  const $availTyp = document.getElementById('kp-avail-typ');
  const $availQ = document.getElementById('kp-avail-q');
  const $availSummary = document.getElementById('kp-avail-summary');
  const $availList = document.getElementById('kp-avail-list');
  const $availStand = document.getElementById('kp-avail-stand');

  // Stammdaten-Dialog
  const $mDialog = document.getElementById('kp-meta-dialog');
  const $miTitle = document.getElementById('kp-mi-title');
  const $miBez = document.getElementById('kp-mi-bezeichnung');
  const $miBezLabel = document.getElementById('kp-mi-bez-label');
  const $miFunktion = document.getElementById('kp-mi-funktion');
  const $miFunktionLabel = document.getElementById('kp-mi-funktion-label');
  const $miName = document.getElementById('kp-mi-name');
  const $miNameLabel = document.getElementById('kp-mi-name-label');
  const $miTelefon = document.getElementById('kp-mi-telefon');
  const $miTelefonLabel = document.getElementById('kp-mi-telefon-label');
  const $miFunkruf = document.getElementById('kp-mi-funkruf');
  const $miFunkrufLabel = document.getElementById('kp-mi-funkruf-label');
  const $miOv = document.getElementById('kp-mi-ov');
  const $miNote = document.getElementById('kp-mi-note');
  const $miSave = document.getElementById('kp-mi-save');
  const $miCancel = document.getElementById('kp-mi-cancel');

  // ---- Helfer ----
  function effectiveAt() {
    if (useCustomTime && customTime) {
      const t = S.parseTime(customTime);
      if (t !== null) return t;
    }
    return Date.now();
  }
  function isZero(arr) { return !arr[0] && !arr[1] && !arr[2]; }
  function anyDialogOpen() { return !!(($sDialog && $sDialog.open) || ($mDialog && $mDialog.open)); }
  function harkeUrl() { return '/harke?einsatz=' + encodeURIComponent(CTX.uuid); }

  // Alle Knoten in Baum-Reihenfolge mit Tiefe (fuer die Perspektiven-Liste)
  function allNodesWithDepth() {
    const out = [];
    const walk = (n, depth) => {
      out.push({ node: n, depth });
      T.children(n.id).forEach(c => walk(c, depth + 1));
    };
    T.roots().forEach(r => walk(r, 0));
    return out;
  }

  function ensurePerspective() {
    if (perspectiveId && T.nodes.has(perspectiveId)) return;
    const roots = T.roots();
    perspectiveId = roots.length ? roots[0].id : '';
  }

  function updateUrl() {
    try {
      const url = new URL(window.location.href);
      if (perspectiveId) url.searchParams.set('node', perspectiveId);
      else url.searchParams.delete('node');
      window.history.replaceState(null, '', url.toString());
    } catch (e) { /* ignore */ }
  }

  // Einklapp-Zustand ueber Re-Renders hinweg merken
  function detailsKey(kind, id) { return kind + ':' + id; }
  function makeDetails(className, key) {
    const d = document.createElement('details');
    d.className = className;
    d.open = !collapsed.has(key);
    d.setAttribute('data-key', key);
    d.addEventListener('toggle', () => {
      if (d.open) collapsed.delete(key); else collapsed.add(key);
    });
    return d;
  }
  function marker() {
    const m = document.createElement('span');
    m.className = 'kp-collapse-marker';
    m.setAttribute('aria-hidden', 'true');
    return m;
  }
  function btn(text, onClick, kind, title) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'luk-btn luk-btn-small kp-btn' + (kind === 'danger' ? ' luk-btn-danger' : kind === 'primary' ? ' luk-btn-primary' : '');
    b.textContent = text;
    if (title) b.title = title;
    b.addEventListener('click', e => { e.preventDefault(); e.stopPropagation(); onClick(); });
    return b;
  }

  // ---- Kraeftekatalog ----
  // Katalog-Schluessel der Einheit, zu der eine Schicht gehoert
  function unitKraftKey(nodeId, mitgliedId) {
    const row = mitgliedId ? T.mitglieder.get(mitgliedId) : T.nodes.get(nodeId);
    return row ? (row.kraft_key || '') : '';
  }
  function kraftDot(key, atMs) {
    if (!key || !K) return null;
    const l = K.lookup(key);
    if (!l) return null;
    const a = K.avail(l.einheit, atMs);
    return K.dot(a, K.label(key) + '\n' + a.text);
  }
  function renderKraftLine(host, key, onRemove) {
    if (!host) return;
    host.innerHTML = '';
    if (!key) return;
    host.appendChild(K.chip(key, { atMs: shiftDlgAt(), onRemove }));
  }
  function shiftDlgAt() {
    const t = $siVon && $siVon.value ? S.parseTime($siVon.value) : null;
    return t === null ? effectiveAt() : t;
  }
  // Ueberschneidungen: dieselbe Katalog-Einheit in anderen aktiven Schichten
  // dieses Einsatzes oder (laut Katalog) in anderen Einsaetzen.
  function overlapWarnings(key, vonMs, bisMs, excludeId) {
    const out = [];
    if (!key) return out;
    T.schichten.forEach(s => {
      if (s.id === excludeId || s.aktiv === false || s.kraft_key !== key || s.status === 'abgerueckt') return;
      const v = S.parseTime(s.von), b = S.parseTime(s.bis);
      if (v === null || b === null || b < vonMs || v > bisMs) return;
      const u = S.shiftUnit(s);
      out.push('in diesem Einsatz bei ' + (u ? u.label : '?') + ' (' + ui.fmtDateTime(s.von) + ' – ' + ui.fmtDateTime(s.bis) + ')');
    });
    const l = K ? K.lookup(key) : null;
    if (l) {
      (l.einheit.bindungen || []).forEach(b => {
        if (b.own) return;
        if (b.kind === 'schicht' && b.von && b.bis) {
          const v = S.parseTime(b.von), e = S.parseTime(b.bis);
          if (v !== null && e !== null && (e < vonMs || v > bisMs)) return;
        }
        out.push(K.bindText(b));
      });
    }
    return out;
  }
  if ($siKraftSuch && K) {
    K.picker($siKraftSuch, {
      typ: () => K.typOf(shiftDlg.kraftKey) || K.typOf(unitKraftKey(shiftDlg.nodeId, shiftDlg.mitgliedId)),
      atMs: () => shiftDlgAt(),
      onPick: entry => {
        const f = K.fill(entry);
        shiftDlg.kraftKey = f.kraft_key;
        $siKraftSuch.value = '';
        $siOv.value = f.ov;
        if (f.funkruf) $siFunkruf.value = f.funkruf;
        renderKraftLine($siKraft, shiftDlg.kraftKey, () => { shiftDlg.kraftKey = ''; renderKraftLine($siKraft, ''); });
        if (entry.avail.cls === 'gebunden') ui.toast('Hinweis: ' + K.label(f.kraft_key) + ' – ' + entry.avail.text, 'warn', 6000);
      },
    });
  }
  if ($miOv && K) {
    K.picker($miOv, {
      typ: () => K.typOf(metaDlg.kraftKey),
      onPick: entry => {
        const f = K.fill(entry);
        metaDlg.kraftKey = f.kraft_key;
        $miOv.value = f.ov;
        if (f.funkruf && !$miFunkruf.value.trim()) $miFunkruf.value = f.funkruf;
        renderKraftLine($miKraft, metaDlg.kraftKey, () => { metaDlg.kraftKey = ''; renderKraftLine($miKraft, ''); });
      },
    });
  }
  if ($siVon) $siVon.addEventListener('change', () => { if (shiftDlg.kraftKey) renderKraftLine($siKraft, shiftDlg.kraftKey, () => { shiftDlg.kraftKey = ''; renderKraftLine($siKraft, ''); }); });

  // Bereich "Verfuegbare Kraefte" (linke Spalte)
  function fillAvailTyp() {
    if (!$availTyp || !K || !K.data) return;
    const cur = $availTyp.value;
    $availTyp.innerHTML = '';
    const o0 = document.createElement('option'); o0.value = ''; o0.textContent = 'Alle Typen'; $availTyp.appendChild(o0);
    const used = new Set();
    K.data.ovs.forEach(ov => (ov.einheiten || []).forEach(e => used.add(e.typ)));
    Array.from(used).map(t => [t, (K.data.typen[t] || {}).short || t]).sort((a, b) => a[1].localeCompare(b[1], 'de')).forEach(([t, s]) => {
      const o = document.createElement('option'); o.value = t; o.textContent = s; $availTyp.appendChild(o);
    });
    $availTyp.value = cur;
    availTyp = $availTyp.value;
  }
  function renderAvail() {
    if (!$availList) return;
    if (!K || !K.data) { $availList.innerHTML = '<div class="kp-empty">Kräftekatalog nicht verfügbar.</div>'; return; }
    const atMs = effectiveAt();
    const s = K.summary(atMs, 0);
    $availSummary.innerHTML = '<span class="luk-kp-dot luk-kp-dot-frei"></span> ' + s.frei + ' <span class="luk-kp-dot luk-kp-dot-bedingt"></span> ' + s.bedingt
      + ' <span class="luk-kp-dot luk-kp-dot-unbekannt"></span> ' + s.unbekannt + ' <span class="luk-kp-dot luk-kp-dot-gebunden"></span> ' + s.gebunden
      + ' <span class="luk-kp-dot luk-kp-dot-nicht"></span> ' + s.nicht + ' <span title="Einheiten im eigenen Bereich">(eigener Bereich)</span>';
    const list = K.candidates({ typ: availTyp || null, typOnly: !!availTyp, query: availQuery, atMs, limit: 25 });
    $availList.innerHTML = '';
    if (!list.length) { $availList.innerHTML = '<div class="kp-empty">Keine Einheit gefunden.</div>'; }
    let lastSec = null;
    list.forEach(it => {
      const sec = K.sectionLabel(it);
      if (sec !== lastSec) {
        const h = document.createElement('div'); h.className = 'kp-avail-sec luk-kp-sec luk-kp-sec-' + it.avail.cls; h.textContent = sec; $availList.appendChild(h); lastSec = sec;
      }
      const row = document.createElement('div');
      row.className = 'kp-avail-row';
      row.appendChild(K.dot(it.avail));
      const main = document.createElement('span');
      main.className = 'kp-avail-main';
      main.textContent = it.einheit.short + (it.einheit.nr ? ' ' + it.einheit.nr : '') + ' · ' + it.ov.name;
      row.appendChild(main);
      const meta = document.createElement('span');
      meta.className = 'kp-avail-meta luk-muted';
      meta.textContent = [it.dist !== null ? K.fmtDist(it.dist) : '', it.avail.cls === 'gebunden' ? 'im Einsatz' : ''].filter(Boolean).join(' · ');
      row.appendChild(meta);
      row.title = it.einheit.label + '\n' + it.avail.text + (it.dist !== null ? '\n' + K.fmtDist(it.dist) + ' / ' + K.fmtEta(it.dist) : '') + '\nKlick: Schicht bei der gewählten Einheit anlegen';
      if (canKraefte) {
        row.classList.add('kp-avail-click');
        row.addEventListener('click', () => planFromKatalog(it));
      }
      const a = document.createElement('a');
      a.className = 'kp-avail-harke luk-no-print';
      a.href = '/harke?einsatz=' + encodeURIComponent(CTX.uuid) + '&kraft=' + encodeURIComponent(it.key);
      a.title = 'Als neue Einheit in der Führungsharke anlegen';
      a.textContent = '→ Harke';
      a.addEventListener('click', e => e.stopPropagation());
      row.appendChild(a);
      $availList.appendChild(row);
    });
    const stein = K.data.stein || {};
    $availStand.textContent = stein.ok ? 'STEIN-Stand ' + ui.fmtDateTime(stein.stand) + (stein.stale ? ' (veraltet)' : '')
      : (stein.aktiv === false ? 'STEIN-Daten für diesen Einsatz abgeschaltet (Verwaltung)' : 'keine STEIN-Live-Daten – Status nur aus eigenen Einsätzen');
  }
  // Klick in "Verfuegbare Kraefte": Schicht bei der aktuellen Perspektive
  // (Einheit) anlegen, mit der Katalog-Einheit vorbelegt.
  function planFromKatalog(entry) {
    const persp = perspectiveId ? T.nodes.get(perspectiveId) : null;
    if (!persp) { ui.toast('Bitte links zuerst eine Einheit als Perspektive wählen.', 'warn'); return; }
    if (persp.type !== 'einheit') {
      const ms = T.mitgliederOf(persp.id, 'einheit');
      if (ms.length === 1) { openShiftDialog('create', null, persp.id, ms[0].id, entry); return; }
      ui.toast('Die gewählte Perspektive ist eine Führungsstelle. Bitte eine Einheit als Perspektive wählen oder die Einheit über „→ Harke“ neu anlegen.', 'warn', 6000);
      return;
    }
    openShiftDialog('create', null, persp.id, null, entry);
  }
  if ($availTyp) $availTyp.addEventListener('change', () => { availTyp = $availTyp.value; renderAvail(); });
  if ($availQ) $availQ.addEventListener('input', () => { availQuery = $availQ.value; renderAvail(); });
  if (K) K.onLoad(() => { fillAvailTyp(); requestRender(); });

  // ---- Laden / Sync ----
  async function load() {
    try {
      const data = await api.get('einsatz', 'tree', { einsatz: CTX.uuid });
      T.load(data.tree);
      ensurePerspective();
      renderAll();
      if (L.sync.running) L.sync.bump(T.seq); else L.sync.start(T.seq);
      if (K) K.load(true);
    } catch (e) {
      ui.toast('Laden fehlgeschlagen: ' + ui.errorText(e), 'error');
    }
  }

  L.sync.onEvent((events, treeChanged) => {
    if (treeChanged) requestRender();
  });

  // Nicht neu zeichnen, solange ein Dialog offen ist (Eingaben wuerden
  // sonst den Kontext verlieren) - nach dem Schliessen nachholen.
  function requestRender() {
    if (anyDialogOpen()) { pendingRender = true; return; }
    renderAll();
  }
  function flushPendingRender() {
    if (!pendingRender) return;
    pendingRender = false;
    if (!anyDialogOpen()) renderAll();
  }
  [$sDialog, $mDialog].forEach(d => { if (d) d.addEventListener('close', () => setTimeout(flushPendingRender, 0)); });

  // ---- Rendering ----
  function renderAll() {
    ensurePerspective();
    renderPerspectiveList();
    renderMainView();
    renderStaerkeOverview();
    renderAvail();
    updateUrl();
  }

  function renderPerspectiveList() {
    if (!$perspList) return;
    $perspList.innerHTML = '';
    const all = allNodesWithDepth();
    if (!all.length) {
      const empty = document.createElement('div');
      empty.className = 'kp-empty';
      empty.innerHTML = 'Die Führungsharke ist leer. Strukturen werden in der '
        + '<a href="' + harkeUrl() + '">Führungsharke</a> angelegt.';
      $perspList.appendChild(empty);
      return;
    }
    const atMs = effectiveAt();
    all.forEach(({ node, depth }) => {
      const row = document.createElement('button');
      row.type = 'button';
      row.className = 'kp-perspective-item';
      if (node.id === perspectiveId) row.classList.add('active');
      if (node.status === 'abgerueckt') row.classList.add('kp-perspective-abgerueckt');
      row.style.paddingLeft = (8 + depth * 14) + 'px';
      row.title = T.path(node.id);
      const title = document.createElement('span');
      title.className = 'kp-perspective-name';
      title.textContent = T.label(node);
      row.appendChild(title);
      const sum = S.sumStaerke(node.id, atMs);
      const sumEl = document.createElement('span');
      sumEl.className = 'kp-perspective-sum';
      sumEl.textContent = S.fmtStaerke(sum);
      row.appendChild(sumEl);
      const amp = ui.ampelDot(S.nodeAmpel(node.id));
      if (amp) row.appendChild(amp);
      row.addEventListener('click', () => {
        perspectiveId = node.id;
        renderAll();
      });
      $perspList.appendChild(row);
    });
  }

  function renderMainView() {
    if (!$content) return;
    const scrollTop = $content.scrollTop;
    $content.innerHTML = '';
    const persp = perspectiveId ? T.nodes.get(perspectiveId) : null;
    if (!persp) {
      $current.textContent = T.roots().length ? 'Keine Perspektive gewählt' : 'Führungsharke leer';
      if ($currentPath) $currentPath.textContent = '';
      const empty = document.createElement('div');
      empty.className = 'kp-empty';
      empty.innerHTML = T.roots().length
        ? 'Bitte links eine Perspektive auswählen.'
        : 'Noch keine Einheiten vorhanden. Strukturen werden in der <a href="' + harkeUrl() + '">Führungsharke</a> angelegt.';
      $content.appendChild(empty);
      return;
    }
    $current.textContent = T.label(persp);
    if ($currentPath) {
      const p = T.path(persp.id);
      $currentPath.textContent = p !== T.label(persp) ? p : '';
    }
    const atMs = effectiveAt();

    // Die Perspektive selbst
    $content.appendChild(renderNodeBox(persp, '(diese Ebene)', atMs));

    // Strukturelle Unterknoten als Abschnitte (einklappbar)
    const kids = T.children(persp.id);
    kids.forEach(absch => {
      const sect = makeDetails('kp-abschnitt', detailsKey('abschnitt', absch.id));
      const head = document.createElement('summary');
      head.className = 'kp-abschnitt-head';
      head.appendChild(marker());
      const h3 = document.createElement('h3');
      h3.textContent = T.label(absch);
      head.appendChild(h3);
      head.appendChild(ui.statusBadge(absch.status));
      const sumEl = document.createElement('span');
      sumEl.className = 'kp-abschnitt-sum';
      sumEl.textContent = 'Σ ' + S.fmtStaerke(S.sumStaerke(absch.id, atMs));
      head.appendChild(sumEl);
      const amp = ui.ampelDot(S.nodeAmpel(absch.id));
      if (amp) head.appendChild(amp);
      sect.appendChild(head);

      sect.appendChild(renderNodeBox(absch, null, atMs));
      T.descendants(absch.id).forEach(node => sect.appendChild(renderNodeBox(node, null, atMs)));
      $content.appendChild(sect);
    });

    if (!kids.length) {
      const note = document.createElement('div');
      note.className = 'kp-empty';
      note.innerHTML = 'Keine untergeordneten Einheiten oder Abschnitte. Diese werden in der '
        + '<a href="' + harkeUrl() + '">Führungsharke</a> angelegt.';
      $content.appendChild(note);
    }
    $content.scrollTop = scrollTop;
  }

  // Stammdaten-Zeile (Funk / OV / Tel / Bemerkung) + Bearbeiten-Button
  function metaRow(row, kind) {
    const meta = document.createElement('div');
    meta.className = 'kp-meta-row';
    const add = (key, val) => {
      if (!val) return;
      const k = document.createElement('span'); k.className = 'kp-meta-key'; k.textContent = key + ':';
      const v = document.createElement('span'); v.textContent = val;
      meta.appendChild(k); meta.appendChild(v);
    };
    add('Funk', row.funkruf);
    add('Tel', row.telefon);
    add('OV', row.ov);
    if (row.kraft_key && K && K.lookup(row.kraft_key)) {
      const l = K.lookup(row.kraft_key);
      const a = K.avail(l.einheit, effectiveAt());
      const k = document.createElement('span'); k.className = 'kp-meta-key'; k.textContent = 'Katalog:';
      const v = document.createElement('span'); v.className = 'kp-meta-kraft';
      v.appendChild(K.dot(a));
      v.appendChild(document.createTextNode(' ' + K.label(row.kraft_key) + ' – ' + a.text));
      meta.appendChild(k); meta.appendChild(v);
    }
    if (row.note) {
      const n = document.createElement('span'); n.className = 'kp-meta-note'; n.textContent = row.note;
      meta.appendChild(n);
    }
    if (canKraefte) {
      const edit = btn('Stammdaten', () => openMetaDialog(kind, row.id), null, 'Funkruf, Ortsverband, Bemerkung bearbeiten');
      edit.classList.add('kp-meta-edit', 'luk-no-print');
      meta.appendChild(edit);
    }
    return meta;
  }

  // Status-Zeile unter dem Kopf: "seit ..." + Schnellaktion fuer den
  // Anfahrts-Workflow (wie harke.js). Das Badge selbst steht im Kopf.
  // Liefert null, wenn nichts anzuzeigen ist.
  function statusRow(kind, row) {
    const wrap = document.createElement('div');
    wrap.className = 'kp-status-row';
    const us = S.unitStatus(kind, row);
    if (us.derived) {
      // Status wird ueber die Schichten gefuehrt (Abloesung: mehrere Schichten
      // koennen gleichzeitig unterschiedliche Status haben).
      const t = document.createElement('span');
      t.className = 'luk-muted kp-status-since';
      t.textContent = 'Status aus ' + us.shifts.length + ' Schicht' + (us.shifts.length === 1 ? '' : 'en')
        + (us.status_at ? ' · seit ' + ui.fmtServerTs(us.status_at) : '') + ' – je Schicht unten setzen';
      wrap.appendChild(t);
      return wrap;
    }
    if (row.status_at) {
      const t = document.createElement('span');
      t.className = 'luk-muted kp-status-since';
      t.textContent = 'seit ' + ui.fmtServerTs(row.status_at);
      wrap.appendChild(t);
    }
    if (canStatus) {
      const next = row.status === 'geplant' ? ['angefordert', 'Anfordern']
        : row.status === 'angefordert' ? ['anfahrt', 'Auf Anfahrt']
        : row.status === 'anfahrt' ? ['vor_ort', 'Eingetroffen (vor Ort)']
        : row.status === 'vor_ort' ? ['abgerueckt', 'Abgerückt']
        : null;
      if (next) {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = 'kp-status-btn luk-no-print' + (next[0] === 'vor_ort' ? ' kp-status-btn-ok' : '');
        b.textContent = next[1];
        b.title = 'Status auf „' + ui.statusLabel(next[0]) + '" setzen';
        b.addEventListener('click', e => { e.preventDefault(); e.stopPropagation(); setStatus(kind, row, next[0]); });
        wrap.appendChild(b);
      }
    }
    return wrap.childNodes.length ? wrap : null;
  }
  function appendIf(parent, el) { if (el) parent.appendChild(el); }

  // Knoten-Box: Fuehrung mit Mitgliederliste, Einheit mit eigenen Schichten.
  function renderNodeBox(node, suffix, atMs) {
    const wrap = makeDetails('kp-einheit kp-einheit-' + (node.type === 'fuehrung' ? 'fuehrung' : 'einheit'), detailsKey('node', node.id));
    if (!canKraefte) wrap.classList.add('kp-einheit-readonly');
    if (node.type !== 'fuehrung' && node.status === 'abgerueckt') wrap.classList.add('kp-einheit-abgerueckt');
    const subSum = S.sumStaerke(node.id, atMs);
    if (isZero(subSum)) wrap.classList.add('kp-einheit-inactive');

    const head = document.createElement('summary');
    head.className = 'kp-einheit-head';
    const title = document.createElement('div');
    title.className = 'kp-einheit-title';
    title.appendChild(marker());
    const typeBadge = document.createElement('span');
    typeBadge.className = 'kp-type-badge';
    typeBadge.textContent = node.type === 'fuehrung' ? 'Führung' : 'Einheit';
    title.appendChild(typeBadge);
    title.appendChild(document.createTextNode(' ' + T.label(node) + (suffix ? ' ' + suffix : '')));
    head.appendChild(title);
    if (node.type !== 'fuehrung') head.appendChild(ui.statusBadge(S.unitStatus('node', node).status));
    const sumEl = document.createElement('span');
    sumEl.className = 'kp-einheit-sum';
    const own = S.ownStaerke(node.id, atMs);
    sumEl.textContent = S.fmtStaerke(own);
    sumEl.title = 'Eigene Stärke' + (T.children(node.id).length ? ' · Σ inkl. unterstellt: ' + S.fmtStaerke(subSum) : '');
    head.appendChild(sumEl);
    const amp = ui.ampelDot(node.type === 'einheit' ? S.ampelForUnit(T.schichtenOf(node.id, null)) : S.nodeAmpel(node.id));
    if (amp) head.appendChild(amp);
    wrap.appendChild(head);

    wrap.appendChild(metaRow(node, 'node'));
    // Status fuehren die Einheiten und Fachberater an der Stelle, nicht die
    // Fuehrungsstelle selbst.
    if (node.type !== 'fuehrung') appendIf(wrap, statusRow('node', node));

    if (node.type === 'fuehrung') {
      const ms = T.mitgliederOf(node.id);
      if (!ms.length) {
        const empty = document.createElement('div');
        empty.className = 'kp-einheit-empty';
        empty.innerHTML = 'Keine Mitglieder. Fachberater und interne Einheiten werden in der '
          + '<a href="' + harkeUrl() + '">Führungsharke</a> angelegt.';
        wrap.appendChild(empty);
      }
      ms.forEach(m => wrap.appendChild(renderMemberBox(node, m, atMs)));
    } else {
      wrap.appendChild(renderShiftSection(node, null, T.schichtenOf(node.id, null), atMs));
    }
    return wrap;
  }

  function renderMemberBox(node, m, atMs) {
    const wrap = makeDetails('kp-member kp-member-' + m.kind, detailsKey('mitglied', m.id));
    if (!canKraefte) wrap.classList.add('kp-member-readonly');
    if (m.status === 'abgerueckt') wrap.classList.add('kp-member-abgerueckt');
    const schichten = T.schichtenOf(node.id, m.id);
    const sum = S.activeShiftStaerke(schichten, atMs);
    if (isZero(sum)) wrap.classList.add('kp-member-inactive');

    const head = document.createElement('summary');
    head.className = 'kp-member-head';
    head.appendChild(marker());
    const badge = document.createElement('span');
    badge.className = 'kp-member-badge kp-member-badge-' + m.kind;
    badge.textContent = m.kind === 'fachberater' ? 'Fachb.' : 'Einheit';
    head.appendChild(badge);
    const title = document.createElement('span');
    title.className = 'kp-member-title';
    title.textContent = ' ' + T.mitgliedLabel(m);
    head.appendChild(title);
    head.appendChild(ui.statusBadge(S.unitStatus('mitglied', m).status));
    const sumEl = document.createElement('span');
    sumEl.className = 'kp-member-sum';
    sumEl.textContent = S.fmtStaerke(sum);
    head.appendChild(sumEl);
    const amp = ui.ampelDot(S.ampelForUnit(schichten));
    if (amp) head.appendChild(amp);
    wrap.appendChild(head);

    wrap.appendChild(metaRow(m, 'mitglied'));
    appendIf(wrap, statusRow('mitglied', m));
    wrap.appendChild(renderShiftSection(node, m, schichten, atMs));
    return wrap;
  }

  // Schichten einer Einheit / eines Mitglieds als Tabelle
  function renderShiftSection(node, m, schichten, atMs) {
    const wrap = document.createElement('div');
    wrap.className = 'kp-schichten-section';

    if (!schichten.length) {
      const empty = document.createElement('div');
      empty.className = 'kp-einheit-empty';
      empty.textContent = canKraefte ? 'Noch keine Schichten geplant.' : 'Keine Schichten geplant.';
      wrap.appendChild(empty);
    } else {
      const table = document.createElement('table');
      table.className = 'kp-schichten-table';
      const thead = document.createElement('thead');
      const hr = document.createElement('tr');
      ['Von', 'Bis', 'Stärke', 'Personal', 'Bemerkung', 'Status', ''].forEach((h, i) => {
        const th = document.createElement('th');
        th.textContent = h;
        if (i === 6) th.className = 'kp-col-actions luk-no-print';
        hr.appendChild(th);
      });
      thead.appendChild(hr);
      table.appendChild(thead);
      const tbody = document.createElement('tbody');
      schichten.forEach(s => tbody.appendChild(renderShiftRow(s, atMs)));
      table.appendChild(tbody);
      wrap.appendChild(table);
    }

    if (canKraefte) {
      const add = btn('+ Schicht', () => openShiftDialog('create', null, node.id, m ? m.id : null), 'primary', 'Neue Schicht anlegen');
      add.classList.add('kp-add-shift', 'luk-no-print');
      wrap.appendChild(add);
    }
    return wrap;
  }

  function renderShiftRow(s, atMs) {
    const tr = document.createElement('tr');
    tr.className = 'kp-schicht-row-display';
    tr.setAttribute('data-id', s.id);
    const isEnabled = s.aktiv !== false;
    const isActiveNow = S.shiftActiveAt(s, atMs);
    const isPast = !isActiveNow && (S.parseTime(s.bis) || 0) < atMs;
    if (isActiveNow) tr.classList.add('kp-schicht-active');
    if (!isEnabled) tr.classList.add('kp-schicht-disabled');
    if (isPast) tr.classList.add('kp-schicht-past');

    const tdVon = document.createElement('td');
    tdVon.className = 'kp-schicht-time';
    tdVon.textContent = ui.fmtDateTime(s.von);
    tr.appendChild(tdVon);

    const tdBis = document.createElement('td');
    tdBis.className = 'kp-schicht-time';
    tdBis.textContent = ui.fmtDateTime(s.bis);
    tr.appendChild(tdBis);

    const tdSt = document.createElement('td');
    const tri = S.parseStaerke(s.staerke);
    const st = document.createElement('span');
    st.className = 'kp-schicht-staerke';
    st.textContent = tri ? S.fmtStaerke(tri) : (s.staerke || '–');
    tdSt.appendChild(st);
    if (isActiveNow) {
      const b = document.createElement('span'); b.className = 'kp-schicht-active-badge'; b.textContent = 'aktiv';
      tdSt.appendChild(b);
    }
    if (!isEnabled) {
      const b = document.createElement('span'); b.className = 'kp-schicht-inactive-badge'; b.textContent = 'inaktiv';
      tdSt.appendChild(b);
    }
    tr.appendChild(tdSt);

    const tdPers = document.createElement('td');
    tdPers.className = 'kp-schicht-personal';
    const parts = [];
    if (s.name) parts.push(s.name);
    if (s.funkruf) parts.push('Funk: ' + s.funkruf);
    if (s.telefon) parts.push('Tel: ' + s.telefon);
    if (s.ov) parts.push('OV: ' + s.ov);
    tdPers.textContent = parts.join(' · ') || '–';
    // Katalog-Einheit der Schicht: Abloesung durch eine andere Einheit kennzeichnen
    if (s.kraft_key && K && K.lookup(s.kraft_key)) {
      const ownKey = unitKraftKey(s.node_id, s.mitglied_id || null);
      const line = document.createElement('div');
      line.className = 'kp-schicht-kraft';
      const d = kraftDot(s.kraft_key, S.parseTime(s.von) || atMs);
      if (d) line.appendChild(d);
      line.appendChild(document.createTextNode(' ' + (s.kraft_key !== ownKey ? 'Ablösung: ' : '') + K.label(s.kraft_key)));
      tdPers.appendChild(line);
    }
    tr.appendChild(tdPers);

    const tdNote = document.createElement('td');
    tdNote.className = 'kp-schicht-note';
    tdNote.textContent = s.note || '';
    tr.appendChild(tdNote);

    // Status je Schicht (Anforderungs-/Anfahrtsworkflow, Abloesung)
    const tdStatus = document.createElement('td');
    tdStatus.className = 'kp-schicht-status';
    const sStatus = s.status || 'geplant';
    tdStatus.appendChild(ui.statusBadge(sStatus));
    if (s.status_at) {
      const since = document.createElement('div');
      since.className = 'luk-muted kp-status-since';
      since.textContent = 'seit ' + ui.fmtServerTs(s.status_at);
      tdStatus.appendChild(since);
    }
    if (canStatus && isEnabled) {
      const next = sStatus === 'geplant' ? ['angefordert', 'Anfordern']
        : sStatus === 'angefordert' ? ['anfahrt', 'Auf Anfahrt']
        : sStatus === 'anfahrt' ? ['vor_ort', 'Eingetroffen (vor Ort)']
        : sStatus === 'vor_ort' ? ['abgerueckt', 'Heimfahrt']
        : null;
      if (next) {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = 'kp-status-btn luk-no-print' + (next[0] === 'vor_ort' ? ' kp-status-btn-ok' : '');
        b.textContent = next[1];
        b.title = 'Schichtstatus auf „' + ui.statusLabel(next[0]) + '" setzen';
        b.addEventListener('click', e => { e.preventDefault(); e.stopPropagation(); setSchichtStatus(s, next[0]); });
        tdStatus.appendChild(b);
      }
    }
    tr.appendChild(tdStatus);

    const tdAct = document.createElement('td');
    tdAct.className = 'kp-col-actions luk-no-print';
    if (canKraefte) {
      const acts = document.createElement('div');
      acts.className = 'kp-schicht-actions';
      acts.appendChild(btn(isEnabled ? 'Deaktivieren' : 'Aktivieren', () => toggleShiftActive(s.id), null,
        isEnabled ? 'Schicht zählt dann nicht mehr zur Stärke' : 'Schicht wieder in die Stärke einrechnen'));
      acts.appendChild(btn('Bearb.', () => openShiftDialog('edit', s.id, s.node_id, s.mitglied_id || null)));
      acts.appendChild(btn('Löschen', () => deleteShift(s.id), 'danger'));
      tdAct.appendChild(acts);
    }
    tr.appendChild(tdAct);
    return tr;
  }

  function renderStaerkeOverview() {
    const atMs = effectiveAt();
    if ($staerke) {
      $staerke.innerHTML = '';
      const root = perspectiveId ? T.nodes.get(perspectiveId) : null;
      if (root) {
        const lab = document.createElement('div');
        lab.className = 'kp-staerke-label';
        lab.textContent = T.label(root) + (useCustomTime && customTime ? ' am ' + ui.fmtDateTime(customTime) : ' jetzt');
        $staerke.appendChild(lab);
        const div = document.createElement('div');
        div.className = 'kp-staerke-row';
        div.textContent = 'Σ ' + S.fmtStaerke(S.sumStaerke(root.id, atMs));
        $staerke.appendChild(div);
      } else {
        $staerke.textContent = '–';
      }
    }
    if ($einsatzStaerke) {
      $einsatzStaerke.innerHTML = '';
      const now = Date.now();
      const total = [0, 0, 0];
      const roots = T.roots();
      roots.forEach(r => {
        const s = S.sumStaerke(r.id, now);
        total[0] += s[0]; total[1] += s[1]; total[2] += s[2];
        if (roots.length > 1) {
          const row = document.createElement('div');
          row.className = 'kp-staerke-sub';
          row.textContent = T.label(r) + ': ' + S.fmtStaerke(s);
          $einsatzStaerke.appendChild(row);
        }
      });
      const row = document.createElement('div');
      row.className = 'kp-staerke-row';
      row.textContent = 'Σ ' + S.fmtStaerke(total);
      $einsatzStaerke.appendChild(row);
    }
  }

  // ---- Mutationen (API) ----
  // Jede Aenderung einzeln; bei 409 die Server-Zeile uebernehmen und neu zeichnen.
  async function run(fn, okMsg) {
    try {
      const res = await fn();
      if (okMsg) ui.toast(okMsg, 'ok', 1500);
      requestRender();
      L.sync.poll();
      return res;
    } catch (e) {
      if (!ui.handleConflict(e, cur => T.upsert(cur))) ui.toast(ui.errorText(e), 'error');
      requestRender();
      return null;
    }
  }
  function applySchicht(res) { if (res && res.schicht) T.upsert(res.schicht); }
  function applyNode(res) { if (res && res.node) T.upsert(res.node); }
  function applyMitglied(res) { if (res && res.mitglied) T.upsert(res.mitglied); }

  function toggleShiftActive(id) {
    const s = T.schichten.get(id);
    if (!s || !canKraefte) return;
    const aktiv = !(s.aktiv !== false);
    return run(async () => applySchicht(await api.post('einsatz', 'schicht_update', { id: s.id, version: s.version, fields: { aktiv } })),
      aktiv ? 'Schicht aktiviert.' : 'Schicht deaktiviert.');
  }
  function deleteShift(id) {
    const s = T.schichten.get(id);
    if (!s || !canKraefte) return;
    if (!window.confirm('Schicht ' + ui.fmtDateTime(s.von) + ' – ' + ui.fmtDateTime(s.bis) + ' löschen?')) return;
    return run(async () => {
      await api.post('einsatz', 'schicht_delete', { id: s.id, version: s.version });
      T.schichten.delete(s.id);
    }, 'Schicht gelöscht.');
  }
  function setStatus(kind, row, status) {
    if (!canStatus) return;
    const action = kind === 'node' ? 'node_status' : 'mitglied_status';
    return run(async () => {
      const res = await api.post('einsatz', action, { id: row.id, version: row.version, status });
      if (kind === 'node') applyNode(res); else applyMitglied(res);
    }, 'Status: ' + ui.statusLabel(status));
  }

  function setSchichtStatus(s, status) {
    if (!canStatus) return;
    return run(async () => {
      const res = await api.post('einsatz', 'schicht_status', { id: s.id, version: s.version, status });
      applySchicht(res);
    }, 'Schicht: ' + ui.statusLabel(status));
  }

  // ---- Schicht-Dialog ----
  function targetLabel(nodeId, mitgliedId) {
    const n = T.nodes.get(nodeId);
    if (mitgliedId) {
      const m = T.mitglieder.get(mitgliedId);
      return (n ? T.path(n.id) + ' › ' : '') + T.mitgliedLabel(m);
    }
    return n ? T.path(n.id) : '?';
  }
  // Standard-Startzeit: schliesst nahtlos an die spaeteste Endzeit aller
  // aktiven Schichten an, wenn diese in der Zukunft liegt - sonst jetzt.
  function defaultVon(schichten) {
    const now = Date.now();
    let lastEnd = 0;
    (schichten || []).forEach(s => {
      if (!s || s.aktiv === false) return;
      const t = S.parseTime(s.bis);
      if (t !== null && t > lastEnd) lastEnd = t;
    });
    const d = new Date(lastEnd > now ? lastEnd : now);
    d.setSeconds(0, 0);
    return ui.toLocalIso(d);
  }
  // Standard-Endzeit: 12 h nach Beginn.
  function defaultBis(vonIso) {
    const startMs = S.parseTime(vonIso) || Date.now();
    const d = new Date(startMs + 12 * 3600 * 1000);
    d.setSeconds(0, 0);
    return ui.toLocalIso(d);
  }

  // prefill: Katalog-Eintrag (aus "Verfuegbare Kraefte"), der die Schicht stellt
  function openShiftDialog(mode, id, nodeId, mitgliedId, prefill) {
    if (!canKraefte) return;
    const node = T.nodes.get(nodeId);
    if (!node) return;
    if (mitgliedId && !T.mitglieder.has(mitgliedId)) return;
    if (!mitgliedId && node.type === 'fuehrung') { ui.toast(ui.errorText(new L.LukError('fuehrung_no_schicht', 409)), 'warn', 5000); return; }

    const existing = mode === 'edit' ? T.schichten.get(id) : null;
    if (mode === 'edit' && !existing) return;
    const schichten = T.schichtenOf(nodeId, mitgliedId || null);

    let draft;
    if (existing) {
      draft = {
        von: existing.von || '', bis: existing.bis || '',
        staerke: existing.staerke || '',
        name: existing.name || '', telefon: existing.telefon || '',
        funkruf: existing.funkruf || '', ov: existing.ov || '',
        note: existing.note || '', aktiv: existing.aktiv !== false,
        kraftKey: existing.kraft_key || '',
      };
    } else {
      // Stammdaten des Ziels (Knoten oder Mitglied) als Vorlage uebernehmen
      const target = mitgliedId ? T.mitglieder.get(mitgliedId) : node;
      const von = defaultVon(schichten);
      draft = {
        von, bis: defaultBis(von),
        staerke: '',
        name: (target && target.name) || '', telefon: (target && target.telefon) || '',
        funkruf: (target && target.funkruf) || '', ov: (target && target.ov) || '',
        note: '', aktiv: true,
        kraftKey: (target && target.kraft_key) || '',
      };
      if (prefill && K) {
        const f = K.fill(prefill);
        draft.kraftKey = f.kraft_key;
        draft.ov = f.ov;
        if (f.funkruf) draft.funkruf = f.funkruf;
        draft.name = '';
        draft.telefon = '';
      }
    }

    shiftDlg = { mode, id: existing ? existing.id : null, version: existing ? existing.version : 0, nodeId, mitgliedId: mitgliedId || null, kraftKey: draft.kraftKey || '' };
    $siTitle.textContent = existing ? 'Schicht bearbeiten' : 'Neue Schicht';
    $siTarget.textContent = targetLabel(nodeId, mitgliedId || null);
    $siVon.value = draft.von;
    $siBis.value = draft.bis;
    $siStaerke.value = draft.staerke;
    $siName.value = draft.name;
    $siTelefon.value = draft.telefon;
    $siFunkruf.value = draft.funkruf;
    $siOv.value = draft.ov;
    $siNote.value = draft.note;
    $siAktiv.checked = !!draft.aktiv;
    if ($siKraftSuch) $siKraftSuch.value = '';
    renderKraftLine($siKraft, shiftDlg.kraftKey, () => { shiftDlg.kraftKey = ''; renderKraftLine($siKraft, ''); });

    // Endzeit/Dauer-Toggle: immer im Endzeit-Modus starten, Dauer-Felder leer.
    $siEndBis.checked = true;
    $siEndDauer.checked = false;
    $siDauerH.value = '';
    $siDauerM.value = '';
    applyEndModeUI();

    $sDialog.showModal();
    setTimeout(() => $siStaerke.focus(), 30);
  }

  function applyEndModeUI() {
    const useDauer = $siEndDauer.checked;
    $siBis.disabled = useDauer;
    $siDauerH.disabled = !useDauer;
    $siDauerM.disabled = !useDauer;
  }
  $siEndBis.addEventListener('change', applyEndModeUI);
  $siEndDauer.addEventListener('change', applyEndModeUI);
  $siCancel.addEventListener('click', () => $sDialog.close());

  $siSave.addEventListener('click', async () => {
    const d = shiftDlg;
    if (!d.nodeId) { $sDialog.close(); return; }
    const von = $siVon.value || '';
    if (!von || S.parseTime(von) === null) { ui.toast('Bitte Beginn (Von) angeben.', 'warn'); $siVon.focus(); return; }
    let bis;
    if ($siEndDauer.checked) {
      const h = parseInt($siDauerH.value || '0', 10) || 0;
      const m = parseInt($siDauerM.value || '0', 10) || 0;
      if (h <= 0 && m <= 0) { ui.toast('Bitte Dauer angeben.', 'warn'); $siDauerH.focus(); return; }
      bis = ui.toLocalIso(new Date(S.parseTime(von) + (h * 3600 + m * 60) * 1000));
    } else {
      bis = $siBis.value || '';
      if (!bis || S.parseTime(bis) === null) { ui.toast('Bitte Endzeit angeben.', 'warn'); $siBis.focus(); return; }
    }
    if (S.parseTime(bis) <= S.parseTime(von)) { ui.toast('Das Schichtende muss nach dem Beginn liegen.', 'warn'); return; }
    const fields = {
      von, bis,
      staerke: ($siStaerke.value || '').trim(),
      name: ($siName.value || '').trim(),
      telefon: ($siTelefon.value || '').trim(),
      funkruf: ($siFunkruf.value || '').trim(),
      ov: ($siOv.value || '').trim(),
      kraft_key: d.kraftKey || '',
      note: ($siNote.value || '').trim(),
      aktiv: !!$siAktiv.checked,
    };
    if (S.parseStaerke(fields.staerke) === null) {
      ui.toast(ui.errorText(new L.LukError('invalid_staerke', 400)), 'warn', 4000);
      $siStaerke.focus();
      return;
    }
    // Ueberschneidung derselben Katalog-Einheit: warnen, nicht sperren
    if (fields.kraft_key && fields.aktiv) {
      const warn = overlapWarnings(fields.kraft_key, S.parseTime(von), S.parseTime(bis), d.id);
      if (warn.length && !window.confirm(K.label(fields.kraft_key) + ' ist im Zeitraum dieser Schicht bereits eingeplant:\n\n– ' + warn.join('\n– ') + '\n\nTrotzdem speichern?')) return;
    }

    if (d.mode === 'create') {
      $sDialog.close();
      await run(async () => applySchicht(await api.post('einsatz', 'schicht_create',
        Object.assign({ node_id: d.nodeId, mitglied_id: d.mitgliedId || null }, fields))), 'Schicht angelegt.');
      return;
    }
    const cur = T.schichten.get(d.id);
    if (!cur) { $sDialog.close(); ui.toast(ui.errorText(new L.LukError('not_found', 404)), 'warn'); requestRender(); return; }
    const changed = {};
    Object.keys(fields).forEach(k => {
      const before = k === 'aktiv' ? (cur.aktiv !== false) : String(cur[k] || '');
      const after = k === 'aktiv' ? fields.aktiv : String(fields[k] || '');
      if (before !== after) changed[k] = fields[k];
    });
    $sDialog.close();
    if (!Object.keys(changed).length) return;
    // Version vom Dialog-Start: wurde die Schicht zwischenzeitlich geaendert,
    // meldet der Server 409 und wir uebernehmen den aktuellen Stand.
    await run(async () => applySchicht(await api.post('einsatz', 'schicht_update', { id: d.id, version: d.version, fields: changed })), 'Schicht gespeichert.');
  });

  // ---- Stammdaten-Dialog ----
  // Mit Kraefte-Recht duerfen an Knoten nur funkruf/ov/note, an Mitgliedern
  // funktion/name/funkruf/telefon/ov/note geaendert werden (Server prueft).
  function openMetaDialog(kind, id) {
    if (!canKraefte) return;
    const row = kind === 'node' ? T.nodes.get(id) : T.mitglieder.get(id);
    if (!row) return;
    metaDlg = { kind, id, version: row.version, kraftKey: row.kraft_key || '' };
    const isMitglied = kind === 'mitglied';
    const isFb = isMitglied && row.kind === 'fachberater';
    renderKraftLine($miKraft, isFb ? '' : metaDlg.kraftKey, () => { metaDlg.kraftKey = ''; renderKraftLine($miKraft, ''); });
    if ($miKraft) $miKraft.hidden = isFb;

    $miTitle.textContent = isFb ? 'Fachberater: Stammdaten'
      : isMitglied ? 'Interne Einheit: Stammdaten'
      : (row.type === 'fuehrung' ? 'Führungsstelle: Stammdaten' : 'Einheit: Stammdaten');
    $miBezLabel.hidden = isFb;
    $miFunktionLabel.hidden = !isFb;
    $miNameLabel.hidden = !isFb;
    $miTelefonLabel.hidden = !isMitglied;
    $miFunkrufLabel.hidden = false;

    $miBez.value = row.bezeichnung || '';
    $miFunktion.value = isFb ? (row.funktion || '') : '';
    $miName.value = isFb ? (row.name || '') : '';
    $miTelefon.value = isMitglied ? (row.telefon || '') : '';
    $miFunkruf.value = row.funkruf || '';
    $miOv.value = row.ov || '';
    $miNote.value = row.note || '';

    $mDialog.showModal();
    setTimeout(() => (isFb ? $miFunktion : $miFunkruf).focus(), 30);
  }
  $miCancel.addEventListener('click', () => $mDialog.close());

  $miSave.addEventListener('click', async () => {
    const d = metaDlg;
    const cur = d.kind === 'node' ? T.nodes.get(d.id) : T.mitglieder.get(d.id);
    if (!cur) { $mDialog.close(); return; }
    const fields = {
      funkruf: ($miFunkruf.value || '').trim(),
      ov: ($miOv.value || '').trim(),
      kraft_key: d.kraftKey || '',
      note: ($miNote.value || '').trim(),
    };
    if (d.kind === 'mitglied') {
      fields.telefon = ($miTelefon.value || '').trim();
      if (cur.kind === 'fachberater') {
        fields.funktion = ($miFunktion.value || '').trim();
        fields.name = ($miName.value || '').trim();
        fields.kraft_key = '';
        if (!fields.funktion && !fields.name) { ui.toast('Bitte Funktion oder Name angeben.', 'warn'); return; }
      }
    }
    const changed = {};
    Object.keys(fields).forEach(k => { if (String(cur[k] || '') !== String(fields[k] || '')) changed[k] = fields[k]; });
    $mDialog.close();
    if (!Object.keys(changed).length) return;
    const action = d.kind === 'node' ? 'node_update' : 'mitglied_update';
    await run(async () => {
      const res = await api.post('einsatz', action, { id: d.id, version: d.version, fields: changed });
      if (d.kind === 'node') applyNode(res); else applyMitglied(res);
    }, 'Stammdaten gespeichert.');
  });

  // ---- Zeitpunkt / Aktualisieren ----
  $useTime.addEventListener('change', () => {
    useCustomTime = !!$useTime.checked;
    $timeRow.hidden = !useCustomTime;
    if (useCustomTime && !$time.value) {
      const d = new Date(); d.setSeconds(0, 0);
      $time.value = ui.toLocalIso(d);
      customTime = $time.value;
    }
    renderAll();
  });
  $time.addEventListener('input', () => {
    customTime = $time.value;
    if (useCustomTime) renderAll();
  });
  $refresh.addEventListener('click', () => load());

  // ---- Drucken ----
  $printBtn.addEventListener('click', () => {
    const persp = perspectiveId ? T.nodes.get(perspectiveId) : null;
    const title = 'Kräfteplanung – ' + (persp ? T.label(persp) : CTX.title);
    // Fuer den Druck alle eingeklappten Bloecke oeffnen, danach wieder
    // schliessen (die toggle-Listener halten "collapsed" dabei konsistent).
    let closedBefore = [];
    ui.print(title, {
      beforePrint: () => {
        closedBefore = Array.from($content.querySelectorAll('details:not([open])'));
        closedBefore.forEach(d => { d.open = true; });
      },
      afterPrint: () => {
        closedBefore.forEach(d => { if (d.isConnected) d.open = false; });
        closedBefore = [];
      },
    });
  });

  // ---- Init ----
  // Perspektive aus der URL (?node=...)
  try {
    const p = new URL(window.location.href).searchParams.get('node');
    if (p) perspectiveId = p;
  } catch (e) { /* ignore */ }

  if (!canKraefte) {
    document.body.classList.add('kp-readonly');
    ui.showReadonlyBanner();
  }
  // Staerke/Ampel haengen von der Uhrzeit ab -> minuetlich nachzeichnen;
  // Kraeftekatalog (STEIN-Status, Bindungen) dabei mit auffrischen.
  setInterval(() => { if (!document.hidden) { requestRender(); if (K) K.load(); } }, 60000);
  load();
})();
