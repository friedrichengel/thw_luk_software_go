/* pwa.js - Service-Worker-Registrierung + Tablet-Hilfen
 *
 * - Registriert /service-worker.js (PWA-Installierbarkeit, Asset-Cache).
 * - Fuegt einen "Bitte Geraet drehen"-Overlay ein. Sichtbar nur, wenn das
 *   Geraet im Hochformat ist (CSS-Regel in tablet.css) - auch auf den
 *   Auth-Seiten. Nur bei erzwungener Passwortaenderung
 *   (body.no-rotate-prompt) blendet tablet.css den Overlay aus.
 * - Verhindert iOS-Doppeltipp-Zoom auf Schaltflaechen, ohne Pinch-Zoom
 *   komplett zu sperren (das waere schlecht fuer Karten/Tabellen).
 */
(function () {
  'use strict';

  // 1. Service Worker registrieren (nur HTTPS / localhost).
  if ('serviceWorker' in navigator) {
    var canRegister = window.isSecureContext
      || location.hostname === 'localhost'
      || location.hostname === '127.0.0.1';
    if (canRegister) {
      window.addEventListener('load', function () {
        navigator.serviceWorker.register('/service-worker.js', { scope: '/' })
          .catch(function () { /* still */ });
      });
    }
  }

  // 2. Seite aus dem Back-Forward-Cache des Browsers (Zurueck-Taste,
  //    App-Wechsel auf dem Tablet) nie mit altem Stand anzeigen: Reload
  //    laesst den Server die Sitzung pruefen und ggf. zum Login umleiten.
  window.addEventListener('pageshow', function (ev) {
    if (ev.persisted) window.location.reload();
  });

  // 3. "Bitte drehen"-Overlay einfuegen, falls noch nicht vorhanden.
  function ensureRotatePrompt() {
    if (document.querySelector('.rotate-prompt')) return;
    var div = document.createElement('div');
    div.className = 'rotate-prompt';
    div.setAttribute('role', 'alertdialog');
    div.setAttribute('aria-live', 'assertive');
    div.innerHTML =
      '<div class="rotate-prompt-icon" aria-hidden="true">&#x1F4F1;</div>' +
      '<div class="rotate-prompt-text">Bitte Ger&auml;t drehen.<br>' +
      'Das Dashboard ist f&uuml;r Querformat optimiert.</div>';
    document.body.appendChild(div);
  }
  if (document.body) {
    ensureRotatePrompt();
  } else {
    document.addEventListener('DOMContentLoaded', ensureRotatePrompt);
  }
})();
