// log.js - Aenderungslog (wer hat wann was geaendert) + Zugriffslog.
(function () {
  'use strict';
  const L = window.LUK;
  const CTX = L.CTX;
  const ui = L.ui;
  const api = L.api;
  if (!CTX) return;

  const ENTITY_LABEL = {
    einsatz: 'Einsatz', member: 'Mitglieder/Rollen', org_member: 'Freigabe an Organisation', node: 'Harke: Knoten', mitglied: 'Harke: Mitglied', schicht: 'Kräfteplanung',
    lagekarte: 'Lagekarte', abschnitt: 'Lagekarte: Abschnitt', feature: 'Lagekarte: Objekt', schadenskonto: 'Schadenskonto',
    sk_ref: 'Schadenskonto: Einheit', sk_fremd: 'Schadenskonto: Fremdeinheit', share_link: 'Freigabelink', einsatz_link: 'Verknüpfung', snapshot: 'Snapshot',
  };
  const ACTION_LABEL = {
    insert: 'angelegt', update: 'geändert', delete: 'gelöscht', move: 'verschoben', status: 'Status', reorder: 'sortiert',
    close: 'abgeschlossen', reopen: 'wieder geöffnet', import: 'Import', export: 'Export',
  };
  const SKIP_DIFF_KEYS = new Set(['version', 'updated_at', 'created_at', 'deleted_at', 'sort_order', 'id', 'node_id', 'mitglied_id', 'lagekarte_id', 'schadenskonto_id', 'parent_id']);

  let events = [];
  let access = [];
  let tab = 'changes';

  const $changes = document.getElementById('lg-changes');
  const $access = document.getElementById('lg-access');
  const $filter = document.getElementById('lg-filter');
  const $entity = document.getElementById('lg-entity');
  const $details = document.getElementById('lg-details');

  async function load() {
    try {
      const data = await api.get('einsatz', 'log', { einsatz: CTX.uuid });
      events = data.events || [];
      access = data.access || [];
      document.getElementById('lg-count-changes').textContent = '(' + events.length + ')';
      document.getElementById('lg-count-access').textContent = '(' + access.length + ')';
      render();
    } catch (e) { ui.toast('Laden fehlgeschlagen: ' + ui.errorText(e), 'error'); }
  }

  function matches(text) {
    const f = $filter.value.trim().toLowerCase();
    return !f || text.toLowerCase().indexOf(f) !== -1;
  }

  function diffHtml(before, after) {
    if (before === null && after === null) return '';
    if (!before || typeof before !== 'object' || Array.isArray(before) || !after || typeof after !== 'object' || Array.isArray(after)) {
      const parts = [];
      if (before !== null && before !== undefined) parts.push('<b>vorher:</b> ' + ui.esc(JSON.stringify(before, null, 1)));
      if (after !== null && after !== undefined) parts.push('<b>nachher:</b> ' + ui.esc(JSON.stringify(after, null, 1)));
      return parts.join('\n');
    }
    const keys = new Set([...Object.keys(before), ...Object.keys(after)]);
    const lines = [];
    keys.forEach(k => {
      if (SKIP_DIFF_KEYS.has(k)) return;
      const b = before[k], a = after[k];
      const bs = b === undefined ? '' : (typeof b === 'object' ? JSON.stringify(b) : String(b));
      const as = a === undefined ? '' : (typeof a === 'object' ? JSON.stringify(a) : String(a));
      if (bs === as) return;
      lines.push('<b>' + ui.esc(k) + ':</b> ' + ui.esc(bs || '∅') + ' → ' + ui.esc(as || '∅'));
    });
    return lines.join('\n');
  }

  function render() {
    renderChanges();
    renderAccess();
  }

  function renderChanges() {
    $changes.innerHTML = '';
    const ent = $entity.value;
    const list = events.filter(ev => (!ent || ev.entity === ent)
      && matches([ev.ts, ev.user_label, ENTITY_LABEL[ev.entity] || ev.entity, ev.summary, ev.action].join(' ')));
    if (!list.length) { $changes.innerHTML = '<div class="luk-empty">Keine Einträge.</div>'; return; }
    const table = document.createElement('table');
    table.className = 'luk-table';
    table.innerHTML = '<thead><tr><th>Zeit</th><th>Wer</th><th>Bereich</th><th>Aktion</th><th>Was</th></tr></thead>';
    const tb = document.createElement('tbody');
    list.forEach(ev => {
      const tr = document.createElement('tr');
      if (ev.imported) tr.className = 'lg-imported';
      const details = $details.checked ? diffHtml(ev.before, ev.after) : '';
      tr.innerHTML = '<td class="lg-ts">' + ui.esc(ui.fmtServerTs(ev.ts)) + '</td>'
        + '<td>' + ui.esc(ev.user_label || '–') + '</td>'
        + '<td class="lg-entity">' + ui.esc(ENTITY_LABEL[ev.entity] || ev.entity) + '</td>'
        + '<td><span class="lg-badge lg-badge-' + ui.esc(ev.action) + '">' + ui.esc(ACTION_LABEL[ev.action] || ev.action) + '</span></td>'
        + '<td>' + ui.esc(ev.summary || '') + (ev.imported ? ' <span class="luk-muted">(importierte Historie)</span>' : '')
        + (details ? '<div class="lg-details">' + details + '</div>' : '') + '</td>';
      tb.appendChild(tr);
    });
    table.appendChild(tb);
    $changes.appendChild(table);
  }

  function renderAccess() {
    $access.innerHTML = '';
    const list = access.filter(a => matches([a.ts, a.user_label, a.share_label, a.page].join(' ')));
    if (!list.length) { $access.innerHTML = '<div class="luk-empty">Keine Zugriffe protokolliert.</div>'; return; }
    const table = document.createElement('table');
    table.className = 'luk-table';
    table.innerHTML = '<thead><tr><th>Zeit</th><th>Wer</th><th>Art</th><th>Seite</th><th>IP (pseudonym)</th></tr></thead>';
    const tb = document.createElement('tbody');
    list.forEach(a => {
      const tr = document.createElement('tr');
      tr.innerHTML = '<td class="lg-ts">' + ui.esc(ui.fmtServerTs(a.ts)) + '</td>'
        + '<td>' + ui.esc(a.share_link_id ? ('Freigabelink „' + (a.share_label || '#' + a.share_link_id) + '"') : (a.user_label || '–')) + '</td>'
        + '<td>' + (a.share_link_id ? '<span class="lg-badge lg-badge-status">extern</span>' : '<span class="lg-badge lg-badge-insert">Mitglied</span>') + '</td>'
        + '<td>' + ui.esc(a.page) + '</td>'
        + '<td class="luk-mono">' + ui.esc(a.ip_hash) + '…</td>';
      tb.appendChild(tr);
    });
    table.appendChild(tb);
    $access.appendChild(table);
  }

  document.querySelectorAll('.lg-tab').forEach(b => b.addEventListener('click', () => {
    tab = b.dataset.tab;
    document.querySelectorAll('.lg-tab').forEach(x => x.classList.toggle('active', x === b));
    $changes.hidden = tab !== 'changes';
    $access.hidden = tab !== 'access';
  }));
  $filter.addEventListener('input', render);
  $entity.addEventListener('change', render);
  $details.addEventListener('change', render);
  document.getElementById('lg-refresh').addEventListener('click', load);
  document.getElementById('lg-print').addEventListener('click', () => {
    const showBoth = true;
    ui.print(tab === 'changes' ? 'Änderungslog' : 'Zugriffslog', {
      beforePrint: () => { if (showBoth) { /* nur aktiven Tab drucken */ } },
    });
  });

  // Live: neue Ereignisse anhaengen
  L.sync.onEvent(() => load());
  load();
  L.sync.start(0);
})();
