// lagekarte.js - Taktische Lagekarte eines Einsatzes (Stabsansicht).
//
// Jedes Kartenobjekt (Zeichen, Flaeche, Linie, Pfeil, Text) ist ein eigener
// versionierter Datensatz. Jede Aenderung ist ein eigener API-Aufruf
// (feature_create / feature_update / feature_delete); andere Clients ziehen
// per Live-Sync (LUK.sync) nach. Zeichen koennen mit Einheiten der
// Fuehrungsharke verknuepft werden (ref_kind/ref_id) und zeigen dann live
// aufgeloeste Werte (Funkruf, OV, Status, Staerke, Schicht).
(function () {
  'use strict';

  const LUK = window.LUK;
  if (!LUK || !LUK.CTX) return;
  const CTX = LUK.CTX;
  const T = LUK.tree;
  const S = LUK.staerke;
  const ui = LUK.ui;
  const api = LUK.api;
  const K = LUK.kraefte;

  const CFG = window.LAGEKARTE_CONFIG || {};
  const CAN_WRITE = ui.can('lagekarte');
  const READONLY = !CAN_WRITE;
  const SYMBOL_BASE = CFG.symbolBaseUrl || '/images/taktische_zeichen/';
  const THUMB_BASE = CFG.thumbBaseUrl || '/images/taktische_zeichen_thumbs/';
  const CATALOG_URL = CFG.catalogUrl || (SYMBOL_BASE + 'catalog.json');
  const SEARCH_LIMIT = 60;
  const GRUNDKARTE_ID = 0;
  const GRUNDKARTE_NAME = 'Grundkarte';
  const DRAW_KINDS = ['polygon', 'polyline', 'rectangle', 'circle'];
  const KIND_LABELS = {
    marker: 'Taktisches Zeichen', polygon: 'Fläche', polyline: 'Linie', rectangle: 'Rechteck',
    circle: 'Kreis', arrow: 'Pfeil', text: 'Text'
  };

  // ---------- State -----------------------------------------------------
  let map;
  let osmLayer, satLayer;
  let drawLayer;                // FeatureGroup fuer Leaflet.Draw-Formen (Edit-Toolbar)
  let symbolLayer;              // LayerGroup fuer Taktische-Zeichen-Marker
  let arrowLayer;               // LayerGroup fuer Pfeile
  let textLayer;                // LayerGroup fuer Freitexte
  let catalog = {};
  let activeCategory = null;
  let activeSearch = '';

  let karten = [];              // Liste aller Karten des Einsatzes (Rows)
  let karte = null;             // aktuell geladene Karte (Row inkl. version)
  const abschnitte = new Map(); // nr -> Row {lagekarte_id, nr, name, version}
  const abschnittVisibility = new Map();  // nr -> boolean (nur Browser-State)
  // Kartenobjekte: id -> { row, layer, pending?, deleted? }
  const features = new Map();

  let selectedId = null;        // ausgewaehltes Zeichen (fuer Slider + Loeschen)
  let placementState = null;    // { file, label, size, rotation }
  let katPending = null;        // Vorbelegung aus dem Kraeftekatalog fuer das naechste Zeichen {kraft_key, ov, call}
  let markerDlgKraft = '';      // Katalog-Verweis im offenen Marker-Dialog
  let heimatLayer = null;       // LayerGroup: Heimatstandorte der OVs (Kraeftekatalog)
  let arrowPlacementState = null;
  let textPlacementActive = false;
  let currentDrawHandler = null;
  let currentDrawType = null;
  let toolbarEditing = false;   // Leaflet.Draw Edit-/Delete-Modus aktiv
  const deferredIds = new Set();// waehrend Edit-Modus zurueckgestellte Sync-Updates
  let syncStarted = false;
  let focusPending = CFG.focus || '';
  let newMapMode = null;        // { mode: 'create' | 'import', mapData? }

  const currentDrawStyle = { color: '#d94b2b', fillColor: '#d94b2b', fillOpacity: 0.3, weight: 3, opacity: 1 };
  const TOOL_IDS = ['polygon', 'polyline', 'rectangle', 'circle', 'arrow', 'text'];

  // ---------- Utils -----------------------------------------------------
  const esc = ui.esc;
  const $ = id => document.getElementById(id);

  function status(msg, kind, timeoutMs) {
    const el = $('lk-status');
    if (!el) return;
    el.textContent = msg;
    el.className = 'lk-status' + (kind ? ' lk-status-' + kind : '');
    el.hidden = false;
    if (status._t) clearTimeout(status._t);
    if (timeoutMs !== 0) status._t = setTimeout(() => { el.hidden = true; }, timeoutMs || 3500);
  }
  function hideStatus() { const el = $('lk-status'); if (el) el.hidden = true; }

  function symbolUrl(file) { return SYMBOL_BASE + String(file).split('/').map(encodeURIComponent).join('/'); }
  function thumbUrl(file) {
    const png = String(file).replace(/\.svg$/i, '.png');
    return THUMB_BASE + png.split('/').map(encodeURIComponent).join('/');
  }
  function clone(o) { return JSON.parse(JSON.stringify(o == null ? null : o)); }
  function sameArr(a, b) {
    a = (a || []).slice().sort(); b = (b || []).slice().sort();
    return a.length === b.length && a.every((x, i) => x === b[i]);
  }
  function unitTypeLabel(kind, type) {
    if (kind === 'node') return type === 'fuehrung' ? 'Führungsstelle' : 'Einheit';
    return type === 'fachberater' ? 'Fachberater' : 'interne Einheit';
  }
  function isDrawKind(kind) { return DRAW_KINDS.indexOf(kind) !== -1; }

  // ---------- Verknuepfung (Harke) --------------------------------------
  // Liefert null (nicht verknuepft), {orphan:true} oder die live
  // aufgeloesten Werte der Einheit.
  function resolveLinked(row) {
    if (!row || !row.ref_id) return null;
    const ref = T.resolveRef(row.ref_kind, row.ref_id);
    if (!ref) return { orphan: true, kind: row.ref_kind, id: row.ref_id };
    const now = Date.now();
    const st = S.unitStaerke(ref.kind, ref.id, now);
    const schichten = ref.kind === 'node' ? T.schichtenOf(ref.id, null) : T.schichtenOf(ref.row.node_id, ref.id);
    const cs = S.currentShift(schichten, now);
    return {
      orphan: false, kind: ref.kind, id: ref.id, ref,
      label: ref.label, path: ref.path, funkruf: ref.funkruf || '', ov: ref.ov || '', status: ref.status,
      // Fuehrungsstellen fuehren keinen Anfahrtsstatus
      fuehrung: ref.kind === 'node' && ref.type === 'fuehrung',
      typeLabel: unitTypeLabel(ref.kind, ref.type),
      staerke: st, staerkeText: S.fmtStaerke(st),
      shift: cs.shift, shiftState: cs.state,
    };
  }
  function shiftText(li) {
    if (!li || !li.shift) return '–';
    const s = li.shift;
    const parts = [];
    if (s.name) parts.push(s.name);
    if (s.funkruf) parts.push('(' + s.funkruf + ')');
    if (s.staerke) parts.push(s.staerke);
    let txt = parts.join(' ') || '–';
    if (li.shiftState === 'running') txt += ' · läuft bis ' + ui.fmtDateTime(s.bis);
    else if (li.shiftState === 'planned') txt += ' · geplant ab ' + ui.fmtDateTime(s.von);
    return txt;
  }
  function linkedDl(li) {
    const dl = document.createDocumentFragment();
    const add = (k, v) => {
      const dt = document.createElement('dt'); dt.textContent = k;
      const dd = document.createElement('dd');
      if (v instanceof Node) dd.appendChild(v); else dd.textContent = v || '–';
      dl.appendChild(dt); dl.appendChild(dd);
    };
    if (li.orphan) {
      add('Verknüpfung', li.kind + ':' + li.id);
      return dl;
    }
    add('Bezeichnung', li.label + ' (' + li.typeLabel + ')');
    if (li.path && li.path !== li.label) add('Gliederung', li.path);
    add('Funkrufname', li.funkruf);
    add('Ortsverband', li.ov);
    const kk = li.ref && li.ref.row ? li.ref.row.kraft_key : '';
    if (kk && K && K.lookup(kk)) {
      const a = K.avail(K.lookup(kk).einheit);
      const span = document.createElement('span');
      span.appendChild(K.dot(a));
      span.appendChild(document.createTextNode(' ' + K.label(kk) + ' – ' + a.text));
      add('Katalog', span);
    }
    if (!li.fuehrung) add('Status', ui.statusBadge(li.status));
    add('Stärke (jetzt)', li.staerkeText);
    add('Schicht', shiftText(li));
    return dl;
  }
  function fillLinkSelect(sel, kind, id) {
    if (!sel) return;
    sel.innerHTML = '';
    const none = document.createElement('option');
    none.value = '';
    none.textContent = '– keine Verknüpfung –';
    sel.appendChild(none);
    const cur = kind && id ? kind + ':' + id : '';
    let found = cur === '';
    T.allUnits().forEach(u => {
      const o = document.createElement('option');
      o.value = u.kind + ':' + u.id;
      o.textContent = '  '.repeat(u.depth) + (u.depth ? '└ ' : '') + u.label + ' [' + unitTypeLabel(u.kind, u.type) + ']';
      if (o.value === cur) found = true;
      sel.appendChild(o);
    });
    if (!found) {
      const o = document.createElement('option');
      o.value = cur;
      o.textContent = '(verwaist) ' + cur;
      sel.appendChild(o);
    }
    sel.value = cur;
  }
  function refFromSelect(sel) {
    const v = sel ? sel.value : '';
    if (!v) return { ref_kind: null, ref_id: null };
    const i = v.indexOf(':');
    return { ref_kind: v.slice(0, i), ref_id: v.slice(i + 1) };
  }

  // ---------- Staerke pro Abschnitt ------------------------------------
  function markerStaerke(row) {
    const li = resolveLinked(row);
    if (li && !li.orphan) return li.staerke;
    if (li) return null;
    return S.parseStaerke(row.props && row.props.info && row.props.info.staerke);
  }
  function computeAbschnittStaerke(nr) {
    const acc = [0, 0, 0];
    features.forEach(f => {
      if (!f.row || f.row.kind !== 'marker' || f.deleted) return;
      if (nr !== GRUNDKARTE_ID && (f.row.abschnitte || []).indexOf(nr) === -1) return;
      const t = markerStaerke(f.row);
      if (t) { acc[0] += t[0]; acc[1] += t[1]; acc[2] += t[2]; }
    });
    return S.fmtStaerke(acc);
  }
  function updateAbschnittStaerken() {
    document.querySelectorAll('.lk-abschnitt-row[data-id]').forEach(row => {
      const nr = parseInt(row.getAttribute('data-id'), 10);
      const span = row.querySelector('.lk-abschnitt-staerke');
      if (span) span.textContent = computeAbschnittStaerke(nr);
    });
  }

  // ---------- Map init --------------------------------------------------
  function initMap() {
    const mc = (LUK.CFG && LUK.CFG.map_center) || {};
    const startCenter = (typeof mc.lat === 'number') ? [mc.lat, mc.lng] : [52.5, 13.4];
    map = L.map('map', { zoomControl: true, preferCanvas: false }).setView(startCenter, 11);

    osmLayer = L.tileLayer(((window.CURRENT_OV && window.CURRENT_OV.tile_url) || '/tiles/{z}/{x}/{y}.png'), {
      attribution: '&copy; OpenStreetMap-Mitwirkende', maxZoom: 19
    });
    satLayer = L.tileLayer('https://server.arcgisonline.com/ArcGIS/rest/services/World_Imagery/MapServer/tile/{z}/{y}/{x}', {
      attribution: 'Tiles &copy; Esri &mdash; Source: Esri, Maxar, Earthstar Geographics', maxZoom: 19
    });
    osmLayer.addTo(map);
    L.control.scale({ position: 'bottomright', imperial: false }).addTo(map);

    symbolLayer = L.layerGroup().addTo(map);
    drawLayer = new L.FeatureGroup();
    map.addLayer(drawLayer);
    arrowLayer = L.layerGroup().addTo(map);
    textLayer = L.layerGroup().addTo(map);

    map.on('zoomend', rebuildAllArrowheads);

    if (CAN_WRITE) {
      const drawControl = new L.Control.Draw({
        position: 'topleft',
        edit: { featureGroup: drawLayer, remove: true },
        draw: false
      });
      map.addControl(drawControl);

      map.on(L.Draw.Event.CREATED, onDrawCreated);
      map.on(L.Draw.Event.EDITED, onDrawEdited);
      map.on(L.Draw.Event.DELETED, onDrawDeleted);
      map.on('draw:editstart draw:deletestart', () => { toolbarEditing = true; });
      map.on('draw:editstop draw:deletestop', () => { toolbarEditing = false; flushDeferred(); });
    }

    document.querySelectorAll('input[name="lk-basemap"]').forEach(el => {
      el.addEventListener('change', () => setBasemap(el.value));
    });
    map.on('click', onMapClick);
  }

  function setBasemap(which) {
    if (which === 'sat') {
      if (map.hasLayer(osmLayer)) map.removeLayer(osmLayer);
      if (!map.hasLayer(satLayer)) satLayer.addTo(map);
    } else {
      if (map.hasLayer(satLayer)) map.removeLayer(satLayer);
      if (!map.hasLayer(osmLayer)) osmLayer.addTo(map);
    }
    const r = document.querySelector('input[name="lk-basemap"][value="' + which + '"]');
    if (r) r.checked = true;
  }
  function currentBasemap() { return map.hasLayer(satLayer) ? 'sat' : 'osm'; }

  // ---------- Katalog / Symbol-Grid ------------------------------------
  async function loadCatalog() {
    try {
      const res = await fetch(CATALOG_URL, { credentials: 'same-origin' });
      if (!res.ok) throw new Error('HTTP ' + res.status);
      catalog = await res.json();
      const cats = Object.keys(catalog).sort();
      activeCategory = cats[0] || null;
      if (CAN_WRITE) { populateCategorySelect(cats); renderSymbolGrid(); }
    } catch (err) {
      console.error('Katalog-Fehler', err);
      ui.toast('Katalog der taktischen Zeichen konnte nicht geladen werden.', 'error');
    }
  }

  function populateCategorySelect(cats) {
    const sel = $('lk-symbol-category');
    if (!sel) return;
    sel.innerHTML = '';
    cats.forEach(key => {
      const opt = document.createElement('option');
      opt.value = key;
      opt.textContent = catalog[key].label + ' (' + catalog[key].items.length + ')';
      sel.appendChild(opt);
    });
    sel.value = activeCategory;
    sel.addEventListener('change', () => { activeCategory = sel.value; renderSymbolGrid(); });
    const search = $('lk-symbol-search');
    if (search) search.addEventListener('input', () => { activeSearch = search.value.trim().toLowerCase(); renderSymbolGrid(); });
  }

  let tileObserver = null;
  function ensureTileObserver() {
    if (tileObserver || !('IntersectionObserver' in window)) return tileObserver;
    tileObserver = new IntersectionObserver(entries => {
      entries.forEach(entry => {
        if (!entry.isIntersecting) return;
        const img = entry.target;
        if (img.dataset.src && !img.src) img.src = img.dataset.src;
        tileObserver.unobserve(img);
      });
    }, { root: $('lk-symbol-grid'), rootMargin: '200px', threshold: 0.01 });
    return tileObserver;
  }

  function renderSymbolGrid() {
    const grid = $('lk-symbol-grid');
    if (!grid) return;
    if (tileObserver) grid.querySelectorAll('img[data-src]').forEach(img => tileObserver.unobserve(img));
    grid.innerHTML = '';
    let items = [];
    let truncated = false;
    if (activeSearch) {
      Object.keys(catalog).forEach(key => {
        catalog[key].items.forEach(it => {
          if (items.length >= SEARCH_LIMIT + 1) return;
          if (it.label.toLowerCase().includes(activeSearch)) items.push({ file: key + '/' + it.file, label: it.label });
        });
      });
      if (items.length > SEARCH_LIMIT) { truncated = true; items = items.slice(0, SEARCH_LIMIT); }
    } else if (activeCategory && catalog[activeCategory]) {
      items = catalog[activeCategory].items.map(it => ({ file: activeCategory + '/' + it.file, label: it.label }));
    }
    const observer = ensureTileObserver();
    const frag = document.createDocumentFragment();
    items.forEach(it => {
      const tile = document.createElement('div');
      tile.className = 'lk-symbol-tile';
      tile.title = it.label;
      tile.dataset.file = it.file;
      const img = document.createElement('img');
      img.loading = 'lazy'; img.decoding = 'async'; img.alt = it.label;
      if (observer) { img.dataset.src = thumbUrl(it.file); observer.observe(img); } else img.src = thumbUrl(it.file);
      img.onerror = () => { img.alt = 'Bild fehlt'; img.style.opacity = '0.3'; };
      tile.appendChild(img);
      tile.addEventListener('click', () => activateTile(tile, it));
      frag.appendChild(tile);
    });
    grid.appendChild(frag);
    const old = $('lk-symbol-grid-note');
    if (old) old.remove();
    if (truncated) {
      const note = document.createElement('div');
      note.id = 'lk-symbol-grid-note';
      note.className = 'lk-hint';
      note.style.marginTop = '6px';
      note.textContent = 'Nur erste ' + SEARCH_LIMIT + ' Treffer — bitte genauer suchen.';
      grid.parentNode.insertBefore(note, grid.nextSibling);
    }
  }

  function activateTile(tile, item) {
    if (!requireKarte()) return;
    cancelAllTools();
    document.querySelectorAll('.lk-symbol-tile.active').forEach(t => t.classList.remove('active'));
    tile.classList.add('active');
    const size = parseInt($('lk-scale-slider').value, 10) || 40;
    const rotation = parseInt($('lk-rotate-slider').value, 10) || 0;
    placementState = { file: item.file, label: item.label, size, rotation };
    $('lk-cancel-place-btn').hidden = false;
    status('Klicke in die Karte, um „' + item.label + '“ zu platzieren.', null, 0);
    map.getContainer().style.cursor = 'crosshair';
  }
  function cancelPlacement() {
    placementState = null;
    katPending = null;
    renderKatLine();
    document.querySelectorAll('.lk-symbol-tile.active').forEach(t => t.classList.remove('active'));
    const b = $('lk-cancel-place-btn');
    if (b) b.hidden = true;
    map.getContainer().style.cursor = '';
    hideStatus();
  }

  // ---------- Kraeftekatalog: Zeichen aus Katalog-Einheit, Heimatstandorte ----
  function renderKatLine() {
    const host = $('lk-kat-line');
    if (!host) return;
    host.innerHTML = '';
    if (katPending && katPending.kraft_key && K) host.appendChild(K.chip(katPending.kraft_key, { onRemove: () => { katPending = null; renderKatLine(); } }));
  }
  // Auswahl im Katalog-Picker: passendes Zeichen aktivieren, Infos vorbelegen
  function placeFromKatalog(entry) {
    if (!requireKarte()) return;
    const f = K.fill(entry);
    katPending = { kraft_key: f.kraft_key, ov: f.bezeichnung + ' ' + f.ov, call: f.funkruf || '' };
    renderKatLine();
    const sym = K.symbolFor(f.kraft_key);
    let item = null;
    if (sym) {
      const slash = sym.indexOf('/');
      const cat = sym.slice(0, slash), file = sym.slice(slash + 1);
      const it = catalog[cat] && catalog[cat].items.find(x => x.file === file);
      if (it) item = { file: sym, label: it.label };
    }
    if (item) {
      cancelAllTools();
      document.querySelectorAll('.lk-symbol-tile.active').forEach(t => t.classList.remove('active'));
      const size = parseInt($('lk-scale-slider').value, 10) || 40;
      const rotation = parseInt($('lk-rotate-slider').value, 10) || 0;
      placementState = { file: item.file, label: item.label, size, rotation };
      $('lk-cancel-place-btn').hidden = false;
      status('Klicke in die Karte, um „' + K.label(f.kraft_key) + '“ (' + item.label + ') zu platzieren.', null, 0);
      map.getContainer().style.cursor = 'crosshair';
    } else {
      status('Kein passendes Zeichen im Katalog – bitte unten ein Zeichen wählen, die Einheit wird dann übernommen.', 'warn', 8000);
    }
    if (entry.avail.cls === 'gebunden') ui.toast('Hinweis: ' + K.label(f.kraft_key) + ' – ' + entry.avail.text, 'warn', 6000);
  }
  function renderMarkerKraft() {
    const host = $('lk-mi-kraft');
    if (!host) return;
    host.innerHTML = '';
    if (!markerDlgKraft || !K) return;
    host.appendChild(K.chip(markerDlgKraft, READONLY ? {} : { onRemove: () => { markerDlgKraft = ''; renderMarkerKraft(); } }));
  }
  const HEIMAT_KEY = 'luk_lk_heimat';
  function bestAvailOf(ov) {
    let best = null;
    (ov.einheiten || []).forEach(e => { const a = K.avail(e); if (!best || a.rank < best.rank) best = a; });
    return best;
  }
  function renderHeimat() {
    const cb = $('lk-show-heimat');
    const stand = $('lk-heimat-stand');
    if (!map || !cb) return;
    if (heimatLayer) { map.removeLayer(heimatLayer); heimatLayer = null; }
    if (!cb.checked) { if (stand) stand.textContent = ''; return; }
    if (!K || !K.data) { if (stand) stand.textContent = 'Kräftekatalog wird geladen…'; if (K) K.load(); return; }
    heimatLayer = L.layerGroup();
    K.data.ovs.forEach(ov => {
      if (ov.lat == null || ov.lng == null || !(ov.einheiten || []).length) return;
      const best = bestAvailOf(ov);
      const m = L.circleMarker([ov.lat, ov.lng], { radius: 7, color: '#333', weight: 1, fillColor: best ? best.color : '#999', fillOpacity: 0.9 });
      const reg = K.region(ov);
      const d = K.distKm(ov);
      m.bindTooltip(esc(ov.name) + (reg ? ' · ' + esc(reg.kurz || reg.name) : '') + (d !== null ? ' · ' + esc(K.fmtDist(d)) : ''), { direction: 'top' });
      m.bindPopup(() => {
        let html = '<div class="lk-heimat-pop"><b>' + esc(ov.name) + '</b>' + (reg ? '<br><span class="lk-tt-muted">' + esc(reg.name) + '</span>' : '')
          + (d !== null ? '<br><span class="lk-tt-muted">' + esc(K.fmtDist(d)) + ' / ' + esc(K.fmtEta(d)) + (K.hasOrt() ? ' zum Einsatzort' : ' zum Kartenzentrum') + '</span>' : '')
          + '<ul class="lk-heimat-list">';
        (ov.einheiten || []).forEach(e => {
          const a = K.avail(e);
          html += '<li><span class="luk-kp-dot luk-kp-dot-' + a.cls + '"></span> ' + esc(e.short + (e.nr ? ' ' + e.nr : '')) + ' <span class="lk-tt-muted">' + esc(a.text) + '</span>'
            + (CAN_WRITE ? ' <a href="/harke?einsatz=' + encodeURIComponent(CTX.uuid) + '&kraft=' + encodeURIComponent(e.key) + '" title="In der Führungsharke als Einheit anlegen">→ Harke</a>' : '')
            + '</li>';
        });
        html += '</ul></div>';
        return html;
      }, { maxWidth: 360 });
      heimatLayer.addLayer(m);
    });
    heimatLayer.addTo(map);
    const st = K.data.stein || {};
    if (stand) stand.textContent = st.ok ? 'STEIN-Stand ' + ui.fmtDateTime(st.stand) + (st.stale ? ' (veraltet)' : '') : 'ohne STEIN-Live-Daten (lila = keine Live-Daten)';
  }

  // ---------- Werkzeuge -------------------------------------------------
  function setActiveTool(type) {
    TOOL_IDS.forEach(id => { const btn = $('lk-tool-' + id); if (btn) btn.classList.toggle('lk-tool-active', id === type); });
  }
  function clearActiveTool() { setActiveTool(null); }
  function isToolActive(type) {
    if (type === 'arrow') return !!arrowPlacementState;
    if (type === 'text') return !!textPlacementActive;
    return currentDrawType === type;
  }
  function cancelAllTools() {
    if (arrowPlacementState) cancelArrowPlacement();
    if (textPlacementActive) cancelTextPlacement();
    if (currentDrawHandler) {
      try { currentDrawHandler.disable(); } catch (_) { /* ignore */ }
      currentDrawHandler = null;
      currentDrawType = null;
    }
    clearActiveTool();
  }
  function startLeafletDraw(type) {
    const opts = { color: currentDrawStyle.color, weight: currentDrawStyle.weight, opacity: 1 };
    if (type !== 'polyline') { opts.fillColor = currentDrawStyle.color; opts.fillOpacity = currentDrawStyle.fillOpacity; }
    let handler = null;
    if (type === 'polygon') handler = new L.Draw.Polygon(map, { allowIntersection: false, showArea: true, shapeOptions: opts });
    else if (type === 'polyline') handler = new L.Draw.Polyline(map, { shapeOptions: opts });
    else if (type === 'rectangle') handler = new L.Draw.Rectangle(map, { shapeOptions: opts });
    else if (type === 'circle') handler = new L.Draw.Circle(map, { shapeOptions: opts });
    if (!handler) return;
    handler.enable();
    currentDrawHandler = handler;
    currentDrawType = type;
    setActiveTool(type);
  }
  function activateTool(type) {
    if (READONLY) return;
    cancelAllTools();
    if (placementState) cancelPlacement();
    if (!type) return;
    if (!requireKarte()) return;
    if (type === 'arrow') { startArrowPlacement(); return; }
    if (type === 'text') { startTextPlacement(); return; }
    startLeafletDraw(type);
  }
  function requireKarte() {
    if (karte) return true;
    ui.toast('Bitte zuerst eine Lagekarte anlegen bzw. auswählen.', 'warn');
    return false;
  }

  // ---------- Einsatzabschnitte ----------------------------------------
  function sortedAbschnitte() {
    return Array.from(abschnitte.values()).sort((a, b) => a.nr - b.nr);
  }
  function isAbschnittVisible(nr) {
    if (nr === GRUNDKARTE_ID) return true;
    return abschnittVisibility.get(nr) !== false;
  }
  // Objekte ohne echten Abschnitt (nur Grundkarte) sind immer sichtbar.
  function isFeatureVisible(row) {
    const real = (row.abschnitte || []).filter(n => n !== GRUNDKARTE_ID);
    if (!real.length) return true;
    return real.some(n => isAbschnittVisible(n));
  }
  function applyVisibilityTo(f) {
    if (!f || !f.layer || f.deleted) return;
    const visible = isFeatureVisible(f.row);
    if (f.row.kind === 'marker') {
      f.layer._lkHidden = !visible;
      if (f.layer._icon) f.layer._icon.style.display = visible ? '' : 'none';
      return;
    }
    const parent = parentLayerFor(f.row.kind);
    const onMap = parent.hasLayer(f.layer);
    if (visible && !onMap) parent.addLayer(f.layer);
    if (!visible && onMap) parent.removeLayer(f.layer);
  }
  function applyVisibility() { features.forEach(applyVisibilityTo); }

  function renderAbschnittList() {
    const host = $('lk-abschnitt-list');
    if (!host) return;
    host.innerHTML = '';
    const rows = [{ nr: GRUNDKARTE_ID, name: GRUNDKARTE_NAME }].concat(sortedAbschnitte());
    rows.forEach(a => {
      const isGk = a.nr === GRUNDKARTE_ID;
      const row = document.createElement('div');
      row.className = 'lk-abschnitt-row' + (isGk ? ' lk-abschnitt-pinned' : '');
      row.setAttribute('data-id', String(a.nr));

      const visCb = document.createElement('input');
      visCb.type = 'checkbox';
      visCb.className = 'lk-abschnitt-vis';
      visCb.checked = true;
      if (isGk) { visCb.disabled = true; visCb.title = 'Grundkarte ist immer sichtbar'; }
      else {
        visCb.checked = isAbschnittVisible(a.nr);
        visCb.title = 'Sichtbarkeit umschalten (nur Anzeige)';
        visCb.addEventListener('change', () => { abschnittVisibility.set(a.nr, visCb.checked); applyVisibility(); });
      }
      const num = document.createElement('span');
      num.className = 'lk-abschnitt-num';
      num.textContent = a.nr;

      const editable = CAN_WRITE && !isGk;
      const name = document.createElement(editable ? 'input' : 'span');
      name.className = 'lk-abschnitt-name';
      if (editable) {
        name.type = 'text'; name.value = a.name; name.maxLength = 80;
        name.addEventListener('change', () => renameAbschnitt(a.nr, name.value));
      } else name.textContent = a.name;

      row.appendChild(visCb); row.appendChild(num); row.appendChild(name);

      const st = document.createElement('span');
      st.className = 'lk-abschnitt-staerke';
      st.title = 'Gesamtstärke aller Zeichen im Abschnitt (X/Y/Z//Σ), verknüpfte Einheiten live';
      st.textContent = computeAbschnittStaerke(a.nr);
      row.appendChild(st);

      if (editable) {
        const del = document.createElement('button');
        del.type = 'button';
        del.className = 'lk-abschnitt-del';
        del.title = 'Abschnitt löschen';
        del.textContent = '×';
        del.addEventListener('click', () => {
          if (confirm('Einsatzabschnitt "' + a.nr + ' ' + a.name + '" löschen?\nDie Zuordnung wird aus allen Objekten entfernt.')) removeAbschnitt(a.nr);
        });
        row.appendChild(del);
      }
      host.appendChild(row);
    });
  }

  // Generischer Abschnitts-Picker (Checkboxen) fuer die Dialoge
  function renderPicker(hostId, selected) {
    const host = $(hostId);
    if (!host) return;
    host.innerHTML = '';
    const sel = new Set(selected || []);
    const rows = [{ nr: GRUNDKARTE_ID, name: GRUNDKARTE_NAME }].concat(sortedAbschnitte());
    rows.forEach(a => {
      const isGk = a.nr === GRUNDKARTE_ID;
      const label = document.createElement('label');
      label.className = 'lk-mi-abschnitt-item' + (isGk ? ' lk-mi-abschnitt-pinned' : '');
      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.value = String(a.nr);
      cb.checked = isGk || sel.has(a.nr);
      if (isGk) { cb.disabled = true; cb.title = 'Grundkarte ist immer zugeordnet'; }
      else if (READONLY) cb.disabled = true;
      label.appendChild(cb);
      const text = document.createElement('span');
      text.textContent = a.nr + ' – ' + a.name;
      label.appendChild(text);
      host.appendChild(label);
    });
  }
  function getPicked(hostId) {
    const host = $(hostId);
    if (!host) return [];
    return Array.from(host.querySelectorAll('input[type="checkbox"]:checked'))
      .map(cb => parseInt(cb.value, 10))
      .filter(n => !isNaN(n) && n >= 1);
  }

  function addAbschnitt(name) {
    const n = (name || '').trim();
    if (!n || !karte) return;
    return call(async () => {
      const res = await api.post('lagekarten', 'abschnitt_create', { karte: karte.id, name: n });
      if (res.abschnitt) { abschnitte.set(res.abschnitt.nr, res.abschnitt); abschnittVisibility.set(res.abschnitt.nr, true); }
      renderAbschnittList();
    }, 'Abschnitt angelegt.');
  }
  function renameAbschnitt(nr, name) {
    const a = abschnitte.get(nr);
    const n = (name || '').trim();
    if (!a || !n || n === a.name) { renderAbschnittList(); return; }
    return call(async () => {
      const res = await api.post('lagekarten', 'abschnitt_update', { karte: karte.id, nr, version: a.version, name: n });
      if (res.abschnitt) abschnitte.set(nr, res.abschnitt);
      renderAbschnittList();
    });
  }
  function removeAbschnitt(nr) {
    const a = abschnitte.get(nr);
    if (!a) return;
    return call(async () => {
      const res = await api.post('lagekarten', 'abschnitt_delete', { karte: karte.id, nr, version: a.version });
      abschnitte.delete(nr);
      abschnittVisibility.delete(nr);
      (res.features || []).forEach(renderFeature);
      renderAbschnittList();
      applyVisibility();
    }, 'Abschnitt gelöscht.');
  }

  // ---------- Kartenobjekte: Layer-Aufbau --------------------------------
  function parentLayerFor(kind) {
    if (kind === 'marker') return symbolLayer;
    if (kind === 'text') return textLayer;
    if (kind === 'arrow') return arrowLayer;
    return drawLayer;
  }
  function styleOptions(kind, style) {
    const s = style || {};
    const color = s.color || '#d94b2b';
    return {
      color,
      weight: typeof s.weight === 'number' ? s.weight : 3,
      opacity: typeof s.opacity === 'number' ? s.opacity : 1,
      fillColor: s.fillColor || color,
      fillOpacity: kind === 'polyline' ? 0 : (typeof s.fillOpacity === 'number' ? s.fillOpacity : 0.3),
    };
  }

  function makeMarkerIcon(row) {
    const p = row.props || {};
    const size = p.baseSize || 40;
    const rot = p.rotation || 0;
    const li = resolveLinked(row);
    let html = '<img src="' + symbolUrl(p.file) + '"'
      + ' style="width:' + size + 'px;height:' + size + 'px;transform:rotate(' + rot + 'deg);transform-origin:50% 50%;user-select:none;-webkit-user-drag:none;"'
      + ' onerror="this.style.border=\'2px dashed red\';this.alt=\'Bild fehlt\';" alt="" />';
    if (li) {
      html += '<span class="lk-link-badge' + (li.orphan ? ' lk-link-badge-orphan' : '') + '" title="'
        + (li.orphan ? 'Verknüpfte Einheit existiert nicht mehr' : 'Verknüpft mit ' + esc(li.label)) + '">' + (li.orphan ? '!' : '🔗') + '</span>';
    }
    const cls = 'lk-marker-icon' + (li && li.orphan ? ' lk-marker-orphan' : '') + (li && !li.orphan ? ' lk-marker-linked' : '')
      + (row.id === selectedId ? ' lk-marker-selected' : '');
    return L.divIcon({ className: cls, html, iconSize: [size, size], iconAnchor: [size / 2, size / 2] });
  }

  function markerInfoHtml(row, full) {
    const p = row.props || {};
    const info = p.info || {};
    const li = resolveLinked(row);
    const lines = [];
    if (li && li.orphan) {
      lines.push('<b>' + esc(p.label || 'Zeichen') + '</b>');
      lines.push('<span class="lk-tt-orphan">Verknüpfung verwaist</span>');
    } else if (li) {
      lines.push('<b>🔗 ' + esc(li.label) + '</b>');
      if (li.path && li.path !== li.label) lines.push('<span class="lk-tt-muted">' + esc(li.path) + '</span>');
      if (li.funkruf) lines.push(esc(li.funkruf));
      if (li.ov) lines.push(esc(li.ov));
      lines.push('Status: ' + esc(ui.statusLabel(li.status)));
      lines.push('Stärke: ' + esc(li.staerkeText));
      if (li.shift) lines.push('Schicht: ' + esc(shiftText(li)));
    } else {
      if (info.ov) lines.push('<b>' + esc(info.ov) + '</b>');
      if (info.call) lines.push(esc(info.call));
      if (info.staerke) lines.push('Stärke: ' + esc(info.staerke));
      if (!lines.length) lines.push(esc(p.label || 'Zeichen'));
    }
    // Kraeftekatalog: STEIN-Status / Bindung der Einheit (verknuepft oder Freitext mit Verweis)
    const kk = katalogKeyOf(row);
    if (kk) lines.push(katalogStatusHtml(kk));
    if (full && info.note) lines.push('<i>' + esc(info.note).replace(/\n/g, '<br>') + '</i>');
    return lines.join('<br>');
  }
  // Katalog-Schluessel eines Zeichens: aus der verknuepften Einheit, sonst aus den Freitext-Infos
  function katalogKeyOf(row) {
    if (!row) return '';
    const li = resolveLinked(row);
    if (li && !li.orphan && li.ref && li.ref.row && li.ref.row.kraft_key) return li.ref.row.kraft_key;
    if (li) return '';
    return (row.props && row.props.info && row.props.info.kraft_key) || '';
  }
  function katalogStatusHtml(key) {
    if (!K || !K.lookup(key)) return '';
    const a = K.avail(K.lookup(key).einheit);
    return '<span class="luk-kp-dot luk-kp-dot-' + a.cls + '"></span> ' + esc(K.label(key)) + ' – ' + esc(a.text);
  }
  function shapeTooltipHtml(row) {
    const p = row.props || {};
    const li = resolveLinked(row);
    const lines = [];
    if (li && li.orphan) lines.push('<span class="lk-tt-orphan">Verknüpfung verwaist</span>');
    else if (li) {
      lines.push('<b>🔗 ' + esc(li.label) + '</b>');
      if (li.funkruf) lines.push(esc(li.funkruf));
      lines.push('Status: ' + esc(ui.statusLabel(li.status)) + ' · Stärke: ' + esc(li.staerkeText));
    }
    if (p.label) lines.unshift('<b>' + esc(p.label) + '</b>');
    if (p.note) lines.push('<i>' + esc(p.note) + '</i>');
    return lines.join('<br>');
  }

  function makeTextIcon(p) {
    p = p || {};
    const isEmpty = !p.text;
    const styleParts = [
      'font-size:' + ((p.fontSize || 16) + 'px'),
      'color:' + (p.color || '#222222'),
      'font-weight:' + (p.bold ? 'bold' : 'normal'),
      'font-style:' + (p.italic ? 'italic' : 'normal'),
      'text-decoration:' + (p.underline ? 'underline' : 'none')
    ];
    const inner = isEmpty ? '[Text]' : esc(p.text).replace(/\n/g, '<br>');
    const html = '<div class="lk-text-marker-content" style="' + styleParts.join(';') + '">' + inner + '</div>';
    return L.divIcon({ className: 'lk-text-marker' + (isEmpty ? ' lk-text-marker-empty' : ''), html, iconSize: [1, 1], iconAnchor: [0, 0] });
  }

  function buildLayer(row) {
    const g = row.geometry || {};
    const kind = row.kind;
    let layer = null;
    if (kind === 'marker') {
      layer = L.marker([g.lat, g.lng], { icon: makeMarkerIcon(row), draggable: CAN_WRITE, keyboard: false, interactive: true, riseOnHover: true });
      layer.on('click', ev => { L.DomEvent.stopPropagation(ev); onMarkerClick(layer); });
      if (CAN_WRITE) layer.on('dragend', () => onMarkerDragEnd(layer));
      layer.bindTooltip(l => { const f = features.get(l.lkId); return f ? markerInfoHtml(f.row, false) : ''; }, { direction: 'top', offset: [0, -((row.props && row.props.baseSize) || 40) / 2] });
    } else if (kind === 'text') {
      layer = L.marker([g.lat, g.lng], { icon: makeTextIcon(row.props), draggable: CAN_WRITE, keyboard: false, interactive: true, riseOnHover: true });
      layer.on('click', ev => { L.DomEvent.stopPropagation(ev); const f = features.get(layer.lkId); if (f) openTextDialog(f, ev); });
      if (CAN_WRITE) layer.on('dragend', () => onMarkerDragEnd(layer));
    } else if (kind === 'arrow') {
      layer = createArrowLayer(L.latLng(g.start[0], g.start[1]), L.latLng(g.end[0], g.end[1]), styleOptions('arrow', row.props && row.props.style));
      attachShapeHandlers(layer);
    } else {
      const opts = styleOptions(kind, row.props && row.props.style);
      if (kind === 'circle') layer = L.circle([g.lat, g.lng], Object.assign({ radius: g.radius }, opts));
      else if (kind === 'rectangle') layer = L.rectangle(g.bounds, opts);
      else if (kind === 'polygon') layer = L.polygon(g.latlngs, opts);
      else if (kind === 'polyline') layer = L.polyline(g.latlngs, opts);
      if (!layer) return null;
      attachShapeHandlers(layer);
    }
    layer.lkId = row.id;
    layer.lkKind = kind;
    return layer;
  }
  function attachShapeHandlers(layer) {
    layer.on('click', ev => {
      // Waehrend Pfeil-/Text-Platzierung Klick durchreichen (map click uebernimmt)
      if (arrowPlacementState || textPlacementActive || placementState) return;
      if (toolbarEditing) return;
      L.DomEvent.stopPropagation(ev);
      const f = features.get(layer.lkId);
      if (f) openShapeDialog(f, ev);
    });
    layer.bindTooltip(l => { const f = features.get(l.lkId); return f ? shapeTooltipHtml(f.row) : ''; }, { sticky: true });
  }

  function updateLayer(f) {
    const row = f.row, layer = f.layer, g = row.geometry || {};
    const kind = row.kind;
    if (kind === 'marker') {
      layer.setLatLng([g.lat, g.lng]);
      layer.setIcon(makeMarkerIcon(row));
      if (layer._tooltip) layer._tooltip.options.offset = [0, -((row.props && row.props.baseSize) || 40) / 2];
      if (layer.isTooltipOpen && layer.isTooltipOpen()) layer.setTooltipContent(markerInfoHtml(row, false));
    } else if (kind === 'text') {
      layer.setLatLng([g.lat, g.lng]);
      layer.setIcon(makeTextIcon(row.props));
    } else if (kind === 'arrow') {
      layer.lkArrow.startLL = L.latLng(g.start[0], g.start[1]);
      layer.lkArrow.endLL = L.latLng(g.end[0], g.end[1]);
      layer.lkArrow.shaft.setLatLngs([layer.lkArrow.startLL, layer.lkArrow.endLL]);
      applyArrowStyle(layer, styleOptions('arrow', row.props && row.props.style));
    } else {
      if (kind === 'circle') { layer.setLatLng([g.lat, g.lng]); layer.setRadius(g.radius); }
      else if (kind === 'rectangle') layer.setBounds(g.bounds);
      else layer.setLatLngs(g.latlngs);
      layer.setStyle(styleOptions(kind, row.props && row.props.style));
      if (layer.isTooltipOpen && layer.isTooltipOpen()) layer.setTooltipContent(shapeTooltipHtml(row));
    }
  }

  // Zentraler Einstieg: Row (vom Server) in den lokalen Zustand uebernehmen.
  function renderFeature(row) {
    if (!row || !row.id) return;
    if (row.deleted_at) { removeFeature(row.id); return; }
    if (karte && row.lagekarte_id && row.lagekarte_id !== karte.id) return;
    const ex = features.get(row.id);
    if (ex && !ex.pending && ex.row && ex.row.version >= row.version && !ex.deleted) return;
    // Temporaere Marker/Texte (nicht interaktiv) werden nach dem Anlegen neu
    // aufgebaut; gezeichnete Formen/Pfeile behalten ihren Layer (kein Flackern).
    const reusable = ex && ex.layer && ex.row.kind === row.kind && !ex.deleted
      && !(ex.pending && (row.kind === 'marker' || row.kind === 'text'));
    if (reusable) {
      ex.row = row;
      ex.pending = false;
      if (toolbarEditing && isDrawKind(row.kind)) { deferredIds.add(row.id); return; }
      updateLayer(ex);
      applyVisibilityTo(ex);
      return;
    }
    if (ex) removeFeature(row.id, true);
    const layer = buildLayer(row);
    if (!layer) return;
    const f = { row, layer };
    features.set(row.id, f);
    parentLayerFor(row.kind).addLayer(layer);
    applyVisibilityTo(f);
    if (row.id === selectedId && layer._icon) layer._icon.classList.add('lk-marker-selected');
  }
  function removeFeature(id, silent) {
    const f = features.get(id);
    if (!f) return;
    if (toolbarEditing && f.layer && isDrawKind(f.row.kind) && drawLayer.hasLayer(f.layer)) {
      f.deleted = true;
      deferredIds.add(id);
      return;
    }
    if (f.layer) {
      const parent = parentLayerFor(f.row.kind);
      if (parent.hasLayer(f.layer)) parent.removeLayer(f.layer);
      if (map.hasLayer(f.layer)) map.removeLayer(f.layer);
    }
    features.delete(id);
    if (selectedId === id) deselectMarker();
    closeDialogsFor(id);
    if (!silent) updateAbschnittStaerken();
  }
  function flushDeferred() {
    deferredIds.forEach(id => {
      const f = features.get(id);
      if (!f) return;
      if (f.deleted) { f.deleted = false; removeFeature(id, true); }
      else { updateLayer(f); applyVisibilityTo(f); }
    });
    deferredIds.clear();
    updateAbschnittStaerken();
  }
  function clearFeatures() {
    features.forEach(f => {
      if (!f.layer) return;
      const parent = parentLayerFor(f.row.kind);
      if (parent.hasLayer(f.layer)) parent.removeLayer(f.layer);
    });
    features.clear();
    deferredIds.clear();
    deselectMarker();
  }
  // Nach Aenderungen in der Fuehrungsharke: alle verknuepften Objekte neu zeichnen
  function refreshLinkedFeatures() {
    features.forEach(f => {
      if (!f.row.ref_id || !f.layer || f.deleted) return;
      if (f.row.kind === 'marker') {
        f.layer.setIcon(makeMarkerIcon(f.row));
        applyVisibilityTo(f);
        if (f.layer.isTooltipOpen && f.layer.isTooltipOpen()) f.layer.setTooltipContent(markerInfoHtml(f.row, false));
      } else if (f.layer.isTooltipOpen && f.layer.isTooltipOpen()) {
        f.layer.setTooltipContent(shapeTooltipHtml(f.row));
      }
    });
    updateAbschnittStaerken();
    // offene Dialoge mit Verknuepfung aktualisieren
    const md = $('lk-marker-dialog');
    if (md && md.open && md._target && features.get(md._target)) renderMarkerDialogLinked(features.get(md._target).row);
    const sd = $('lk-shape-dialog');
    if (sd && sd.open && sd._target && features.get(sd._target)) renderShapeDialogLinked(features.get(sd._target).row);
  }

  function geometryFromLayer(kind, layer) {
    if (kind === 'marker' || kind === 'text') { const ll = layer.getLatLng(); return { lat: ll.lat, lng: ll.lng }; }
    if (kind === 'circle') { const ll = layer.getLatLng(); return { lat: ll.lat, lng: ll.lng, radius: layer.getRadius() }; }
    if (kind === 'rectangle') { const b = layer.getBounds(); return { bounds: [[b.getSouth(), b.getWest()], [b.getNorth(), b.getEast()]] }; }
    if (kind === 'arrow') { const a = layer.lkArrow; return { start: [a.startLL.lat, a.startLL.lng], end: [a.endLL.lat, a.endLL.lng] }; }
    return { latlngs: flattenLatlngs(layer.getLatLngs()) };
  }
  function flattenLatlngs(latlngs) {
    let arr = latlngs;
    while (Array.isArray(arr[0]) && !(arr[0].lat || arr[0].lat === 0)) arr = arr[0];
    return arr.map(ll => [ll.lat, ll.lng]);
  }

  // ---------- API-Aufrufe (Mutationen) ----------------------------------
  function applyConflictCurrent(cur) {
    if (!cur) return;
    if (cur.kind) renderFeature(cur);
    else if ('nr' in cur && cur.lagekarte_id) { if (cur.deleted_at) abschnitte.delete(cur.nr); else abschnitte.set(cur.nr, cur); renderAbschnittList(); }
    else if (cur.basemap !== undefined) applyKarte(cur, false);
  }
  async function call(fn, okMsg) {
    try {
      const res = await fn();
      if (okMsg) ui.toast(okMsg, 'ok', 1500);
      LUK.sync.poll();
      return res === undefined ? true : res;
    } catch (e) {
      if (!ui.handleConflict(e, applyConflictCurrent)) {
        if (e && e.code === 'not_found') LUK.sync.poll();
        ui.toast(ui.errorText(e), 'error');
      }
      return null;
    }
  }

  // Neues Objekt anlegen. tempLayer (optional) wird sofort angezeigt und bei
  // Erfolg als Layer des Objekts weiterverwendet (kein Flackern).
  async function createFeature(kind, geometry, props, abschnitteArr, ref, tempLayer) {
    if (!karte) return null;
    const id = ui.uid();
    if (tempLayer) {
      tempLayer.lkId = id;
      tempLayer.lkKind = kind;
      features.set(id, { row: { id, lagekarte_id: karte.id, kind, geometry, props, abschnitte: abschnitteArr || [], ref_kind: null, ref_id: null, version: 0 }, layer: tempLayer, pending: true });
      parentLayerFor(kind).addLayer(tempLayer);
    }
    const body = { karte: karte.id, id, kind, geometry, props, abschnitte: abschnitteArr || [] };
    if (ref && ref.ref_id) { body.ref_kind = ref.ref_kind; body.ref_id = ref.ref_id; }
    const res = await call(async () => api.post('lagekarten', 'feature_create', body));
    if (!res || !res.feature) {
      if (tempLayer) { const f = features.get(id); if (f && f.pending) { parentLayerFor(kind).removeLayer(tempLayer); features.delete(id); } }
      return null;
    }
    renderFeature(res.feature);
    updateAbschnittStaerken();
    return features.get(res.feature.id) || null;
  }
  async function updateFeature(id, fields) {
    const f = features.get(id);
    if (!f || f.pending) return null;
    const body = Object.assign({ id, version: f.row.version }, fields);
    const res = await call(async () => api.post('lagekarten', 'feature_update', body));
    if (!res) {
      // Fehler: lokalen Zustand aus bekannter Row wiederherstellen
      const cur = features.get(id);
      if (cur && cur.layer) { updateLayer(cur); applyVisibilityTo(cur); }
      return null;
    }
    if (res.feature) renderFeature(res.feature);
    updateAbschnittStaerken();
    return features.get(id) || null;
  }
  async function deleteFeature(id) {
    const f = features.get(id);
    if (!f) return null;
    if (f.pending) return null;
    const res = await call(async () => api.post('lagekarten', 'feature_delete', { id, version: f.row.version }), 'Gelöscht.');
    if (res) removeFeature(id);
    else { const cur = features.get(id); if (cur && cur.layer) { updateLayer(cur); applyVisibilityTo(cur); } }
    return res;
  }

  // ---------- Marker (taktische Zeichen) --------------------------------
  function onMapClick(e) {
    if (READONLY) return;
    if (arrowPlacementState) { handleArrowMapClick(e.latlng); return; }
    if (textPlacementActive) { handleTextMapClick(e.latlng); return; }
    if (placementState) {
      const ps = placementState;
      const info = { ov: '', call: '', staerke: '', note: '' };
      if (katPending) { info.ov = katPending.ov || ''; info.call = katPending.call || ''; if (katPending.kraft_key) info.kraft_key = katPending.kraft_key; }
      const props = { file: ps.file, label: ps.label, baseSize: ps.size, rotation: ps.rotation, info };
      const tmpRow = { id: '', kind: 'marker', geometry: { lat: e.latlng.lat, lng: e.latlng.lng }, props, abschnitte: [] };
      const temp = L.marker(e.latlng, { icon: makeMarkerIcon(tmpRow), draggable: false, keyboard: false, interactive: false, opacity: 0.6 });
      cancelPlacement();
      createFeature('marker', tmpRow.geometry, props, [], null, temp).then(f => { if (f) selectMarker(f.row.id); });
      return;
    }
    if (selectedId) deselectMarker();
  }

  function onMarkerClick(layer) {
    const f = features.get(layer.lkId);
    if (!f || f.pending) return;
    if (READONLY) { openMarkerDialog(f); return; }
    if (selectedId === f.row.id) { openMarkerDialog(f); return; }
    selectMarker(f.row.id);
  }
  function onMarkerDragEnd(layer) {
    const f = features.get(layer.lkId);
    if (!f || f.pending) return;
    sendGeometry(f, geometryFromLayer(f.row.kind, layer));
  }
  // Geometrie optimistisch uebernehmen; bei Fehler (ausser Konflikt, der die
  // Server-Row liefert) auf den vorherigen Stand zuruecksetzen.
  function sendGeometry(f, g) {
    const id = f.row.id;
    const prev = f.row.geometry;
    f.row.geometry = g;
    return updateFeature(id, { geometry: g }).then(res => {
      if (res) return res;
      const cur = features.get(id);
      if (cur && cur.row.geometry === g) { cur.row.geometry = prev; updateLayer(cur); applyVisibilityTo(cur); }
      return null;
    });
  }

  function selectMarker(id) {
    if (selectedId && selectedId !== id) {
      const old = features.get(selectedId);
      if (old && old.layer && old.layer._icon) old.layer._icon.classList.remove('lk-marker-selected');
    }
    selectedId = id;
    const f = features.get(id);
    if (!f || !f.layer) { selectedId = null; return; }
    if (f.layer._icon) f.layer._icon.classList.add('lk-marker-selected');
    const p = f.row.props || {};
    const ss = $('lk-scale-slider'), rs = $('lk-rotate-slider');
    if (ss) {
      const disp = p.baseSize || 40;
      ss.value = Math.max(parseInt(ss.min, 10) || 20, Math.min(parseInt(ss.max, 10) || 240, disp));
      $('lk-scale-val').textContent = disp;
    }
    if (rs) { rs.value = p.rotation || 0; $('lk-rotate-val').textContent = p.rotation || 0; }
    const del = $('lk-delete-marker-btn');
    if (del) del.disabled = READONLY;
  }
  function deselectMarker() {
    if (selectedId) {
      const f = features.get(selectedId);
      if (f && f.layer && f.layer._icon) f.layer._icon.classList.remove('lk-marker-selected');
    }
    selectedId = null;
    const del = $('lk-delete-marker-btn');
    if (del) del.disabled = true;
  }

  // Groesse/Drehung: lokal sofort anwenden, Speichern entprellt
  let propsSaveTimer = null;
  function applyScaleRotationToSelected() {
    const f = selectedId ? features.get(selectedId) : null;
    if (!f || f.pending) return;
    const size = parseInt($('lk-scale-slider').value, 10) || 40;
    const rotation = parseInt($('lk-rotate-slider').value, 10) || 0;
    f.row.props = Object.assign({}, f.row.props, { baseSize: size, rotation });
    updateLayer(f);
    applyVisibilityTo(f);
    if (propsSaveTimer) clearTimeout(propsSaveTimer);
    const id = f.row.id;
    propsSaveTimer = setTimeout(() => {
      const cur = features.get(id);
      if (cur) updateFeature(id, { props: cur.row.props });
    }, 500);
  }

  // ---------- Pfeile ----------------------------------------------------
  function startArrowPlacement() {
    arrowPlacementState = { start: null, previewLine: null };
    map.getContainer().style.cursor = 'crosshair';
    setActiveTool('arrow');
    status('Startpunkt des Pfeils anklicken (Esc bricht ab).', null, 0);
    map.on('mousemove', onArrowMouseMove);
  }
  function cancelArrowPlacement() {
    if (!arrowPlacementState) return;
    if (arrowPlacementState.previewLine) map.removeLayer(arrowPlacementState.previewLine);
    arrowPlacementState = null;
    map.off('mousemove', onArrowMouseMove);
    map.getContainer().style.cursor = '';
    const btn = $('lk-tool-arrow');
    if (btn && btn.classList.contains('lk-tool-active')) clearActiveTool();
    hideStatus();
  }
  function onArrowMouseMove(e) {
    if (!arrowPlacementState || !arrowPlacementState.start) return;
    const s = arrowPlacementState.start;
    if (!arrowPlacementState.previewLine) {
      arrowPlacementState.previewLine = L.polyline([s, e.latlng], { color: currentDrawStyle.color, weight: currentDrawStyle.weight, opacity: 0.7, dashArray: '6 4', interactive: false }).addTo(map);
    } else arrowPlacementState.previewLine.setLatLngs([s, e.latlng]);
  }
  function handleArrowMapClick(latlng) {
    if (!arrowPlacementState) return;
    if (!arrowPlacementState.start) {
      arrowPlacementState.start = latlng;
      status('Endpunkt des Pfeils anklicken (Esc bricht ab).', null, 0);
      return;
    }
    const start = arrowPlacementState.start, end = latlng;
    const p1 = map.latLngToContainerPoint(start), p2 = map.latLngToContainerPoint(end);
    if (Math.hypot(p2.x - p1.x, p2.y - p1.y) < 5) { status('Pfeil zu kurz – bitte anderen Endpunkt wählen.', 'error', 2500); return; }
    const style = { color: currentDrawStyle.color, weight: currentDrawStyle.weight, fillColor: currentDrawStyle.color, fillOpacity: 1, opacity: 1 };
    const temp = createArrowLayer(start, end, style);
    attachShapeHandlers(temp);
    cancelArrowPlacement();
    createFeature('arrow', { start: [start.lat, start.lng], end: [end.lat, end.lng] }, { style, note: '', label: '' }, [], null, temp);
  }
  function createArrowLayer(startLL, endLL, style) {
    const color = style.color || '#d94b2b';
    const weight = typeof style.weight === 'number' ? style.weight : 3;
    const fillOpacity = typeof style.fillOpacity === 'number' ? style.fillOpacity : 1;
    const shaft = L.polyline([startLL, endLL], { color, weight, opacity: 1 });
    const head = L.polygon(computeArrowheadVertices(startLL, endLL, weight), { color, fillColor: color, fillOpacity, weight: Math.min(2, weight), opacity: 1 });
    const group = L.featureGroup([shaft, head]);
    group.lkArrow = { startLL, endLL, shaft, head };
    return group;
  }
  function computeArrowheadVertices(startLL, endLL, weight) {
    const p1 = map.latLngToContainerPoint(startLL), p2 = map.latLngToContainerPoint(endLL);
    let dx = p2.x - p1.x, dy = p2.y - p1.y;
    let len = Math.hypot(dx, dy);
    if (len < 1) len = 1;
    const ux = dx / len, uy = dy / len;
    const headLen = Math.max(12, 8 + weight * 3);
    const headWid = headLen * 0.6;
    const base = { x: p2.x - ux * headLen, y: p2.y - uy * headLen };
    return [
      map.containerPointToLatLng([p2.x, p2.y]),
      map.containerPointToLatLng([base.x - uy * headWid, base.y + ux * headWid]),
      map.containerPointToLatLng([base.x + uy * headWid, base.y - ux * headWid])
    ];
  }
  function rebuildArrowhead(group) {
    if (!group || !group.lkArrow) return;
    const a = group.lkArrow;
    const weight = (a.shaft.options && a.shaft.options.weight) || 3;
    a.head.setLatLngs(computeArrowheadVertices(a.startLL, a.endLL, weight));
  }
  function rebuildAllArrowheads() {
    features.forEach(f => { if (f.row.kind === 'arrow' && f.layer) rebuildArrowhead(f.layer); });
  }
  function applyArrowStyle(arrow, style) {
    if (!arrow || !arrow.lkArrow) return;
    const color = style.color || '#d94b2b';
    const weight = typeof style.weight === 'number' ? style.weight : 3;
    const fillOpacity = typeof style.fillOpacity === 'number' ? style.fillOpacity : 1;
    arrow.lkArrow.shaft.setStyle({ color, weight, opacity: 1 });
    arrow.lkArrow.head.setStyle({ color, fillColor: color, fillOpacity, weight: Math.min(2, weight), opacity: 1 });
    rebuildArrowhead(arrow);
  }

  // ---------- Leaflet.Draw-Ereignisse -----------------------------------
  function onDrawCreated(e) {
    const layer = e.layer;
    const kind = e.layerType;
    if (!isDrawKind(kind)) return;
    const style = { color: currentDrawStyle.color, fillColor: currentDrawStyle.fillColor, fillOpacity: kind === 'polyline' ? 0 : currentDrawStyle.fillOpacity, weight: currentDrawStyle.weight, opacity: 1 };
    if (layer.setStyle) layer.setStyle(style);
    attachShapeHandlers(layer);
    currentDrawHandler = null;
    currentDrawType = null;
    clearActiveTool();
    createFeature(kind, geometryFromLayer(kind, layer), { style, note: '', label: '' }, [], null, layer);
  }
  function onDrawEdited(e) {
    e.layers.eachLayer(layer => {
      const f = features.get(layer.lkId);
      if (!f || f.pending) return;
      sendGeometry(f, geometryFromLayer(f.row.kind, layer));
    });
  }
  function onDrawDeleted(e) {
    e.layers.eachLayer(layer => {
      const f = features.get(layer.lkId);
      if (!f) return;
      if (f.pending) { features.delete(layer.lkId); return; }
      // Leaflet.Draw hat den Layer bereits entfernt; bei Fehler wird er wieder eingefuegt
      api.post('lagekarten', 'feature_delete', { id: f.row.id, version: f.row.version })
        .then(() => { f.layer = null; removeFeature(f.row.id); LUK.sync.poll(); })
        .catch(err => {
          if (!ui.handleConflict(err, applyConflictCurrent)) ui.toast(ui.errorText(err), 'error');
          const cur = features.get(f.row.id);
          if (cur && cur.layer && !drawLayer.hasLayer(cur.layer)) drawLayer.addLayer(cur.layer);
        });
    });
  }

  // ---------- Freitexte -------------------------------------------------
  function startTextPlacement() {
    textPlacementActive = true;
    map.getContainer().style.cursor = 'text';
    setActiveTool('text');
    status('Klick in die Karte, um einen Text zu platzieren (Esc bricht ab).', null, 0);
  }
  function cancelTextPlacement() {
    if (!textPlacementActive) return;
    textPlacementActive = false;
    map.getContainer().style.cursor = '';
    const btn = $('lk-tool-text');
    if (btn && btn.classList.contains('lk-tool-active')) clearActiveTool();
    hideStatus();
  }
  function handleTextMapClick(latlng) {
    if (!textPlacementActive) return;
    textPlacementActive = false;
    map.getContainer().style.cursor = '';
    clearActiveTool();
    hideStatus();
    const draft = { id: null, kind: 'text', geometry: { lat: latlng.lat, lng: latlng.lng },
      props: { text: '', fontSize: 16, bold: false, italic: false, underline: false, color: '#222222', note: '' }, abschnitte: [] };
    openTextDialog({ row: draft, layer: null, isNew: true }, { latlng });
  }

  // ---------- Dialog-Positionierung (anchored) --------------------------
  function openAnchored(dlg, latlng) {
    dlg._anchorLatlng = latlng || null;
    dlg.classList.add('lk-dialog-anchored');
    dlg.style.left = '-9999px';
    dlg.style.top = '0px';
    if (!dlg.open) dlg.show();
    requestAnimationFrame(() => positionAnchoredDialog(dlg));
  }
  function positionAnchoredDialog(dlg) {
    const mapEl = $('map');
    const mapRect = mapEl.getBoundingClientRect();
    let anchorX = mapRect.left + mapRect.width / 2;
    let anchorY = mapRect.top + mapRect.height / 2;
    if (dlg._anchorLatlng) {
      const p = map.latLngToContainerPoint(dlg._anchorLatlng);
      anchorX = mapRect.left + p.x;
      anchorY = mapRect.top + p.y;
    }
    const r = dlg.getBoundingClientRect();
    const w = r.width, h = r.height, OFFSET = 14, PAD = 8;
    let x = anchorX + OFFSET, y = anchorY + OFFSET;
    if (x + w > mapRect.right - PAD) x = anchorX - OFFSET - w;
    if (y + h > mapRect.bottom - PAD) y = anchorY - OFFSET - h;
    const minX = mapRect.left + PAD, minY = mapRect.top + PAD;
    const maxX = Math.max(minX, mapRect.right - w - PAD), maxY = Math.max(minY, mapRect.bottom - h - PAD);
    dlg.style.left = Math.max(minX, Math.min(x, maxX)) + 'px';
    dlg.style.top = Math.max(minY, Math.min(y, maxY)) + 'px';
  }
  function closeAnchored(dlg) {
    if (!dlg) return;
    dlg._target = null;
    dlg._anchorLatlng = null;
    dlg.classList.remove('lk-dialog-anchored');
    dlg.style.left = '';
    dlg.style.top = '';
    if (dlg.open) dlg.close();
  }
  function closeDialogsFor(id) {
    ['lk-marker-dialog', 'lk-shape-dialog', 'lk-text-dialog'].forEach(did => {
      const d = $(did);
      if (d && d.open && d._target === id) { closeAnchored(d); ui.toast('Das Objekt wurde zwischenzeitlich gelöscht.', 'warn'); }
    });
  }

  // ---------- Marker-Info-Dialog ----------------------------------------
  function openMarkerDialog(f) {
    const dlg = $('lk-marker-dialog');
    if (!dlg || !f) return;
    const row = f.row;
    const p = row.props || {};
    const info = p.info || {};
    dlg._target = row.id;
    $('lk-mi-title').textContent = p.label || 'Zeichen-Informationen';
    const sym = $('lk-mi-symbol');
    if (sym) sym.innerHTML = '<img src="' + symbolUrl(p.file) + '" alt="" /><span>' + esc(p.label || '') + '</span>';
    $('lk-mi-ov').value = info.ov || '';
    $('lk-mi-call').value = info.call || '';
    $('lk-mi-staerke').value = info.staerke || '';
    $('lk-mi-note').value = info.note || '';
    ['lk-mi-ov', 'lk-mi-call', 'lk-mi-staerke', 'lk-mi-note'].forEach(id => { $(id).disabled = READONLY; });
    markerDlgKraft = info.kraft_key || '';
    renderMarkerKraft();
    renderPicker('lk-mi-abschnitte', row.abschnitte);
    fillLinkSelect($('lk-mi-link'), row.ref_kind, row.ref_id);
    renderMarkerDialogLinked(row);
    openAnchored(dlg, f.layer ? f.layer.getLatLng() : null);
  }
  function renderMarkerDialogLinked(row) {
    const li = resolveLinked(row);
    const box = $('lk-mi-linked'), free = $('lk-mi-free');
    const dl = $('lk-mi-linked-dl');
    if (!box || !free) return;
    if (!li) { box.hidden = true; free.hidden = false; return; }
    box.hidden = false;
    free.hidden = true;
    dl.innerHTML = '';
    dl.appendChild(linkedDl(li));
    $('lk-mi-orphan').hidden = !li.orphan;
    $('lk-mi-orphan-hint').hidden = !li.orphan;
    box.classList.toggle('lk-linked-box-orphan', !!li.orphan);
  }
  function closeMarkerDialog() { closeAnchored($('lk-marker-dialog')); }

  async function saveMarkerDialog() {
    const dlg = $('lk-marker-dialog');
    const f = dlg && dlg._target ? features.get(dlg._target) : null;
    if (!f) { closeMarkerDialog(); return; }
    const row = f.row;
    const props = clone(row.props || {});
    const oldInfo = props.info || {};
    const sel = $('lk-mi-link');
    const ref = sel ? refFromSelect(sel) : { ref_kind: row.ref_kind || null, ref_id: row.ref_id || null };
    const linked = !!ref.ref_id;
    props.info = linked
      ? { ov: oldInfo.ov || '', call: oldInfo.call || '', staerke: oldInfo.staerke || '', note: $('lk-mi-note').value.trim() }
      : { ov: $('lk-mi-ov').value.trim(), call: $('lk-mi-call').value.trim(), staerke: $('lk-mi-staerke').value.trim(), note: $('lk-mi-note').value.trim() };
    // Katalog-Verweis nur im Freitext-Modus (verknuepft kommt er live aus der Harke)
    if (!linked && markerDlgKraft) props.info.kraft_key = markerDlgKraft;
    else if (linked && oldInfo.kraft_key) props.info.kraft_key = oldInfo.kraft_key;
    const picked = getPicked('lk-mi-abschnitte');
    const body = {};
    if (JSON.stringify(props) !== JSON.stringify(row.props)) body.props = props;
    if (!sameArr(picked, row.abschnitte)) body.abschnitte = picked;
    if ((ref.ref_id || null) !== (row.ref_id || null) || (ref.ref_kind || null) !== (row.ref_kind || null)) { body.ref_kind = ref.ref_kind; body.ref_id = ref.ref_id; }
    closeMarkerDialog();
    if (!Object.keys(body).length) return;
    await updateFeature(row.id, body);
  }
  // Verknuepfung loesen: aufgeloeste Werte als festen Text uebernehmen
  async function unlinkMarker() {
    const dlg = $('lk-marker-dialog');
    const f = dlg && dlg._target ? features.get(dlg._target) : null;
    if (!f || !f.row.ref_id) return;
    const row = f.row;
    const li = resolveLinked(row);
    const props = clone(row.props || {});
    const note = $('lk-mi-note').value.trim();
    if (li && !li.orphan) {
      props.info = { ov: li.ov || '', call: li.funkruf || '', staerke: li.staerkeText, note };
      if (li.ref && li.ref.row && li.ref.row.kraft_key) props.info.kraft_key = li.ref.row.kraft_key;
    } else {
      props.info = Object.assign({ ov: '', call: '', staerke: '' }, props.info || {}, { note });
    }
    const res = await updateFeature(row.id, { props, ref_kind: null, ref_id: null });
    if (res) {
      ui.toast('Verknüpfung gelöst – Werte als Text übernommen.', 'ok', 2500);
      const cur = features.get(row.id);
      if (cur && dlg.open) openMarkerDialog(cur);
    }
  }
  function deleteMarkerFromDialog() {
    const dlg = $('lk-marker-dialog');
    const f = dlg && dlg._target ? features.get(dlg._target) : null;
    if (!f) { closeMarkerDialog(); return; }
    if (!confirm('Zeichen „' + ((f.row.props && f.row.props.label) || '') + '“ wirklich löschen?')) return;
    closeMarkerDialog();
    deleteFeature(f.row.id);
  }

  // ---------- Shape-Dialog ----------------------------------------------
  function openShapeDialog(f, clickEv) {
    const dlg = $('lk-shape-dialog');
    if (!dlg || !f || f.pending) return;
    const row = f.row;
    const p = row.props || {};
    const st = p.style || {};
    dlg._target = row.id;
    $('lk-shape-title').textContent = (KIND_LABELS[row.kind] || 'Struktur') + (p.label ? ' „' + p.label + '“' : '');
    $('lk-shape-label').value = p.label || '';
    $('lk-shape-note').value = p.note || '';
    renderPicker('lk-shape-abschnitte', row.abschnitte);
    const color = st.color || '#d94b2b';
    const weight = typeof st.weight === 'number' ? st.weight : 3;
    const fillPct = Math.round((typeof st.fillOpacity === 'number' ? st.fillOpacity : (row.kind === 'arrow' ? 1 : 0.3)) * 100);
    $('lk-shape-color').value = color;
    $('lk-shape-weight').value = weight; $('lk-shape-weight-val').textContent = weight;
    $('lk-shape-fill').value = fillPct; $('lk-shape-fill-val').textContent = fillPct;
    $('lk-shape-fill-label').style.display = row.kind === 'polyline' ? 'none' : '';
    ['lk-shape-label', 'lk-shape-note', 'lk-shape-color', 'lk-shape-weight', 'lk-shape-fill'].forEach(id => { $(id).disabled = READONLY; });
    fillLinkSelect($('lk-shape-link'), row.ref_kind, row.ref_id);
    renderShapeDialogLinked(row);
    openAnchored(dlg, clickEv && clickEv.latlng ? clickEv.latlng : null);
  }
  function renderShapeDialogLinked(row) {
    const li = resolveLinked(row);
    const box = $('lk-shape-linked');
    if (!box) return;
    if (!li) { box.hidden = true; return; }
    box.hidden = false;
    const dl = $('lk-shape-linked-dl');
    dl.innerHTML = '';
    dl.appendChild(linkedDl(li));
    $('lk-shape-orphan').hidden = !li.orphan;
    box.classList.toggle('lk-linked-box-orphan', !!li.orphan);
  }
  function closeShapeDialog() { closeAnchored($('lk-shape-dialog')); }
  async function saveShapeDialog() {
    const dlg = $('lk-shape-dialog');
    const f = dlg && dlg._target ? features.get(dlg._target) : null;
    if (!f) { closeShapeDialog(); return; }
    const row = f.row;
    const color = $('lk-shape-color').value || '#d94b2b';
    const weight = parseInt($('lk-shape-weight').value, 10) || 3;
    const fillPct = parseInt($('lk-shape-fill').value, 10);
    const fillOpacity = isNaN(fillPct) ? 0.3 : fillPct / 100;
    const props = {
      style: { color, weight, fillColor: color, fillOpacity: row.kind === 'polyline' ? 0 : fillOpacity, opacity: 1 },
      note: $('lk-shape-note').value.trim(),
      label: $('lk-shape-label').value.trim(),
    };
    const picked = getPicked('lk-shape-abschnitte');
    const linkSel = $('lk-shape-link');
    const ref = linkSel ? refFromSelect(linkSel) : { ref_kind: row.ref_kind || null, ref_id: row.ref_id || null };
    const body = {};
    if (JSON.stringify(props) !== JSON.stringify(row.props)) body.props = props;
    if (!sameArr(picked, row.abschnitte)) body.abschnitte = picked;
    if ((ref.ref_id || null) !== (row.ref_id || null)) { body.ref_kind = ref.ref_kind; body.ref_id = ref.ref_id; }
    closeShapeDialog();
    if (!Object.keys(body).length) return;
    await updateFeature(row.id, body);
  }
  function deleteShapeFromDialog() {
    const dlg = $('lk-shape-dialog');
    const f = dlg && dlg._target ? features.get(dlg._target) : null;
    if (!f) { closeShapeDialog(); return; }
    if (!confirm('Diese Struktur wirklich löschen?')) return;
    closeShapeDialog();
    deleteFeature(f.row.id);
  }

  // ---------- Text-Dialog -----------------------------------------------
  // f: { row, layer, isNew? } – bei isNew ist row ein Entwurf ohne id.
  function openTextDialog(f, clickEv) {
    const dlg = $('lk-text-dialog');
    if (!dlg || !f) return;
    const p = f.row.props || {};
    dlg._target = f.row.id || null;
    dlg._draft = f.isNew ? f.row : null;
    $('lk-text-content').value = p.text || '';
    $('lk-text-color').value = p.color || '#222222';
    const size = p.fontSize || 16;
    $('lk-text-size').value = size; $('lk-text-size-val').textContent = size;
    $('lk-text-bold').checked = !!p.bold;
    $('lk-text-italic').checked = !!p.italic;
    $('lk-text-underline').checked = !!p.underline;
    $('lk-text-note').value = p.note || '';
    renderPicker('lk-text-abschnitte', f.row.abschnitte);
    ['lk-text-content', 'lk-text-color', 'lk-text-size', 'lk-text-bold', 'lk-text-italic', 'lk-text-underline', 'lk-text-note'].forEach(id => { $(id).disabled = READONLY; });
    const del = $('lk-text-delete');
    if (del) del.hidden = !!f.isNew;
    openAnchored(dlg, (clickEv && clickEv.latlng) ? clickEv.latlng : (f.layer ? f.layer.getLatLng() : null));
    setTimeout(() => { const ta = $('lk-text-content'); if (ta && !READONLY) ta.focus(); }, 50);
  }
  function closeTextDialog() { const d = $('lk-text-dialog'); if (d) d._draft = null; closeAnchored(d); }
  async function saveTextDialog() {
    const dlg = $('lk-text-dialog');
    if (!dlg) return;
    const props = {
      text: $('lk-text-content').value,
      color: $('lk-text-color').value || '#222222',
      fontSize: parseInt($('lk-text-size').value, 10) || 16,
      bold: $('lk-text-bold').checked,
      italic: $('lk-text-italic').checked,
      underline: $('lk-text-underline').checked,
      note: $('lk-text-note').value.trim(),
    };
    const picked = getPicked('lk-text-abschnitte');
    if (dlg._draft) {
      const draft = dlg._draft;
      closeTextDialog();
      if (!props.text.trim()) return;
      const temp = L.marker([draft.geometry.lat, draft.geometry.lng], { icon: makeTextIcon(props), draggable: false, keyboard: false, interactive: false, opacity: 0.6 });
      await createFeature('text', draft.geometry, props, picked, null, temp);
      return;
    }
    const f = dlg._target ? features.get(dlg._target) : null;
    closeTextDialog();
    if (!f) return;
    const body = {};
    if (JSON.stringify(props) !== JSON.stringify(f.row.props)) body.props = props;
    if (!sameArr(picked, f.row.abschnitte)) body.abschnitte = picked;
    if (!Object.keys(body).length) return;
    await updateFeature(f.row.id, body);
  }
  function deleteTextFromDialog() {
    const dlg = $('lk-text-dialog');
    const f = dlg && dlg._target ? features.get(dlg._target) : null;
    if (!f) { closeTextDialog(); return; }
    if (!confirm('Text wirklich löschen?')) return;
    closeTextDialog();
    deleteFeature(f.row.id);
  }

  // ---------- Karten (Liste, Laden, Wechsel) -----------------------------
  function renderMapSelect() {
    const sel = $('lk-map-select');
    if (!sel) return;
    sel.innerHTML = '';
    if (!karten.length) {
      const o = document.createElement('option');
      o.value = '';
      o.textContent = '– keine Lagekarte vorhanden –';
      sel.appendChild(o);
      sel.disabled = true;
      return;
    }
    sel.disabled = false;
    karten.forEach(k => {
      const o = document.createElement('option');
      o.value = k.id;
      o.textContent = k.title + (typeof k.feature_count === 'number' ? ' (' + k.feature_count + ')' : '');
      sel.appendChild(o);
    });
    sel.value = karte ? karte.id : (karten[0] ? karten[0].id : '');
  }
  function upsertKarteInList(row) {
    const i = karten.findIndex(k => k.id === row.id);
    if (i === -1) karten.push(row);
    else karten[i] = Object.assign({}, karten[i], row);
    karten.sort((a, b) => (a.sort_order - b.sort_order) || String(a.created_at).localeCompare(String(b.created_at)));
  }
  function updateUrl() {
    try {
      const url = new URL(window.location.href);
      if (karte) url.searchParams.set('karte', karte.id); else url.searchParams.delete('karte');
      if (!focusPending) url.searchParams.delete('focus');
      history.replaceState(null, '', url.toString());
    } catch (_) { /* ignore */ }
  }
  function showEmpty(show) {
    const el = $('lk-empty');
    if (el) el.hidden = !show;
  }
  function updateKarteUi() {
    const meta = $('lk-map-meta');
    if (meta) {
      meta.textContent = karte
        ? 'Zuletzt geändert: ' + ui.fmtServerTs(karte.updated_at) + ' · Standardansicht: ' + (karte.basemap === 'sat' ? 'Satellit' : 'Karte') + ', Zoom ' + (karte.zoom != null ? karte.zoom : '–')
        : '';
    }
    ['lk-delete-map-btn', 'lk-save-view-btn', 'lk-export-json-btn', 'lk-rename-btn']
      .forEach(id => { const b = $(id); if (b) b.disabled = !karte; });
    renderMapSelect();
  }
  // Karten-Row uebernehmen (eigene Antwort oder Sync). Bei remote=true wird
  // die Ansicht des Nutzers nicht veraendert.
  function applyKarte(row, remote) {
    if (!row || !row.id) return;
    if (karte && karte.id === row.id) {
      const titleChanged = karte.title !== row.title;
      karte = Object.assign({}, karte, row);
      if (remote && titleChanged) ui.toast('Die Lagekarte wurde umbenannt: „' + row.title + '“', 'info');
    }
    upsertKarteInList(row);
    updateKarteUi();
  }

  async function loadList() {
    const data = await api.get('lagekarten', 'list', { einsatz: CTX.uuid });
    karten = (data.items || []).slice();
    renderMapSelect();
    return karten;
  }

  function clearMap() {
    cancelAllTools();
    if (placementState) cancelPlacement();
    ['lk-marker-dialog', 'lk-shape-dialog', 'lk-text-dialog'].forEach(id => closeAnchored($(id)));
    clearFeatures();
    abschnitte.clear();
    abschnittVisibility.clear();
    karte = null;
    renderAbschnittList();
    updateKarteUi();
  }

  async function openKarte(id, opts) {
    opts = opts || {};
    clearMap();
    if (!id) {
      showEmpty(true);
      updateUrl();
      await ensureSyncWithoutKarte();
      return;
    }
    try {
      const data = await api.get('lagekarten', 'load', { einsatz: CTX.uuid, karte: id });
      T.load(data.tree);
      karte = data.karte;
      upsertKarteInList(karte);
      (data.abschnitte || []).forEach(a => { abschnitte.set(a.nr, a); abschnittVisibility.set(a.nr, true); });
      (data.features || []).forEach(renderFeature);
      if (!opts.keepView) {
        if (karte.basemap) setBasemap(karte.basemap);
        if (typeof karte.center_lat === 'number' && typeof karte.center_lng === 'number') {
          map.setView([karte.center_lat, karte.center_lng], karte.zoom || 11, { animate: false });
          setTimeout(() => map.invalidateSize(), 0);
        }
      }
      showEmpty(false);
      renderAbschnittList();
      applyVisibility();
      updateKarteUi();
      updateUrl();
      if (!syncStarted) { LUK.sync.start(data.tree.seq); syncStarted = true; }
      else LUK.sync.bump(data.tree.seq);
      if (focusPending) { applyFocus(focusPending); focusPending = ''; updateUrl(); }
    } catch (e) {
      if (e && e.code === 'not_found') {
        ui.toast('Die Lagekarte wurde nicht gefunden (gelöscht?).', 'warn');
        karten = karten.filter(k => k.id !== id);
        renderMapSelect();
        if (karten.length) return openKarte(karten[0].id);
        showEmpty(true);
        updateUrl();
        await ensureSyncWithoutKarte();
        return;
      }
      ui.toast('Laden fehlgeschlagen: ' + ui.errorText(e), 'error');
    }
  }
  // Ohne geladene Karte trotzdem Live-Sync starten (damit eine neu angelegte
  // Karte bei Mitlesern erscheint). Aktuelle seq ueber den Tree holen, statt
  // alle Ereignisse seit 0 zu ziehen.
  async function ensureSyncWithoutKarte() {
    if (syncStarted) return;
    let seq = 0;
    try {
      const t = await api.get('einsatz', 'tree', { einsatz: CTX.uuid });
      T.load(t.tree);
      seq = t.tree.seq || 0;
    } catch (_) { /* Fallback: seq 0 */ }
    if (!syncStarted) { LUK.sync.start(seq); syncStarted = true; }
  }

  // Fokus auf das erste Objekt, das die Einheit referenziert
  function applyFocus(focusStr) {
    const i = focusStr.indexOf(':');
    if (i === -1) return;
    const kind = focusStr.slice(0, i), id = focusStr.slice(i + 1);
    let hit = null;
    features.forEach(f => { if (!hit && f.row.ref_kind === kind && f.row.ref_id === id && f.layer) hit = f; });
    if (!hit) { ui.toast('Kein Objekt mit dieser Verknüpfung auf der Karte.', 'warn'); return; }
    highlightFeature(hit);
  }
  function highlightFeature(f) {
    const layer = f.layer;
    if (f.row.kind === 'marker' || f.row.kind === 'text') {
      map.setView(layer.getLatLng(), Math.max(map.getZoom(), 15), { animate: true });
      const mark = () => {
        if (layer._icon) layer._icon.classList.add('lk-marker-focus');
        if (layer.openTooltip) layer.openTooltip();
      };
      setTimeout(mark, 350);
      setTimeout(() => { if (layer._icon) layer._icon.classList.remove('lk-marker-focus'); }, 8000);
      if (f.row.kind === 'marker' && CAN_WRITE) selectMarker(f.row.id);
    } else if (layer.getBounds) {
      map.fitBounds(layer.getBounds(), { padding: [40, 40], maxZoom: 16 });
      const target = f.row.kind === 'arrow' ? layer.lkArrow.shaft : layer;
      const orig = Object.assign({}, target.options);
      let on = false, n = 0;
      const iv = setInterval(() => {
        on = !on; n++;
        target.setStyle(on ? { color: '#ff9800', weight: (orig.weight || 3) + 3 } : { color: orig.color, weight: orig.weight });
        if (n >= 8) { clearInterval(iv); target.setStyle({ color: orig.color, weight: orig.weight }); }
      }, 350);
    }
  }

  // ---------- Karten-Aktionen -------------------------------------------
  function openNewMapDialog(mode, mapData, suggestedTitle) {
    const dlg = $('lk-newmap-dialog');
    if (!dlg) return;
    newMapMode = { mode, mapData: mapData || null };
    const titel = { import: 'Lagekarte importieren', rename: 'Lagekarte umbenennen' };
    const hinweis = {
      import: 'Die Datei wird als neue Lagekarte dieses Einsatzes angelegt (' + countMapData(mapData) + ' Objekte).',
      rename: 'Betrifft nur den Namen - die Karte selbst bleibt unveraendert.',
      create: 'Die neue Karte startet mit dem aktuellen Kartenausschnitt.',
    };
    const knopf = { import: 'Importieren', rename: 'Umbenennen', create: 'Anlegen' };
    $('lk-newmap-title').textContent = titel[mode] || 'Neue Lagekarte';
    $('lk-newmap-hint').textContent = hinweis[mode] || hinweis.create;
    $('lk-newmap-ok').textContent = knopf[mode] || knopf.create;
    $('lk-newmap-name').value = suggestedTitle || '';
    dlg.showModal();
    // Beim Umbenennen steht der alte Name schon drin - markieren, damit
    // Tippen ihn ersetzt.
    setTimeout(() => { const el = $('lk-newmap-name'); el.focus(); if (mode === 'rename') el.select(); }, 30);
  }
  function countMapData(md) {
    if (!md) return 0;
    return ((md.markers || []).length + (md.shapes || []).length + (md.texts || []).length);
  }
  async function confirmNewMapDialog() {
    const dlg = $('lk-newmap-dialog');
    const title = $('lk-newmap-name').value.trim();
    if (!title) { $('lk-newmap-name').focus(); return; }
    const mode = newMapMode || { mode: 'create' };
    dlg.close();
    if (mode.mode === 'rename') { await renameCurrentMap(title); return; }
    if (mode.mode === 'import') {
      const res = await call(async () => api.post('lagekarten', 'import_json', { title, map_data: mode.mapData }), 'Lagekarte importiert.');
      if (res && res.karte) { upsertKarteInList(res.karte); await openKarte(res.karte.id); }
      return;
    }
    const c = map.getCenter();
    const res = await call(async () => api.post('lagekarten', 'create', { title, center_lat: c.lat, center_lng: c.lng, zoom: map.getZoom(), basemap: currentBasemap() }), 'Lagekarte angelegt.');
    if (res && res.karte) { upsertKarteInList(res.karte); await openKarte(res.karte.id, { keepView: true }); }
  }
  async function deleteCurrentMap() {
    if (!karte) return;
    const n = features.size;
    if (!confirm('Lagekarte „' + karte.title + '“ wirklich löschen?' + (n ? '\n' + n + ' Objekt(e) werden mit gelöscht.' : ''))) return;
    const id = karte.id;
    const res = await call(async () => api.post('lagekarten', 'delete', { karte: id, version: karte.version }), 'Lagekarte gelöscht.');
    if (!res) return;
    karten = karten.filter(k => k.id !== id);
    clearMap();
    if (karten.length) await openKarte(karten[0].id); else { showEmpty(true); updateUrl(); }
  }
  async function saveView() {
    if (!karte) return;
    const c = map.getCenter();
    const res = await call(async () => api.post('lagekarten', 'update', {
      karte: karte.id, version: karte.version, center_lat: c.lat, center_lng: c.lng, zoom: map.getZoom(), basemap: currentBasemap()
    }), 'Ansicht gespeichert.');
    if (res && res.karte) applyKarte(res.karte, false);
  }
  /** Titel der aktuellen Karte setzen (aus dem Umbenennen-Dialog). */
  async function renameCurrentMap(title) {
    if (!karte) return;
    const t = (title || '').trim();
    if (!t || t === karte.title) { updateKarteUi(); return; }
    const res = await call(async () => api.post('lagekarten', 'update', { karte: karte.id, version: karte.version, title: t }), 'Umbenannt.');
    if (res && res.karte) applyKarte(res.karte, false); else updateKarteUi();
  }
  function onCurrentMapDeleted() {
    ui.toast('Diese Lagekarte wurde von einem anderen Nutzer gelöscht.', 'warn', 6000);
    clearMap();
    if (karten.length) openKarte(karten[0].id); else { showEmpty(true); updateUrl(); }
  }

  // ---------- Live-Sync ---------------------------------------------------
  LUK.sync.onEvent((events, treeChanged) => {
    let abschnittChanged = false, staerkeDirty = false, listChanged = false;
    events.forEach(ev => {
      const a = ev.after || {};
      if (ev.entity === 'feature') {
        if (ev.action === 'delete' || a.deleted_at) {
          if (features.has(ev.entity_id)) { removeFeature(ev.entity_id, true); staerkeDirty = true; }
        } else if (a.id && karte && a.lagekarte_id === karte.id) {
          renderFeature(a);
          staerkeDirty = true;
        }
      } else if (ev.entity === 'abschnitt') {
        if (!karte || a.lagekarte_id !== karte.id) return;
        if (ev.action === 'delete' || a.deleted_at) { abschnitte.delete(a.nr); abschnittVisibility.delete(a.nr); }
        else if (typeof a.nr === 'number') {
          const cur = abschnitte.get(a.nr);
          if (!cur || cur.version <= a.version) abschnitte.set(a.nr, a);
          if (!abschnittVisibility.has(a.nr)) abschnittVisibility.set(a.nr, true);
        }
        abschnittChanged = true;
      } else if (ev.entity === 'lagekarte') {
        if (ev.action === 'delete' || a.deleted_at) {
          const wasCurrent = karte && karte.id === ev.entity_id;
          karten = karten.filter(k => k.id !== ev.entity_id);
          listChanged = true;
          if (wasCurrent) onCurrentMapDeleted();
        } else if (a.id && a.title !== undefined) {
          if (karte && karte.id === a.id) { if (a.version > karte.version) applyKarte(a, true); }
          else upsertKarteInList(a);
          listChanged = true;
        } else if (ev.action === 'import' || ev.action === 'insert') {
          // Import-Ereignis ohne Row: Liste nachladen
          loadList().catch(() => {});
        }
      }
    });
    if (abschnittChanged) { renderAbschnittList(); applyVisibility(); }
    if (listChanged) renderMapSelect();
    if (treeChanged) refreshLinkedFeatures();
    else if (staerkeDirty || abschnittChanged) updateAbschnittStaerken();
  });

  // Staerke/Schichten sind zeitabhaengig -> periodisch nachrechnen
  setInterval(() => { if (karte) updateAbschnittStaerken(); }, 60000);

  // ---------- JSON Export / Import ----------------------------------------
  function serializeMapData() {
    const markers = [], shapes = [], texts = [];
    features.forEach(f => {
      const r = f.row;
      if (f.pending || f.deleted) return;
      const ab = [GRUNDKARTE_ID].concat((r.abschnitte || []).filter(n => n !== GRUNDKARTE_ID));
      const p = r.props || {};
      const g = r.geometry || {};
      if (r.kind === 'marker') {
        const li = resolveLinked(r);
        let info = Object.assign({ ov: '', call: '', staerke: '', note: '' }, p.info || {});
        if (li && !li.orphan) info = { ov: li.ov || '', call: li.funkruf || '', staerke: li.staerkeText, note: info.note || '' };
        markers.push({ id: r.id, file: p.file, label: p.label, lat: g.lat, lng: g.lng, baseSize: p.baseSize, rotation: p.rotation, abschnitte: ab, info,
          ref_kind: r.ref_kind || null, ref_id: r.ref_id || null, ref_label: li && !li.orphan ? li.label : null });
      } else if (r.kind === 'text') {
        texts.push({ id: r.id, lat: g.lat, lng: g.lng, text: p.text || '', color: p.color || '#222222', fontSize: p.fontSize || 16,
          bold: !!p.bold, italic: !!p.italic, underline: !!p.underline, abschnitte: ab, note: p.note || '' });
      } else {
        const base = { id: r.id, type: r.kind, style: styleOptions(r.kind, p.style), abschnitte: ab, note: p.note || '', label: p.label || '' };
        if (r.kind === 'circle') Object.assign(base, { lat: g.lat, lng: g.lng, radius: g.radius });
        else if (r.kind === 'rectangle') base.bounds = g.bounds;
        else if (r.kind === 'arrow') { base.start = g.start; base.end = g.end; }
        else base.latlngs = g.latlngs;
        shapes.push(base);
      }
    });
    const c = map.getCenter();
    return {
      version: 1,
      created: new Date().toISOString(),
      center: [c.lat, c.lng],
      zoom: map.getZoom(),
      basemap: currentBasemap(),
      abschnitte: [{ id: GRUNDKARTE_ID, name: GRUNDKARTE_NAME }].concat(sortedAbschnitte().map(a => ({ id: a.nr, name: a.name }))),
      markers, shapes, texts
    };
  }
  function exportJson() {
    if (!karte) return;
    const wrap = { version: 1, title: karte.title, uuid: karte.id, einsatz: CTX.title, map_data: serializeMapData() };
    const blob = new Blob([JSON.stringify(wrap, null, 2)], { type: 'application/json' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = (String(wrap.title).replace(/[^\w\d._-]+/g, '_') || 'lagekarte') + '.json';
    document.body.appendChild(a);
    a.click();
    setTimeout(() => { URL.revokeObjectURL(a.href); a.remove(); }, 100);
  }
  function importJsonFile(file) {
    const reader = new FileReader();
    reader.onload = () => {
      try {
        const parsed = JSON.parse(reader.result);
        let md = null, title = '';
        if (parsed && parsed.map_data && typeof parsed.map_data === 'object') { md = parsed.map_data; title = parsed.title || ''; }
        else if (parsed && parsed.version === 1 && (parsed.markers || parsed.shapes || parsed.texts)) md = parsed;
        if (!md) throw new Error('Format nicht erkannt');
        openNewMapDialog('import', md, title || file.name.replace(/\.json$/i, ''));
      } catch (err) {
        ui.toast('Import fehlgeschlagen: ' + err.message, 'error');
      }
    };
    reader.onerror = () => ui.toast('Datei konnte nicht gelesen werden.', 'error');
    reader.readAsText(file);
  }

  // ---------- Druck / PDF --------------------------------------------------
  let printLibsPromise = null;
  function loadPrintLibs() {
    if (printLibsPromise) return printLibsPromise;
    printLibsPromise = new Promise((resolve, reject) => {
      const scripts = ['/js/dom-to-image-more.min.js', '/js/jspdf.umd.min.js', '/js/html2canvas.min.js'];
      let pending = scripts.length;
      scripts.forEach(src => {
        const s = document.createElement('script');
        s.src = src; s.async = false;
        s.onload = () => { if (--pending === 0) resolve(); };
        s.onerror = () => reject(new Error('Konnte ' + src + ' nicht laden.'));
        document.head.appendChild(s);
      });
    });
    return printLibsPromise;
  }
  function openPrintModal() {
    const dlg = $('printDialog');
    const fn = $('printFilename'), ti = $('printTitle');
    fn.value = 'Lagekarte_' + (typeof getTacticalTime === 'function' ? getTacticalTime('export') : new Date().toISOString().slice(0, 10));
    ti.value = (karte ? karte.title : '') + (CTX.title ? ' – ' + CTX.title : '');
    dlg.showModal();
    ti.focus();
    loadPrintLibs().catch(err => console.warn('PDF-Libs:', err));
  }
  function closePrintModal() { const d = $('printDialog'); if (d.open) d.close(); }
  async function executePrint() {
    const titleText = $('printTitle').value.trim();
    let filename = $('printFilename').value.trim() || 'Lagekarte';
    if (!filename.toLowerCase().endsWith('.pdf')) filename += '.pdf';
    closePrintModal();
    try { status('PDF wird vorbereitet…', null, 0); await loadPrintLibs(); }
    catch (err) { status('PDF-Bibliotheken konnten nicht geladen werden.', 'error', 6000); return; }

    const mapContainer = $('map');
    const scaleControl = L.control.scale({ position: 'bottomleft', imperial: false }).addTo(map);
    const northDiv = L.DomUtil.create('div', 'north-arrow');
    northDiv.style.cssText = 'position:absolute;top:10px;right:10px;z-index:1000;';
    northDiv.innerHTML = '<img src="/images/Nordpfeil.png" style="width:40px;opacity:0.8;" />';
    mapContainer.appendChild(northDiv);
    const emptyEl = $('lk-empty');
    const statusEl = $('lk-status');
    const emptyWasHidden = emptyEl ? emptyEl.hidden : true;
    if (emptyEl) emptyEl.hidden = true;
    if (statusEl) statusEl.hidden = true;

    setTimeout(() => {
      const hiddenControls = [];
      document.querySelectorAll('.leaflet-control-zoom, .leaflet-control-layers, .leaflet-draw').forEach(el => {
        hiddenControls.push({ el, display: el.style.display });
        el.style.display = 'none';
      });
      const cleanup = () => {
        map.removeControl(scaleControl);
        northDiv.remove();
        hiddenControls.forEach(({ el, display }) => { el.style.display = display; });
        if (emptyEl) emptyEl.hidden = emptyWasHidden;
      };
      window.domtoimage.toPng(mapContainer, {
        width: mapContainer.offsetWidth, height: mapContainer.offsetHeight,
        style: { transform: 'none', width: mapContainer.offsetWidth + 'px', height: mapContainer.offsetHeight + 'px' }
      }).then(dataUrl => {
        const { jsPDF } = window.jspdf;
        const pdf = new jsPDF('landscape', 'mm', 'a4');
        const pdfWidth = pdf.internal.pageSize.getWidth();
        const pdfHeight = pdf.internal.pageSize.getHeight();
        const margin = 10;
        let cursorY = margin;
        const showStand = $('printShowStand') && $('printShowStand').checked;
        const standzeit = (showStand && typeof getTacticalTime === 'function') ? getTacticalTime('export') : '';
        let headerText = titleText || '';
        if (headerText && standzeit) headerText += ' (Stand: ' + standzeit + ')';
        else if (!headerText && standzeit) headerText = 'Stand: ' + standzeit;
        if (headerText) {
          pdf.setFont('helvetica', 'bold');
          pdf.setFontSize(16);
          pdf.text(headerText, pdfWidth / 2, cursorY + 6, { align: 'center' });
          cursorY += 14;
        }
        const img = new Image();
        img.onload = () => {
          const availableWidth = pdfWidth - 2 * margin;
          const ratio = img.height / img.width;
          let imgWidth = availableWidth, imgHeight = availableWidth * ratio;
          const maxImgHeight = pdfHeight - cursorY - margin;
          if (imgHeight > maxImgHeight) { imgHeight = maxImgHeight; imgWidth = imgHeight / ratio; }
          pdf.addImage(dataUrl, 'PNG', (pdfWidth - imgWidth) / 2, cursorY, imgWidth, imgHeight);
          pdf.save(filename);
          cleanup();
          hideStatus();
        };
        img.src = dataUrl;
      }).catch(err => {
        console.error('PDF-Fehler', err);
        status('PDF-Erstellung fehlgeschlagen.', 'error', 6000);
        cleanup();
      });
    }, 500);
  }

  // ---------- Event-Bindings -----------------------------------------------
  function on(id, evt, fn) { const el = $(id); if (el) el.addEventListener(evt, fn); return el; }

  function bindUi() {
    // Kartenwahl / Kartenaktionen
    on('lk-map-select', 'change', e => { if (e.target.value && (!karte || e.target.value !== karte.id)) openKarte(e.target.value); });
    on('lk-new-btn', 'click', () => openNewMapDialog('create', null, ''));
    on('lk-empty-new-btn', 'click', () => openNewMapDialog('create', null, ''));
    on('lk-delete-map-btn', 'click', deleteCurrentMap);
    on('lk-save-view-btn', 'click', saveView);
    on('lk-rename-btn', 'click', () => { if (karte) openNewMapDialog('rename', null, karte.title); });
    on('lk-newmap-ok', 'click', confirmNewMapDialog);
    on('lk-newmap-cancel', 'click', () => { newMapMode = null; $('lk-newmap-dialog').close(); });
    on('lk-newmap-name', 'keydown', e => { if (e.key === 'Enter') { e.preventDefault(); confirmNewMapDialog(); } });

    // JSON Ex-/Import
    on('lk-export-json-btn', 'click', exportJson);
    const impFile = $('lk-import-json-file');
    on('lk-import-json-btn', 'click', () => impFile && impFile.click());
    if (impFile) impFile.addEventListener('change', () => { if (impFile.files && impFile.files[0]) importJsonFile(impFile.files[0]); impFile.value = ''; });

    // Slider
    const ss = $('lk-scale-slider'), rs = $('lk-rotate-slider');
    if (ss) ss.addEventListener('input', () => { $('lk-scale-val').textContent = ss.value; if (selectedId) applyScaleRotationToSelected(); });
    if (rs) rs.addEventListener('input', () => { $('lk-rotate-val').textContent = rs.value; if (selectedId) applyScaleRotationToSelected(); });
    on('lk-cancel-place-btn', 'click', cancelPlacement);
    on('lk-delete-marker-btn', 'click', () => {
      const f = selectedId ? features.get(selectedId) : null;
      if (!f) return;
      if (!confirm('Zeichen „' + ((f.row.props && f.row.props.label) || '') + '“ löschen?')) return;
      deleteFeature(f.row.id);
    });

    // Einsatzabschnitte
    const abAddBtn = $('lk-abschnitt-add-btn'), abNameInput = $('lk-abschnitt-new-name');
    if (abAddBtn && abNameInput) {
      const doAdd = async () => {
        const name = abNameInput.value;
        if (!name.trim()) { abNameInput.focus(); return; }
        if (!requireKarte()) return;
        abAddBtn.disabled = true;
        await addAbschnitt(name);
        abAddBtn.disabled = false;
        abNameInput.value = '';
        abNameInput.focus();
      };
      abAddBtn.addEventListener('click', doAdd);
      abNameInput.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); doAdd(); } });
    }

    // Marker-Dialog
    on('lk-mi-save', 'click', saveMarkerDialog);
    on('lk-mi-cancel', 'click', closeMarkerDialog);
    on('lk-mi-delete', 'click', deleteMarkerFromDialog);
    on('lk-mi-unlink', 'click', unlinkMarker);
    // Kraeftekatalog: Picker im Freitext-Feld des Marker-Dialogs, Picker fuer
    // "Zeichen aus Katalog-Einheit", Heimatstandorte-Layer
    if (K && CAN_WRITE && $('lk-mi-ov')) {
      K.picker($('lk-mi-ov'), {
        typ: () => K.typOf(markerDlgKraft),
        onPick: entry => {
          const f = K.fill(entry);
          markerDlgKraft = f.kraft_key;
          $('lk-mi-ov').value = f.bezeichnung + ' ' + f.ov;
          if (f.funkruf && !$('lk-mi-call').value.trim()) $('lk-mi-call').value = f.funkruf;
          renderMarkerKraft();
        },
      });
    }
    if (K && CAN_WRITE && $('lk-kat-pick')) {
      K.picker($('lk-kat-pick'), { onPick: entry => { $('lk-kat-pick').value = ''; placeFromKatalog(entry); } });
    }
    const heimatCb = $('lk-show-heimat');
    if (heimatCb) {
      try { heimatCb.checked = localStorage.getItem(HEIMAT_KEY) === '1'; } catch (e) { /* ignore */ }
      heimatCb.addEventListener('change', () => {
        try { localStorage.setItem(HEIMAT_KEY, heimatCb.checked ? '1' : '0'); } catch (e) { /* ignore */ }
        renderHeimat();
      });
    }
    on('lk-mi-link', 'change', e => {
      // Vorschau: bei Auswahl einer Einheit die Freitextfelder ausblenden
      const dlg = $('lk-marker-dialog');
      const f = dlg && dlg._target ? features.get(dlg._target) : null;
      if (!f) return;
      const ref = refFromSelect(e.target);
      renderMarkerDialogLinked(Object.assign({}, f.row, ref));
    });

    // Shape-Dialog
    on('lk-shape-save', 'click', saveShapeDialog);
    on('lk-shape-cancel', 'click', closeShapeDialog);
    on('lk-shape-delete', 'click', deleteShapeFromDialog);
    on('lk-shape-fill', 'input', e => { $('lk-shape-fill-val').textContent = e.target.value; });
    on('lk-shape-weight', 'input', e => { $('lk-shape-weight-val').textContent = e.target.value; });
    on('lk-shape-link', 'change', e => {
      const dlg = $('lk-shape-dialog');
      const f = dlg && dlg._target ? features.get(dlg._target) : null;
      if (!f) return;
      renderShapeDialogLinked(Object.assign({}, f.row, refFromSelect(e.target)));
    });

    // Zeichen-Stil
    on('lk-draw-color', 'input', e => { currentDrawStyle.color = e.target.value; currentDrawStyle.fillColor = e.target.value; });
    on('lk-draw-fill', 'input', e => { const pct = parseInt(e.target.value, 10) || 0; currentDrawStyle.fillOpacity = pct / 100; $('lk-draw-fill-val').textContent = pct; });
    on('lk-draw-weight', 'input', e => { const w = parseInt(e.target.value, 10) || 3; currentDrawStyle.weight = w; $('lk-draw-weight-val').textContent = w; });

    // Druck
    on('printBtn', 'click', openPrintModal);
    on('confirmPrintBtn', 'click', executePrint);
    on('cancelPrintBtn', 'click', closePrintModal);

    // Werkzeuge
    TOOL_IDS.forEach(type => on('lk-tool-' + type, 'click', () => activateTool(isToolActive(type) ? null : type)));

    // Text-Dialog
    on('lk-text-save', 'click', saveTextDialog);
    on('lk-text-cancel', 'click', closeTextDialog);
    on('lk-text-delete', 'click', deleteTextFromDialog);
    on('lk-text-size', 'input', e => { $('lk-text-size-val').textContent = e.target.value; });

    // Dialoge: Schliessen per Escape (dialog.show() ohne Modal faengt Esc nicht ab)
    document.addEventListener('keydown', e => {
      if (e.key !== 'Escape') return;
      if (arrowPlacementState) { cancelArrowPlacement(); return; }
      if (textPlacementActive) { cancelTextPlacement(); return; }
      if (currentDrawHandler) { cancelAllTools(); return; }
      if (placementState) { cancelPlacement(); return; }
      ['lk-marker-dialog', 'lk-shape-dialog', 'lk-text-dialog'].forEach(id => { const d = $(id); if (d && d.open) closeAnchored(d); });
    });
    // Anchored Dialoge bei Kartenbewegung mitfuehren
    map.on('move zoom', () => {
      ['lk-marker-dialog', 'lk-shape-dialog', 'lk-text-dialog'].forEach(id => {
        const d = $(id);
        if (d && d.open && d.classList.contains('lk-dialog-anchored') && d._anchorLatlng) positionAnchoredDialog(d);
      });
    });
  }

  // ---------- Adresssuche --------------------------------------------------
  // Ort anspringen: Suche ueber den Server (/api/proxy?type=geocode, Nominatim),
  // Treffer als Liste, Klick setzt eine reine Anzeige-Markierung (kein
  // Objekt der Lagekarte, wird nicht gespeichert und nicht synchronisiert).
  let geoMarker = null;
  let geoLastAt = 0;

  function hideGeoHits() {
    const hits = $('lk-geo-hits');
    if (hits) { hits.innerHTML = ''; hits.hidden = true; }
  }

  function clearGeoMarker() {
    if (geoMarker) { map.removeLayer(geoMarker); geoMarker = null; }
    const row = $('lk-geo-clear-row');
    if (row) row.hidden = true;
    hideGeoHits();
  }

  function showGeoHit(lat, lng, label) {
    clearGeoMarker();
    geoMarker = L.circleMarker([lat, lng], {
      radius: 9, color: '#b3261e', weight: 3, fillColor: '#fff', fillOpacity: 0.9,
      bubblingMouseEvents: false, // Klick auf die Markierung ist kein Kartenklick (kein Zeichen platzieren)
    }).addTo(map);
    geoMarker.bindPopup('<strong>Adresssuche</strong><br>' + ui.esc(label) + '<br><small>' + lat.toFixed(5) + ', ' + lng.toFixed(5) + '</small>', { offset: L.point(0, -6) });
    map.setView([lat, lng], Math.max(map.getZoom(), 15), { animate: true });
    geoMarker.openPopup();
    const row = $('lk-geo-clear-row');
    if (row) row.hidden = false;
  }

  async function searchAddress() {
    const input = $('lk-geo-input');
    const hits = $('lk-geo-hits');
    if (!input || !hits) return;
    const q = input.value.trim();
    if (!q) { input.focus(); return; }
    // Doppelklick-Schutz; die eigentliche Bremse (1 Anfrage/s) sitzt im Server
    if (Date.now() - geoLastAt < 2000) { ui.toast('Bitte kurz warten, dann erneut suchen.', 'warn', 2000); return; }
    geoLastAt = Date.now();
    hits.hidden = false;
    hits.innerHTML = '<div class="lk-geo-none">Suche …</div>';
    try {
      const res = await fetch('/api/proxy?type=geocode&limit=6&q=' + encodeURIComponent(q), { headers: { 'Accept': 'application/json' } });
      if (res.status === 429) { hits.innerHTML = '<div class="lk-geo-none">Adresssuche gerade ausgelastet – bitte kurz warten und erneut suchen.</div>'; return; }
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const list = await res.json();
      hits.innerHTML = '';
      if (!Array.isArray(list) || !list.length) {
        hits.innerHTML = '<div class="lk-geo-none">Keine Treffer. Bitte genauer eingeben (Ort, Straße, Hausnummer).</div>';
        return;
      }
      list.forEach(r => {
        const lat = parseFloat(r.lat), lng = parseFloat(r.lon);
        if (!isFinite(lat) || !isFinite(lng)) return;
        const b = document.createElement('button');
        b.type = 'button';
        b.className = 'lk-geo-hit';
        b.textContent = r.display_name || (lat.toFixed(5) + ', ' + lng.toFixed(5));
        b.addEventListener('click', () => {
          // Treffer uebernommen: Liste schliessen, Adresse ins Feld
          const label = b.textContent;
          showGeoHit(lat, lng, label);
          input.value = label;
        });
        hits.appendChild(b);
      });
      // Ein eindeutiger Treffer: direkt anspringen
      if (list.length === 1 && hits.firstChild) hits.firstChild.click();
    } catch (e) {
      hits.innerHTML = '<div class="lk-geo-none">Adresssuche nicht erreichbar (der Server braucht Internetzugang zu nominatim.openstreetmap.org).</div>';
    }
  }

  function bindGeoSearch() {
    const input = $('lk-geo-input');
    if (!input) return;
    on('lk-geo-btn', 'click', searchAddress);
    input.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); searchAddress(); } });
    on('lk-geo-clear', 'click', () => { clearGeoMarker(); input.value = ''; input.focus(); });
    // Eingabe geleert (auch ueber das x des Suchfelds): Liste schliessen
    input.addEventListener('input', () => { if (!input.value.trim()) hideGeoHits(); });
  }

  // ---------- Boot ---------------------------------------------------------
  document.addEventListener('DOMContentLoaded', async () => {
    initMap();
    bindUi();
    bindGeoSearch();
    renderAbschnittList();
    updateKarteUi();
    if (READONLY) ui.showReadonlyBanner();
    const catalogP = loadCatalog();
    try {
      await loadList();
      let target = CFG.karte && karten.some(k => k.id === CFG.karte) ? CFG.karte : (karten[0] ? karten[0].id : '');
      if (CFG.karte && target !== CFG.karte) ui.toast('Die angeforderte Lagekarte existiert nicht (mehr).', 'warn');
      await openKarte(target);
    } catch (e) {
      ui.toast('Laden fehlgeschlagen: ' + ui.errorText(e), 'error');
      showEmpty(true);
      await ensureSyncWithoutKarte();
    }
    await catalogP;
    if (K) {
      K.onLoad(() => renderHeimat());
      K.load();
      setInterval(() => { if (!document.hidden) K.load(); }, 60000);
    }
  });
})();
