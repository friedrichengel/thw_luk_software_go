// changelog.js - Dialog "Was ist neu" (alle Seiten, unabhaengig von luk_core).
//
// Erwartet window.LUK_CHANGELOG (PageHelper::printPwaHead):
//   {version, latest, seen, pending, logged_in, csrf, api}
// - Klick auf .luk-version-btn (Versionsstempel im Footer, Einstellungen) oeffnet
//   die vollstaendige Liste, neueste Version zuerst.
// - pending=true: nach dem Login automatisch oeffnen; alle Eintraege, die neuer
//   sind als der bestaetigte Stand, erscheinen gemeinsam (scrollbar) und sind
//   als "neu" markiert. "Verstanden" bestaetigt bis zur neuesten Version,
//   "Später" schliesst nur fuer diese Sitzung.
(function () {
  'use strict';
  const cfg = window.LUK_CHANGELOG || {};
  let dlg = null;
  let entries = null;
  const SESSION_KEY = 'luk_changelog_later';

  function esc(s) { const d = document.createElement('div'); d.textContent = String(s == null ? '' : s); return d.innerHTML; }
  function cmp(a, b) {
    // Versionsvergleich "2.10" > "2.9"
    const pa = String(a || '').split('.').map(x => parseInt(x, 10) || 0), pb = String(b || '').split('.').map(x => parseInt(x, 10) || 0);
    for (let i = 0; i < Math.max(pa.length, pb.length); i++) { const d = (pa[i] || 0) - (pb[i] || 0); if (d) return d; }
    return 0;
  }
  function isNew(v) { return !cfg.seen || cmp(v, cfg.seen) > 0; }
  function fmtDate(s) {
    const t = Date.parse(String(s || ''));
    return isNaN(t) ? String(s || '') : new Date(t).toLocaleDateString('de-DE', { dateStyle: 'medium' });
  }

  async function loadEntries() {
    if (entries) return entries;
    try {
      const res = await fetch((cfg.api || '/api/changelog') + '?action=list', { credentials: 'same-origin' });
      const data = await res.json();
      entries = (data && data.entries) || [];
      if (data && typeof data.seen === 'string') cfg.seen = data.seen;
    } catch (e) {
      entries = [];
    }
    return entries;
  }

  function ensureDialog() {
    if (dlg) return dlg;
    dlg = document.createElement('dialog');
    dlg.className = 'luk-changelog';
    dlg.innerHTML = '<div class="luk-changelog-head"><h2>Was ist neu</h2><span class="luk-changelog-version"></span></div>'
      + '<div class="luk-changelog-body"><div class="luk-changelog-empty">Lade…</div></div>'
      + '<div class="luk-changelog-actions">'
      + '<button type="button" class="luk-btn luk-changelog-later">Später</button>'
      + '<button type="button" class="luk-btn luk-btn-primary luk-changelog-ok">Verstanden</button>'
      + '</div>';
    document.body.appendChild(dlg);
    dlg.querySelector('.luk-changelog-later').addEventListener('click', () => {
      try { sessionStorage.setItem(SESSION_KEY, '1'); } catch (e) { /* ignore */ }
      dlg.close();
    });
    dlg.querySelector('.luk-changelog-ok').addEventListener('click', markSeen);
    dlg.addEventListener('cancel', e => { e.preventDefault(); try { sessionStorage.setItem(SESSION_KEY, '1'); } catch (x) { /* ignore */ } dlg.close(); });
    return dlg;
  }

  function render(list, autoMode) {
    const body = dlg.querySelector('.luk-changelog-body');
    const ver = dlg.querySelector('.luk-changelog-version');
    ver.textContent = cfg.version ? 'Version ' + cfg.version : '';
    const newCount = list.filter(e => isNew(e.version)).length;
    let html = '';
    if (autoMode && newCount > 1) {
      html += '<p class="luk-changelog-intro">Seit deiner letzten Bestätigung gab es ' + newCount + ' Aktualisierungen. Hier alle zusammen, neueste zuerst.</p>';
    }
    if (!list.length) html += '<div class="luk-changelog-empty">Keine Einträge vorhanden.</div>';
    list.forEach(e => {
      const neu = isNew(e.version);
      html += '<section class="luk-changelog-entry' + (neu ? ' luk-changelog-entry-new' : '') + '">'
        + '<h3>' + (neu ? '<span class="luk-changelog-badge">neu</span>' : '') + 'Version ' + esc(e.version)
        + (e.date ? ' <span class="luk-changelog-date">' + esc(fmtDate(e.date)) + '</span>' : '') + '</h3>'
        + (e.title ? '<div class="luk-changelog-title">' + esc(e.title) + '</div>' : '')
        + '<ul>' + (e.items || []).map(i => '<li>' + esc(i) + '</li>').join('') + '</ul></section>';
    });
    body.innerHTML = html;
    body.scrollTop = 0;
    // Ohne offene Punkte reicht "Schliessen"
    const ok = dlg.querySelector('.luk-changelog-ok');
    const later = dlg.querySelector('.luk-changelog-later');
    ok.textContent = newCount ? 'Verstanden' : 'Schließen';
    later.hidden = !newCount || !cfg.logged_in;
  }

  async function open(autoMode) {
    ensureDialog();
    if (!dlg.open) dlg.showModal();
    const list = await loadEntries();
    render(list, !!autoMode);
  }

  async function markSeen() {
    const latest = (entries && entries.length) ? entries[0].version : (cfg.latest || cfg.version || '');
    if (cfg.logged_in && latest && isNew(latest)) {
      try {
        await fetch((cfg.api || '/api/changelog') + '?action=seen', {
          method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ csrf: cfg.csrf || '', version: latest }),
        });
        cfg.seen = latest;
        cfg.pending = false;
      } catch (e) { /* beim naechsten Login erneut */ }
    }
    dlg.close();
  }

  function bind() {
    // Ohne Login (Login-/Reset-Seiten) bleibt der Stempel reiner Text
    if (!cfg.logged_in) { document.querySelectorAll('.luk-version-btn').forEach(b => { b.style.cursor = 'default'; b.style.textDecoration = 'none'; }); return; }
    document.querySelectorAll('.luk-version-btn').forEach(b => {
      if (b._lukCl) return;
      b._lukCl = true;
      b.addEventListener('click', e => { e.preventDefault(); open(false); });
    });
    let later = false;
    try { later = sessionStorage.getItem(SESSION_KEY) === '1'; } catch (e) { /* ignore */ }
    if (cfg.pending && cfg.logged_in && !later && !document.body.classList.contains('embed-mode')) {
      setTimeout(() => open(true), 700);
    }
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', bind); else bind();
  window.LUK_CHANGELOG_OPEN = () => open(false);
})();
