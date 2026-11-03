// verwaltung.js - Einsatzdaten, Mitglieder/Rollen, Freigabelinks, Snapshots, Abschluss.
(function () {
  'use strict';
  const L = window.LUK;
  const CTX = L.CTX;
  const ui = L.ui;
  const api = L.api;
  if (!CTX) return;

  let ctx = CTX;           // wird nach Meta-Aenderungen aktualisiert
  let members = [];
  let orgMembers = [];
  let orgs = [];
  let users = [];
  let shares = [];
  let incoming = [];   // Anfragen/Freigaben an die eigenen Freigabelinks
  let outgoing = [];   // eigene Verknuepfungen zu fremden Einsaetzen
  let snapshots = [];
  let rolesDlgUser = null;

  const $title = document.getElementById('vw-title');
  const $ebene = document.getElementById('vw-ebene');
  const $stab = document.getElementById('vw-stab');
  const $note = document.getElementById('vw-note');
  const $vorwarn = document.getElementById('vw-vorwarn');
  const $bereich = document.getElementById('vw-bereich');
  const $ortName = document.getElementById('vw-ort-name');
  const $ortLat = document.getElementById('vw-ort-lat');
  const $ortLng = document.getElementById('vw-ort-lng');
  const $steinAktiv = document.getElementById('vw-stein-aktiv');
  const $members = document.getElementById('vw-members');
  const $userSel = document.getElementById('vw-user-select');
  const $orgMembers = document.getElementById('vw-org-members');
  const $orgSel = document.getElementById('vw-org-select');
  const $shares = document.getElementById('vw-shares');
  const $links = document.getElementById('vw-links');
  const $shareNew = document.getElementById('vw-share-new');
  const $snapshots = document.getElementById('vw-snapshots');
  const $statusText = document.getElementById('vw-status-text');
  const $closeBtn = document.getElementById('vw-close-btn');
  const $reopenBtn = document.getElementById('vw-reopen-btn');

  const $rd = document.getElementById('vw-roles-dialog');
  const $rdTitle = document.getElementById('vw-roles-title');
  const $rdList = document.getElementById('vw-roles-list');

  async function load() {
    try {
      const data = await api.get('einsatz', 'load', { einsatz: CTX.uuid });
      ctx = data.ctx;
      members = data.members || [];
      orgMembers = data.org_members || [];
      fillMeta();
      renderMembers();
      renderOrgMembers();
      renderStatus();
    } catch (e) { ui.toast('Laden fehlgeschlagen: ' + ui.errorText(e), 'error'); }
    try {
      const u = await api.get('einsatz', 'users');
      users = u.users || [];
      fillUserSelect();
    } catch (e) { /* kein Leiter -> Dropdown bleibt leer */ fillUserSelect(); }
    try {
      const o = await api.get('einsatz', 'orgs');
      orgs = o.orgs || [];
      fillOrgSelect();
    } catch (e) { fillOrgSelect(); }
    loadBereiche();
    loadShares();
    loadLinks();
    loadSnapshots();
  }

  // Kraeftekatalog: Auswahlliste "eigener Bereich" (Regionalstellen, OVs)
  async function loadBereiche() {
    if (!$bereich) return;
    let list = [{ key: '', label: 'Gesamter Landesverband (keine Einschränkung)' }];
    try {
      const res = await api.get('kraefte', 'bereiche');
      list = res.bereiche || list;
    } catch (e) { /* Katalog nicht verfuegbar -> nur Standard */ }
    $bereich.innerHTML = '';
    const cur = ctx.bereich_key || '';
    let found = false;
    list.forEach(b => {
      const o = document.createElement('option');
      o.value = b.key;
      o.textContent = b.label;
      if (b.key === cur) found = true;
      $bereich.appendChild(o);
    });
    if (!found && cur) {
      const o = document.createElement('option'); o.value = cur; o.textContent = cur + ' (unbekannt)'; $bereich.appendChild(o);
    }
    $bereich.value = cur;
  }

  function fillMeta() {
    $title.value = ctx.title || '';
    $ebene.value = ctx.ebene || 'ov';
    $stab.value = ctx.stab_name || '';
    $note.value = ctx.note || '';
    const vm = typeof ctx.vorwarn_min === 'number' ? ctx.vorwarn_min : 180;
    $vorwarn.value = String(Math.round(vm / 30) / 2); // Minuten -> Stunden in 0,5er-Schritten
    if ($ortName) $ortName.value = ctx.ort_name || '';
    if ($ortLat) $ortLat.value = ctx.ort_lat != null ? String(ctx.ort_lat) : '';
    if ($ortLng) $ortLng.value = ctx.ort_lng != null ? String(ctx.ort_lng) : '';
    if ($steinAktiv) $steinAktiv.checked = ctx.stein_aktiv !== false;
    if ($bereich && $bereich.options.length) $bereich.value = ctx.bereich_key || '';
  }

  function renderStatus() {
    $statusText.textContent = ctx.is_open
      ? 'Offen seit ' + ui.fmtServerTs(ctx.created_at) + '.'
      : 'Abgeschlossen am ' + ui.fmtServerTs(ctx.closed_at) + ' – alle Seiten sind schreibgeschützt.';
    $closeBtn.hidden = !ctx.is_open;
    $reopenBtn.hidden = ctx.is_open;
  }

  // ---- Meta ----
  document.getElementById('vw-meta-save').addEventListener('click', async () => {
    const title = $title.value.trim();
    if (!title) { ui.toast('Bitte einen Titel angeben.', 'warn'); return; }
    const vorwarnH = parseFloat(String($vorwarn.value || '').replace(',', '.'));
    if (isNaN(vorwarnH) || vorwarnH < 0 || vorwarnH > 168) { ui.toast('Vorwarnzeit bitte zwischen 0 und 168 Stunden angeben.', 'warn'); return; }
    try {
      const res = await api.post('einsatz', 'update_meta', {
        version: ctx.version, title, ebene: $ebene.value, stab_name: $stab.value.trim(), note: $note.value.trim(),
        vorwarn_min: Math.round(vorwarnH * 60),
      });
      if (res.ctx) { ctx = res.ctx; }
      ui.toast('Gespeichert.', 'ok');
      setTimeout(() => window.location.reload(), 600);
    } catch (e) {
      if (e.code === 'conflict') { ui.toast('Die Einsatzdaten wurden zwischenzeitlich geändert. Seite wird neu geladen.', 'warn'); setTimeout(() => window.location.reload(), 1200); return; }
      ui.toast(ui.errorText(e), 'error');
    }
  });

  // ---- Kraeftekatalog (eigene Kachel): Bereich, Einsatzort, STEIN ----
  function readOrt() {
    const latRaw = $ortLat ? $ortLat.value.trim().replace(',', '.') : '';
    const lngRaw = $ortLng ? $ortLng.value.trim().replace(',', '.') : '';
    if (latRaw === '' && lngRaw === '') return { lat: null, lng: null };
    const lat = parseFloat(latRaw), lng = parseFloat(lngRaw);
    if (isNaN(lat) || isNaN(lng) || lat < -90 || lat > 90 || lng < -180 || lng > 180) return null;
    return { lat, lng };
  }
  function setOrt(lat, lng) {
    if ($ortLat) $ortLat.value = lat === null ? '' : String(Math.round(lat * 1e6) / 1e6);
    if ($ortLng) $ortLng.value = lng === null ? '' : String(Math.round(lng * 1e6) / 1e6);
  }
  const $katSave = document.getElementById('vw-katalog-save');
  if ($katSave) $katSave.addEventListener('click', async () => {
    const ort = readOrt();
    if (ort === null) {
      ui.toast('Einsatzort: bitte Breite und Länge als Dezimalzahlen angeben (z.B. 52.52 und 13.40), beide leer lassen oder die Adresssuche nutzen.', 'warn', 5000);
      return;
    }
    try {
      const res = await api.post('einsatz', 'update_meta', {
        version: ctx.version,
        bereich_key: $bereich ? $bereich.value : '',
        ort_name: $ortName ? $ortName.value.trim() : '',
        ort_lat: ort.lat, ort_lng: ort.lng,
        stein_aktiv: $steinAktiv ? $steinAktiv.checked : true,
      });
      if (res.ctx) { ctx = res.ctx; fillMeta(); }
      ui.toast('Kräftekatalog-Einstellungen gespeichert.', 'ok');
    } catch (e) {
      if (e.code === 'no_fields') { ui.toast('Keine Änderungen.', 'info'); return; }
      if (e.code === 'conflict') { ui.toast('Die Einsatzdaten wurden zwischenzeitlich geändert. Seite wird neu geladen.', 'warn'); setTimeout(() => window.location.reload(), 1200); return; }
      ui.toast(ui.errorText(e), 'error');
    }
  });
  const $ortClear = document.getElementById('vw-ort-clear');
  if ($ortClear) $ortClear.addEventListener('click', () => { setOrt(null, null); });

  // Adresssuche (Nominatim, wie in STEINmap), hoechstens alle 2 s
  const $such = document.getElementById('vw-ort-such');
  const $suchBtn = document.getElementById('vw-ort-such-btn');
  const $treffer = document.getElementById('vw-ort-treffer');
  let lastGeo = 0;
  async function sucheAdresse() {
    const q = ($such.value || '').trim();
    if (!q) { $such.focus(); return; }
    if (Date.now() - lastGeo < 2000) { ui.toast('Bitte kurz warten, dann erneut suchen.', 'warn'); return; }
    lastGeo = Date.now();
    $treffer.hidden = false;
    $treffer.innerHTML = '<div class="luk-muted">Suche…</div>';
    try {
      // Ueber den Server (Nominatim direkt laesst die CSP nicht zu; der Server bremst und cacht)
      const url = '/api/proxy?type=geocode&limit=6&countrycodes=de&q=' + encodeURIComponent(q);
      const res = await fetch(url, { headers: { 'Accept': 'application/json' } });
      if (res.status === 429) { $treffer.innerHTML = '<div class="luk-muted">Adresssuche gerade ausgelastet – bitte kurz warten und erneut suchen.</div>'; return; }
      const list = await res.json();
      $treffer.innerHTML = '';
      if (!Array.isArray(list) || !list.length) { $treffer.innerHTML = '<div class="luk-muted">Keine Treffer. Bitte genauer eingeben (Ort, Straße) oder den Punkt auf der Karte setzen.</div>'; return; }
      list.forEach(r => {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = 'vw-geo-hit';
        b.textContent = r.display_name;
        b.addEventListener('click', () => {
          setOrt(parseFloat(r.lat), parseFloat(r.lon));
          if ($ortName && !$ortName.value.trim()) $ortName.value = String(r.display_name).split(',').slice(0, 2).join(',').trim().slice(0, 160);
          $treffer.hidden = true;
          ui.toast('Koordinaten übernommen. Mit „Speichern“ in der Kachel sichern.', 'ok', 3000);
        });
        $treffer.appendChild(b);
      });
    } catch (e) {
      $treffer.innerHTML = '<div class="luk-muted">Adresssuche nicht erreichbar (der Server braucht Internetzugang zu nominatim.openstreetmap.org). Alternativ den Punkt auf der Karte setzen.</div>';
    }
  }
  if ($suchBtn) $suchBtn.addEventListener('click', sucheAdresse);
  if ($such) $such.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); sucheAdresse(); } });

  // Karten-Overlay: Punkt setzen / verschieben
  const $mapDlg = document.getElementById('vw-map-dialog');
  const $mapCoords = document.getElementById('vw-map-coords');
  const Leaflet = window.L;   // L ist in dieser Datei die LuK-Bibliothek
  let map = null, marker = null, pick = null;
  function showPick(lat, lng) {
    pick = { lat, lng };
    if (!marker) {
      marker = Leaflet.marker([lat, lng], { draggable: true }).addTo(map);
      marker.on('dragend', () => { const p = marker.getLatLng(); showPick(p.lat, p.lng); });
    } else marker.setLatLng([lat, lng]);
    $mapCoords.textContent = 'Breite ' + lat.toFixed(6) + ', Länge ' + lng.toFixed(6);
  }
  function openMap() {
    if (!$mapDlg || !Leaflet) return;
    $mapDlg.showModal();
    const cur = readOrt();
    const mc = (L_CFG && L_CFG.map_center) || {};
    const start = cur && cur.lat !== null ? cur : { lat: mc.lat || 52.52, lng: mc.lng || 13.405 };
    if (!map) {
      map = Leaflet.map('vw-map', { zoomControl: true }).setView([start.lat, start.lng], cur && cur.lat !== null ? 13 : 9);
      Leaflet.tileLayer(((window.CURRENT_OV && window.CURRENT_OV.tile_url) || '/tiles/{z}/{x}/{y}.png'), { attribution: '&copy; OpenStreetMap-Mitwirkende', maxZoom: 19 }).addTo(map);
      Leaflet.control.scale({ position: 'bottomright', imperial: false }).addTo(map);
      map.on('click', e => showPick(e.latlng.lat, e.latlng.lng));
    } else {
      map.setView([start.lat, start.lng], cur && cur.lat !== null ? 13 : 9);
    }
    pick = null;
    $mapCoords.textContent = 'Noch kein Punkt gesetzt.';
    if (cur && cur.lat !== null) showPick(cur.lat, cur.lng);
    else if (marker) { map.removeLayer(marker); marker = null; }
    setTimeout(() => map.invalidateSize(), 60);
  }
  const L_CFG = L.CFG;
  const $mapBtn = document.getElementById('vw-ort-karte-btn');
  if ($mapBtn) $mapBtn.addEventListener('click', openMap);
  const $mapCancel = document.getElementById('vw-map-cancel');
  if ($mapCancel) $mapCancel.addEventListener('click', () => $mapDlg.close());
  const $mapOk = document.getElementById('vw-map-ok');
  if ($mapOk) $mapOk.addEventListener('click', () => {
    if (!pick) { ui.toast('Bitte zuerst einen Punkt in die Karte setzen.', 'warn'); return; }
    setOrt(pick.lat, pick.lng);
    $mapDlg.close();
    ui.toast('Koordinaten übernommen. Mit „Speichern“ in der Kachel sichern.', 'ok', 3000);
  });

  // ---- Status ----
  $closeBtn.addEventListener('click', async () => {
    if (!window.confirm('Einsatz abschließen? Danach ist alles schreibgeschützt, ein Snapshot wird abgelegt. Der Einsatz kann später wieder geöffnet werden.')) return;
    try { await api.post('einsatz', 'close', {}); ui.toast('Einsatz abgeschlossen.', 'ok'); setTimeout(() => window.location.reload(), 600); }
    catch (e) { ui.toast(ui.errorText(e), 'error'); }
  });
  $reopenBtn.addEventListener('click', async () => {
    if (!window.confirm('Einsatz wieder öffnen?')) return;
    try { await api.post('einsatz', 'reopen', {}); ui.toast('Einsatz wieder geöffnet.', 'ok'); setTimeout(() => window.location.reload(), 600); }
    catch (e) { ui.toast(ui.errorText(e), 'error'); }
  });
  document.getElementById('vw-delete-btn').addEventListener('click', async () => {
    const typed = window.prompt('Einsatz wirklich löschen? Zur Bestätigung bitte den Titel eingeben:\n' + ctx.title);
    if (typed === null) return;
    if (typed.trim() !== ctx.title) { ui.toast('Titel stimmt nicht überein, nicht gelöscht.', 'warn'); return; }
    try { await api.post('einsatz', 'delete', {}); window.location.href = '/einsatz'; }
    catch (e) { ui.toast(ui.errorText(e), 'error'); }
  });

  // ---- Mitglieder ----
  function fillUserSelect() {
    $userSel.innerHTML = '';
    const have = new Set(members.map(m => m.user_id));
    const opt0 = document.createElement('option');
    opt0.value = '';
    opt0.textContent = users.length ? '– Nutzer wählen –' : '(Nutzerliste nur für Leiter-Konten)';
    $userSel.appendChild(opt0);
    users.filter(u => !have.has(u.id)).forEach(u => {
      const o = document.createElement('option');
      o.value = String(u.id);
      o.textContent = u.label + ' (' + u.username + (u.role === 'leiter' ? ', Leiter' : '') + ')';
      $userSel.appendChild(o);
    });
  }
  function renderMembers() {
    $members.innerHTML = '';
    if (!members.length) {
      const e = document.createElement('div');
      e.className = 'luk-empty';
      e.textContent = 'Noch keine Mitglieder.';
      $members.appendChild(e);
      return;
    }
    members.forEach(m => {
      const row = document.createElement('div');
      row.className = 'vw-member' + (m.user_id === ctx.owner_id ? ' vw-member-owner' : '');
      const main = document.createElement('div');
      main.className = 'vw-member-main';
      const name = document.createElement('div');
      name.className = 'vw-member-name';
      name.textContent = m.label + (m.user_id === ctx.owner_id ? ' (Ersteller)' : '') + (m.account_role === 'leiter' ? ' · Leiter-Konto' : '');
      main.appendChild(name);
      const roles = document.createElement('div');
      roles.className = 'vw-member-roles';
      roles.innerHTML = (m.roles || []).length
        ? m.roles.map(r => '<span class="luk-chip" title="' + ui.esc(ui.roleLabel(r)) + '">' + ui.esc(ui.roleShort(r)) + '</span>').join(' ')
        : '<span class="luk-muted">keine Rolle (nur Verwaltung, kein Zugriff auf Inhalte)</span>';
      main.appendChild(roles);
      row.appendChild(main);
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'luk-btn luk-btn-small';
      b.textContent = 'Rollen…';
      b.addEventListener('click', () => openRolesDialog(m));
      row.appendChild(b);
      $members.appendChild(row);
    });
    fillUserSelect();
  }
  document.getElementById('vw-user-add').addEventListener('click', () => {
    const id = parseInt($userSel.value, 10);
    if (!id) return;
    const u = users.find(x => x.id === id);
    if (!u) return;
    openRolesDialog({ user_id: id, label: u.label, roles: [] });
  });

  // ---- Freigabe an Organisationen ----
  function fillOrgSelect() {
    if (!$orgSel) return;
    $orgSel.innerHTML = '';
    const have = new Set(orgMembers.map(m => m.org_id));
    const opt0 = document.createElement('option');
    opt0.value = '';
    opt0.textContent = orgs.length ? '– Organisation wählen –' : '(Organisationsliste nur für Leiter-Konten)';
    $orgSel.appendChild(opt0);
    orgs.filter(o => !have.has(o.id)).forEach(o => {
      const opt = document.createElement('option');
      opt.value = String(o.id);
      opt.textContent = o.name + ' (' + o.ebene_label + ')';
      $orgSel.appendChild(opt);
    });
  }
  function renderOrgMembers() {
    if (!$orgMembers) return;
    $orgMembers.innerHTML = '';
    if (!orgMembers.length) {
      const e = document.createElement('div');
      e.className = 'luk-empty';
      e.textContent = 'Keine Organisation freigegeben.';
      $orgMembers.appendChild(e);
      fillOrgSelect();
      return;
    }
    orgMembers.forEach(m => {
      const row = document.createElement('div');
      row.className = 'vw-member vw-member-org';
      const main = document.createElement('div');
      main.className = 'vw-member-main';
      const name = document.createElement('div');
      name.className = 'vw-member-name';
      name.textContent = m.name + ' · Organisation';
      main.appendChild(name);
      const roles = document.createElement('div');
      roles.className = 'vw-member-roles';
      roles.innerHTML = (m.roles || []).length
        ? m.roles.map(r => '<span class="luk-chip" title="' + ui.esc(ui.roleLabel(r)) + '">' + ui.esc(ui.roleShort(r)) + '</span>').join(' ')
        : '<span class="luk-muted">keine Rolle</span>';
      main.appendChild(roles);
      row.appendChild(main);
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'luk-btn luk-btn-small';
      b.textContent = 'Rollen…';
      b.addEventListener('click', () => openRolesDialog({ org_id: m.org_id, label: 'Organisation ' + m.name, roles: m.roles || [] }));
      row.appendChild(b);
      $orgMembers.appendChild(row);
    });
    fillOrgSelect();
  }
  const $orgAdd = document.getElementById('vw-org-add');
  if ($orgAdd) $orgAdd.addEventListener('click', () => {
    const id = parseInt($orgSel.value, 10);
    if (!id) return;
    const o = orgs.find(x => x.id === id);
    if (!o) return;
    openRolesDialog({ org_id: id, label: 'Organisation ' + o.name, roles: [] });
  });

  function openRolesDialog(m) {
    rolesDlgUser = m;
    $rdTitle.textContent = 'Rollen für ' + m.label;
    $rdList.innerHTML = '';
    Object.keys(L.CFG.roles || {}).forEach(k => {
      const r = L.CFG.roles[k];
      const lab = document.createElement('label');
      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.value = k;
      cb.checked = (m.roles || []).indexOf(k) !== -1;
      lab.appendChild(cb);
      const span = document.createElement('span');
      span.innerHTML = ui.esc(r.label) + '<span class="vw-role-caps">' + (r.caps.length ? 'schreibt: ' + r.caps.map(capLabel).join(', ') : 'nur lesen') + '</span>';
      lab.appendChild(span);
      $rdList.appendChild(lab);
    });
    document.getElementById('vw-roles-remove').hidden = !(m.roles || []).length;
    $rd.showModal();
  }
  function capLabel(c) {
    return { harke: 'Harke', kraefte: 'Kräfte', lagekarte: 'Lagekarte', schadenskonten: 'Schadenskonten', log: 'Log' }[c] || c;
  }
  document.getElementById('vw-roles-cancel').addEventListener('click', () => $rd.close());
  document.getElementById('vw-roles-save').addEventListener('click', async () => {
    const roles = Array.from($rdList.querySelectorAll('input:checked')).map(cb => cb.value);
    if (!roles.length && !window.confirm(rolesDlgUser.org_id
      ? 'Keine Rolle gewählt – die Freigabe für die Organisation wird entfernt. Fortfahren?'
      : 'Keine Rolle gewählt – der Nutzer wird aus dem Einsatz entfernt. Fortfahren?')) return;
    await saveRoles(roles);
  });
  document.getElementById('vw-roles-remove').addEventListener('click', async () => {
    if (!window.confirm(rolesDlgUser.label + ' aus dem Einsatz entfernen?')) return;
    await saveRoles([]);
  });
  async function saveRoles(roles) {
    try {
      if (rolesDlgUser.org_id) {
        const res = await api.post('einsatz', 'org_member_set', { org_id: rolesDlgUser.org_id, roles });
        orgMembers = res.org_members || orgMembers;
        $rd.close();
        renderOrgMembers();
      } else {
        const res = await api.post('einsatz', 'member_set', { user_id: rolesDlgUser.user_id, roles });
        members = res.members || members;
        $rd.close();
        renderMembers();
      }
      ui.toast('Rollen gespeichert.', 'ok');
    } catch (e) { ui.toast(ui.errorText(e), 'error'); }
  }

  // ---- Freigabelinks ----
  async function loadShares() {
    try {
      const res = await api.get('einsatz', 'share_list', { einsatz: CTX.uuid });
      shares = res.links || [];
      incoming = res.incoming || [];
      renderShares();
    } catch (e) { /* still */ }
  }
  function renderShares() {
    $shares.innerHTML = '';
    if (!shares.length) {
      const e = document.createElement('div');
      e.className = 'luk-empty';
      e.textContent = 'Keine Freigabelinks. Standardmäßig ist der Einsatz nicht für andere Stäbe sichtbar.';
      $shares.appendChild(e);
      return;
    }
    shares.forEach(s => {
      const row = document.createElement('div');
      row.className = 'vw-share' + (s.active ? '' : ' vw-share-revoked');
      const main = document.createElement('div');
      main.className = 'vw-share-main';
      main.innerHTML = '<strong>' + ui.esc(s.label) + '</strong> '
        + (s.active ? '<span class="luk-chip" style="background:#d1e7dd;color:#0f5132">aktiv</span>' : '<span class="luk-chip" style="background:#fde2e1;color:#842029">' + (s.revoked_at ? 'widerrufen' : 'abgelaufen') + '</span>')
        + '<div class="luk-muted">erstellt ' + ui.esc(ui.fmtServerTs(s.created_at)) + ' von ' + ui.esc(s.created_by || '?')
        + (s.expires_at ? ' · gültig bis ' + ui.esc(ui.fmtServerTs(s.expires_at)) : '')
        + ' · ' + s.use_count + ' Zugriff' + (s.use_count === 1 ? '' : 'e')
        + (s.last_used_at ? ', zuletzt ' + ui.esc(ui.fmtServerTs(s.last_used_at)) : '') + '</div>'
        + '<div class="luk-muted" style="font-size:0.8em">Der Link selbst wird aus Sicherheitsgründen nur einmal beim Erstellen angezeigt.</div>';
      main.appendChild(renderIncoming(s));
      row.appendChild(main);
      if (s.active) {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = 'luk-btn luk-btn-small luk-btn-danger';
        b.textContent = 'Widerrufen';
        b.addEventListener('click', async () => {
          if (!window.confirm('Freigabelink „' + s.label + '" widerrufen? Zugriffe damit sind sofort nicht mehr möglich.')) return;
          try { const res = await api.post('einsatz', 'share_revoke', { id: s.id }); shares = res.links || []; renderShares(); ui.toast('Widerrufen.', 'ok'); }
          catch (e) { ui.toast(ui.errorText(e), 'error'); }
        });
        row.appendChild(b);
      }
      $shares.appendChild(row);
    });
  }
  // Anfragen/Freigaben anderer Einsaetze an diesen Freigabelink
  function renderIncoming(s) {
    const box = document.createElement('div');
    box.className = 'vw-link-incoming';
    const list = incoming.filter(k => k.share_link_id === s.id);
    if (!list.length) {
      box.innerHTML = '<div class="luk-muted" style="font-size:0.85em">Noch kein anderer Einsatz hat diesen Link eingetragen.</div>';
      return box;
    }
    list.forEach(k => {
      const row = document.createElement('div');
      row.className = 'vw-link-row vw-link-' + k.status;
      const b = k.einsatz_b;
      row.innerHTML = '<span>' + linkStatusChip(k) + ' <strong>' + ui.esc(b.title) + '</strong>'
        + (b.stab_name ? ' <span class="luk-muted">(' + ui.esc(b.stab_name) + ')</span>' : '')
        + (k.label ? ' · ' + ui.esc(k.label) : '')
        + '<div class="luk-muted" style="font-size:0.85em">angefragt ' + ui.esc(ui.fmtServerTs(k.requested_at)) + ' von ' + ui.esc(k.requested_by || '?')
        + (k.decided_at ? ' · entschieden ' + ui.esc(ui.fmtServerTs(k.decided_at)) + ' von ' + ui.esc(k.decided_by || '?') : '') + '</div></span>';
      if (s.active) {
        const acts = document.createElement('span');
        acts.className = 'vw-link-actions';
        if (k.status === 'approved') acts.appendChild(decideBtn(k, false, 'Zugriff entziehen', 'luk-btn-danger'));
        else acts.appendChild(decideBtn(k, true, 'Wieder freigeben', 'luk-btn-primary'));
        row.appendChild(acts);
      }
      box.appendChild(row);
    });
    return box;
  }
  function decideBtn(k, approve, label, cls) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'luk-btn luk-btn-small ' + cls;
    b.textContent = label;
    b.addEventListener('click', async () => {
      if (!approve && k.status === 'approved' && !window.confirm('Lesezugriff für „' + k.einsatz_b.title + '" entziehen?')) return;
      try {
        const res = await api.post('einsatz', 'link_decide', { id: k.id, approve });
        incoming = res.incoming || [];
        renderShares();
        ui.toast(approve ? 'Zugriff wieder freigegeben.' : 'Zugriff entzogen.', 'ok');
      } catch (e) { ui.toast(ui.errorText(e), 'error'); }
    });
    return b;
  }
  function linkStatusChip(k) {
    if (k.status === 'approved' && k.effective) return '<span class="luk-chip" style="background:#d1e7dd;color:#0f5132">aktiv</span>';
    if (k.status === 'approved') return '<span class="luk-chip" style="background:#fde2e1;color:#842029">Link ungültig</span>';
    if (k.status === 'rejected') return '<span class="luk-chip" style="background:#fde2e1;color:#842029">Zugriff entzogen</span>';
    return '<span class="luk-chip" style="background:#fff3cd;color:#664d03">offen</span>';
  }

  // ---- Verknuepfte Einsaetze (eigene Anfragen an fremde Freigabelinks) ----
  async function loadLinks() {
    try {
      const res = await api.get('einsatz', 'link_list', { einsatz: CTX.uuid });
      outgoing = res.outgoing || [];
      renderLinks();
    } catch (e) { /* still */ }
  }
  function renderLinks() {
    $links.innerHTML = '';
    if (!outgoing.length) {
      const e = document.createElement('div');
      e.className = 'luk-empty';
      e.textContent = 'Keine verknüpften Einsätze.';
      $links.appendChild(e);
      return;
    }
    outgoing.forEach(k => {
      const row = document.createElement('div');
      row.className = 'vw-share vw-link-row vw-link-' + k.status;
      const a = k.einsatz_a;
      const main = document.createElement('div');
      main.className = 'vw-share-main';
      main.innerHTML = linkStatusChip(k) + ' '
        + (k.effective ? '<a href="/harke?einsatz=' + encodeURIComponent(a.uuid) + '"><strong>' + ui.esc(a.title) + '</strong></a>' : '<strong>' + ui.esc(a.title) + '</strong>')
        + ' <span class="luk-muted">' + ui.esc(a.ebene_label) + (a.stab_name ? ' · ' + ui.esc(a.stab_name) : '') + '</span>'
        + (k.label ? ' · ' + ui.esc(k.label) : '')
        + '<div class="luk-muted">eingetragen ' + ui.esc(ui.fmtServerTs(k.requested_at)) + ' von ' + ui.esc(k.requested_by || '?')
        + ' · Freigabelink „' + ui.esc(k.share_label) + '"'
        + (k.decided_at ? ' · ' + (k.status === 'approved' ? 'freigegeben' : 'abgelehnt') + ' ' + ui.esc(ui.fmtServerTs(k.decided_at)) : '') + '</div>';
      row.appendChild(main);
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'luk-btn luk-btn-small luk-btn-danger';
      b.textContent = 'Entfernen';
      b.addEventListener('click', async () => {
        if (!window.confirm('Verknüpfung mit „' + a.title + '" entfernen?')) return;
        try { const res = await api.post('einsatz', 'link_remove', { id: k.id }); outgoing = res.outgoing || []; renderLinks(); ui.toast('Entfernt.', 'ok'); }
        catch (e) { ui.toast(ui.errorText(e), 'error'); }
      });
      row.appendChild(b);
      $links.appendChild(row);
    });
  }
  document.getElementById('vw-link-add').addEventListener('click', async () => {
    const $tok = document.getElementById('vw-link-token');
    const $lab = document.getElementById('vw-link-label');
    const token = $tok.value.trim();
    if (!token) { ui.toast('Bitte den Freigabelink einfügen.', 'warn'); return; }
    try {
      const res = await api.post('einsatz', 'link_request', { token, label: $lab.value.trim() });
      outgoing = res.outgoing || outgoing;
      renderLinks();
      $tok.value = '';
      $lab.value = '';
      ui.toast('Verknüpft. Alle Mitglieder dieses Einsatzes können den anderen Einsatz jetzt lesend öffnen.', 'ok', 6000);
    } catch (e) { ui.toast(ui.errorText(e), 'error'); }
  });

  document.getElementById('vw-share-create').addEventListener('click', async () => {
    const label = document.getElementById('vw-share-label').value.trim();
    const exp = document.getElementById('vw-share-expires').value;
    try {
      const res = await api.post('einsatz', 'share_create', { label, expires_at: exp || '' });
      shares = res.links || shares;
      renderShares();
      const url = res.link.url;
      $shareNew.hidden = false;
      $shareNew.innerHTML = 'Neuer Freigabelink (jetzt kopieren, er wird nicht erneut angezeigt):<div class="vw-share-url">' + ui.esc(url) + '</div>';
      const copy = document.createElement('button');
      copy.type = 'button';
      copy.className = 'luk-btn luk-btn-small';
      copy.textContent = 'In Zwischenablage kopieren';
      copy.addEventListener('click', () => {
        if (navigator.clipboard) navigator.clipboard.writeText(url).then(() => ui.toast('Kopiert.', 'ok'), () => window.prompt('Link:', url));
        else window.prompt('Link:', url);
      });
      $shareNew.appendChild(copy);
      document.getElementById('vw-share-label').value = '';
    } catch (e) { ui.toast(ui.errorText(e), 'error'); }
  });

  // ---- Snapshots ----
  async function loadSnapshots() {
    try {
      const res = await api.get('einsatz', 'snapshots', { einsatz: CTX.uuid });
      snapshots = res.snapshots || [];
      renderSnapshots();
    } catch (e) { /* still */ }
  }
  function renderSnapshots() {
    $snapshots.innerHTML = '';
    if (!snapshots.length) { $snapshots.innerHTML = '<div class="luk-muted">Noch keine Snapshots.</div>'; return; }
    snapshots.forEach(s => {
      const row = document.createElement('div');
      row.className = 'vw-snapshot';
      row.innerHTML = '<span>' + ui.esc(ui.fmtServerTs(s.created_at)) + ' · ' + ui.esc(s.kind === 'close' ? 'Abschluss' : s.kind === 'manual' ? 'manuell' : s.kind)
        + ' · ' + ui.esc(s.created_by || '?') + ' · ' + Math.round(s.size / 1024) + ' KB</span>';
      const a = document.createElement('a');
      a.className = 'luk-btn luk-btn-small';
      a.href = '/api/einsatz?action=snapshot_download&einsatz=' + encodeURIComponent(CTX.uuid) + '&id=' + s.id;
      a.textContent = 'Download';
      row.appendChild(a);
      $snapshots.appendChild(row);
    });
  }
  document.getElementById('vw-snapshot-btn').addEventListener('click', async () => {
    try { const res = await api.post('einsatz', 'snapshot_create', {}); snapshots = res.snapshots || snapshots; renderSnapshots(); ui.toast('Snapshot erstellt.', 'ok'); }
    catch (e) { ui.toast(ui.errorText(e), 'error'); }
  });

  load();
})();
