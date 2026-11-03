// admin_update.js - System-Update: Status, Chunk-Upload, Trigger.
//
// Erwartet window.LUK_UPDATE = {csrf, api, info} (admin_update).
// Zwei Betriebsarten (info.mode):
//   docker  Der Server (api/update) legt nur Dateien ab; der Host-Dienst
//           meldet seinen Stand in status.json, das hier alle paar Sekunden
//           abgefragt wird. Waehrend des Container-Neustarts schlagen die
//           Abfragen fehl - das ist erwartet ("Neustart laeuft").
//   binary  Der Server nimmt ein neues Programm-Binary entgegen, prueft es
//           (Pruefsumme, optional minisign-Signatur, Startprobe), tauscht sich
//           selbst aus und startet neu. Nach "finish" folgt "apply".
(function () {
  'use strict';
  const cfg = window.LUK_UPDATE || {};
  const api = cfg.api || '/api/update';
  const mode = (cfg.info && cfg.info.mode) || 'docker';
  const binary = mode === 'binary';
  const $ = id => document.getElementById(id);
  const show = (id, on) => { const el = $(id); if (el) el.classList.toggle('hidden', !on); };
  const setDisabled = (id, on) => { const el = $(id); if (el) el.disabled = !!on; };

  const ERROR_TEXT = {
    unauthorized: 'Sitzung abgelaufen. Bitte neu anmelden.',
    forbidden: 'Nur Admin-Konten dürfen Updates auslösen.',
    not_enabled: binary ? 'Diese Aktion gibt es im Binary-Betrieb nicht.' : 'Update-Kanal auf diesem Server nicht verfügbar.',
    busy: 'Es wartet bereits ein Auftrag oder der Updater arbeitet gerade.',
    bad_name: binary
      ? 'Dateiname nicht erlaubt (nur Buchstaben, Ziffern, . _ -).'
      : 'Dateiname nicht erlaubt (nur Buchstaben, Ziffern, . _ -, Endung .tar oder .tar.gz).',
    too_large: 'Datei zu groß (Limit ' + (cfg.info && cfg.info.max_mb) + ' MB).',
    bad_sha256: 'Prüfsumme konnte nicht berechnet werden.',
    bad_index: 'Übertragung aus der Reihe geraten. Bitte erneut starten.',
    bad_chunk: 'Teilstück konnte nicht übernommen werden.',
    incomplete: 'Übertragung unvollständig.',
    sha256_mismatch: 'Prüfsumme stimmt nicht – Datei beim Hochladen beschädigt. Bitte erneut versuchen.',
    not_an_archive: 'Das ist kein tar- oder tar.gz-Archiv.',
    not_a_binary: 'Das ist kein ausführbares Programm (Linux-, Windows- oder macOS-Binary erwartet).',
    signature_missing: 'Auf diesem Server ist ein Signaturschlüssel hinterlegt – bitte die .minisig-Datei mitgeben.',
    signature_invalid: 'Signatur ungültig – Datei abgelehnt.',
    binary_invalid: 'Das neue Binary startet nicht (falsche Plattform oder beschädigt) – verworfen.',
    no_previous: 'Kein vorheriger Stand vorhanden.',
    unknown_upload: 'Upload unbekannt oder abgelaufen.',
    io: 'Schreibfehler im Update-Verzeichnis. Rechte prüfen.',
    csrf: 'Sicherheitstoken ungültig. Seite neu laden.',
    server_error: 'Serverfehler.',
    network: 'Server nicht erreichbar.'
  };
  const STATE_TEXT = {
    unknown: 'kein Stand gemeldet', idle: 'bereit', running: 'arbeitet', ok: 'erfolgreich', error: 'Fehler'
  };

  let aborted = false;
  let uploading = false;
  let offlineSince = 0;
  let restartExpected = false;
  let lastStatus = (cfg.info && cfg.info.status) || null;

  function esc(s) { const d = document.createElement('div'); d.textContent = String(s == null ? '' : s); return d.innerHTML; }
  function fmtBytes(n) {
    n = Number(n) || 0;
    if (n >= 1024 * 1024 * 1024) return (n / 1024 / 1024 / 1024).toFixed(2) + ' GB';
    if (n >= 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB';
    return Math.round(n / 1024) + ' KB';
  }
  function fmtTime(s) {
    const t = Date.parse(String(s || ''));
    return isNaN(t) ? '' : new Date(t).toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'short' });
  }
  function flash(msg, kind) {
    const el = $('upd-flash');
    if (!el) return;
    el.className = kind === 'error' ? 'error' : 'notice';
    el.textContent = msg;
    el.classList.remove('hidden');
  }
  function errText(code) { return ERROR_TEXT[code] || ('Fehler: ' + code); }

  async function postJson(body) {
    body.csrf = cfg.csrf;
    let r;
    try {
      r = await fetch(api, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), credentials: 'same-origin' });
    } catch (e) { throw new Error('network'); }
    const j = await r.json().catch(() => ({ error: 'server_error' }));
    if (!r.ok || !j.ok) throw new Error(j.error || 'server_error');
    return j;
  }

  // --- Status ------------------------------------------------------------

  function render(info) {
    const st = info.status || {};
    const stateEl = $('upd-state');
    if (stateEl) {
      stateEl.className = 'upd-state upd-state-' + (st.state || 'unknown');
      stateEl.textContent = STATE_TEXT[st.state] || st.state || '–';
    }
    const line = [];
    if (st.step) line.push(esc(st.step));
    if (st.message) line.push(esc(st.message));
    if (st.image_version) line.push((binary ? 'Binary v' : 'Image v') + esc(st.image_version));
    if (st.at) line.push('<span class="upd-muted">' + esc(fmtTime(st.at)) + '</span>');
    if ($('upd-status-line')) $('upd-status-line').innerHTML = line.length ? line.join(' · ') : '<span class="upd-muted">noch keine Rückmeldung</span>';

    const pend = info.pending || [];
    const pendEl = $('upd-pending');
    if (pendEl) {
      if (pend.length) {
        pendEl.innerHTML = '<strong>' + (binary ? 'Bereitgestellt, noch nicht eingespielt:' : 'Wartet auf den Host-Dienst:') + '</strong> ' + pend.map(p =>
          esc(p.name) + ' <span class="upd-muted">(' + fmtBytes(p.size) + ', ' + esc(fmtTime(p.mtime)) + ')</span>').join(', ');
        pendEl.classList.remove('hidden');
      } else {
        pendEl.classList.add('hidden');
      }
    }
    show('upd-discard-btn', pend.length > 0);
    show('upd-apply-btn', binary && pend.length > 0);
    const busy = uploading || (!binary && pend.length > 0) || st.state === 'running';
    setDisabled('upd-pull-btn', busy);
    setDisabled('upd-rollback-btn', busy || !st.previous_available);
    if ($('upd-rollback-btn')) $('upd-rollback-btn').title = st.previous_available ? '' : (binary ? 'Kein vorheriges Binary vorhanden.' : 'Kein vorheriges Image vorhanden.');
    setDisabled('upd-upload-btn', busy);
    setDisabled('upd-apply-btn', busy);
    if ($('upd-log')) $('upd-log').textContent = info.log || '–';
    lastStatus = st;
  }

  async function poll() {
    try {
      const r = await fetch(api + '?action=status', { credentials: 'same-origin', cache: 'no-store' });
      if (r.status === 401) { flash('Sitzung abgelaufen. Bitte neu anmelden.', 'error'); return; }
      const j = await r.json();
      if (!j.ok) throw new Error(j.error || 'server_error');
      if (offlineSince) {
        offlineSince = 0;
        restartExpected = false;
        flash('Server wieder erreichbar. Laufende Version: v' + (j.version || '?')
          + ((cfg.info && cfg.info.version && cfg.info.version !== j.version) ? ' – Seite neu laden, um die neue Version zu sehen.' : ''));
      }
      render(j);
    } catch (e) {
      if (!offlineSince) offlineSince = Date.now();
      const secs = Math.round((Date.now() - offlineSince) / 1000);
      const stateEl = $('upd-state');
      if (stateEl) {
        stateEl.className = 'upd-state upd-state-running';
        stateEl.textContent = 'Neustart läuft';
      }
      if ($('upd-status-line')) $('upd-status-line').innerHTML = '<span class="upd-muted">Server antwortet nicht (seit ' + secs + ' s) – beim Update normal.</span>';
    }
  }
  function schedule() {
    const fast = uploading || offlineSince || restartExpected || (lastStatus && lastStatus.state === 'running');
    setTimeout(async () => { await poll(); schedule(); }, fast ? 2500 : 8000);
  }

  // --- Upload --------------------------------------------------------------

  async function sha256Hex(buf) {
    const h = await crypto.subtle.digest('SHA-256', buf);
    return Array.from(new Uint8Array(h)).map(b => b.toString(16).padStart(2, '0')).join('');
  }
  function progress(frac, text) {
    show('upd-progress', true);
    if ($('upd-bar-fill')) $('upd-bar-fill').style.width = Math.round(Math.max(0, Math.min(1, frac)) * 100) + '%';
    if ($('upd-progress-text')) $('upd-progress-text').textContent = text || '';
  }

  async function applyStaged(name) {
    restartExpected = true;
    await postJson({ action: 'apply', name: name || '' });
    flash('Binary eingespielt. Der Dienst startet jetzt neu – das dauert einige Sekunden.');
  }

  async function upload(file, sigFile) {
    const chunkSize = (cfg.info && cfg.info.chunk_bytes) || 8 * 1024 * 1024;
    uploading = true; aborted = false;
    setDisabled('upd-upload-btn', true);
    show('upd-abort-btn', true);
    let uploadId = null;
    try {
      progress(0, 'Prüfsumme wird berechnet …');
      const buf = await file.arrayBuffer();
      const sha = await sha256Hex(buf);
      let minisig = '';
      if (sigFile) minisig = (await sigFile.text()).slice(0, 4096);
      const begin = await postJson({ action: 'begin', name: file.name, size: file.size, sha256: sha });
      uploadId = begin.upload_id;
      const total = Math.ceil(file.size / chunkSize);
      for (let i = 0; i < total; i++) {
        if (aborted) throw new Error('aborted');
        const fd = new FormData();
        fd.append('csrf', cfg.csrf);
        fd.append('action', 'chunk');
        fd.append('upload_id', uploadId);
        fd.append('index', String(i));
        fd.append('chunk', file.slice(i * chunkSize, Math.min(file.size, (i + 1) * chunkSize)), 'chunk.bin');
        let r;
        try { r = await fetch(api, { method: 'POST', body: fd, credentials: 'same-origin' }); }
        catch (e) { throw new Error('network'); }
        const j = await r.json().catch(() => ({ error: 'server_error' }));
        if (!r.ok || !j.ok) throw new Error(j.error || 'server_error');
        progress((i + 1) / total, 'Übertragen: ' + fmtBytes(j.received) + ' von ' + fmtBytes(file.size) + ' (Teil ' + (i + 1) + '/' + total + ')');
      }
      progress(1, binary ? 'Server prüft das Binary …' : 'Server prüft das Archiv …');
      const fin = await postJson({ action: 'finish', upload_id: uploadId, minisig: minisig });
      uploadId = null;
      if (binary) {
        progress(1, 'Bereitgestellt: ' + fin.name + ' – Startprobe und Austausch …');
        await applyStaged(fin.name);
        progress(1, 'Eingespielt: ' + fin.name + ' – Neustart läuft');
      } else {
        progress(1, 'Übergeben an den Host-Dienst: ' + fin.name);
        flash('Archiv „' + fin.name + '“ liegt bereit. Der Host-Dienst lädt es jetzt und startet den Container neu.');
      }
      if ($('upd-form')) $('upd-form').reset();
    } catch (e) {
      if (uploadId) postJson({ action: 'cancel', upload_id: uploadId }).catch(() => {});
      if (e.message === 'aborted') { flash('Upload abgebrochen.'); progress(0, ''); }
      else { flash(errText(e.message), 'error'); progress(0, ''); }
    } finally {
      uploading = false;
      show('upd-abort-btn', false);
      setDisabled('upd-upload-btn', false);
      poll();
    }
  }

  // --- Verdrahtung ----------------------------------------------------------

  if ($('upd-form')) $('upd-form').addEventListener('submit', ev => {
    ev.preventDefault();
    const f = $('upd-file').files[0];
    if (!f) return;
    const maxMb = (cfg.info && cfg.info.max_mb) || 500;
    if (f.size > maxMb * 1024 * 1024) { flash(errText('too_large'), 'error'); return; }
    const q = binary
      ? '„' + f.name + '“ (' + fmtBytes(f.size) + ') hochladen und als neues Programm einspielen?\nDer Dienst wird danach neu gestartet.'
      : '„' + f.name + '“ (' + fmtBytes(f.size) + ') hochladen und als neues Image einspielen?\nDer Container wird danach neu gestartet.';
    if (!confirm(q)) return;
    upload(f, $('upd-sig') ? ($('upd-sig').files[0] || null) : null);
  });
  if ($('upd-abort-btn')) $('upd-abort-btn').addEventListener('click', () => { aborted = true; });
  if ($('upd-pull-btn')) {
    if (binary) $('upd-pull-btn').classList.add('hidden');
    $('upd-pull-btn').addEventListener('click', async () => {
      if (!confirm('Aktuelles Image aus der Registry ziehen und den Container neu starten?')) return;
      try { await postJson({ action: 'pull' }); flash('Auftrag „aus Registry ziehen“ liegt beim Host-Dienst.'); }
      catch (e) { flash(errText(e.message), 'error'); }
      poll();
    });
  }
  if ($('upd-rollback-btn')) $('upd-rollback-btn').addEventListener('click', async () => {
    const q = binary
      ? 'Vorherigen Stand wiederherstellen? Der Dienst wird mit dem alten Binary neu gestartet.'
      : 'Vorherigen Stand wiederherstellen? Der Container wird mit dem alten Image neu gestartet.';
    if (!confirm(q)) return;
    try {
      if (binary) restartExpected = true;
      await postJson({ action: 'rollback' });
      flash(binary ? 'Vorheriger Stand wird aktiviert, der Dienst startet neu.' : 'Auftrag „Rückfall“ liegt beim Host-Dienst.');
    } catch (e) { restartExpected = false; flash(errText(e.message), 'error'); }
    poll();
  });
  if ($('upd-apply-btn')) $('upd-apply-btn').addEventListener('click', async () => {
    if (!confirm('Bereitgestelltes Binary jetzt einspielen? Der Dienst wird danach neu gestartet.')) return;
    try { await applyStaged(''); }
    catch (e) { restartExpected = false; flash(errText(e.message), 'error'); }
    poll();
  });
  if ($('upd-discard-btn')) $('upd-discard-btn').addEventListener('click', async () => {
    if (!confirm(binary ? 'Bereitgestelltes Binary verwerfen?' : 'Alle wartenden Aufträge verwerfen?')) return;
    try { const r = await postJson({ action: 'discard' }); flash(r.removed + ' Datei(en) verworfen.'); }
    catch (e) { flash(errText(e.message), 'error'); }
    poll();
  });

  if (cfg.info) render(cfg.info);
  schedule();
})();
