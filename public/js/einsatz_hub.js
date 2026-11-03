// einsatz_hub.js - Einsatz-Uebersicht: Liste, Anlegen (Leiter), Import.
(function () {
  'use strict';
  const L = window.LUK;
  const ui = L.ui;
  const api = L.api;
  const isLeiter = !!L.CFG.is_leiter;

  const $list = document.getElementById('ez-hub-list');
  const $shared = document.getElementById('ez-hub-shared');
  const $sharedList = document.getElementById('ez-hub-shared-list');
  let shared = [];
  const $status = document.getElementById('ez-hub-status');
  let items = [];
  let filter = 'open';

  function showStatus(msg, kind) {
    if (!$status) return;
    $status.textContent = msg || '';
    $status.className = 'ez-status' + (kind ? ' ez-status-' + kind : '');
    $status.hidden = !msg;
  }

  async function load() {
    try {
      const data = await api.get('einsatz', 'list');
      items = data.items || [];
      shared = data.shared || [];
      render();
      renderShared();
    } catch (e) {
      showStatus('Laden fehlgeschlagen: ' + ui.errorText(e), 'error');
    }
  }

  function render() {
    $list.innerHTML = '';
    const visible = items.filter(it => !filter || it.status === filter);
    if (!visible.length) {
      const empty = document.createElement('div');
      empty.className = 'ez-empty';
      empty.textContent = items.length ? 'Keine Einsätze in dieser Ansicht.'
        : (isLeiter ? 'Noch keine Einsätze. Mit „+ Neuer Einsatz" den ersten anlegen.' : 'Dir wurde noch kein Einsatz freigegeben.');
      $list.appendChild(empty);
      return;
    }
    visible.forEach(it => $list.appendChild(renderCard(it)));
  }

  // Fremde Einsaetze mit Lesezugriff ueber freigegebene Verknuepfungen
  function renderShared() {
    if (!$shared || !$sharedList) return;
    $shared.hidden = !shared.length;
    $sharedList.innerHTML = '';
    shared.forEach(it => {
      const card = document.createElement('div');
      card.className = 'ez-card ez-card-' + it.status + ' ez-card-shared';
      const head = document.createElement('div');
      head.className = 'ez-card-head';
      const title = document.createElement('a');
      title.className = 'ez-card-title';
      title.href = '/harke?einsatz=' + encodeURIComponent(it.uuid);
      title.textContent = it.title;
      head.appendChild(title);
      const badges = document.createElement('span');
      const b = document.createElement('span');
      b.className = 'ez-badge ez-badge-' + it.status;
      b.textContent = it.status === 'open' ? 'Offen' : 'Abgeschlossen';
      badges.appendChild(b);
      const ro = document.createElement('span');
      ro.className = 'ez-badge ez-badge-share';
      ro.textContent = 'nur lesen';
      badges.appendChild(ro);
      head.appendChild(badges);
      card.appendChild(head);
      const meta = document.createElement('div');
      meta.className = 'ez-card-meta';
      meta.textContent = [it.ebene_label, it.stab_name].filter(Boolean).join(' · ')
        + ' · freigegeben für: ' + it.via.map(v => v.title).join(', ');
      card.appendChild(meta);
      $sharedList.appendChild(card);
    });
  }

  function renderCard(it) {
    const card = document.createElement('div');
    card.className = 'ez-card ez-card-' + it.status;
    const head = document.createElement('div');
    head.className = 'ez-card-head';
    const title = document.createElement('a');
    title.className = 'ez-card-title';
    title.href = '/harke?einsatz=' + encodeURIComponent(it.uuid);
    title.textContent = it.title;
    head.appendChild(title);
    const badges = document.createElement('span');
    const b = document.createElement('span');
    b.className = 'ez-badge ez-badge-' + it.status;
    b.textContent = it.status === 'open' ? 'Offen' : 'Abgeschlossen';
    badges.appendChild(b);
    if (it.is_owner) {
      const o = document.createElement('span');
      o.className = 'ez-badge ez-badge-selected';
      o.style.marginLeft = '6px';
      o.textContent = 'Ersteller';
      badges.appendChild(o);
    }
    head.appendChild(badges);
    card.appendChild(head);

    const meta = document.createElement('div');
    meta.className = 'ez-card-meta';
    const parts = [it.ebene_label];
    if (it.stab_name) parts.push(it.stab_name);
    parts.push('Ersteller: ' + it.owner_label);
    parts.push(it.member_count + ' Mitglied' + (it.member_count === 1 ? '' : 'er'));
    parts.push('angelegt ' + ui.fmtServerTs(it.created_at));
    if (it.closed_at) parts.push('abgeschlossen ' + ui.fmtServerTs(it.closed_at));
    meta.textContent = parts.join(' · ');
    card.appendChild(meta);

    const roles = document.createElement('div');
    roles.className = 'ez-card-meta';
    roles.innerHTML = 'Meine Rollen: ' + ((it.roles || []).length
      ? it.roles.map(r => '<span class="luk-chip" title="' + ui.esc(ui.roleLabel(r)) + '">' + ui.esc(ui.roleShort(r)) + '</span>').join(' ')
      : '<span class="luk-muted">keine (nur Verwaltung)</span>');
    card.appendChild(roles);
    if (it.note) {
      const note = document.createElement('div');
      note.className = 'ez-card-meta';
      note.textContent = it.note;
      card.appendChild(note);
    }

    const actions = document.createElement('div');
    actions.className = 'ez-card-actions';
    const q = '?einsatz=' + encodeURIComponent(it.uuid);
    const links = [
      ['Führungsharke', '/harke' + q], ['Kräfteplanung', '/kraefteplanung' + q], ['Lagekarte', '/lagekarte' + q],
      ['Schadenskonten', '/schadenskonten' + q], ['Kräfteübersicht', '/anfahrt' + q],
    ];
    const caps = it.caps || [];
    if (it.is_owner || caps.indexOf('log') !== -1) links.push(['Log', '/log' + q]);
    if (it.is_owner) links.push(['Verwaltung', '/verwaltung' + q]);
    links.forEach(([label, href]) => {
      const a = document.createElement('a');
      a.className = 'ez-btn';
      a.href = href;
      a.textContent = label;
      actions.appendChild(a);
    });
    card.appendChild(actions);
    return card;
  }

  document.querySelectorAll('input[name="ez-status-filter"]').forEach(r => {
    r.addEventListener('change', () => { filter = r.value; render(); });
  });

  // ---- Anlegen ----
  const $dlg = document.getElementById('ez-new-dialog');
  if ($dlg) {
    document.getElementById('ez-new-btn').addEventListener('click', () => {
      document.getElementById('ez-new-title').value = '';
      document.getElementById('ez-new-stab').value = '';
      document.getElementById('ez-new-note').value = '';
      $dlg.showModal();
      setTimeout(() => document.getElementById('ez-new-title').focus(), 30);
    });
    document.getElementById('ez-new-cancel').addEventListener('click', () => $dlg.close());
    document.getElementById('ez-new-save').addEventListener('click', async () => {
      const title = document.getElementById('ez-new-title').value.trim();
      if (!title) { ui.toast('Bitte einen Titel angeben.', 'warn'); return; }
      try {
        const res = await api.post('einsatz', 'create', {
          title,
          ebene: document.getElementById('ez-new-ebene').value,
          stab_name: document.getElementById('ez-new-stab').value.trim(),
          note: document.getElementById('ez-new-note').value.trim(),
        });
        $dlg.close();
        window.location.href = '/verwaltung?einsatz=' + encodeURIComponent(res.uuid) + '&neu=1';
      } catch (e) {
        ui.toast(ui.errorText(e), 'error');
      }
    });
  }

  // ---- Import ----
  const $impBtn = document.getElementById('ez-import-btn');
  const $impFile = document.getElementById('ez-import-file');
  if ($impBtn && $impFile) {
    $impBtn.addEventListener('click', () => $impFile.click());
    $impFile.addEventListener('change', async () => {
      const file = $impFile.files && $impFile.files[0];
      $impFile.value = '';
      if (!file) return;
      if (!window.confirm('Einsatz aus „' + file.name + '" als NEUEN Einsatz importieren? Du wirst Ersteller des importierten Einsatzes.')) return;
      const fd = new FormData();
      fd.append('csrf', L.CFG.csrf || '');
      fd.append('file', file);
      showStatus('Import läuft…', 'info');
      try {
        const res = await fetch('/api/einsatz?action=import', { method: 'POST', body: fd, credentials: 'same-origin' });
        const data = await res.json().catch(() => null);
        if (!res.ok || !data || data.error) throw new L.LukError((data && data.error) || ('http_' + res.status), res.status, data || {});
        const warn = (data.warnings || []);
        showStatus('Import abgeschlossen.' + (warn.length ? ' Hinweise: ' + warn.join(' ') : ''), warn.length ? 'warn' : 'ok');
        await load();
        if (!warn.length) setTimeout(() => { window.location.href = '/verwaltung?einsatz=' + encodeURIComponent(data.uuid); }, 800);
      } catch (e) {
        showStatus('Import fehlgeschlagen: ' + ui.errorText(e), 'error');
      }
    });
  }

  load();
})();
