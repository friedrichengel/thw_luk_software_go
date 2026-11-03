// footer_menu.js - Klappmenues der Fussleiste (alle Seiten).
//
// Das Markup aus lib/PageHelper.php ist jeweils ein <details>: Aufklappen
// kann der Browser allein, auch ohne JavaScript - sonst waeren Navigation
// und Abmelden unerreichbar, wenn ein Skript nicht laedt. Dieses Skript
// ergaenzt nur, was <details> nicht mitbringt: Schliessen bei einem Klick
// daneben, mit Escape, und beim Oeffnen eines anderen Menues.
(function () {
  'use strict';

  function init() {
    const pops = Array.prototype.slice.call(document.querySelectorAll('.footer .footer-pop'));
    if (!pops.length) return;

    const close = p => { if (p.open) p.removeAttribute('open'); };
    const closeAll = except => pops.forEach(p => { if (p !== except) close(p); });

    pops.forEach(p => {
      // Immer nur ein Menue offen
      p.addEventListener('toggle', () => { if (p.open) closeAll(p); });
      // Ein Klick im Menue navigiert ohnehin - zuklappen, damit es beim
      // Zuruecknavigieren aus dem Verlauf nicht offen steht.
      p.querySelectorAll('.footer-pop-item').forEach(a => a.addEventListener('click', () => close(p)));
    });

    document.addEventListener('click', e => {
      pops.forEach(p => { if (!p.contains(e.target)) close(p); });
    });
    document.addEventListener('keydown', e => {
      if (e.key !== 'Escape') return;
      const offen = pops.filter(p => p.open);
      if (!offen.length) return;
      offen.forEach(close);
      const sum = offen[0].querySelector('summary');
      if (sum) sum.focus();
    });
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
})();
