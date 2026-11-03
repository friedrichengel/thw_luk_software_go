// kraefte_typ.js - Kraeftekatalog fuer STEINboard / STEINmap (ohne luk_core).
//
//   KraefteStamm.load()            Stammdaten ueber api/kraefte?action=stamm laden und in die
//                                  bisherige Struktur (config.stein.units ...) uebersetzen
//   KraefteStamm.bindungen()       kraft_key => Bindungen in offenen Einsaetzen (api/kraefte)
//   KraefteStamm.normTyp(raw)      Typschluessel wie lib/KraefteKatalog.php::normTyp
//   KraefteStamm.keysFor(buid, cat) kraft_keys der Einheiten eines OV zu einer STEIN-Kategorie
//   KraefteStamm.bindBadge(...)    Band "im Einsatz: ..." fuer eine Asset-Box
//
// Die Typ-Normalisierung ist bewusst eine Kopie der PHP-Logik: STEIN liefert
// "N", "WP (A)", "ZTr TZ", die Stammdaten "FGr N", "FGr WP (A)", "ZTr".
// Aenderungen bitte an beiden Stellen nachziehen.
(function () {
  'use strict';

  const FGR_SHORTS = ['n', 'e', 'i', 'bel', 'brb', 'f', 'fk', 'fkf', 'fkfk', 'fkfm', 'ka', 'kb', 'logv', 'logmw',
    'o', 'oa', 'ob', 'oc', 'oel', 'oela', 'oelb', 'oelc', 'r', 'ra', 'rb', 'rc',
    'sb', 'sba', 'sbb', 'sp', 'spr', 'tw', 'w', 'wa', 'wb', 'wp', 'wpa', 'wpb', 'wpc'];

  function normTyp(raw) {
    let r = String(raw || '').trim();
    let nr = null;
    let m = r.match(/^(.*?)\s*\((\d)\)\s*\)?\s*$/);
    if (m) { r = m[1]; nr = parseInt(m[2], 10); }
    r = r.replace(/[)\s]+$/, '');
    m = r.match(/^(b|bgr)\s*(\d)\b(.*)$/i);
    if (m) { if (nr === null) nr = parseInt(m[2], 10); r = m[1] + m[3]; }
    let s = r.toLowerCase().replace(/ö/g, 'oe').replace(/ä/g, 'ae').replace(/ü/g, 'ue').replace(/ß/g, 'ss').replace(/[^a-z0-9]+/g, '');
    if (!s) return { key: '', nr };
    let key;
    if (s.indexOf('ztr') === 0) key = s.indexOf('fk') !== -1 ? 'ztrfk' : (s.indexOf('log') !== -1 ? 'ztrlog' : 'ztr');
    else if ((m = s.match(/^b(?:gr)?(ash|egs)?$/))) key = 'bgr' + (m[1] || '');
    else if (s.indexOf('fgr') === 0) key = s;
    else if (/^tr(ul|ess|mhp|ts|logm|logvg|erlo)$/.test(s)) key = s;
    else if (FGR_SHORTS.indexOf(s) !== -1) key = 'fgr' + s;
    else key = s;
    if (key === 'fgrspr') key = 'fgrsp';
    if (key === 'fgrfkf') key = 'fgrf';
    if (key === 'fgrfkfk' || key === 'fgrfkfm') key = 'fgrfk';
    if (key.indexOf('oega') === 0) key = 'oega';
    return { key, nr };
  }

  const state = { stamm: null, typen: null, bindungen: null, byPrefix: null, loadedAt: 0 };

  async function fetchJson(url) {
    const res = await fetch(url, { credentials: 'same-origin' });
    if (!res.ok) throw new Error('HTTP ' + res.status + ' ' + url);
    return res.json();
  }

  // Stammdaten laden; liefert {units, units_nachbar, filter, aktiv} im Format der alten config.stein.
  async function load() {
    // Stammdaten kommen nur ueber die sitzungsgeschuetzte API (public/daten/ wird
    // nicht ausgeliefert).
    const [stammRes, typenRes] = await Promise.all([
      fetchJson('/api/kraefte?action=stamm'),
      fetchJson('/api/kraefte?action=typen').catch(() => ({ typen: {} })),
    ]);
    const stamm = (stammRes && stammRes.stamm) || {};
    state.stamm = stamm;
    state.typen = (typenRes && typenRes.typen) || {};
    const regionen = {};
    (stamm.regionen || []).forEach(r => { regionen[r.key] = r; });
    const units = [], nachbar = [], filter = [], aktiv = [];
    (stamm.regionen || []).forEach(r => {
      if (r.extern) return;
      filter.push(r.name);
      if (r.live) aktiv.push(r.name);
    });
    (stamm.ovs || []).forEach(ov => {
      const reg = regionen[ov.region] || {};
      const einheiten = ov.einheiten || [];
      const base = {
        name: ov.name, lat: ov.lat, lng: ov.lng, zust_landkreis: ov.landkreis || '',
        einheiten,
        // Anzeige-Labels ("BGr 2", "FGr N") und STEIN-Kategorien ("B", "N") fuer Dummy-Assets
        units: einheiten.length ? einheiten.map(e => e.short + (e.nr ? ' ' + e.nr : '')) : undefined,
        stein_cats: einheiten.map(e => (state.typen[e.typ] && state.typen[e.typ].stein_cat) || e.short),
      };
      if (ov.extern) {
        nachbar.push(Object.assign(base, { lv: reg.name || '', region: ov.region }));
      } else {
        units.push(Object.assign(base, { buid: String(ov.buid || ''), sammel: reg.name || '', art: ov.art, region: ov.region }));
      }
    });
    return { units, units_nachbar: nachbar, filter, aktiv };
  }

  // Bindungen in offenen Einsaetzen (nur fuer eingeloggte Einsatz-Konten). Fehler -> {}.
  async function bindungen() {
    try {
      const res = await fetchJson('/api/kraefte?action=bindungen');
      state.bindungen = res.bindungen || {};
    } catch (e) {
      state.bindungen = state.bindungen || {};
    }
    // Index ohne laufende Nummer: "103:bgr" -> [key, key...]
    state.byPrefix = {};
    Object.keys(state.bindungen).forEach(k => {
      const p = k.split('#')[0];
      (state.byPrefix[p] = state.byPrefix[p] || []).push(k);
    });
    state.loadedAt = Date.now();
    return state.bindungen;
  }

  // Alle Katalog-Schluessel eines OV zu einer STEIN-Kategorie (ggf. mehrere, z.B. zwei BGr)
  // Typ-Familie ohne Variante (fgroc -> fgro, bgrash -> bgr), Kopie von KraefteKatalog::typFamily
  function typFamily(key) {
    const m = String(key || '').match(/^(fgro|fgroel|fgrr|fgrsb|fgrw|fgrwp|fgrk)[abc]$/);
    if (m) return m[1];
    return key === 'bgrash' || key === 'bgregs' ? 'bgr' : key;
  }
  function keysFor(buid, category) {
    if (!state.stamm) return [];
    const t = normTyp(category);
    if (!t.key) return [];
    const ov = (state.stamm.ovs || []).find(o => String(o.buid || '') === String(buid) || o.key === String(buid));
    if (!ov) return [];
    const exact = (ov.einheiten || []).filter(e => e.typ === t.key).map(e => e.key);
    if (exact.length) return exact;
    // keine Einheit genau dieses Typs: gleiche Familie (FGr O statt FGr O (B) usw.)
    const fam = typFamily(t.key);
    return (ov.einheiten || []).filter(e => typFamily(e.typ) === fam).map(e => e.key);
  }
  function keysForUnitKey(ovKey, typ) {
    if (!state.stamm) return [];
    const ov = (state.stamm.ovs || []).find(o => o.key === ovKey);
    return ov ? (ov.einheiten || []).filter(e => e.typ === typ).map(e => e.key) : [];
  }

  function bindText(b) {
    const where = b.einsatz_title ? 'im Einsatz „' + b.einsatz_title + '“' : 'in anderem Einsatz (' + (b.stab_name || b.ebene_label || 'Stab') + ')';
    return where + (b.status && b.status !== 'geplant' ? ' · ' + b.status.replace('_', ' ') : '');
  }
  function esc(s) { const d = document.createElement('div'); d.textContent = String(s == null ? '' : s); return d.innerHTML; }

  /**
   * Band "im Einsatz" fuer eine Asset-Box oder null. keys = Katalog-Schluessel
   * der Einheit(en); bei mehreren gleichen Einheiten eines OV wird gezaehlt.
   */
  function bindBadge(keys, opts) {
    if (!keys || !keys.length || !state.bindungen) return null;
    const bound = keys.filter(k => (state.bindungen[k] || []).length);
    if (!bound.length) return null;
    const first = state.bindungen[bound[0]][0];
    const div = document.createElement('div');
    div.className = 'asset-bind';
    let txt = '⚑ ' + bindText(first);
    if (keys.length > 1) txt += ' (' + bound.length + ' von ' + keys.length + ')';
    div.textContent = txt;
    div.title = bound.map(k => (state.bindungen[k] || []).map(b => (b.label ? b.label + ': ' : '') + bindText(b)).join('\n')).join('\n');
    if (first.einsatz_uuid) {
      const a = document.createElement('a');
      a.href = '/harke?einsatz=' + encodeURIComponent(first.einsatz_uuid);
      a.textContent = ' öffnen';
      a.className = 'asset-bind-link';
      a.addEventListener('click', e => e.stopPropagation());
      div.appendChild(a);
    }
    return div;
  }

  function einsatzUuid() {
    try { return new URL(window.location.href).searchParams.get('einsatz') || ''; } catch (e) { return ''; }
  }
  // Link "in Einsatz übernehmen" (nur im Einsatz-Kontext, ?einsatz=UUID).
  // keys: Katalog-Schluessel der Einheit(en). Ohne Katalog-Gegenstueck (z.B.
  // LuK, OV-Stab) wird die Einheit als Freitext uebergeben: text = {bez, ov, funkruf}.
  function takeLink(keys, text) {
    const uuid = einsatzUuid();
    if (!uuid) return null;
    const a = document.createElement('a');
    a.className = 'asset-take-link';
    if (keys && keys.length) {
      // bevorzugt eine noch nicht gebundene Einheit
      const free = keys.find(k => !(state.bindungen && (state.bindungen[k] || []).length)) || keys[0];
      a.href = '/harke?einsatz=' + encodeURIComponent(uuid) + '&kraft=' + encodeURIComponent(free);
      a.title = 'In der Führungsharke des gewählten Einsatzes als Einheit anlegen';
    } else if (text && text.bez) {
      a.href = '/harke?einsatz=' + encodeURIComponent(uuid)
        + '&kraft_text=' + encodeURIComponent(text.bez)
        + (text.ov ? '&kraft_ov=' + encodeURIComponent(text.ov) : '')
        + (text.funkruf ? '&kraft_funk=' + encodeURIComponent(text.funkruf) : '');
      a.title = 'In der Führungsharke des gewählten Einsatzes als Einheit anlegen (ohne Katalog-Verweis, Bezeichnung und OV werden vorbelegt)';
    } else {
      return null;
    }
    a.textContent = '→ in Einsatz übernehmen';
    a.addEventListener('click', e => e.stopPropagation());
    return a;
  }

  window.KraefteStamm = { load, bindungen, normTyp, keysFor, keysForUnitKey, bindBadge, takeLink, einsatzUuid, state, esc };
})();
