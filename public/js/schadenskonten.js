// schadenskonten.js - Schadenskonten / Einsatzauftraege je Einsatz (Stabsansicht).
//
// Layout:
//   - Sidebar (links): "+ Neu", Ansicht/Druck, anklickbare Sprung-Liste,
//     Tree-Source (Knoten + Mitglieder der Fuehrungsharke, per Drag).
//   - Hauptbereich (rechts): horizontal scrollbare Reihe aus Karten - eine
//     pro Schadenskonto - oder die kompakte Uebersicht (Kacheln).
//
// Datenhaltung: jede Aenderung ist ein eigener, versionierter API-Aufruf
// (LUK.api). Titel/Beschreibung/taktische Zeichen werden pro Karte debounced
// gespeichert (nur geaenderte Felder). Einheiten werden nur referenziert
// (ref_kind 'node' | 'mitglied'); ihre Staerke, Status und Schicht werden
// live aus LUK.tree / LUK.staerke aufgeloest. Fremdeinheiten sind eigene
// Datensaetze mit Version. Andere Clients kommen per LUK.sync herein.
(function () {
  'use strict';

  const L = window.LUK;
  if (!L || !L.CTX) return;
  const CTX = L.CTX;
  const T = L.tree;
  const S = L.staerke;
  const ui = L.ui;
  const api = L.api;

  const CFG = window.SK_CONFIG || {};
  const TZ_BASE = CFG.tzBasePath || '/images/taktische_zeichen';
  const TZ_CATALOG_URL = CFG.tzCatalogUrl || (TZ_BASE + '/catalog.json');
  const FOCUS_SK = CFG.focusSk || (new URLSearchParams(location.search).get('sk') || '');

  const canWrite = ui.can('schadenskonten');

  const TZ_KINDS = ['gefahren', 'schaeden', 'massnahmen'];
  const TZ_KIND_FOLDER = { gefahren: 'Gefahren', schaeden: 'Schäden', massnahmen: 'Maßnahmen' };
  const TZ_KIND_LABEL = { gefahren: 'Gefahren', schaeden: 'Schäden', massnahmen: 'Maßnahmen' };
  const FREMD_KIND_LABELS = {
    fw: 'Feuerwehr', polizei: 'Polizei', rd: 'Rettungsdienst',
    sanitaet: 'Sanitätsdienst', bw: 'Bundeswehr', sonstige: 'Sonstige'
  };
  // Fehlercodes dieser API, die luk_core nicht kennt.
  const LOCAL_ERROR_TEXT = {
    ref_not_found: 'Die Einheit existiert nicht (mehr) in der Führungsharke.',
    invalid_ref: 'Ungültige Einheiten-Verknüpfung.',
    no_fields: 'Keine Änderungen zu speichern.',
    id_exists: 'Datensatz existiert bereits.',
    network: 'Keine Verbindung zum Server.',
  };
  function errText(e) {
    if (e && e.code && LOCAL_ERROR_TEXT[e.code]) return LOCAL_ERROR_TEXT[e.code];
    return ui.errorText(e);
  }
  const DRAG_MIME = 'application/x-sk-unit';

  // ---- State ----
  let items = [];                 // Schadenskonten, sortiert (sort_order, created_at)
  let assignedIndex = {};         // "kind:id" -> {skId, skTitle}
  let catalog = null;             // TZ-Katalog (catalog.json)
  const saveTimers = {};          // skId -> Timeout
  const pending = {};             // skId -> Set(feldnamen) mit lokal ungespeicherten Aenderungen
  const saving = {};              // skId -> true solange ein Update laeuft
  const inflight = {};            // skId -> Set(feldnamen), die gerade zum Server unterwegs sind
  const queuedRerender = new Set();   // Karten, deren Re-Render auf Blur wartet
  let tzDialogContext = { skId: null, kind: null };
  let fdDialogContext = { skId: null, fremdId: null, isNew: false };
  let viewMode = 'cards';         // 'cards' | 'overview'

  // ---- DOM ----
  const $list = document.getElementById('sk-list');
  const $newTitle = document.getElementById('sk-new-title');
  const $newBtn = document.getElementById('sk-new-btn');
  const $createSection = document.getElementById('sk-create-section');
  const $treeSource = document.getElementById('sk-tree-source');
  const $treeHint = document.getElementById('sk-tree-hint');
  const $empty = document.getElementById('sk-empty');
  const $emptyText = document.getElementById('sk-empty-text');
  const $cardsScroll = document.getElementById('sk-cards-scroll');
  const $cards = document.getElementById('sk-cards');
  const $overviewBtn = document.getElementById('sk-overview-btn');
  const $overviewView = document.getElementById('sk-overview-view');
  const $overviewGrid = document.getElementById('sk-overview-grid');
  const $printBtn = document.getElementById('sk-print-btn');

  const $tzDialog = document.getElementById('sk-tz-dialog');
  const $tzDialogTitle = document.getElementById('sk-tz-dialog-title');
  const $tzSearch = document.getElementById('sk-tz-search');
  const $tzGrid = document.getElementById('sk-tz-grid');
  const $tzClose = document.getElementById('sk-tz-close');
  const $tzCustomInput = document.getElementById('sk-tz-custom-input');
  const $tzCustomAdd = document.getElementById('sk-tz-custom-add');

  const $fdDialog = document.getElementById('sk-fremd-dialog');
  const $fdTitle = document.getElementById('sk-fd-title');
  const $fdKind = document.getElementById('sk-fd-kind');
  const $fdBez = document.getElementById('sk-fd-bezeichnung');
  const $fdFunkruf = document.getElementById('sk-fd-funkruf');
  const $fdOv = document.getElementById('sk-fd-ov');
  const $fdStaerke = document.getElementById('sk-fd-staerke');
  const $fdNote = document.getElementById('sk-fd-note');
  const $fdSave = document.getElementById('sk-fd-save');
  const $fdCancel = document.getElementById('sk-fd-cancel');

  // ---- Helpers ----
  function findItem(id) { return items.find(x => x.id === id) || null; }
  function cardEl(id) { return $cards ? $cards.querySelector('.sk-card[data-id="' + cssEsc(id) + '"]') : null; }
  function cssEsc(s) { return (window.CSS && CSS.escape) ? CSS.escape(String(s)) : String(s).replace(/"/g, '\\"'); }
  function refKey(kind, id) { return kind + ':' + id; }
  function encodePath(p) { return String(p || '').split('/').map(encodeURIComponent).join('/'); }
  function isTextField(el) {
    return !!el && (el.tagName === 'TEXTAREA' || (el.tagName === 'INPUT' && (el.type === 'text' || el.type === 'search')));
  }
  function emptyTz() { return { gefahren: [], schaeden: [], massnahmen: [] }; }
  // Serverzeile (auch Roh-DB-Zeile aus einem 409) in das Item-Format bringen.
  function normalizeItemRow(row) {
    if (!row) return null;
    const out = Object.assign({}, row);
    if (typeof out.taktische_zeichen === 'string') {
      try { out.taktische_zeichen = JSON.parse(out.taktische_zeichen); } catch (e) { out.taktische_zeichen = emptyTz(); }
    }
    if (!out.taktische_zeichen || typeof out.taktische_zeichen !== 'object') out.taktische_zeichen = emptyTz();
    TZ_KINDS.forEach(k => { if (!Array.isArray(out.taktische_zeichen[k])) out.taktische_zeichen[k] = []; });
    out.version = parseInt(out.version, 10) || 0;
    out.sort_order = parseInt(out.sort_order, 10) || 0;
    out.title = out.title == null ? '' : String(out.title);
    out.description = out.description == null ? '' : String(out.description);
    return out;
  }
  function normalizeFremdRow(row) {
    if (!row) return null;
    const out = Object.assign({}, row);
    out.version = parseInt(out.version, 10) || 0;
    out.sort_order = parseInt(out.sort_order, 10) || 0;
    ['kind', 'bezeichnung', 'funkruf', 'ov', 'staerke', 'note'].forEach(k => { out[k] = out[k] == null ? '' : String(out[k]); });
    return out;
  }
  function sortItems() {
    items.sort((a, b) => (a.sort_order - b.sort_order) || String(a.created_at || '').localeCompare(String(b.created_at || '')) || String(a.id).localeCompare(String(b.id)));
  }
  function sortFremd(it) {
    it.fremd.sort((a, b) => (a.sort_order - b.sort_order) || String(a.id).localeCompare(String(b.id)));
  }
  // Felder aus einer Serverzeile ins lokale Item uebernehmen; Felder mit
  // lokal noch ungespeicherten (pending) oder gerade gesendeten (inflight)
  // Eingaben bleiben unangetastet.
  function mergeItemRow(it, row, respectPending) {
    const skip = new Set();
    if (respectPending) {
      (pending[it.id] || []).forEach(k => skip.add(k));
      (inflight[it.id] || []).forEach(k => skip.add(k));
    }
    ['title', 'description', 'taktische_zeichen'].forEach(k => {
      if (k in row && !skip.has(k)) it[k] = row[k];
    });
    if ('sort_order' in row) it.sort_order = row.sort_order;
    if ('version' in row) it.version = row.version;
    if ('updated_at' in row) it.updated_at = row.updated_at;
  }
  function flashSaved(id) {
    const card = cardEl(id);
    const b = card && card.querySelector('.sk-saved-badge');
    if (!b) return;
    b.hidden = false;
    clearTimeout(b._t);
    b._t = setTimeout(() => { b.hidden = true; }, 1200);
  }
  function unitTypeInfo(ref) {
    // ref = Ergebnis von T.resolveRef
    if (ref.kind === 'node') {
      return ref.type === 'fuehrung' ? { label: 'Führung', cls: 'sk-unit-type-fuehrung' } : { label: 'Einheit', cls: 'sk-unit-type-einheit' };
    }
    return ref.type === 'fachberater' ? { label: 'Fachb.', cls: 'sk-unit-type-fachberater' } : { label: 'Int. Einheit', cls: 'sk-unit-type-intern' };
  }
  function unitSchichten(ref) {
    if (ref.kind === 'node') return ref.type === 'fuehrung' ? [] : T.schichtenOf(ref.id, null);
    return T.schichtenOf(ref.row.node_id, ref.id);
  }
  function fmtClock(s) {
    const t = S.parseTime(s);
    if (t === null) return '?';
    return new Date(t).toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' });
  }

  // assignedIndex spiegelt die Eindeutigkeits-Regel (serverseitig erzwungen):
  // jede Einheit ist hoechstens einem Schadenskonto zugeordnet. Dient der
  // Verschiebe-Bestaetigung vor dem Drop und der Markierung in der Tree-Source.
  function rebuildAssignedIndex() {
    assignedIndex = {};
    items.forEach(it => {
      (it.refs || []).forEach(r => {
        const k = refKey(r.ref_kind, r.ref_id);
        if (!assignedIndex[k]) assignedIndex[k] = { skId: it.id, skTitle: it.title || '(ohne Titel)' };
      });
    });
  }

  // ------------------------------------------------------------------
  // Sidebar: Sprung-Liste
  // ------------------------------------------------------------------
  function renderJumpList() {
    if (!$list) return;
    const activeEl = $list.querySelector('.sk-list-row-active');
    const activeId = activeEl ? activeEl.getAttribute('data-id') : null;
    $list.innerHTML = '';
    if (!items.length) {
      const empty = document.createElement('div');
      empty.className = 'sk-list-empty';
      empty.textContent = 'Noch keine Schadenskonten angelegt.';
      $list.appendChild(empty);
      return;
    }
    items.forEach(it => $list.appendChild(renderJumpRow(it, it.id === activeId)));
  }
  function renderJumpRow(it, active) {
    const row = document.createElement('div');
    row.className = 'sk-list-row' + (active ? ' sk-list-row-active' : '');
    row.setAttribute('data-id', it.id);
    row.setAttribute('role', 'button');
    row.tabIndex = 0;

    const title = document.createElement('div');
    title.className = 'sk-list-title';
    title.textContent = it.title || '(ohne Titel)';
    row.appendChild(title);

    const meta = document.createElement('div');
    meta.className = 'sk-list-meta';
    const sum = cardStaerkeSum(it);
    meta.textContent = (it.refs || []).length + ' Einh. + ' + (it.fremd || []).length + ' Fremd · Σ ' + S.fmtStaerke(sum);
    row.appendChild(meta);

    row.addEventListener('click', () => { if (viewMode !== 'cards') setViewMode('cards'); setTimeout(() => scrollToCard(it.id), 20); });
    row.addEventListener('keydown', e => {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); row.click(); }
    });
    if (canWrite) attachDropZone(row, it.id, 'sk-list-row-drop', true);
    return row;
  }
  function highlightJumpRow(id) {
    if (!$list) return;
    $list.querySelectorAll('.sk-list-row').forEach(r => r.classList.toggle('sk-list-row-active', r.getAttribute('data-id') === id));
  }
  function scrollToCard(id) {
    const card = cardEl(id);
    if (!card) return;
    card.scrollIntoView({ behavior: 'smooth', block: 'nearest', inline: 'center' });
    card.classList.add('sk-card-pulse');
    setTimeout(() => card.classList.remove('sk-card-pulse'), 1400);
    highlightJumpRow(id);
  }

  // ------------------------------------------------------------------
  // Sidebar: Tree-Source (Knoten + Mitglieder, flach mit Einrueckung)
  // ------------------------------------------------------------------
  function renderTreeSource() {
    if (!$treeSource) return;
    const scrollTop = $treeSource.parentElement ? $treeSource.parentElement.scrollTop : 0;
    $treeSource.innerHTML = '';
    const units = T.allUnits();
    if (!units.length) {
      const hint = document.createElement('div');
      hint.className = 'sk-tree-empty';
      hint.textContent = 'Die Führungsharke dieses Einsatzes ist leer.';
      $treeSource.appendChild(hint);
      return;
    }
    const now = Date.now();
    units.forEach(u => $treeSource.appendChild(renderTreeUnit(u, now)));
    if ($treeSource.parentElement) $treeSource.parentElement.scrollTop = scrollTop;
  }
  function renderTreeUnit(u, now) {
    const card = document.createElement('div');
    const ref = T.resolveRef(u.kind, u.id);
    const ti = ref ? unitTypeInfo(ref) : { label: '?', cls: '' };
    card.className = 'sk-tree-node sk-tree-kind-' + u.kind + ' sk-tree-type-' + u.type;
    card.setAttribute('data-kind', u.kind);
    card.setAttribute('data-id', u.id);
    card.style.marginLeft = (Math.min(u.depth, 8) * 14) + 'px';
    card.title = ref ? ref.path : u.label;
    if (u.row && u.row.status === 'abgerueckt') card.classList.add('sk-tree-node-abgerueckt');

    if (canWrite) {
      card.draggable = true;
      card.addEventListener('dragstart', e => {
        if (!e.dataTransfer) return;
        const payload = JSON.stringify({ kind: u.kind, id: u.id });
        e.dataTransfer.setData(DRAG_MIME, payload);
        e.dataTransfer.setData('text/plain', payload);
        e.dataTransfer.effectAllowed = 'copy';
        card.classList.add('sk-tree-node-dragging');
      });
      card.addEventListener('dragend', () => {
        card.classList.remove('sk-tree-node-dragging');
        document.querySelectorAll('.sk-card-drop-active, .sk-list-row-drop').forEach(el => el.classList.remove('sk-card-drop-active', 'sk-list-row-drop'));
      });
    }

    const badge = document.createElement('span');
    badge.className = 'sk-tree-badge ' + ti.cls;
    badge.textContent = ti.label;
    card.appendChild(badge);

    const title = document.createElement('span');
    title.className = 'sk-tree-title';
    title.textContent = u.label;
    card.appendChild(title);

    if (u.row && u.row.funkruf) {
      const fr = document.createElement('span');
      fr.className = 'sk-tree-funk';
      fr.textContent = u.row.funkruf;
      card.appendChild(fr);
    }

    const own = S.unitStaerke(u.kind, u.id, now);
    if (own[0] || own[1] || own[2]) {
      const st = document.createElement('span');
      st.className = 'sk-tree-staerke';
      st.textContent = S.fmtStaerke(own);
      st.title = 'Eigene Stärke (ohne unterstellte Einheiten)';
      card.appendChild(st);
    }

    const assigned = assignedIndex[refKey(u.kind, u.id)];
    if (assigned) {
      card.classList.add('sk-tree-node-assigned');
      const check = document.createElement('span');
      check.className = 'sk-tree-check';
      check.textContent = '✓';
      check.title = 'Zugeordnet zu: ' + assigned.skTitle + ' (Klick: zur Karte)';
      check.addEventListener('click', e => {
        e.stopPropagation();
        if (viewMode !== 'cards') setViewMode('cards');
        setTimeout(() => scrollToCard(assigned.skId), 20);
      });
      card.appendChild(check);
    }
    return card;
  }

  // ------------------------------------------------------------------
  // Drag & Drop (Einheit -> Karte / Sprungzeile)
  // ------------------------------------------------------------------
  function parseDrag(dt) {
    if (!dt) return null;
    let raw = '';
    try { raw = dt.getData(DRAG_MIME) || dt.getData('text/plain'); } catch (e) { raw = ''; }
    if (!raw) return null;
    try {
      const o = JSON.parse(raw);
      if (o && (o.kind === 'node' || o.kind === 'mitglied') && o.id) return o;
    } catch (e) { /* ignore */ }
    return null;
  }
  function hasDragType(dt) {
    if (!dt || !dt.types) return false;
    return Array.prototype.indexOf.call(dt.types, DRAG_MIME) !== -1;
  }
  function attachDropZone(el, skId, activeClass, scrollAfter) {
    el.addEventListener('dragover', e => {
      if (!canWrite || !hasDragType(e.dataTransfer)) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = 'copy';
      el.classList.add(activeClass);
    });
    el.addEventListener('dragleave', e => {
      if (e.currentTarget !== e.target && el.contains(e.relatedTarget)) return;
      el.classList.remove(activeClass);
    });
    el.addEventListener('drop', e => {
      el.classList.remove(activeClass);
      if (!canWrite) return;
      const src = parseDrag(e.dataTransfer);
      if (!src) return;
      e.preventDefault();
      addUnit(skId, src.kind, src.id);
      if (scrollAfter) { if (viewMode !== 'cards') setViewMode('cards'); setTimeout(() => scrollToCard(skId), 20); }
    });
  }

  // ------------------------------------------------------------------
  // Karten
  // ------------------------------------------------------------------
  function renderCards() {
    if (!$cards) return;
    sortItems();
    $cards.innerHTML = '';
    items.forEach(it => $cards.appendChild(renderCard(it)));
    updateEmptyHint();
    if (viewMode === 'overview') renderOverviewGrid();
  }
  function updateEmptyHint() {
    if (!$empty) return;
    $empty.hidden = items.length > 0 || viewMode === 'overview';
    if ($emptyText) $emptyText.textContent = canWrite ? 'Links über „+ Neues Schadenskonto" das erste anlegen.' : 'Für diesen Einsatz wurden noch keine Schadenskonten angelegt.';
  }
  // Karten-DOM in die Reihenfolge von items bringen (ohne Neuaufbau).
  function syncCardOrder() {
    if (!$cards) return;
    sortItems();
    const active = document.activeElement;
    if ($cards.contains(active) && isTextField(active)) { queuedRerender.add('__order__'); return; }
    items.forEach(it => { const el = cardEl(it.id); if (el) $cards.appendChild(el); });
    renderJumpList();
    if (viewMode === 'overview') renderOverviewGrid();
  }

  function renderCard(it) {
    const card = document.createElement('section');
    card.className = 'sk-card';
    card.setAttribute('data-id', it.id);
    if (canWrite) attachDropZone(card, it.id, 'sk-card-drop-active', false);

    card.appendChild(renderCardHead(it));

    const body = document.createElement('div');
    body.className = 'sk-card-body';
    body.appendChild(renderCardDescription(it));
    body.appendChild(renderCardTz(it));
    body.appendChild(renderCardUnits(it));
    body.appendChild(renderCardFremd(it));
    card.appendChild(body);
    return card;
  }

  function renderCardHead(it) {
    const head = document.createElement('div');
    head.className = 'sk-card-head';

    const titleInput = document.createElement('input');
    titleInput.type = 'text';
    titleInput.className = 'sk-card-title sk-print-hide';
    titleInput.maxLength = 200;
    titleInput.value = it.title || '';
    titleInput.placeholder = 'Titel';
    titleInput.setAttribute('aria-label', 'Titel des Schadenskontos');
    titleInput.disabled = !canWrite;
    // Druck-Darstellung des Titels (Inputs drucken schlecht)
    const titlePrint = document.createElement('div');
    titlePrint.className = 'sk-card-title-print sk-print-only';
    titlePrint.textContent = it.title || '(ohne Titel)';
    titleInput.addEventListener('input', () => {
      const ref = findItem(it.id); if (!ref) return;
      ref.title = titleInput.value;
      titlePrint.textContent = ref.title || '(ohne Titel)';
      const jumpTitle = $list && $list.querySelector('.sk-list-row[data-id="' + cssEsc(it.id) + '"] .sk-list-title');
      if (jumpTitle) jumpTitle.textContent = ref.title || '(ohne Titel)';
      Object.keys(assignedIndex).forEach(k => { if (assignedIndex[k].skId === ref.id) assignedIndex[k].skTitle = ref.title || '(ohne Titel)'; });
      queueSave(it.id, 'title');
    });
    titleInput.addEventListener('blur', () => flushQueuedRerender());
    head.appendChild(titleInput);
    head.appendChild(titlePrint);

    const savedBadge = document.createElement('span');
    savedBadge.className = 'sk-saved-badge luk-no-print';
    savedBadge.hidden = true;
    savedBadge.textContent = 'gespeichert';
    head.appendChild(savedBadge);

    if (canWrite) {
      const left = document.createElement('button');
      left.type = 'button';
      left.className = 'sk-btn sk-btn-small sk-btn-ghost luk-no-print';
      left.textContent = '◀';
      left.title = 'Nach links verschieben';
      left.addEventListener('click', () => moveCard(it.id, -1));
      head.appendChild(left);
      const right = document.createElement('button');
      right.type = 'button';
      right.className = 'sk-btn sk-btn-small sk-btn-ghost luk-no-print';
      right.textContent = '▶';
      right.title = 'Nach rechts verschieben';
      right.addEventListener('click', () => moveCard(it.id, +1));
      head.appendChild(right);

      const delBtn = document.createElement('button');
      delBtn.type = 'button';
      delBtn.className = 'sk-btn sk-btn-small sk-btn-danger luk-no-print';
      delBtn.textContent = 'Löschen';
      delBtn.title = 'Schadenskonto löschen';
      delBtn.addEventListener('click', () => deleteSchadenskonto(it.id));
      head.appendChild(delBtn);
    }
    return head;
  }

  function renderCardDescription(it) {
    const sec = document.createElement('section');
    sec.className = 'sk-card-section sk-card-desc';
    const t = document.createElement('div');
    t.className = 'sk-card-section-title';
    t.textContent = 'Beschreibung';
    sec.appendChild(t);
    const ta = document.createElement('textarea');
    ta.rows = 3;
    ta.maxLength = 5000;
    ta.className = 'sk-card-description sk-print-hide';
    ta.placeholder = 'Was ist passiert, welche Lage, welche Aufträge?';
    ta.value = it.description || '';
    ta.disabled = !canWrite;
    ta.setAttribute('aria-label', 'Beschreibung');
    const print = document.createElement('div');
    print.className = 'sk-desc-print sk-print-only';
    print.textContent = it.description || '–';
    ta.addEventListener('input', () => {
      const ref = findItem(it.id); if (!ref) return;
      ref.description = ta.value;
      print.textContent = ref.description || '–';
      queueSave(it.id, 'description');
    });
    ta.addEventListener('blur', () => flushQueuedRerender());
    sec.appendChild(ta);
    sec.appendChild(print);
    return sec;
  }

  function renderCardTz(it) {
    const sec = document.createElement('section');
    sec.className = 'sk-card-section sk-card-tz';
    const t = document.createElement('div');
    t.className = 'sk-card-section-title';
    t.textContent = 'Taktische Zeichen';
    sec.appendChild(t);

    TZ_KINDS.forEach(kind => {
      const block = document.createElement('div');
      block.className = 'sk-tz-block';
      const head = document.createElement('div');
      head.className = 'sk-tz-block-head';
      const lbl = document.createElement('span');
      lbl.className = 'sk-tz-block-title';
      lbl.textContent = TZ_KIND_LABEL[kind];
      head.appendChild(lbl);
      if (canWrite) {
        const add = document.createElement('button');
        add.type = 'button';
        add.className = 'sk-btn sk-tz-add luk-no-print';
        add.textContent = '+';
        add.title = TZ_KIND_LABEL[kind] + ' hinzufügen (Symbol oder Freitext)';
        add.addEventListener('click', () => openTzDialog(it.id, kind));
        head.appendChild(add);
      }
      block.appendChild(head);

      const list = document.createElement('div');
      list.className = 'sk-tz-list';
      const arr = ((it.taktische_zeichen || {})[kind]) || [];
      if (!arr.length) {
        const empty = document.createElement('div');
        empty.className = 'sk-tz-empty';
        empty.textContent = 'Keine.';
        list.appendChild(empty);
      } else {
        arr.forEach((entry, idx) => list.appendChild(renderTzItem(it.id, kind, entry, idx)));
      }
      block.appendChild(list);
      sec.appendChild(block);
    });
    return sec;
  }
  function renderTzItem(skId, kind, entry, idx) {
    const item = document.createElement('div');
    item.className = 'sk-tz-item';
    const isCustom = !!entry.custom || !entry.file;
    if (isCustom) {
      item.classList.add('sk-tz-item-custom');
      const box = document.createElement('div');
      box.className = 'sk-tz-custom-box';
      box.textContent = entry.label || '';
      box.title = canWrite ? 'Freier Eintrag – Klick zum Bearbeiten' : 'Freier Eintrag';
      if (canWrite) {
        box.addEventListener('click', () => editCustomTz(skId, kind, idx));
        box.style.cursor = 'pointer';
      }
      item.appendChild(box);
    } else {
      const img = document.createElement('img');
      img.alt = entry.label || entry.file;
      img.src = TZ_BASE + '/' + encodePath(TZ_KIND_FOLDER[kind] + '/' + entry.file);
      img.title = entry.label || entry.file;
      img.loading = 'lazy';
      item.appendChild(img);
      const lbl = document.createElement('span');
      lbl.className = 'sk-tz-label';
      lbl.textContent = entry.label || String(entry.file).replace(/\.svg$/i, '').replace(/_/g, ' ');
      item.appendChild(lbl);
    }
    if (canWrite) {
      const rm = document.createElement('button');
      rm.type = 'button';
      rm.className = 'sk-tz-remove luk-no-print';
      rm.textContent = '×';
      rm.title = 'Entfernen';
      rm.addEventListener('click', () => removeTz(skId, kind, idx));
      item.appendChild(rm);
    }
    return item;
  }

  // Summe der Eigenstaerken aller zugeordneten Einheiten + Fremdeinheiten
  function cardStaerkeSum(it) {
    const now = Date.now();
    const acc = [0, 0, 0];
    (it.refs || []).forEach(r => {
      const s = S.unitStaerke(r.ref_kind, r.ref_id, now);
      acc[0] += s[0]; acc[1] += s[1]; acc[2] += s[2];
    });
    (it.fremd || []).forEach(f => {
      const t = S.parseStaerke(f.staerke);
      if (t) { acc[0] += t[0]; acc[1] += t[1]; acc[2] += t[2]; }
    });
    return acc;
  }

  function renderCardUnits(it) {
    const sec = document.createElement('section');
    sec.className = 'sk-card-section sk-card-units';

    const headRow = document.createElement('div');
    headRow.className = 'sk-card-section-title';
    const lbl = document.createElement('span');
    lbl.textContent = 'Einheiten (Führungsharke)';
    headRow.appendChild(lbl);
    const sum = document.createElement('span');
    sum.className = 'sk-staerke-sum';
    sum.title = 'Summe der Eigenstärken (aktive Schichten jetzt)';
    headRow.appendChild(sum);
    sec.appendChild(headRow);

    const refs = it.refs || [];
    const now = Date.now();
    const sumAcc = [0, 0, 0];
    const list = document.createElement('div');
    list.className = 'sk-units-list';
    if (!refs.length) {
      const hint = document.createElement('div');
      hint.className = 'sk-units-empty';
      hint.textContent = canWrite ? 'Einheit aus der Führungsharke (links) hierher ziehen.' : 'Keine Einheiten zugeordnet.';
      list.appendChild(hint);
    } else {
      refs.forEach(r => {
        const own = renderUnitRow(it, r, now, list);
        sumAcc[0] += own[0]; sumAcc[1] += own[1]; sumAcc[2] += own[2];
      });
    }
    sec.appendChild(list);
    sum.textContent = 'Σ ' + S.fmtStaerke(sumAcc);
    return sec;
  }
  // Rendert eine Einheiten-Zeile in list; liefert die Eigenstaerke.
  function renderUnitRow(it, r, now, list) {
    const ref = T.resolveRef(r.ref_kind, r.ref_id);
    const row = document.createElement('div');
    row.className = 'sk-unit-row';
    let own = [0, 0, 0];
    if (!ref) {
      row.classList.add('sk-unit-row-missing');
      const badge = document.createElement('span');
      badge.className = 'sk-unit-badge sk-unit-type-missing';
      badge.textContent = 'verwaist';
      row.appendChild(badge);
      const txt = document.createElement('span');
      txt.className = 'sk-unit-title';
      txt.textContent = (r.ref_kind === 'mitglied' ? 'Mitglied' : 'Einheit') + ' existiert nicht mehr in der Führungsharke';
      row.appendChild(txt);
    } else {
      const ti = unitTypeInfo(ref);
      // Fuehrungsstellen fuehren keinen Anfahrtsstatus (den haben die Einheiten
      // und Fachberater an der Stelle).
      const refFuehrung = ref.kind === 'node' && ref.type === 'fuehrung';
      if (!refFuehrung && ref.status === 'abgerueckt') row.classList.add('sk-unit-row-abgerueckt');
      const badge = document.createElement('span');
      badge.className = 'sk-unit-badge ' + ti.cls;
      badge.textContent = ti.label;
      row.appendChild(badge);

      const title = document.createElement('span');
      title.className = 'sk-unit-title';
      title.textContent = ref.label;
      title.title = ref.path;
      row.appendChild(title);

      if (ref.funkruf) {
        const fr = document.createElement('span');
        fr.className = 'sk-unit-funk';
        fr.textContent = ref.funkruf;
        row.appendChild(fr);
      }
      if (!refFuehrung) row.appendChild(ui.statusBadge(ref.status));

      // Nur eigene Staerke - unterstellte Einheiten koennen zu einem anderen
      // Schadenskonto gehoeren und werden dort gezaehlt.
      own = S.unitStaerke(r.ref_kind, r.ref_id, now);
      const st = document.createElement('span');
      st.className = 'sk-unit-staerke';
      st.textContent = S.fmtStaerke(own);
      st.title = 'Eigene Stärke (aktive Schichten jetzt)';
      if (!(own[0] || own[1] || own[2])) {
        st.classList.add('sk-unit-staerke-zero');
        st.title = 'Aktuell keine aktive Schicht in der Kräfteplanung';
      }
      row.appendChild(st);

      // Aktuelle bzw. naechste Schicht (Personal)
      const cs = S.currentShift(unitSchichten(ref), now);
      if (cs.shift) {
        const sh = cs.shift;
        const parts = [];
        if (sh.name) parts.push(sh.name);
        if (sh.telefon) parts.push('Tel. ' + sh.telefon);
        if (sh.funkruf && sh.funkruf !== ref.funkruf) parts.push(sh.funkruf);
        const span = document.createElement('span');
        span.className = 'sk-unit-shift' + (cs.state === 'planned' ? ' sk-unit-shift-planned' : '');
        span.textContent = (cs.state === 'running' ? 'Schicht bis ' + fmtClock(sh.bis) : 'Nächste Schicht ab ' + ui.fmtDateTime(sh.von))
          + (parts.length ? ' · ' + parts.join(' · ') : '');
        span.title = cs.state === 'running' ? 'Laufende Schicht ' + ui.fmtDateTime(sh.von) + ' – ' + ui.fmtDateTime(sh.bis) : 'Geplante Schicht';
        row.appendChild(span);
      }
    }
    if (canWrite) {
      const rm = document.createElement('button');
      rm.type = 'button';
      rm.className = 'sk-unit-remove luk-no-print';
      rm.textContent = '×';
      rm.title = 'Zuordnung entfernen';
      rm.addEventListener('click', () => removeUnit(it.id, r.ref_kind, r.ref_id));
      row.appendChild(rm);
    }
    list.appendChild(row);
    return own;
  }

  function renderCardFremd(it) {
    const sec = document.createElement('section');
    sec.className = 'sk-card-section sk-card-fremd';

    const headRow = document.createElement('div');
    headRow.className = 'sk-card-section-title';
    const lbl = document.createElement('span');
    lbl.textContent = 'Fremdeinheiten';
    headRow.appendChild(lbl);
    const sum = document.createElement('span');
    sum.className = 'sk-staerke-sum';
    headRow.appendChild(sum);
    sec.appendChild(headRow);

    const arr = it.fremd || [];
    const sumAcc = [0, 0, 0];
    const list = document.createElement('div');
    list.className = 'sk-fremd-list';
    if (!arr.length) {
      const empty = document.createElement('div');
      empty.className = 'sk-fremd-empty';
      empty.textContent = 'Keine Fremdeinheiten erfasst.';
      list.appendChild(empty);
    } else {
      arr.forEach(entry => {
        const row = document.createElement('div');
        row.className = 'sk-fremd-row sk-fremd-kind-' + (entry.kind || 'sonstige');

        const badge = document.createElement('span');
        badge.className = 'sk-fremd-badge';
        badge.textContent = FREMD_KIND_LABELS[entry.kind] || 'Sonstige';
        row.appendChild(badge);

        const title = document.createElement('span');
        title.className = 'sk-fremd-title';
        title.textContent = entry.bezeichnung || '(ohne Bezeichnung)';
        row.appendChild(title);

        if (entry.funkruf) {
          const fr = document.createElement('span');
          fr.className = 'sk-fremd-funk';
          fr.textContent = entry.funkruf;
          row.appendChild(fr);
        }
        if (entry.ov) {
          const ov = document.createElement('span');
          ov.className = 'sk-fremd-ov';
          ov.textContent = entry.ov;
          row.appendChild(ov);
        }

        const tri = S.parseStaerke(entry.staerke);
        if (tri) { sumAcc[0] += tri[0]; sumAcc[1] += tri[1]; sumAcc[2] += tri[2]; }
        const st = document.createElement('span');
        st.className = 'sk-fremd-staerke';
        st.textContent = tri ? S.fmtStaerke(tri) : (entry.staerke || '—');
        row.appendChild(st);

        if (entry.note) {
          const note = document.createElement('span');
          note.className = 'sk-fremd-note';
          note.textContent = entry.note;
          row.appendChild(note);
        }

        if (canWrite) {
          const ed = document.createElement('button');
          ed.type = 'button';
          ed.className = 'sk-btn sk-btn-small luk-no-print';
          ed.textContent = 'Bearbeiten';
          ed.addEventListener('click', () => openFremdDialog(it.id, entry.id));
          row.appendChild(ed);

          const rm = document.createElement('button');
          rm.type = 'button';
          rm.className = 'sk-btn sk-btn-small sk-btn-danger luk-no-print';
          rm.textContent = '×';
          rm.title = 'Entfernen';
          rm.addEventListener('click', () => removeFremd(it.id, entry.id));
          row.appendChild(rm);
        }
        list.appendChild(row);
      });
    }
    sec.appendChild(list);
    sum.textContent = 'Σ ' + S.fmtStaerke(sumAcc);

    if (canWrite) {
      const addRow = document.createElement('div');
      addRow.className = 'sk-row sk-row-split luk-no-print';
      const sel = document.createElement('select');
      sel.className = 'sk-fremd-kind-select';
      sel.setAttribute('aria-label', 'Art der Fremdeinheit');
      Object.keys(FREMD_KIND_LABELS).forEach(val => {
        const opt = document.createElement('option');
        opt.value = val;
        opt.textContent = FREMD_KIND_LABELS[val];
        sel.appendChild(opt);
      });
      addRow.appendChild(sel);
      const addBtn = document.createElement('button');
      addBtn.type = 'button';
      addBtn.className = 'sk-btn';
      addBtn.textContent = '+ Fremdeinheit';
      addBtn.addEventListener('click', () => openFremdDialog(it.id, null, sel.value || 'sonstige'));
      addRow.appendChild(addBtn);
      sec.appendChild(addRow);
    }
    return sec;
  }

  // ---- Re-Render-Steuerung ----
  // Textfelder, in denen gerade getippt wird, werden nie ueberschrieben:
  // dann werden nur die Nicht-Text-Sektionen ausgetauscht und der volle
  // Neuaufbau bis zum Blur zurueckgestellt.
  function rerenderCard(id) {
    const it = findItem(id);
    const old = cardEl(id);
    if (!it) { if (old) old.remove(); renderJumpList(); updateEmptyHint(); if (viewMode === 'overview') renderOverviewGrid(); return; }
    if (!old) { renderCards(); renderJumpList(); return; }
    const active = document.activeElement;
    if (old.contains(active) && isTextField(active)) {
      patchCardSections(old, it, active);
      queuedRerender.add(id);
    } else {
      old.replaceWith(renderCard(it));
    }
    renderJumpList();
    if (viewMode === 'overview') renderOverviewGrid();
  }
  function patchCardSections(card, it, active) {
    const body = card.querySelector('.sk-card-body');
    if (!body) return;
    const swap = (sel, fresh) => { const o = body.querySelector(sel); if (o) o.replaceWith(fresh); };
    swap('.sk-card-tz', renderCardTz(it));
    swap('.sk-card-units', renderCardUnits(it));
    swap('.sk-card-fremd', renderCardFremd(it));
    const ti = card.querySelector('input.sk-card-title');
    if (ti && ti !== active) { ti.value = it.title || ''; const p = card.querySelector('.sk-card-title-print'); if (p) p.textContent = it.title || '(ohne Titel)'; }
    const ta = card.querySelector('textarea.sk-card-description');
    if (ta && ta !== active) { ta.value = it.description || ''; const p = card.querySelector('.sk-desc-print'); if (p) p.textContent = it.description || '–'; }
  }
  function flushQueuedRerender() {
    if (!queuedRerender.size) return;
    // Verzoegert pruefen: Blur feuert vor dem Focus des naechsten Feldes und
    // vor dem Click auf einen Button in der Karte (der sonst ins Leere liefe).
    setTimeout(() => {
      const active = document.activeElement;
      if ($cards && $cards.contains(active) && isTextField(active)) return;
      const ids = Array.from(queuedRerender);
      queuedRerender.clear();
      if (ids.indexOf('__order__') !== -1) syncCardOrder();
      ids.filter(x => x !== '__order__').forEach(id => rerenderCard(id));
    }, 350);
  }
  // Nur Einheiten-Sektionen + Summen aktualisieren (Tree/Zeit geaendert)
  function refreshUnitsEverywhere() {
    items.forEach(it => {
      const card = cardEl(it.id);
      if (!card) return;
      const o = card.querySelector('.sk-card-units');
      if (o) o.replaceWith(renderCardUnits(it));
    });
    renderJumpList();
    renderTreeSource();
    if (viewMode === 'overview') renderOverviewGrid();
  }

  // ------------------------------------------------------------------
  // Speichern (Titel / Beschreibung / taktische Zeichen) - debounced pro Karte
  // ------------------------------------------------------------------
  function queueSave(id, field) {
    if (!canWrite) return;
    if (!pending[id]) pending[id] = new Set();
    pending[id].add(field);
    if (saveTimers[id]) clearTimeout(saveTimers[id]);
    saveTimers[id] = setTimeout(() => doSave(id), 600);
  }
  async function doSave(id) {
    delete saveTimers[id];
    const it = findItem(id);
    if (!it || !canWrite || !pending[id] || !pending[id].size) return;
    if (saving[id]) { saveTimers[id] = setTimeout(() => doSave(id), 300); return; }
    const fields = Array.from(pending[id]);
    const body = { id: it.id, version: it.version };
    fields.forEach(f => {
      if (f === 'taktische_zeichen') body.taktische_zeichen = it.taktische_zeichen || emptyTz();
      else body[f] = it[f] == null ? '' : String(it[f]);
    });
    // Felder gelten ab jetzt als "in Arbeit beim Server"; tippt der Nutzer
    // weiter, landen sie erneut in pending und werden nachgeschoben.
    pending[id] = new Set();
    saving[id] = true;
    inflight[id] = new Set(fields);
    try {
      const res = await api.post('schadenskonten', 'update', body);
      inflight[id] = new Set();
      const row = normalizeItemRow(res.item);
      const cur = findItem(id);
      if (cur && row) {
        mergeItemRow(cur, row, true);
        // Servernormalisierungen (z.B. getrimmter Titel) nur sichtbar machen,
        // wenn nicht gerade in dem Feld getippt wird.
        const card = cardEl(id);
        if (card) patchCardSections(card, cur, document.activeElement);
        renderJumpList();
      }
      flashSaved(id);
      L.sync.poll();
    } catch (e) {
      const cur = findItem(id);
      if (ui.handleConflict(e, server => {
        if (!cur) return;
        const row = normalizeItemRow(server);
        // Konflikt: Serverstand uebernehmen, eigene Eingaben verwerfen
        pending[id] = new Set();
        mergeItemRow(cur, row, false);
        rerenderCard(id);
      })) { saving[id] = false; return; }
      if (e && e.code === 'not_found') {
        ui.toast('Das Schadenskonto wurde zwischenzeitlich gelöscht.', 'warn', 5000);
        items = items.filter(x => x.id !== id);
        rebuildAssignedIndex();
        rerenderCard(id);
        renderTreeSource();
      } else {
        // Fehlgeschlagene Felder erneut vormerken, damit der naechste Tipp sie mitnimmt.
        fields.forEach(f => { if (!pending[id]) pending[id] = new Set(); pending[id].add(f); });
        ui.toast('Speichern fehlgeschlagen: ' + errText(e), 'error');
      }
    } finally {
      inflight[id] = new Set();
      saving[id] = false;
      if (pending[id] && pending[id].size && !saveTimers[id]) saveTimers[id] = setTimeout(() => doSave(id), 200);
    }
  }
  // Beim Verlassen der Seite ausstehende Saves sofort schicken (best effort).
  function flushOnUnload() {
    if (!canWrite) return;
    Object.keys(saveTimers).forEach(id => {
      clearTimeout(saveTimers[id]);
      delete saveTimers[id];
      const it = findItem(id);
      if (!it || !pending[id] || !pending[id].size) return;
      const body = { csrf: L.CFG.csrf || '', einsatz: CTX.uuid, id: it.id, version: it.version };
      pending[id].forEach(f => { body[f] = f === 'taktische_zeichen' ? (it.taktische_zeichen || emptyTz()) : (it[f] == null ? '' : String(it[f])); });
      try {
        const url = (L.CFG.api && L.CFG.api.schadenskonten) || '/api/schadenskonten';
        const blob = new Blob([JSON.stringify(body)], { type: 'application/json' });
        if (navigator.sendBeacon) navigator.sendBeacon(url + '?action=update', blob);
      } catch (e) { /* best effort */ }
    });
  }
  window.addEventListener('pagehide', flushOnUnload);

  // ------------------------------------------------------------------
  // Mutationen: Schadenskonto
  // ------------------------------------------------------------------
  async function createSchadenskonto(title) {
    if (!canWrite) return;
    $newBtn.disabled = true;
    try {
      const res = await api.post('schadenskonten', 'create', { title: title || '' });
      const it = normalizeItemRow(res.item);
      it.refs = Array.isArray(it.refs) ? it.refs : [];
      it.fremd = (Array.isArray(it.fremd) ? it.fremd : []).map(normalizeFremdRow);
      if (!findItem(it.id)) items.push(it);
      sortItems();
      if (!cardEl(it.id) && $cards) $cards.appendChild(renderCard(it));
      syncCardOrder();
      updateEmptyHint();
      $newTitle.value = '';
      if (viewMode !== 'cards') setViewMode('cards');
      setTimeout(() => { scrollToCard(it.id); const ti = cardEl(it.id) && cardEl(it.id).querySelector('input.sk-card-title'); if (ti && !title) ti.focus(); }, 60);
      ui.toast('Schadenskonto angelegt.', 'ok', 1500);
      L.sync.poll();
    } catch (e) {
      ui.toast('Anlegen fehlgeschlagen: ' + errText(e), 'error');
    } finally {
      $newBtn.disabled = false;
    }
  }
  async function deleteSchadenskonto(id) {
    const it = findItem(id);
    if (!it || !canWrite) return;
    const n = (it.refs || []).length, f = (it.fremd || []).length;
    if (!window.confirm('Schadenskonto „' + (it.title || 'ohne Titel') + '" wirklich löschen?'
      + (n ? '\n' + n + ' Einheiten-Zuordnung(en) werden aufgehoben.' : '')
      + (f ? '\n' + f + ' Fremdeinheit(en) werden mit gelöscht.' : ''))) return;
    if (saveTimers[id]) { clearTimeout(saveTimers[id]); delete saveTimers[id]; }
    pending[id] = new Set();
    try {
      await api.post('schadenskonten', 'delete', { id: it.id, version: it.version });
      items = items.filter(x => x.id !== id);
      rebuildAssignedIndex();
      rerenderCard(id);
      renderTreeSource();
      ui.toast('Gelöscht.', 'ok', 1500);
      L.sync.poll();
    } catch (e) {
      if (ui.handleConflict(e, server => { const row = normalizeItemRow(server); mergeItemRow(it, row, false); rerenderCard(id); })) return;
      if (e && e.code === 'not_found') { items = items.filter(x => x.id !== id); rebuildAssignedIndex(); rerenderCard(id); renderTreeSource(); return; }
      ui.toast('Löschen fehlgeschlagen: ' + errText(e), 'error');
    }
  }
  async function moveCard(id, dir) {
    if (!canWrite) return;
    sortItems();
    const idx = items.findIndex(x => x.id === id);
    const target = idx + dir;
    if (idx < 0 || target < 0 || target >= items.length) return;
    const order = items.map(x => x.id);
    order.splice(idx, 1);
    order.splice(target, 0, id);
    order.forEach((oid, i) => { const it = findItem(oid); if (it) it.sort_order = (i + 1) * 10; });
    syncCardOrder();
    setTimeout(() => scrollToCard(id), 20);
    try {
      await api.post('schadenskonten', 'reorder', { order });
      L.sync.poll();
    } catch (e) {
      ui.toast('Reihenfolge konnte nicht gespeichert werden: ' + errText(e), 'error');
    }
  }

  // ------------------------------------------------------------------
  // Mutationen: Einheiten-Referenzen
  // ------------------------------------------------------------------
  function applyRefLocally(skId, kind, refId) {
    // Eindeutigkeit: ueberall entfernen, dann am Ziel eintragen
    const touched = new Set();
    items.forEach(it => {
      const before = it.refs.length;
      it.refs = it.refs.filter(r => !(r.ref_kind === kind && r.ref_id === refId));
      if (it.refs.length !== before) touched.add(it.id);
    });
    const it = findItem(skId);
    if (it) { it.refs.push({ schadenskonto_id: skId, ref_kind: kind, ref_id: refId }); touched.add(skId); }
    rebuildAssignedIndex();
    touched.forEach(id => rerenderCard(id));
    renderTreeSource();
  }
  function removeRefLocally(skId, kind, refId) {
    const it = findItem(skId);
    if (!it) return;
    const before = it.refs.length;
    it.refs = it.refs.filter(r => !(r.ref_kind === kind && r.ref_id === refId));
    if (it.refs.length === before) return;
    rebuildAssignedIndex();
    rerenderCard(skId);
    renderTreeSource();
  }
  async function addUnit(skId, kind, refId) {
    const it = findItem(skId);
    if (!it || !canWrite) return;
    const ref = T.resolveRef(kind, refId);
    if (!ref) { ui.toast('Einheit nicht in der Führungsharke gefunden.', 'error'); return; }
    if (it.refs.some(r => r.ref_kind === kind && r.ref_id === refId)) {
      ui.toast('Einheit ist bereits diesem Schadenskonto zugeordnet.', 'info', 1800);
      return;
    }
    let force = false;
    const existing = assignedIndex[refKey(kind, refId)];
    if (existing && existing.skId !== skId) {
      if (!confirmMove(ref.label, existing.skTitle, it.title)) return;
      force = true;
    }
    try {
      let res;
      try {
        res = await api.post('schadenskonten', 'ref_add', { id: skId, ref_kind: kind, ref_id: refId, force });
      } catch (e) {
        if (e && e.code === 'ref_assigned' && !force) {
          // Lokaler Stand war veraltet: Server kennt eine andere Zuordnung
          const other = e.extra || {};
          if (!confirmMove(ref.label, other.title || 'anderes Schadenskonto', it.title)) return;
          res = await api.post('schadenskonten', 'ref_add', { id: skId, ref_kind: kind, ref_id: refId, force: true });
        } else throw e;
      }
      applyRefLocally(skId, kind, refId);
      if (res && res.removed_from && res.removed_from.length) {
        ui.toast('„' + ref.label + '" verschoben.', 'ok', 1800);
      } else if (!(res && res.noop)) {
        ui.toast('„' + ref.label + '" zugeordnet.', 'ok', 1500);
      }
      L.sync.poll();
    } catch (e) {
      if (e && e.code === 'not_found') { ui.toast('Das Schadenskonto wurde zwischenzeitlich gelöscht.', 'warn', 5000); return; }
      ui.toast('Zuordnen fehlgeschlagen: ' + errText(e), 'error');
    }
  }
  function confirmMove(unitLabel, fromTitle, toTitle) {
    return window.confirm(
      'Die Einheit „' + unitLabel + '" ist bereits dem Schadenskonto „' + (fromTitle || 'ohne Titel') + '" zugeordnet.\n\n'
      + 'Wirklich auf „' + (toTitle || 'ohne Titel') + '" verschieben?'
    );
  }
  async function removeUnit(skId, kind, refId) {
    if (!canWrite) return;
    try {
      await api.post('schadenskonten', 'ref_remove', { id: skId, ref_kind: kind, ref_id: refId });
      removeRefLocally(skId, kind, refId);
      L.sync.poll();
    } catch (e) {
      if (e && e.code === 'not_found') { removeRefLocally(skId, kind, refId); return; }
      ui.toast('Entfernen fehlgeschlagen: ' + errText(e), 'error');
    }
  }

  // ------------------------------------------------------------------
  // Mutationen: Taktische Zeichen (Teil des Schadenskontos -> update)
  // ------------------------------------------------------------------
  function ensureTz(it) {
    if (!it.taktische_zeichen || typeof it.taktische_zeichen !== 'object') it.taktische_zeichen = emptyTz();
    TZ_KINDS.forEach(k => { if (!Array.isArray(it.taktische_zeichen[k])) it.taktische_zeichen[k] = []; });
    return it.taktische_zeichen;
  }
  function addTz(skId, kind, entry) {
    const it = findItem(skId);
    if (!it || !canWrite) return;
    const tz = ensureTz(it);
    if (entry.file && tz[kind].some(x => x.file && x.file === entry.file)) {
      ui.toast('Zeichen ist bereits hinzugefügt.', 'info', 1500);
      return;
    }
    if (tz[kind].length >= 16) { ui.toast('Maximal 16 Einträge je Kategorie.', 'warn'); return; }
    tz[kind].push(entry);
    rerenderCard(skId);
    queueSave(skId, 'taktische_zeichen');
  }
  function addCustomTz(skId, kind, label) {
    label = (label || '').trim();
    if (!label) return;
    addTz(skId, kind, { file: '', label, custom: true });
  }
  function editCustomTz(skId, kind, idx) {
    const it = findItem(skId);
    if (!it || !canWrite) return;
    const tz = ensureTz(it);
    const entry = tz[kind][idx];
    if (!entry) return;
    const next = (window.prompt('Beschriftung:', entry.label || '') || '').trim();
    if (next === '' || next === entry.label) return;
    entry.label = next;
    rerenderCard(skId);
    queueSave(skId, 'taktische_zeichen');
  }
  function removeTz(skId, kind, idx) {
    const it = findItem(skId);
    if (!it || !canWrite) return;
    const tz = ensureTz(it);
    if (!tz[kind][idx]) return;
    tz[kind].splice(idx, 1);
    rerenderCard(skId);
    queueSave(skId, 'taktische_zeichen');
  }

  // ---- TZ-Dialog ----
  async function loadCatalog() {
    if (catalog) return catalog;
    try {
      const res = await fetch(TZ_CATALOG_URL, { credentials: 'same-origin' });
      catalog = await res.json();
    } catch (e) { catalog = {}; }
    return catalog;
  }
  function tzItemsForKind(kind) {
    const cat = catalog && catalog[TZ_KIND_FOLDER[kind]];
    return cat && Array.isArray(cat.items) ? cat.items : [];
  }
  async function openTzDialog(skId, kind) {
    if (!canWrite) return;
    tzDialogContext = { skId, kind };
    await loadCatalog();
    $tzDialogTitle.textContent = TZ_KIND_LABEL[kind] + ' hinzufügen';
    $tzSearch.value = '';
    if ($tzCustomInput) $tzCustomInput.value = '';
    renderTzPickerGrid(tzItemsForKind(kind), '');
    openDialog($tzDialog);
    setTimeout(() => $tzSearch.focus(), 30);
  }
  function renderTzPickerGrid(allItems, filter) {
    if (!$tzGrid) return;
    $tzGrid.innerHTML = '';
    const filt = (filter || '').trim().toLowerCase();
    const list = filt ? allItems.filter(it => (it.label || it.file).toLowerCase().indexOf(filt) !== -1) : allItems;
    if (!list.length) {
      const empty = document.createElement('div');
      empty.className = 'sk-tz-picker-empty';
      empty.textContent = allItems.length ? 'Keine Treffer.' : 'Kein Katalog verfügbar – Freitext unten nutzen.';
      $tzGrid.appendChild(empty);
      return;
    }
    const folder = TZ_KIND_FOLDER[tzDialogContext.kind];
    list.forEach(it => {
      const cell = document.createElement('button');
      cell.type = 'button';
      cell.className = 'sk-tz-picker-cell';
      cell.title = it.label || it.file;
      const img = document.createElement('img');
      img.src = TZ_BASE + '/' + encodePath(folder + '/' + it.file);
      img.alt = it.label || it.file;
      img.loading = 'lazy';
      cell.appendChild(img);
      const lbl = document.createElement('span');
      lbl.textContent = it.label || String(it.file).replace(/\.svg$/i, '').replace(/_/g, ' ');
      cell.appendChild(lbl);
      cell.addEventListener('click', () => {
        if (!tzDialogContext.skId) return;
        addTz(tzDialogContext.skId, tzDialogContext.kind, { file: it.file, label: it.label || '' });
      });
      $tzGrid.appendChild(cell);
    });
  }
  function submitCustomFromDialog() {
    if (!tzDialogContext.skId || !tzDialogContext.kind || !$tzCustomInput) return;
    const text = ($tzCustomInput.value || '').trim();
    if (!text) { $tzCustomInput.focus(); return; }
    addCustomTz(tzDialogContext.skId, tzDialogContext.kind, text);
    $tzCustomInput.value = '';
    $tzCustomInput.focus();
  }
  if ($tzSearch) $tzSearch.addEventListener('input', () => { if (tzDialogContext.kind) renderTzPickerGrid(tzItemsForKind(tzDialogContext.kind), $tzSearch.value); });
  if ($tzClose) $tzClose.addEventListener('click', () => closeDialog($tzDialog));
  if ($tzCustomAdd) $tzCustomAdd.addEventListener('click', submitCustomFromDialog);
  if ($tzCustomInput) $tzCustomInput.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); submitCustomFromDialog(); } });
  if ($tzSearch) $tzSearch.addEventListener('keydown', e => { if (e.key === 'Enter') e.preventDefault(); });

  function openDialog(d) { if (typeof d.showModal === 'function') d.showModal(); else d.setAttribute('open', ''); }
  function closeDialog(d) { if (typeof d.close === 'function') d.close(); else d.removeAttribute('open'); }

  // ------------------------------------------------------------------
  // Mutationen: Fremdeinheiten (eigene Datensaetze mit Version)
  // ------------------------------------------------------------------
  function openFremdDialog(skId, fremdId, presetKind) {
    const it = findItem(skId);
    if (!it || !canWrite) return;
    let entry = null;
    if (fremdId) {
      entry = (it.fremd || []).find(x => x.id === fremdId) || null;
      if (!entry) return;
    }
    fdDialogContext = { skId, fremdId: entry ? entry.id : null, isNew: !entry };
    $fdTitle.textContent = entry ? 'Fremdeinheit bearbeiten' : 'Neue Fremdeinheit';
    $fdKind.value = entry ? (entry.kind || 'sonstige') : (presetKind || 'sonstige');
    $fdBez.value = entry ? entry.bezeichnung : '';
    $fdFunkruf.value = entry ? entry.funkruf : '';
    $fdOv.value = entry ? entry.ov : '';
    $fdStaerke.value = entry ? entry.staerke : '';
    $fdNote.value = entry ? entry.note : '';
    openDialog($fdDialog);
    setTimeout(() => $fdBez.focus(), 30);
  }
  function upsertFremdLocally(row) {
    const it = findItem(row.schadenskonto_id);
    if (!it) return null;
    const idx = it.fremd.findIndex(x => x.id === row.id);
    if (idx === -1) it.fremd.push(row); else it.fremd[idx] = row;
    sortFremd(it);
    return it;
  }
  function removeFremdLocally(skId, fremdId) {
    const it = findItem(skId) || items.find(x => x.fremd.some(f => f.id === fremdId));
    if (!it) return null;
    const before = it.fremd.length;
    it.fremd = it.fremd.filter(x => x.id !== fremdId);
    return it.fremd.length !== before ? it : null;
  }
  async function saveFremdDialog() {
    const ctxd = fdDialogContext;
    const it = findItem(ctxd.skId);
    if (!it) { closeDialog($fdDialog); return; }
    const fields = {
      kind: $fdKind.value || 'sonstige',
      bezeichnung: ($fdBez.value || '').trim(),
      funkruf: ($fdFunkruf.value || '').trim(),
      ov: ($fdOv.value || '').trim(),
      staerke: ($fdStaerke.value || '').trim(),
      note: ($fdNote.value || '').trim(),
    };
    if (fields.staerke && !S.parseStaerke(fields.staerke)) {
      ui.toast(ui.errorText(new L.LukError('invalid_staerke', 400)), 'warn', 4000);
      $fdStaerke.focus();
      return;
    }
    if (!fields.bezeichnung && !fields.funkruf) { ui.toast('Bitte Bezeichnung oder Funkrufname angeben.', 'warn'); $fdBez.focus(); return; }
    $fdSave.disabled = true;
    try {
      if (ctxd.isNew) {
        const res = await api.post('schadenskonten', 'fremd_create', Object.assign({ id: it.id }, fields));
        const row = normalizeFremdRow(res.fremd);
        if (row) upsertFremdLocally(row);
        closeDialog($fdDialog);
        rerenderCard(it.id);
        ui.toast('Fremdeinheit hinzugefügt.', 'ok', 1500);
      } else {
        const cur = it.fremd.find(x => x.id === ctxd.fremdId);
        if (!cur) { closeDialog($fdDialog); rerenderCard(it.id); return; }
        const changed = {};
        Object.keys(fields).forEach(k => { if (String(cur[k] || '') !== String(fields[k] || '')) changed[k] = fields[k]; });
        if (!Object.keys(changed).length) { closeDialog($fdDialog); return; }
        const res = await api.post('schadenskonten', 'fremd_update', Object.assign({ fremd_id: cur.id, version: cur.version }, changed));
        const row = normalizeFremdRow(res.fremd);
        if (row) upsertFremdLocally(row);
        closeDialog($fdDialog);
        rerenderCard(it.id);
        ui.toast('Gespeichert.', 'ok', 1200);
      }
      L.sync.poll();
    } catch (e) {
      if (ui.handleConflict(e, server => {
        const row = normalizeFremdRow(server);
        if (row && row.schadenskonto_id) { if (row.deleted_at) removeFremdLocally(row.schadenskonto_id, row.id); else upsertFremdLocally(row); }
        closeDialog($fdDialog);
        rerenderCard(it.id);
      })) return;
      if (e && e.code === 'not_found') {
        closeDialog($fdDialog);
        if (!ctxd.isNew) removeFremdLocally(it.id, ctxd.fremdId);
        rerenderCard(it.id);
        ui.toast('Der Datensatz existiert nicht mehr.', 'warn', 4000);
        return;
      }
      ui.toast('Speichern fehlgeschlagen: ' + errText(e), 'error');
    } finally {
      $fdSave.disabled = false;
    }
  }
  async function removeFremd(skId, fremdId) {
    const it = findItem(skId);
    if (!it || !canWrite) return;
    const cur = it.fremd.find(x => x.id === fremdId);
    if (!cur) return;
    if (!window.confirm('Fremdeinheit „' + (cur.bezeichnung || FREMD_KIND_LABELS[cur.kind] || 'ohne Bezeichnung') + '" entfernen?')) return;
    try {
      await api.post('schadenskonten', 'fremd_delete', { fremd_id: cur.id, version: cur.version });
      removeFremdLocally(skId, fremdId);
      rerenderCard(skId);
      L.sync.poll();
    } catch (e) {
      if (ui.handleConflict(e, server => {
        const row = normalizeFremdRow(server);
        if (row && row.id) { if (row.deleted_at) removeFremdLocally(skId, row.id); else upsertFremdLocally(row); }
        rerenderCard(skId);
      })) return;
      if (e && e.code === 'not_found') { removeFremdLocally(skId, fremdId); rerenderCard(skId); return; }
      ui.toast('Entfernen fehlgeschlagen: ' + errText(e), 'error');
    }
  }
  if ($fdSave) $fdSave.addEventListener('click', saveFremdDialog);
  if ($fdCancel) $fdCancel.addEventListener('click', () => closeDialog($fdDialog));
  if ($fdDialog) $fdDialog.addEventListener('keydown', e => {
    if (e.key === 'Enter' && e.target && e.target.tagName !== 'TEXTAREA' && e.target.tagName !== 'BUTTON') { e.preventDefault(); saveFremdDialog(); }
  });

  // ------------------------------------------------------------------
  // Kompakte Uebersicht
  // ------------------------------------------------------------------
  function setViewMode(mode) {
    viewMode = mode === 'overview' ? 'overview' : 'cards';
    document.body.classList.toggle('sk-mode-overview', viewMode === 'overview');
    if ($cardsScroll) $cardsScroll.hidden = (viewMode === 'overview');
    if ($overviewView) $overviewView.hidden = (viewMode !== 'overview');
    if ($overviewBtn) {
      $overviewBtn.textContent = viewMode === 'overview' ? 'Karten' : 'Übersicht';
      $overviewBtn.title = viewMode === 'overview' ? 'Zurück zur Karten-Ansicht' : 'Kompakte Übersicht aller Schadenskonten';
    }
    updateEmptyHint();
    if (viewMode === 'overview') renderOverviewGrid();
    try { localStorage.setItem('luk_sk_view', viewMode); } catch (e) { /* ignore */ }
  }
  function renderOverviewGrid() {
    if (!$overviewGrid) return;
    $overviewGrid.innerHTML = '';
    if (!items.length) {
      const empty = document.createElement('div');
      empty.className = 'sk-overview-empty';
      empty.textContent = 'Keine Schadenskonten angelegt.';
      $overviewGrid.appendChild(empty);
      return;
    }
    items.forEach(it => {
      const tile = document.createElement('button');
      tile.type = 'button';
      tile.className = 'sk-overview-tile';
      tile.setAttribute('data-id', it.id);
      if (canWrite) attachDropZone(tile, it.id, 'sk-card-drop-active', false);

      const title = document.createElement('div');
      title.className = 'sk-overview-tile-title';
      title.textContent = it.title || '(ohne Titel)';
      tile.appendChild(title);

      const desc = document.createElement('div');
      desc.className = 'sk-overview-tile-desc';
      desc.textContent = it.description || '';
      tile.appendChild(desc);

      // Zeichen als kleine Icons
      const tzRow = document.createElement('div');
      tzRow.className = 'sk-overview-tile-tz';
      TZ_KINDS.forEach(kind => {
        ((it.taktische_zeichen || {})[kind] || []).forEach(entry => {
          if (entry.file && !entry.custom) {
            const img = document.createElement('img');
            img.src = TZ_BASE + '/' + encodePath(TZ_KIND_FOLDER[kind] + '/' + entry.file);
            img.alt = entry.label || entry.file;
            img.title = TZ_KIND_LABEL[kind] + ': ' + (entry.label || entry.file);
            img.loading = 'lazy';
            tzRow.appendChild(img);
          } else if (entry.label) {
            const chip = document.createElement('span');
            chip.className = 'luk-chip';
            chip.textContent = entry.label;
            chip.title = TZ_KIND_LABEL[kind];
            tzRow.appendChild(chip);
          }
        });
      });
      if (tzRow.childNodes.length) tile.appendChild(tzRow);

      const units = document.createElement('div');
      units.className = 'sk-overview-tile-units';
      (it.refs || []).forEach(r => {
        const ref = T.resolveRef(r.ref_kind, r.ref_id);
        const chip = document.createElement('span');
        chip.className = 'luk-chip' + (ref ? '' : ' luk-chip-orphan');
        chip.textContent = ref ? ref.label + (ref.funkruf ? ' (' + ref.funkruf + ')' : '') : 'verwaist';
        if (ref) chip.title = ref.path + ' · ' + ui.statusLabel(ref.status);
        units.appendChild(chip);
      });
      (it.fremd || []).forEach(f => {
        const chip = document.createElement('span');
        chip.className = 'luk-chip sk-overview-chip-fremd';
        chip.textContent = (FREMD_KIND_LABELS[f.kind] || 'Fremd') + ': ' + (f.bezeichnung || f.funkruf || '–');
        units.appendChild(chip);
      });
      if (units.childNodes.length) tile.appendChild(units);

      const meta = document.createElement('div');
      meta.className = 'sk-overview-tile-meta';
      meta.textContent = (it.refs || []).length + ' Einh. + ' + (it.fremd || []).length + ' Fremd · Σ ' + S.fmtStaerke(cardStaerkeSum(it));
      tile.appendChild(meta);

      tile.addEventListener('click', () => {
        setViewMode('cards');
        setTimeout(() => scrollToCard(it.id), 30);
      });
      $overviewGrid.appendChild(tile);
    });
  }
  if ($overviewBtn) $overviewBtn.addEventListener('click', () => setViewMode(viewMode === 'overview' ? 'cards' : 'overview'));

  // ------------------------------------------------------------------
  // Drucken
  // ------------------------------------------------------------------
  if ($printBtn) {
    $printBtn.addEventListener('click', () => {
      ui.print(viewMode === 'overview' ? 'Schadenskonten – Übersicht' : 'Schadenskonten');
    });
  }

  // ------------------------------------------------------------------
  // Live-Sync
  // ------------------------------------------------------------------
  L.sync.onEvent((events, treeChanged) => {
    const touched = new Set();
    let listChanged = false;
    let refsChanged = false;
    events.forEach(ev => {
      const a = ev.after || {};
      if (ev.entity === 'schadenskonto') {
        if (ev.action === 'reorder') {
          const order = Array.isArray(a.order) ? a.order : [];
          order.forEach((oid, i) => { const it = findItem(oid); if (it) it.sort_order = (i + 1) * 10; });
          if (order.length) listChanged = true;
          return;
        }
        if (ev.action === 'delete' || a.deleted_at) {
          const id = a.id || ev.entity_id;
          if (findItem(id)) {
            if (saveTimers[id]) { clearTimeout(saveTimers[id]); delete saveTimers[id]; }
            pending[id] = new Set();
            items = items.filter(x => x.id !== id);
            touched.add(id);
            refsChanged = true;
          }
          return;
        }
        if (!a.id) return;
        const row = normalizeItemRow(a);
        const it = findItem(row.id);
        if (it) {
          mergeItemRow(it, row, true);
          touched.add(it.id);
        } else {
          row.refs = Array.isArray(row.refs) ? row.refs : [];
          row.fremd = (Array.isArray(row.fremd) ? row.fremd : []).map(normalizeFremdRow);
          items.push(row);
          listChanged = true;
        }
      } else if (ev.entity === 'sk_ref') {
        if (!a.schadenskonto_id || !a.ref_kind || !a.ref_id) return;
        items.forEach(it => {
          const before = it.refs.length;
          it.refs = it.refs.filter(r => !(r.ref_kind === a.ref_kind && r.ref_id === a.ref_id));
          if (it.refs.length !== before) touched.add(it.id);
        });
        if (!a.removed && ev.action !== 'delete') {
          const it = findItem(a.schadenskonto_id);
          if (it) { it.refs.push({ schadenskonto_id: it.id, ref_kind: a.ref_kind, ref_id: a.ref_id }); touched.add(it.id); }
        }
        refsChanged = true;
      } else if (ev.entity === 'sk_fremd') {
        if (!a.id) return;
        if (ev.action === 'delete' || a.deleted_at) {
          const it = removeFremdLocally(a.schadenskonto_id, a.id);
          if (it) touched.add(it.id);
        } else {
          const it = upsertFremdLocally(normalizeFremdRow(a));
          if (it) touched.add(it.id);
        }
      }
    });
    if (refsChanged) rebuildAssignedIndex();
    if (listChanged) {
      // Neue Karten anhaengen, dann Reihenfolge angleichen
      sortItems();
      items.forEach(it => { if (!cardEl(it.id) && $cards) $cards.appendChild(renderCard(it)); });
      syncCardOrder();
      updateEmptyHint();
    }
    touched.forEach(id => rerenderCard(id));
    if (treeChanged) refreshUnitsEverywhere();
    else if (refsChanged) renderTreeSource();
    if (touched.size || listChanged) renderJumpList();
  });

  // ------------------------------------------------------------------
  // Laden / Init
  // ------------------------------------------------------------------
  async function load() {
    try {
      const data = await api.get('schadenskonten', 'list', { einsatz: CTX.uuid });
      T.load(data.tree || {});
      items = (data.items || []).map(r => {
        const it = normalizeItemRow(r);
        it.refs = Array.isArray(r.refs) ? r.refs.slice() : [];
        it.fremd = (Array.isArray(r.fremd) ? r.fremd : []).map(normalizeFremdRow);
        sortFremd(it);
        return it;
      });
      sortItems();
      rebuildAssignedIndex();
      renderTreeSource();
      renderCards();
      renderJumpList();
      L.sync.start(T.seq);
      if (FOCUS_SK && findItem(FOCUS_SK)) {
        if (viewMode !== 'cards') setViewMode('cards');
        setTimeout(() => scrollToCard(FOCUS_SK), 150);
      }
    } catch (e) {
      ui.toast('Laden fehlgeschlagen: ' + errText(e), 'error', 8000);
    }
  }

  if ($newBtn) {
    $newBtn.addEventListener('click', () => createSchadenskonto(($newTitle.value || '').trim()));
  }
  if ($newTitle) {
    $newTitle.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); $newBtn.click(); } });
  }

  // Lese-Modus
  if (!canWrite) {
    document.body.classList.add('sk-readonly');
    if ($createSection) $createSection.hidden = true;
    if ($newTitle) $newTitle.disabled = true;
    if ($newBtn) $newBtn.disabled = true;
    if ($treeHint) $treeHint.textContent = 'Einheiten der Führungsharke. Zugeordnete Einheiten sind mit ✓ markiert (Klick: zur Karte).';
    ui.showReadonlyBanner();
  }

  // Ansicht aus letzter Sitzung (nur wenn nicht gezielt eine Karte angesprungen wird)
  try {
    if (!FOCUS_SK && localStorage.getItem('luk_sk_view') === 'overview') setViewMode('overview');
  } catch (e) { /* ignore */ }

  // Staerke/Schichten haengen von der Uhrzeit ab -> minuetlich nachziehen
  setInterval(() => { if (!document.hidden) refreshUnitsEverywhere(); }, 60000);

  load();
})();
