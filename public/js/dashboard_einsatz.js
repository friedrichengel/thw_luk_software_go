// dashboard_einsatz.js - Box 1 im Dashboard: Einsatzliste bzw. (im
// Einsatz-Kontext) die Anfahrtsuebersicht des gewaehlten Einsatzes.
(function () {
  'use strict';
  const L = window.LUK;
  if (!L) return;
  const ui = L.ui;
  const api = L.api;
  const CTX = L.CTX;
  const $main = document.getElementById('db-einsatz-main');
  const $list = document.getElementById('db-einsatz-list');
  const isEinsatzUser = !!L.CFG.is_einsatz_user;

  function statusChip(st) {
    return '<span class="luk-status luk-status-' + ui.esc(st) + '">' + ui.esc(ui.statusLabel(st)) + '</span>';
  }

  async function renderList(target) {
    if (!target) return;
    if (!isEinsatzUser) {
      target.innerHTML = '<div class="db-empty">Admin-Konten haben keinen Zugriff auf Einsätze.</div>';
      return;
    }
    try {
      const data = await api.get('einsatz', 'list');
      const items = (data.items || []);
      if (!items.length) {
        target.innerHTML = '<div class="db-empty">Keine Einsätze freigegeben. <a href="/einsatz">Zur Einsatzübersicht</a></div>';
        return;
      }
      target.innerHTML = '';
      items.slice(0, 12).forEach(it => {
        const q = '?einsatz=' + encodeURIComponent(it.uuid);
        const div = document.createElement('div');
        div.className = 'db-einsatz-item' + (it.status === 'open' ? '' : ' db-einsatz-item-closed');
        div.innerHTML = '<a class="db-einsatz-title" href="/harke' + q + '">' + ui.esc(it.title) + '</a>'
          + '<div class="db-einsatz-meta">' + ui.esc(it.ebene_label + (it.stab_name ? ' · ' + it.stab_name : '') + ' · ' + (it.status === 'open' ? 'offen' : 'abgeschlossen'))
          + (it.roles && it.roles.length ? ' · ' + ui.esc(it.roles.map(ui.roleShort).join(', ')) : '') + '</div>'
          + '<div class="db-einsatz-links"><a href="/harke' + q + '">Harke</a><a href="/kraefteplanung' + q + '">Kräfte</a>'
          + '<a href="/lagekarte' + q + '">Lagekarte</a><a href="/schadenskonten' + q + '">Schadenskonten</a><a href="/anfahrt' + q + '">Kräfteübersicht</a>'
          + '<a href="/?einsatz=' + encodeURIComponent(it.uuid) + '">Dashboard-Kontext</a></div>';
        target.appendChild(div);
      });
      if (items.length > 12) target.insertAdjacentHTML('beforeend', '<div class="db-empty"><a href="/einsatz">Alle ' + items.length + ' Einsätze…</a></div>');
    } catch (e) {
      target.innerHTML = '<div class="db-empty">Einsätze konnten nicht geladen werden (' + ui.esc(ui.errorText(e)) + ').</div>';
    }
  }

  // Kraeftekatalog: Kacheln fuer den eigenen Bereich (einsatzbereit / gebunden / ohne Live-Daten)
  const K = L.kraefte;
  function katalogTiles() {
    if (!K || !K.data) return '';
    const s = K.summary(Date.now(), 0);
    const stein = K.data.stein || {};
    const q = '?einsatz=' + encodeURIComponent(CTX.uuid) + '#verfuegbar';
    return '<div class="db-tiles db-tiles-katalog" title="Kräftekatalog, eigener Bereich (Verwaltung)">'
      + '<a class="db-tile" href="/kraefteplanung' + q + '"><div class="db-tile-num" style="color:#1f7a3a">' + s.frei + '</div><div class="db-tile-label">einsatzbereit</div></a>'
      + '<a class="db-tile" href="/kraefteplanung' + q + '"><div class="db-tile-num" style="color:#003399">' + s.gebunden + '</div><div class="db-tile-label">im Einsatz</div></a>'
      + '<a class="db-tile" href="/kraefteplanung' + q + '"><div class="db-tile-num" style="color:#8a2be2">' + s.unbekannt + '</div><div class="db-tile-label">ohne Live-Daten</div></a>'
      + '</div><div class="db-katalog-stand">' + (stein.ok ? 'STEIN-Stand ' + ui.esc(ui.fmtDateTime(stein.stand)) + (stein.stale ? ' (veraltet)' : '') : (stein.aktiv === false ? 'STEIN-Daten abgeschaltet' : 'keine STEIN-Live-Daten')) + '</div>';
  }

  async function renderAnfahrt() {
    if (!$main || !CTX) return;
    try {
      const data = await api.get('einsatz', 'anfahrt', { einsatz: CTX.uuid });
      const items = data.items || [];
      const nAng = items.filter(i => i.status === 'angefordert').length;
      const nAnf = items.filter(i => i.status === 'anfahrt').length;
      let html = '<h3>' + ui.esc(CTX.title) + '</h3>'
        + '<div class="db-tiles"><div class="db-tile"><div class="db-tile-num">' + nAng + '</div><div class="db-tile-label">angefordert</div></div>'
        + '<div class="db-tile"><div class="db-tile-num">' + nAnf + '</div><div class="db-tile-label">auf Anfahrt</div></div></div>'
        + katalogTiles();
      if (!items.length) {
        html += '<div class="db-empty">Keine Kräfte angefordert oder auf Anfahrt.</div>';
      } else {
        items.slice(0, 20).forEach(it => {
          html += '<div class="db-anfahrt-row"><div class="db-anfahrt-name"><strong>' + ui.esc(it.bezeichnung || '(ohne Bezeichnung)') + '</strong>'
            + (it.funkruf ? ' · ' + ui.esc(it.funkruf) : '') + '<div class="db-anfahrt-path">' + ui.esc(it.path || '') + (it.status_at ? ' · seit ' + ui.esc(ui.fmtServerTs(it.status_at)) : '') + '</div></div>'
            + statusChip(it.status) + '</div>';
        });
        if (items.length > 20) html += '<div class="db-empty">… und ' + (items.length - 20) + ' weitere</div>';
      }
      html += '<div class="db-einsatz-links"><a href="/anfahrt?einsatz=' + encodeURIComponent(CTX.uuid) + '">Kräfteübersicht öffnen</a><a href="/harke?einsatz=' + encodeURIComponent(CTX.uuid) + '">Führungsharke</a></div>';
      $main.innerHTML = html;
    } catch (e) {
      $main.innerHTML = '<div class="db-empty">Anfahrtsdaten konnten nicht geladen werden (' + ui.esc(ui.errorText(e)) + ').</div>';
    }
  }

  if (CTX) {
    renderAnfahrt();
    renderList($list);
    if (K) { K.onLoad(() => renderAnfahrt()); K.load(); }
    setInterval(() => { if (!document.hidden) { renderAnfahrt(); if (K) K.load(); } }, 30000);
  } else {
    renderList($main);
    setInterval(() => { if (!document.hidden) renderList($main); }, 120000);
  }
})();
