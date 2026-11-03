// Serververwaltung: Formular-Komfort ohne Inline-Handler (CSP ohne
// 'unsafe-inline'). Die Vorlage markiert Elemente mit data-Attributen:
//   data-autosubmit        Aenderung schickt das Formular ab
//   data-confirm="Frage"   Formular (submit) oder Schaltflaeche (click) fragt nach
//   data-set="id=wert"     Schaltflaeche setzt vor dem Absenden ein verstecktes Feld
(function () {
  'use strict';
  document.addEventListener('change', function (e) {
    const el = e.target.closest ? e.target.closest('[data-autosubmit]') : null;
    if (el && el.form) el.form.submit();
  });
  document.addEventListener('submit', function (e) {
    const form = e.target;
    if (form.dataset && form.dataset.confirm && !window.confirm(form.dataset.confirm)) e.preventDefault();
  });
  document.addEventListener('click', function (e) {
    const btn = e.target.closest ? e.target.closest('button[data-set], button[data-confirm]') : null;
    if (!btn) return;
    if (btn.dataset.confirm && !window.confirm(btn.dataset.confirm)) { e.preventDefault(); return; }
    if (btn.dataset.set) {
      const i = btn.dataset.set.indexOf('=');
      const target = document.getElementById(btn.dataset.set.slice(0, i));
      if (target) target.value = btn.dataset.set.slice(i + 1);
    }
  });
})();
