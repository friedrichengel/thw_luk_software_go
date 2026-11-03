// harke.js - Fuehrungsharke: Knoten, Mitglieder (Fachberater / interne
// Einheiten), Status, externe Links. Jede Aenderung ist ein eigener
// API-Aufruf mit Versionspruefung; andere Clients bekommen sie per
// Live-Sync (LUK.sync).
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

  const canHarke = ui.can('harke');
  const canKraefte = ui.can('kraefte');
  const canStatus = ui.can('status');
  const canEditMeta = canHarke || canKraefte;   // Funkruf/OV/Bemerkung

  // ---- Zusatzdaten (Verknuepfungen) ----
  let lagekarten = new Map();          // id -> {id,title}
  let featureRefs = new Map();         // feature id -> {lagekarte_id, ref_kind, ref_id}
  let schadenskonten = new Map();      // id -> {id,title}
  let skRefs = [];                     // [{schadenskonto_id, ref_kind, ref_id}]
  let recent = [];

  // ---- DOM ----
  const $tree = document.getElementById('fh-tree');
  const $staerke = document.getElementById('hk-staerke');
  const $anfahrt = document.getElementById('hk-anfahrt');
  const $recent = document.getElementById('hk-recent');
  const $rootAdd = document.getElementById('hk-root-add');

  const $nd = document.getElementById('hk-node-dialog');
  const $ndTitle = document.getElementById('hk-nd-title');
  const $ndType = document.getElementById('hk-nd-type');
  const $ndStatusLabel = document.getElementById('hk-nd-status-label');
  const $ndStatus = document.getElementById('hk-nd-status');
  const $ndBez = document.getElementById('hk-nd-bez');
  const $ndFunkruf = document.getElementById('hk-nd-funkruf');
  const $ndOv = document.getElementById('hk-nd-ov');
  const $ndNote = document.getElementById('hk-nd-note');
  const $ndLinkUrl = document.getElementById('hk-nd-link-url');
  const $ndLinkLabel = document.getElementById('hk-nd-link-label');
  const $ndLinkFs = document.getElementById('hk-nd-link-fs');
  const $ndSave = document.getElementById('hk-nd-save');
  const $ndCancel = document.getElementById('hk-nd-cancel');
  const $ndKraft = document.getElementById('hk-nd-kraft');
  const $mdKraft = document.getElementById('hk-md-kraft');
  const $katalog = document.getElementById('hk-katalog');

  const $md = document.getElementById('hk-member-dialog');
  const $mdTitle = document.getElementById('hk-md-title');
  const $mdFunktion = document.getElementById('hk-md-funktion');
  const $mdName = document.getElementById('hk-md-name');
  const $mdBez = document.getElementById('hk-md-bez');
  const $mdFunkruf = document.getElementById('hk-md-funkruf');
  const $mdTelefon = document.getElementById('hk-md-telefon');
  const $mdOv = document.getElementById('hk-md-ov');
  const $mdStatus = document.getElementById('hk-md-status');
  const $mdNote = document.getElementById('hk-md-note');
  const $mdSave = document.getElementById('hk-md-save');
  const $mdCancel = document.getElementById('hk-md-cancel');

  const $ext = document.getElementById('hk-extern-dialog');
  const $extText = document.getElementById('hk-extern-text');
  const $extOpen = document.getElementById('hk-extern-open');
  const $extCancel = document.getElementById('hk-extern-cancel');

  let nodeDlg = { mode: 'edit', id: null, parentId: null, type: 'einheit', kraftKey: '' };
  let memberDlg = { mode: 'edit', id: null, nodeId: null, kind: 'fachberater', kraftKey: '' };

  // ---- Kraeftekatalog (Verweis kraft_key, Picker, Statuspunkt) ----
  // Punkt mit Verfuegbarkeit fuer eine Einheit mit Katalog-Verweis (oder null)
  function kraftDot(key) {
    if (!key) return null;
    const l = K.lookup(key);
    if (!l) return null;
    const a = K.avail(l.einheit);
    const d = K.dot(a, K.label(key) + '\n' + a.text);
    d.classList.add('hk-kraft-dot');
    return d;
  }
  // Ist dieselbe Katalog-Einheit schon an anderer Stelle dieses Einsatzes eingeplant?
  function kraftDuplicates(key, excludeKind, excludeId) {
    const out = [];
    if (!key) return out;
    T.nodes.forEach(n => { if (n.kraft_key === key && !(excludeKind === 'node' && n.id === excludeId) && n.status !== 'abgerueckt') out.push(T.label(n)); });
    T.mitglieder.forEach(m => { if (m.kraft_key === key && !(excludeKind === 'mitglied' && m.id === excludeId) && m.status !== 'abgerueckt') out.push(T.mitgliedLabel(m)); });
    return out;
  }
  function renderKraftLine(host, key, onRemove) {
    if (!host) return;
    host.innerHTML = '';
    if (!key) return;
    host.appendChild(K.chip(key, { onRemove }));
  }
  // Auswahl aus dem Picker in die Dialogfelder uebernehmen (Freitext bleibt editierbar)
  function applyPick(entry, fields, state, lineHost, excludeKind, excludeId) {
    const f = K.fill(entry);
    state.kraftKey = f.kraft_key;
    if (fields.bez) fields.bez.value = f.bezeichnung;
    if (fields.ov) fields.ov.value = f.ov;
    if (fields.funkruf && f.funkruf && !fields.funkruf.value.trim()) fields.funkruf.value = f.funkruf;
    renderKraftLine(lineHost, state.kraftKey, () => { state.kraftKey = ''; renderKraftLine(lineHost, ''); });
    const dup = kraftDuplicates(f.kraft_key, excludeKind, excludeId);
    if (dup.length) ui.toast('Hinweis: „' + K.label(f.kraft_key) + '“ ist in diesem Einsatz bereits eingeplant (' + dup.join(', ') + ').', 'warn', 6000);
    if (entry.avail.cls === 'gebunden' && !dup.length) ui.toast('Hinweis: ' + K.label(f.kraft_key) + ' – ' + entry.avail.text, 'warn', 6000);
  }
  if ($ndBez && K) {
    K.picker($ndBez, {
      typ: () => K.typOf(nodeDlg.kraftKey),
      exclude: () => new Set(),
      onPick: entry => applyPick(entry, { bez: $ndBez, ov: $ndOv, funkruf: $ndFunkruf }, nodeDlg, $ndKraft, 'node', nodeDlg.id),
    });
  }
  if ($mdBez && K) {
    K.picker($mdBez, {
      typ: () => K.typOf(memberDlg.kraftKey),
      onPick: entry => applyPick(entry, { bez: $mdBez, ov: $mdOv, funkruf: $mdFunkruf }, memberDlg, $mdKraft, 'mitglied', memberDlg.id),
    });
  }
  function renderKatalog() {
    if (!$katalog) return;
    if (!K.data) { $katalog.textContent = 'Kräftekatalog nicht verfügbar.'; return; }
    const s = K.summary(Date.now(), 0);
    const stein = K.data.stein || {};
    const bereich = K.data.bereich_key ? (K.data.bereich_key.indexOf('ov:') === 0
      ? 'OV ' + ((K.data.ovs.find(o => 'ov:' + o.key === K.data.bereich_key) || {}).name || '')
      : ((K.regionen.get(K.data.bereich_key) || {}).name || K.data.bereich_key)) : 'gesamter LV';
    $katalog.innerHTML = '<div><strong>' + ui.esc(bereich) + '</strong></div>'
      + '<div class="hk-katalog-row"><span class="luk-kp-dot luk-kp-dot-frei"></span> ' + s.frei + ' einsatzbereit'
      + ' <span class="luk-kp-dot luk-kp-dot-bedingt"></span> ' + s.bedingt + ' bedingt'
      + ' <span class="luk-kp-dot luk-kp-dot-gebunden"></span> ' + s.gebunden + ' im Einsatz</div>'
      + '<div class="hk-katalog-row"><span class="luk-kp-dot luk-kp-dot-unbekannt"></span> ' + s.unbekannt + ' ohne Live-Daten'
      + ' <span class="luk-kp-dot luk-kp-dot-nicht"></span> ' + s.nicht + ' nicht einsatzbereit</div>'
      + '<div class="luk-kp-stand">' + (stein.ok ? 'STEIN-Stand ' + ui.esc(ui.fmtDateTime(stein.stand)) + (stein.stale ? ' (veraltet)' : '')
        : (stein.aktiv === false ? 'STEIN-Daten für diesen Einsatz abgeschaltet' : 'keine STEIN-Live-Daten')) + '</div>';
  }
  if (K) K.onLoad(() => { render(); renderKatalog(); });

  // ---- Status-Selects fuellen ----
  function fillStatusSelect(sel) {
    sel.innerHTML = '';
    Object.keys(L.CFG.status || {}).forEach(k => {
      const o = document.createElement('option');
      o.value = k;
      o.textContent = L.CFG.status[k];
      sel.appendChild(o);
    });
  }
  fillStatusSelect($ndStatus);
  fillStatusSelect($mdStatus);

  // ---- Laden ----
  async function load() {
    try {
      const data = await api.get('einsatz', 'load', { einsatz: CTX.uuid });
      T.load(data.tree);
      lagekarten = new Map((data.lagekarten || []).map(k => [k.id, k]));
      featureRefs = new Map((data.feature_refs || []).map(f => [f.id, f]));
      schadenskonten = new Map((data.schadenskonten || []).map(s => [s.id, s]));
      skRefs = [];
      (data.schadenskonten || []).forEach(s => (s.refs || []).forEach(r => skRefs.push(r)));
      recent = data.recent_events || [];
      render();
      renderRecent();
      L.sync.start(T.seq);
      if (K) K.load().then(() => openFromUrl());
    } catch (e) {
      ui.toast('Laden fehlgeschlagen: ' + ui.errorText(e), 'error');
    }
  }

  L.sync.onEvent((events, treeChanged) => {
    let extraChanged = false;
    events.forEach(ev => {
      const a = ev.after || {};
      if (ev.entity === 'lagekarte') {
        if (ev.action === 'delete' || a.deleted_at) lagekarten.delete(ev.entity_id);
        else if (a.id) lagekarten.set(a.id, a);
        extraChanged = true;
      } else if (ev.entity === 'feature') {
        if (ev.action === 'delete' || a.deleted_at) featureRefs.delete(ev.entity_id);
        else if (a.id) { if (a.ref_id) featureRefs.set(a.id, a); else featureRefs.delete(a.id); }
        extraChanged = true;
      } else if (ev.entity === 'schadenskonto') {
        if (ev.action === 'delete' || a.deleted_at) { schadenskonten.delete(ev.entity_id); skRefs = skRefs.filter(r => r.schadenskonto_id !== ev.entity_id); }
        else if (a.id) schadenskonten.set(a.id, a);
        extraChanged = true;
      } else if (ev.entity === 'sk_ref') {
        if (a.schadenskonto_id) {
          skRefs = skRefs.filter(r => !(r.schadenskonto_id === a.schadenskonto_id && r.ref_kind === a.ref_kind && r.ref_id === a.ref_id));
          if (!a.removed && ev.action !== 'delete') skRefs.push({ schadenskonto_id: a.schadenskonto_id, ref_kind: a.ref_kind, ref_id: a.ref_id });
        }
        extraChanged = true;
      }
      recent.push(ev);
    });
    if (recent.length > 40) recent = recent.slice(-40);
    if (treeChanged || extraChanged) render();
    renderRecent();
  });

  // ---- Rendering ----
  function render() {
    if (!$tree) return;
    const scrollTop = $tree.parentElement ? $tree.parentElement.scrollTop : 0;
    const scrollLeft = $tree.parentElement ? $tree.parentElement.scrollLeft : 0;
    $tree.innerHTML = '';
    const roots = T.roots();
    if (!roots.length) {
      const hint = document.createElement('div');
      hint.className = 'fh-empty-hint';
      hint.textContent = canHarke
        ? 'Noch leer. Rechts unter „Oberste Ebene" eine Führungsstelle oder Einheit anlegen.'
        : 'Diese Führungsharke enthält noch keine Elemente.';
      $tree.appendChild(hint);
    } else {
      roots.forEach(r => $tree.appendChild(renderSubtree(r)));
    }
    if (canHarke) {
      const rootDrop = document.createElement('div');
      rootDrop.className = 'fh-root-drop';
      rootDrop.textContent = 'Hier ablegen, um auf oberste Ebene zu verschieben';
      rootDrop.addEventListener('dragover', e => { e.preventDefault(); if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'; rootDrop.classList.add('fh-root-drop-over'); });
      rootDrop.addEventListener('dragleave', () => rootDrop.classList.remove('fh-root-drop-over'));
      rootDrop.addEventListener('drop', e => {
        e.preventDefault();
        rootDrop.classList.remove('fh-root-drop-over');
        const src = parseDragData(e.dataTransfer);
        if (!src) return;
        if (src.kind === 'node') moveNode(src.id, null, '', '');
        else if (src.kind === 'mitglied') mitgliedToNode(src.id, null);
      });
      $tree.appendChild(rootDrop);
    }
    if ($tree.parentElement) { $tree.parentElement.scrollTop = scrollTop; $tree.parentElement.scrollLeft = scrollLeft; }
    renderStaerke();
    renderAnfahrt();
  }

  function renderSubtree(node) {
    const wrap = document.createElement('div');
    wrap.className = 'fh-subtree';
    wrap.setAttribute('data-id', node.id);
    wrap.appendChild(renderNode(node));
    const kids = T.children(node.id);
    if (kids.length) {
      wrap.classList.add('fh-has-children');
      const kidsEl = document.createElement('div');
      kidsEl.className = 'fh-children';
      if (kids.length === 1) kidsEl.classList.add('fh-single');
      kids.forEach(c => kidsEl.appendChild(renderSubtree(c)));
      wrap.appendChild(kidsEl);
    }
    return wrap;
  }

  function metaRow(label, value) {
    const row = document.createElement('div');
    row.className = 'fh-meta-row';
    const k = document.createElement('span'); k.className = 'fh-meta-key'; k.textContent = label;
    const v = document.createElement('span'); v.className = 'fh-meta-val'; v.textContent = value || '-';
    row.appendChild(k); row.appendChild(v);
    return row;
  }
  function btn(text, onClick, kind, title) {
    const b = document.createElement('button');
    b.type = 'button';
    b.textContent = text;
    b.className = 'fh-action-btn';
    if (kind === 'danger') b.classList.add('fh-danger');
    else if (kind === 'add') b.classList.add('fh-primary');
    if (title) b.title = title;
    b.addEventListener('click', e => { e.stopPropagation(); onClick(); });
    return b;
  }

  // Schnellaktionen fuer den Anfahrts-Workflow
  function statusQuickButtons(kind, row) {
    const wrap = document.createElement('div');
    wrap.className = 'fh-node-status-row';
    const us = S.unitStatus(kind, row);
    wrap.appendChild(ui.statusBadge(us.status));
    if (us.status_at) {
      const t = document.createElement('span');
      t.className = 'luk-muted';
      t.style.fontSize = '10px';
      t.textContent = 'seit ' + ui.fmtServerTs(us.status_at);
      wrap.appendChild(t);
    }
    if (us.derived) {
      // Status kommt aus den Schichten (Kraefteplanung / Kraefteuebersicht)
      const t = document.createElement('span');
      t.className = 'luk-muted';
      t.style.fontSize = '10px';
      t.textContent = '(aus Schichten)';
      t.title = 'Der Status wird je Schicht in der Kräfteplanung oder Kräfteübersicht gesetzt.';
      wrap.appendChild(t);
      return wrap;
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
        b.className = 'hk-status-btn' + (next[0] === 'vor_ort' ? ' hk-status-btn-ok' : '');
        b.textContent = next[1];
        b.title = 'Status auf „' + ui.statusLabel(next[0]) + '" setzen';
        b.addEventListener('click', e => { e.stopPropagation(); setStatus(kind, row, next[0]); });
        wrap.appendChild(b);
      }
    }
    return wrap;
  }

  function linksRow(kind, id) {
    const row = document.createElement('div');
    row.className = 'hk-links-row';
    // Lagekarten, auf denen diese Einheit verknuepft ist
    const karten = new Map();
    featureRefs.forEach(f => { if (f.ref_kind === kind && f.ref_id === id) karten.set(f.lagekarte_id, (karten.get(f.lagekarte_id) || 0) + 1); });
    karten.forEach((count, kid) => {
      const k = lagekarten.get(kid);
      const a = document.createElement('a');
      a.className = 'luk-chip luk-chip-link';
      a.textContent = '🗺 ' + (k ? k.title : 'Lagekarte') + (count > 1 ? ' (' + count + ')' : '');
      a.href = '/lagekarte?einsatz=' + encodeURIComponent(CTX.uuid) + '&karte=' + encodeURIComponent(kid) + '&focus=' + encodeURIComponent(kind + ':' + id);
      a.title = 'Auf Lagekarte anzeigen';
      a.addEventListener('click', e => e.stopPropagation());
      row.appendChild(a);
    });
    skRefs.filter(r => r.ref_kind === kind && r.ref_id === id).forEach(r => {
      const sk = schadenskonten.get(r.schadenskonto_id);
      const a = document.createElement('a');
      a.className = 'luk-chip luk-chip-link';
      a.textContent = '📋 ' + (sk ? (sk.title || 'Schadenskonto') : 'Schadenskonto');
      a.href = '/schadenskonten?einsatz=' + encodeURIComponent(CTX.uuid) + '&sk=' + encodeURIComponent(r.schadenskonto_id);
      a.title = 'Schadenskonto öffnen';
      a.addEventListener('click', e => e.stopPropagation());
      row.appendChild(a);
    });
    return row.childNodes.length ? row : null;
  }

  function renderNode(node) {
    const card = document.createElement('div');
    card.className = 'fh-node fh-type-' + node.type;
    card.setAttribute('data-id', node.id);
    if (node.type !== 'fuehrung' && node.status === 'abgerueckt') card.classList.add('fh-node-abgerueckt');

    if (canHarke) attachNodeDnd(card, node);

    const now = Date.now();
    const sum = S.sumStaerke(node.id, now);
    if (!sum[0] && !sum[1] && !sum[2]) card.classList.add('fh-node-inactive');

    const hdr = document.createElement('div');
    hdr.className = 'fh-node-header';
    const badge = document.createElement('span');
    badge.className = 'fh-node-type-badge';
    badge.textContent = node.type === 'einheit' ? 'Einheit' : 'Führung';
    hdr.appendChild(badge);
    const title = document.createElement('div');
    title.className = 'fh-node-title';
    title.textContent = T.label(node);
    hdr.appendChild(title);
    const kd = kraftDot(node.kraft_key);
    if (kd) hdr.appendChild(kd);
    const a = ui.ampelDot(S.nodeAmpel(node.id, now));
    if (a) hdr.appendChild(a);
    card.appendChild(hdr);

    const own = S.ownStaerke(node.id, now);
    const hasKids = T.children(node.id).length > 0;
    if (own[0] || own[1] || own[2] || hasKids || node.funkruf || node.ov) {
      const meta = document.createElement('div');
      meta.className = 'fh-node-meta';
      if (node.funkruf) meta.appendChild(metaRow('Funk', node.funkruf));
      if (node.ov) meta.appendChild(metaRow('OV', node.ov));
      meta.appendChild(metaRow('Stärke', S.fmtStaerke(own)));
      if (hasKids && (sum[0] || sum[1] || sum[2])) meta.appendChild(metaRow('Σ inkl. unter', S.fmtStaerke(sum)));
      card.appendChild(meta);
    }
    if (node.note) {
      const nt = document.createElement('div');
      nt.className = 'fh-node-note';
      nt.textContent = node.note;
      card.appendChild(nt);
    }

    // Anforderungs-/Anfahrtsstatus gehoert zu den Einheiten und Fachberatern,
    // die an einer Fuehrungsstelle sitzen - die Fuehrungsstelle selbst (TEL,
    // EAL, Abschnittsleitung) faehrt nicht an und hat deshalb keinen Status.
    if (node.type !== 'fuehrung') card.appendChild(statusQuickButtons('node', node));

    const links = linksRow('node', node.id);
    if (links) card.appendChild(links);
    if (node.extern_link_url) {
      const row = document.createElement('div');
      row.className = 'hk-links-row';
      const chip = document.createElement('span');
      chip.className = 'luk-chip hk-extern-chip';
      chip.textContent = '↗ ' + (node.extern_link_label || 'Externer Stab');
      chip.title = 'Leseansicht des anderen Stabes öffnen';
      chip.addEventListener('click', e => { e.stopPropagation(); openExtern(node); });
      row.appendChild(chip);
      card.appendChild(row);
    }

    // Mitglieder (nur Fuehrung)
    if (node.type === 'fuehrung') {
      const members = document.createElement('div');
      members.className = 'fh-members-section';
      const lab = document.createElement('div');
      lab.className = 'fh-section-label';
      lab.textContent = 'An dieser Stelle';
      members.appendChild(lab);
      const ms = T.mitgliederOf(node.id);
      ms.forEach(m => members.appendChild(renderMitglied(node, m)));
      if (!ms.length) {
        const empty = document.createElement('div');
        empty.className = 'fh-members-empty';
        empty.textContent = canHarke ? 'Noch keine Mitglieder.' : 'Keine Mitglieder.';
        members.appendChild(empty);
      }
      if (canHarke) {
        const adds = document.createElement('div');
        adds.className = 'fh-add-row';
        adds.appendChild(btn('+ Fachberater', () => openMemberDialog('create', null, node.id, 'fachberater'), 'add'));
        adds.appendChild(btn('+ Einheit', () => openMemberDialog('create', null, node.id, 'einheit'), 'add'));
        members.appendChild(adds);
      }
      card.appendChild(members);
    }

    // Aktionen
    if (canHarke || canEditMeta) {
      const ops = document.createElement('div');
      ops.className = 'fh-node-ops';
      ops.appendChild(btn('Bearbeiten', () => openNodeDialog('edit', node.id)));
      if (canHarke) {
        ops.appendChild(btn('↑', () => moveSibling(node, -1), null, 'nach oben/links'));
        ops.appendChild(btn('↓', () => moveSibling(node, +1), null, 'nach unten/rechts'));
        ops.appendChild(btn('Löschen', () => deleteNode(node), 'danger'));
      }
      card.appendChild(ops);
      if (canHarke) {
        const hier = document.createElement('div');
        hier.className = 'fh-hierarchy-section';
        const hLab = document.createElement('div');
        hLab.className = 'fh-section-label';
        hLab.textContent = 'Unterstellt';
        hier.appendChild(hLab);
        const hAdds = document.createElement('div');
        hAdds.className = 'fh-add-row';
        hAdds.appendChild(btn('+ Führungsstelle', () => openNodeDialog('create', null, node.id, 'fuehrung'), 'add'));
        hAdds.appendChild(btn('+ Einheit', () => openNodeDialog('create', null, node.id, 'einheit'), 'add'));
        hier.appendChild(hAdds);
        card.appendChild(hier);
      }
    }
    return card;
  }

  function renderMitglied(node, m) {
    const row = document.createElement('div');
    row.className = 'fh-fb' + (m.kind === 'einheit' ? ' fh-internal-einheit' : '');
    if (m.status === 'abgerueckt') row.classList.add('fh-fb-abgerueckt');
    const schichten = T.schichtenOf(node.id, m.id);
    const st = S.activeShiftStaerke(schichten, Date.now());
    if (!st[0] && !st[1] && !st[2]) row.classList.add('fh-fb-inactive');
    if (canHarke) {
      row.draggable = true;
      row.addEventListener('dragstart', e => {
        e.stopPropagation();
        if (!e.dataTransfer) return;
        e.dataTransfer.setData('text/plain', JSON.stringify({ kind: 'mitglied', id: m.id, nodeId: node.id, mkind: m.kind }));
        e.dataTransfer.effectAllowed = 'move';
        row.classList.add('fh-fb-dragging');
      });
      row.addEventListener('dragend', () => { row.classList.remove('fh-fb-dragging'); clearDropHighlights(); });
    }
    const main = document.createElement('div');
    main.className = 'fh-fb-main';
    const badge = document.createElement('span');
    badge.className = 'fh-fb-typebadge' + (m.kind === 'fachberater' ? ' fh-fb-typebadge-fb' : '');
    badge.textContent = m.kind === 'fachberater' ? 'Fachb.' : 'Einheit';
    main.appendChild(badge);
    const amp = ui.ampelDot(S.ampelForUnit(schichten));
    if (amp) main.appendChild(amp);
    const text = document.createElement('span');
    text.textContent = ' ' + T.mitgliedLabel(m)
      + (m.funkruf ? ' · ' + m.funkruf : '')
      + (m.telefon ? ' (' + m.telefon + ')' : '')
      + (m.ov ? ' · ' + m.ov : '')
      + ((st[0] || st[1] || st[2]) ? ' · ' + S.fmtStaerke(st) : '');
    main.appendChild(text);
    const mkd = kraftDot(m.kraft_key);
    if (mkd) main.appendChild(mkd);
    const mus = S.unitStatus('mitglied', m);
    main.appendChild(ui.statusBadge(mus.status));
    const links = linksRow('mitglied', m.id);
    if (links) main.appendChild(links);
    row.appendChild(main);
    if (canHarke || canEditMeta || canStatus) {
      const actions = document.createElement('div');
      actions.className = 'fh-fb-actions';
      if (canStatus && !mus.derived) {
        const next = m.status === 'geplant' ? ['angefordert', 'Anfordern']
          : m.status === 'angefordert' ? ['anfahrt', 'Anfahrt']
          : m.status === 'anfahrt' ? ['vor_ort', 'Vor Ort']
          : m.status === 'vor_ort' ? ['abgerueckt', 'Abgerückt'] : null;
        if (next) {
          const b = document.createElement('button');
          b.type = 'button';
          b.className = 'hk-status-btn' + (next[0] === 'vor_ort' ? ' hk-status-btn-ok' : '');
          b.textContent = next[1];
          b.addEventListener('click', e => { e.stopPropagation(); setStatus('mitglied', m, next[0]); });
          actions.appendChild(b);
        }
      }
      if (canHarke || canEditMeta) actions.appendChild(btn('Bearb.', () => openMemberDialog('edit', m.id, node.id, m.kind)));
      if (canHarke) actions.appendChild(btn('Löschen', () => deleteMitglied(m), 'danger'));
      row.appendChild(actions);
    }
    return row;
  }

  function renderStaerke() {
    if (!$staerke) return;
    $staerke.innerHTML = '';
    const now = Date.now();
    const total = [0, 0, 0];
    T.roots().forEach(r => {
      const s = S.sumStaerke(r.id, now);
      total[0] += s[0]; total[1] += s[1]; total[2] += s[2];
      const row = document.createElement('div');
      row.className = 'fh-staerke-row';
      row.textContent = T.label(r) + ': ' + S.fmtStaerke(s);
      $staerke.appendChild(row);
    });
    const row = document.createElement('div');
    row.className = 'fh-staerke-row';
    row.style.fontWeight = 'bold';
    row.textContent = 'Σ ' + S.fmtStaerke(total);
    $staerke.appendChild(row);
  }

  function renderAnfahrt() {
    if (!$anfahrt) return;
    $anfahrt.innerHTML = '';
    const list = [];
    const isAnfahrt = st => st === 'angefordert' || st === 'anfahrt';
    // Einheiten ohne Schichtstatus ueber ihren eigenen Status, sonst je Schicht
    T.nodes.forEach(n => { if (n.type !== 'fuehrung' && !S.unitStatus('node', n).derived && isAnfahrt(n.status)) list.push({ kind: 'node', row: n, label: T.label(n) }); });
    T.mitglieder.forEach(m => { if (!S.unitStatus('mitglied', m).derived && isAnfahrt(m.status)) list.push({ kind: 'mitglied', row: m, label: T.mitgliedLabel(m) }); });
    T.schichten.forEach(s => {
      if (s.aktiv === false || !isAnfahrt(s.status)) return;
      const u = S.shiftUnit(s);
      if (u) list.push({ kind: 'schicht', row: s, label: u.label + ' · Schicht ab ' + ui.fmtDateTime(s.von) });
    });
    if (!list.length) {
      const e = document.createElement('div');
      e.className = 'hk-anfahrt-empty';
      e.textContent = 'Keine Kräfte angefordert oder auf Anfahrt.';
      $anfahrt.appendChild(e);
      return;
    }
    list.sort((a, b) => String(a.row.status_at || '').localeCompare(String(b.row.status_at || '')));
    list.slice(0, 12).forEach(it => {
      const row = document.createElement('div');
      row.className = 'hk-anfahrt-row';
      const name = document.createElement('span');
      name.className = 'hk-anfahrt-name';
      name.textContent = it.label;
      name.title = it.label;
      row.appendChild(name);
      row.appendChild(ui.statusBadge(it.row.status));
      $anfahrt.appendChild(row);
    });
    if (list.length > 12) {
      const more = document.createElement('div');
      more.className = 'hk-anfahrt-empty';
      more.textContent = '… und ' + (list.length - 12) + ' weitere';
      $anfahrt.appendChild(more);
    }
  }

  function renderRecent() {
    if (!$recent) return;
    $recent.innerHTML = '';
    const list = recent.slice().reverse().slice(0, 25);
    if (!list.length) {
      const e = document.createElement('div');
      e.className = 'ez-audit-empty';
      e.textContent = 'Noch keine Änderungen.';
      $recent.appendChild(e);
      return;
    }
    list.forEach(ev => {
      const row = document.createElement('div');
      row.className = 'ez-audit-row';
      row.innerHTML = '<span class="luk-muted">' + ui.esc(ui.fmtServerTs(ev.ts)) + '</span> '
        + '<strong>' + ui.esc(ev.user_label || '?') + '</strong>: ' + ui.esc(ev.summary || ev.action);
      $recent.appendChild(row);
    });
  }

  // ---- Drag & Drop ----
  function parseDragData(dt) {
    if (!dt) return null;
    const raw = dt.getData('text/plain');
    if (!raw) return null;
    try {
      const obj = JSON.parse(raw);
      if (obj && (obj.kind === 'node' || obj.kind === 'mitglied')) return obj;
    } catch (e) { /* ignore */ }
    return null;
  }
  function clearDropHighlights() {
    document.querySelectorAll('.fh-node-drop-left, .fh-node-drop-right, .fh-node-drop-above, .fh-node-drop-below, .fh-node-drop-center')
      .forEach(el => el.classList.remove('fh-node-drop-left', 'fh-node-drop-right', 'fh-node-drop-above', 'fh-node-drop-below', 'fh-node-drop-center'));
  }
  function dropPositionFromEvent(e, card) {
    const rect = card.getBoundingClientRect();
    const xR = (e.clientX - rect.left) / (rect.width || 1);
    const yR = (e.clientY - rect.top) / (rect.height || 1);
    if (xR < 0.20) return 'left';
    if (xR > 0.80) return 'right';
    if (yR < 0.30) return 'above';
    if (yR > 0.70) return 'below';
    return 'center';
  }
  function attachNodeDnd(card, node) {
    card.draggable = true;
    card.addEventListener('dragstart', e => {
      e.stopPropagation();
      if (!e.dataTransfer) return;
      e.dataTransfer.setData('text/plain', JSON.stringify({ kind: 'node', id: node.id }));
      e.dataTransfer.effectAllowed = 'move';
      card.classList.add('fh-node-dragging');
    });
    card.addEventListener('dragend', () => { card.classList.remove('fh-node-dragging'); clearDropHighlights(); });
    card.addEventListener('dragover', e => {
      if (!e.dataTransfer) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = 'move';
      let pos = dropPositionFromEvent(e, card);
      if (pos === 'center' && node.type !== 'fuehrung') pos = 'below';
      card.classList.remove('fh-node-drop-left', 'fh-node-drop-right', 'fh-node-drop-above', 'fh-node-drop-below', 'fh-node-drop-center');
      card.classList.add('fh-node-drop-' + pos);
    });
    card.addEventListener('dragleave', e => {
      if (e.currentTarget !== e.target && card.contains(e.relatedTarget)) return;
      card.classList.remove('fh-node-drop-left', 'fh-node-drop-right', 'fh-node-drop-above', 'fh-node-drop-below', 'fh-node-drop-center');
    });
    card.addEventListener('drop', e => {
      e.preventDefault();
      e.stopPropagation();
      clearDropHighlights();
      let pos = dropPositionFromEvent(e, card);
      if (pos === 'center' && node.type !== 'fuehrung') pos = 'below';
      const src = parseDragData(e.dataTransfer);
      if (!src) return;
      handleDrop(src, node, pos);
    });
  }
  function handleDrop(src, target, pos) {
    if (pos === 'center') {
      if (target.type !== 'fuehrung') return;
      if (src.kind === 'node') {
        if (src.id === target.id) return;
        nodeToMitglied(src.id, target.id);
      } else {
        if (src.nodeId === target.id) return;
        moveMitglied(src.id, target.id);
      }
      return;
    }
    if (src.kind === 'mitglied') {
      if (src.mkind !== 'einheit') { ui.toast('Fachberater können nur an Führungsstellen abgelegt werden.', 'warn'); return; }
      // Interne Einheit -> eigener Knoten an der Zielposition
      const parentId = (pos === 'left' || pos === 'right') ? (target.parent_id || null) : (pos === 'below' ? target.id : (target.parent_id || null));
      mitgliedToNode(src.id, parentId, pos === 'left' ? target.id : '', pos === 'right' ? target.id : '');
      return;
    }
    if (src.id === target.id) return;
    if (pos === 'left') moveNode(src.id, target.parent_id || null, target.id, '');
    else if (pos === 'right') moveNode(src.id, target.parent_id || null, '', target.id);
    else if (pos === 'below') moveNode(src.id, target.id, '', '');
    else if (pos === 'above') insertAbove(src.id, target);
  }

  // ---- Mutationen (API) ----
  async function run(fn, okMsg) {
    try {
      const res = await fn();
      if (okMsg) ui.toast(okMsg, 'ok', 1500);
      render();
      L.sync.poll();
      return res;
    } catch (e) {
      if (!ui.handleConflict(e, cur => T.upsert(cur))) ui.toast(ui.errorText(e), 'error');
      render();
      return null;
    }
  }
  function applyNode(res) { if (res && res.node) T.upsert(res.node); }
  function applyMitglied(res) { if (res && res.mitglied) T.upsert(res.mitglied); }

  function moveNode(id, parentId, beforeId, afterId) {
    const n = T.nodes.get(id);
    if (!n) return;
    if (parentId && T.isDescendant(parentId, id)) { ui.toast('Knoten kann nicht in den eigenen Teilbaum verschoben werden.', 'warn'); return; }
    return run(async () => applyNode(await api.post('einsatz', 'node_move', { id, version: n.version, parent_id: parentId, before_id: beforeId || '', after_id: afterId || '' })));
  }
  async function insertAbove(srcId, target) {
    // Quelle uebernimmt die Position des Ziels, Ziel wird Kind der Quelle.
    const src = T.nodes.get(srcId);
    if (!src || T.isDescendant(target.id, srcId)) { ui.toast('Knoten kann nicht in den eigenen Teilbaum verschoben werden.', 'warn'); return; }
    await run(async () => {
      const r1 = await api.post('einsatz', 'node_move', { id: srcId, version: src.version, parent_id: target.parent_id || null, before_id: target.id, after_id: '' });
      applyNode(r1);
      const t2 = T.nodes.get(target.id);
      const r2 = await api.post('einsatz', 'node_move', { id: target.id, version: t2.version, parent_id: srcId, before_id: '', after_id: '' });
      applyNode(r2);
    });
  }
  function moveSibling(node, dir) {
    const sibs = node.parent_id ? T.children(node.parent_id) : T.roots();
    const idx = sibs.findIndex(x => x.id === node.id);
    const target = idx + dir;
    if (target < 0 || target >= sibs.length) return;
    const other = sibs[target];
    if (dir < 0) moveNode(node.id, node.parent_id || null, other.id, '');
    else moveNode(node.id, node.parent_id || null, '', other.id);
  }
  function deleteNode(node) {
    if (T.children(node.id).length) { ui.toast(ui.errorText(new L.LukError('has_children', 409)), 'warn', 5000); return; }
    const ms = T.mitgliederOf(node.id).length;
    const sc = T.schichtenOf(node.id, null).length;
    if (!window.confirm('„' + T.label(node) + '" löschen?' + (ms ? '\n' + ms + ' Mitglied(er) werden mit gelöscht.' : '') + (sc ? '\n' + sc + ' Schicht(en) werden mit gelöscht.' : ''))) return;
    run(async () => { await api.post('einsatz', 'node_delete', { id: node.id, version: node.version }); T.removeNode(node.id); }, 'Gelöscht.');
  }
  function deleteMitglied(m) {
    if (!window.confirm('„' + T.mitgliedLabel(m) + '" löschen?')) return;
    run(async () => { await api.post('einsatz', 'mitglied_delete', { id: m.id, version: m.version }); T.removeMitglied(m.id); }, 'Gelöscht.');
  }
  function moveMitglied(id, targetNodeId) {
    const m = T.mitglieder.get(id);
    if (!m) return;
    return run(async () => applyMitglied(await api.post('einsatz', 'mitglied_move', { id, version: m.version, node_id: targetNodeId })));
  }
  function nodeToMitglied(nodeId, targetNodeId) {
    const n = T.nodes.get(nodeId);
    if (!n) return;
    if (n.type === 'fuehrung') { ui.toast('Nur Einheiten können als interne Einheit eingegliedert werden.', 'warn'); return; }
    return run(async () => {
      const res = await api.post('einsatz', 'node_to_mitglied', { id: nodeId, version: n.version, target_node_id: targetNodeId });
      T.removeNode(nodeId);
      if (res.mitglied) T.upsert(res.mitglied);
      // Schichten wurden serverseitig umgehaengt -> Tree nachladen
      const t = await api.get('einsatz', 'tree', { einsatz: CTX.uuid });
      T.load(t.tree);
      L.sync.bump(t.tree.seq);
    });
  }
  function mitgliedToNode(id, parentId, beforeId, afterId) {
    const m = T.mitglieder.get(id);
    if (!m) return;
    return run(async () => {
      const res = await api.post('einsatz', 'mitglied_to_node', { id, version: m.version, parent_id: parentId });
      T.removeMitglied(id);
      if (res.node) {
        T.upsert(res.node);
        if (beforeId || afterId) {
          const r2 = await api.post('einsatz', 'node_move', { id: res.node.id, version: res.node.version, parent_id: parentId, before_id: beforeId || '', after_id: afterId || '' });
          applyNode(r2);
        }
      }
      const t = await api.get('einsatz', 'tree', { einsatz: CTX.uuid });
      T.load(t.tree);
      L.sync.bump(t.tree.seq);
    });
  }
  function setStatus(kind, row, status) {
    const action = kind === 'node' ? 'node_status' : 'mitglied_status';
    return run(async () => {
      const res = await api.post('einsatz', action, { id: row.id, version: row.version, status });
      if (kind === 'node') applyNode(res); else applyMitglied(res);
    }, 'Status: ' + ui.statusLabel(status));
  }

  // ---- Knoten-Dialog ----
  // Fuehrungsstellen haben keinen eigenen Status (siehe renderNode) - das
  // Statusfeld erscheint nur fuer Einheiten.
  function syncStatusField() {
    const fuehrung = $ndType.value === 'fuehrung';
    $ndStatusLabel.hidden = fuehrung;
    if (fuehrung) $ndStatus.value = 'geplant';
  }
  $ndType.addEventListener('change', syncStatusField);

  function openNodeDialog(mode, id, parentId, type, prefill) {
    nodeDlg = { mode, id, parentId: parentId || null, type: type || 'einheit', kraftKey: '' };
    const n = mode === 'edit' ? T.nodes.get(id) : null;
    if (mode === 'edit' && !n) return;
    nodeDlg.kraftKey = n ? (n.kraft_key || '') : '';
    renderKraftLine($ndKraft, nodeDlg.kraftKey, () => { nodeDlg.kraftKey = ''; renderKraftLine($ndKraft, ''); });
    $ndTitle.textContent = mode === 'edit' ? (n.type === 'fuehrung' ? 'Führungsstelle bearbeiten' : 'Einheit bearbeiten')
      : (nodeDlg.type === 'fuehrung' ? 'Neue Führungsstelle' : 'Neue Einheit');
    $ndType.value = n ? n.type : nodeDlg.type;
    $ndStatus.value = n ? n.status : 'geplant';
    syncStatusField();
    $ndBez.value = n ? n.bezeichnung : '';
    $ndFunkruf.value = n ? n.funkruf : '';
    $ndOv.value = n ? n.ov : '';
    $ndNote.value = n ? n.note : '';
    $ndLinkUrl.value = n ? n.extern_link_url : '';
    $ndLinkLabel.value = n ? n.extern_link_label : '';
    // Rechte: nur Kraefte-Recht -> nur Funkruf/OV/Bemerkung editierbar
    const structural = canHarke;
    [$ndType, $ndBez, $ndLinkUrl, $ndLinkLabel].forEach(el => el.disabled = !structural);
    $ndStatus.disabled = !canStatus;
    [$ndFunkruf, $ndOv, $ndNote].forEach(el => el.disabled = !canEditMeta);
    $ndLinkFs.hidden = false;
    if (mode === 'edit' && n && T.mitgliederOf(n.id).length && n.type === 'fuehrung') {
      $ndType.disabled = true;
      $ndType.title = 'Typwechsel nicht möglich, solange Mitglieder vorhanden sind.';
    } else {
      $ndType.title = '';
    }
    $nd.showModal();
    if (prefill && K) {
      // Vorbelegung aus dem Kraeftekatalog (z.B. ?kraft=... aus Kraefteplanung / STEINmap)
      applyPick(prefill, { bez: $ndBez, ov: $ndOv, funkruf: $ndFunkruf }, nodeDlg, $ndKraft, 'node', null);
      setTimeout(() => $ndNote.focus(), 30);
      return;
    }
    setTimeout(() => $ndBez.focus(), 30);
  }
  $ndCancel.addEventListener('click', () => $nd.close());
  // ?kraft=<key>: Einheit aus dem Katalog direkt als neuen Knoten anlegen.
  // ?kraft_text=<Bezeichnung>[&kraft_ov=..&kraft_funk=..]: dasselbe als Freitext
  // fuer STEIN-Einheiten ohne Katalog-Gegenstueck (LuK, OV-Stab ...).
  function openFromUrl() {
    let key = '', text = '', ov = '', funk = '';
    try {
      const p = new URL(window.location.href).searchParams;
      key = p.get('kraft') || ''; text = p.get('kraft_text') || ''; ov = p.get('kraft_ov') || ''; funk = p.get('kraft_funk') || '';
    } catch (e) { return; }
    if ((!key && !text) || !canHarke) return;
    try {
      const u = new URL(window.location.href);
      ['kraft', 'kraft_text', 'kraft_ov', 'kraft_funk'].forEach(k => u.searchParams.delete(k));
      window.history.replaceState(null, '', u.toString());
    } catch (e) { /* ignore */ }
    if (!key) {
      openNodeDialog('create', null, null, 'einheit');
      $ndBez.value = text.trim();
      if (canEditMeta) { $ndOv.value = ov.trim(); $ndFunkruf.value = funk.trim(); }
      setTimeout(() => $ndNote.focus(), 30);
      return;
    }
    const l = K.lookup(key);
    if (!l) { ui.toast('Katalog-Einheit nicht gefunden: ' + key, 'warn'); return; }
    const entry = { key, einheit: l.einheit, ov: l.ov, avail: K.avail(l.einheit), bereich: K.bereichRank(l.ov), dist: K.distKm(l.ov) };
    openNodeDialog('create', null, null, 'einheit', entry);
  }
  $ndSave.addEventListener('click', async () => {
    const fields = {};
    if (canHarke) {
      fields.type = $ndType.value === 'fuehrung' ? 'fuehrung' : 'einheit';
      fields.bezeichnung = $ndBez.value.trim();
      fields.extern_link_url = $ndLinkUrl.value.trim();
      fields.extern_link_label = $ndLinkLabel.value.trim();
      if (fields.extern_link_url && !/^https?:\/\//i.test(fields.extern_link_url)) { ui.toast('Bitte eine gültige http(s)-Adresse angeben.', 'warn'); return; }
    }
    if (canEditMeta) {
      fields.funkruf = $ndFunkruf.value.trim();
      fields.ov = $ndOv.value.trim();
      fields.kraft_key = nodeDlg.kraftKey || '';
      fields.note = $ndNote.value.trim();
    }
    const fuehrung = (fields.type || (T.nodes.get(nodeDlg.id) || {}).type || nodeDlg.type) === 'fuehrung';
    const status = fuehrung ? 'geplant' : $ndStatus.value;
    if (nodeDlg.mode === 'create') {
      if (!fields.bezeichnung) { ui.toast('Bitte eine Bezeichnung angeben.', 'warn'); $ndBez.focus(); return; }
      $nd.close();
      await run(async () => applyNode(await api.post('einsatz', 'node_create', Object.assign({ parent_id: nodeDlg.parentId, status }, fields))), 'Angelegt.');
      return;
    }
    const n = T.nodes.get(nodeDlg.id);
    if (!n) { $nd.close(); return; }
    $nd.close();
    await run(async () => {
      let cur = n;
      const changed = Object.keys(fields).filter(k => String(cur[k] || '') !== String(fields[k] || ''));
      if (changed.length) {
        const sub = {};
        changed.forEach(k => sub[k] = fields[k]);
        const res = await api.post('einsatz', 'node_update', { id: cur.id, version: cur.version, fields: sub });
        applyNode(res);
        cur = res.node;
      }
      if (canStatus && cur.type !== 'fuehrung' && status !== cur.status) {
        applyNode(await api.post('einsatz', 'node_status', { id: cur.id, version: cur.version, status }));
      }
    }, 'Gespeichert.');
  });

  // ---- Mitglieder-Dialog ----
  function openMemberDialog(mode, id, nodeId, kind) {
    memberDlg = { mode, id, nodeId, kind, kraftKey: '' };
    const m = mode === 'edit' ? T.mitglieder.get(id) : null;
    if (mode === 'edit' && !m) return;
    const isFb = kind === 'fachberater';
    memberDlg.kraftKey = m ? (m.kraft_key || '') : '';
    renderKraftLine($mdKraft, memberDlg.kraftKey, () => { memberDlg.kraftKey = ''; renderKraftLine($mdKraft, ''); });
    $mdKraft.hidden = isFb;
    $mdTitle.textContent = (mode === 'edit' ? '' : 'Neu: ') + (isFb ? 'Fachberater' : 'Interne Einheit');
    document.getElementById('hk-md-funktion-label').hidden = !isFb;
    document.getElementById('hk-md-name-label').hidden = !isFb;
    document.getElementById('hk-md-bez-label').hidden = isFb;
    document.getElementById('hk-md-telefon-label').hidden = !isFb;
    $mdFunktion.value = m ? m.funktion : '';
    $mdName.value = m ? m.name : '';
    $mdBez.value = m ? m.bezeichnung : '';
    $mdFunkruf.value = m ? m.funkruf : '';
    $mdTelefon.value = m ? m.telefon : '';
    $mdOv.value = m ? m.ov : '';
    $mdNote.value = m ? m.note : '';
    $mdStatus.value = m ? m.status : 'geplant';
    $mdBez.disabled = !canHarke;
    $mdStatus.disabled = !canStatus;
    [$mdFunktion, $mdName, $mdFunkruf, $mdTelefon, $mdOv, $mdNote].forEach(el => el.disabled = !canEditMeta);
    $md.showModal();
    setTimeout(() => (isFb ? $mdFunktion : $mdBez).focus(), 30);
  }
  $mdCancel.addEventListener('click', () => $md.close());
  $mdSave.addEventListener('click', async () => {
    const fields = {};
    if (canHarke) fields.bezeichnung = $mdBez.value.trim();
    if (canEditMeta) {
      fields.funktion = $mdFunktion.value.trim();
      fields.name = $mdName.value.trim();
      fields.funkruf = $mdFunkruf.value.trim();
      fields.telefon = $mdTelefon.value.trim();
      fields.ov = $mdOv.value.trim();
      fields.kraft_key = memberDlg.kind === 'einheit' ? (memberDlg.kraftKey || '') : '';
      fields.note = $mdNote.value.trim();
    }
    const status = $mdStatus.value;
    if (memberDlg.mode === 'create') {
      if (memberDlg.kind === 'einheit' && !fields.bezeichnung) { ui.toast('Bitte eine Bezeichnung angeben.', 'warn'); return; }
      if (memberDlg.kind === 'fachberater' && !fields.funktion && !fields.name) { ui.toast('Bitte Funktion oder Name angeben.', 'warn'); return; }
      $md.close();
      await run(async () => applyMitglied(await api.post('einsatz', 'mitglied_create', Object.assign({ node_id: memberDlg.nodeId, kind: memberDlg.kind, status }, fields))), 'Angelegt.');
      return;
    }
    const m = T.mitglieder.get(memberDlg.id);
    if (!m) { $md.close(); return; }
    $md.close();
    await run(async () => {
      let cur = m;
      const changed = Object.keys(fields).filter(k => String(cur[k] || '') !== String(fields[k] || ''));
      if (changed.length) {
        const sub = {};
        changed.forEach(k => sub[k] = fields[k]);
        const res = await api.post('einsatz', 'mitglied_update', { id: cur.id, version: cur.version, fields: sub });
        applyMitglied(res);
        cur = res.mitglied;
      }
      if (canStatus && status !== cur.status) {
        applyMitglied(await api.post('einsatz', 'mitglied_status', { id: cur.id, version: cur.version, status }));
      }
    }, 'Gespeichert.');
  });

  // ---- Externer Link ----
  function openExtern(node) {
    $extText.textContent = (node.extern_link_label || 'Externer Stab') + ' – ' + node.extern_link_url;
    $extOpen.href = node.extern_link_url;
    $ext.showModal();
  }
  $extCancel.addEventListener('click', () => $ext.close());
  $extOpen.addEventListener('click', () => setTimeout(() => $ext.close(), 50));

  // ---- Oberste Ebene ----
  if ($rootAdd) {
    $rootAdd.hidden = !canHarke;
    document.getElementById('hk-root-add-fuehrung').addEventListener('click', () => openNodeDialog('create', null, null, 'fuehrung'));
    document.getElementById('hk-root-add-einheit').addEventListener('click', () => openNodeDialog('create', null, null, 'einheit'));
  }

  // ---- Zoom / Filter ----
  const ZOOM_KEY = 'luk_harke_zoom';
  let zoomLevel = parseFloat(localStorage.getItem(ZOOM_KEY) || '1') || 1;
  function applyZoom() {
    zoomLevel = Math.min(2.5, Math.max(0.4, +zoomLevel.toFixed(2)));
    $tree.style.zoom = String(zoomLevel);
    document.getElementById('hk-zoom-display').textContent = Math.round(zoomLevel * 100) + ' %';
    try { localStorage.setItem(ZOOM_KEY, String(zoomLevel)); } catch (e) {}
  }
  document.getElementById('hk-zoom-in').addEventListener('click', () => { zoomLevel += 0.1; applyZoom(); });
  document.getElementById('hk-zoom-out').addEventListener('click', () => { zoomLevel -= 0.1; applyZoom(); });
  document.getElementById('hk-zoom-reset').addEventListener('click', () => { zoomLevel = 1; applyZoom(); });
  applyZoom();

  const $hideInactive = document.getElementById('hk-hide-inactive');
  const $hideAbg = document.getElementById('hk-hide-abgerueckt');
  function applyFilters() {
    document.body.classList.toggle('ez-hide-inactive', $hideInactive.checked);
    document.body.classList.toggle('hk-hide-abgerueckt', $hideAbg.checked);
    try { localStorage.setItem('luk_harke_hide_inactive', $hideInactive.checked ? '1' : '0'); localStorage.setItem('luk_harke_hide_abg', $hideAbg.checked ? '1' : '0'); } catch (e) {}
  }
  try {
    $hideInactive.checked = localStorage.getItem('luk_harke_hide_inactive') === '1';
    $hideAbg.checked = localStorage.getItem('luk_harke_hide_abg') === '1';
  } catch (e) {}
  $hideInactive.addEventListener('change', applyFilters);
  $hideAbg.addEventListener('change', applyFilters);
  applyFilters();

  // ---- Drucken ----
  document.getElementById('hk-print-btn').addEventListener('click', () => {
    const prevZoom = $tree.style.zoom;
    ui.print('Führungsharke', {
      beforePrint: () => { $tree.style.zoom = ''; document.body.classList.add('ez-printing'); },
      afterPrint: () => { $tree.style.zoom = prevZoom; document.body.classList.remove('ez-printing'); },
    });
  });

  // ---- Init ----
  if (!canHarke) document.body.classList.add('hk-readonly');
  if (!canHarke && !canKraefte) ui.showReadonlyBanner();
  // Staerke/Ampel haengen von der Uhrzeit ab -> minuetlich nachzeichnen;
  // Kraeftekatalog (STEIN-Status, Bindungen) dabei mit auffrischen.
  setInterval(() => { if (!document.hidden) { render(); if (K) K.load().then(renderKatalog); } }, 60000);
  load();
})();
