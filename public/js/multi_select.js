/* multi_select.js
 * Leichtgewichtiger Multi-Select-Dropdown mit Chip-Darstellung.
 * Erwartet folgende Struktur:
 *
 *   <div class="multi-select" data-placeholder="...">
 *     <div class="multi-select-control">
 *       <div class="multi-select-chips"></div>
 *       <span class="multi-select-arrow">...</span>
 *     </div>
 *     <div class="multi-select-panel">
 *       <label><input type="checkbox" name="..." value="..."> <span>Label</span></label>
 *       ...
 *     </div>
 *   </div>
 *
 * Initialzustand (checked) wird beim Laden in Chips uebersetzt.
 */
(function () {
  'use strict';

  function renderChips(ms) {
    var chipsEl = ms.querySelector('.multi-select-chips');
    var placeholder = ms.getAttribute('data-placeholder') || '';
    chipsEl.textContent = '';

    var checked = ms.querySelectorAll('.multi-select-panel input[type=checkbox]:checked');
    if (checked.length === 0) {
      var ph = document.createElement('span');
      ph.className = 'multi-select-placeholder';
      ph.textContent = placeholder;
      chipsEl.appendChild(ph);
      return;
    }

    checked.forEach(function (cb) {
      var labelEl = cb.closest('label');
      var textEl = labelEl ? labelEl.querySelector('span') : null;
      var label = textEl ? textEl.textContent : cb.value;

      var chip = document.createElement('span');
      chip.className = 'multi-select-chip';

      var txt = document.createElement('span');
      txt.textContent = label;
      chip.appendChild(txt);

      var rm = document.createElement('button');
      rm.type = 'button';
      rm.className = 'multi-select-chip-remove';
      rm.setAttribute('aria-label', 'Entfernen: ' + label);
      rm.innerHTML = '&times;';
      rm.addEventListener('click', function (e) {
        e.stopPropagation();
        cb.checked = false;
        renderChips(ms);
      });
      chip.appendChild(rm);

      chipsEl.appendChild(chip);
    });
  }

  function positionPanel(ms) {
    var control = ms.querySelector('.multi-select-control');
    var panel = ms.querySelector('.multi-select-panel');
    var rect = control.getBoundingClientRect();
    var gap = 4;
    var panelMax = 260;
    var spaceBelow = window.innerHeight - rect.bottom;
    var openUp = spaceBelow < panelMax + gap && rect.top > spaceBelow;

    panel.style.left = rect.left + 'px';
    panel.style.minWidth = rect.width + 'px';
    panel.style.maxWidth = Math.max(rect.width, 240) + 'px';
    if (openUp) {
      panel.style.top = 'auto';
      panel.style.bottom = (window.innerHeight - rect.top + gap) + 'px';
    } else {
      panel.style.bottom = 'auto';
      panel.style.top = (rect.bottom + gap) + 'px';
    }
  }

  function closeAll(except) {
    document.querySelectorAll('.multi-select.open').forEach(function (ms) {
      if (ms !== except) {
        ms.classList.remove('open');
        var ctrl = ms.querySelector('.multi-select-control');
        if (ctrl) ctrl.setAttribute('aria-expanded', 'false');
        var panel = ms.querySelector('.multi-select-panel');
        if (panel) panel.hidden = true;
      }
    });
  }

  function open(ms) {
    closeAll(ms);
    ms.classList.add('open');
    var ctrl = ms.querySelector('.multi-select-control');
    if (ctrl) ctrl.setAttribute('aria-expanded', 'true');
    var panel = ms.querySelector('.multi-select-panel');
    if (panel) panel.hidden = false;
    positionPanel(ms);
  }

  function close(ms) {
    ms.classList.remove('open');
    var ctrl = ms.querySelector('.multi-select-control');
    if (ctrl) ctrl.setAttribute('aria-expanded', 'false');
    var panel = ms.querySelector('.multi-select-panel');
    if (panel) panel.hidden = true;
  }

  function toggle(ms) {
    if (ms.classList.contains('open')) {
      close(ms);
    } else {
      open(ms);
    }
  }

  function init(ms) {
    if (ms.dataset.msReady === '1') return;
    ms.dataset.msReady = '1';

    renderChips(ms);

    var control = ms.querySelector('.multi-select-control');
    control.addEventListener('click', function (e) {
      if (e.target.closest('.multi-select-chip-remove')) return;
      toggle(ms);
    });
    control.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        toggle(ms);
      } else if (e.key === 'Escape') {
        close(ms);
      }
    });

    ms.querySelectorAll('.multi-select-panel input[type=checkbox]').forEach(function (cb) {
      cb.addEventListener('change', function () { renderChips(ms); });
    });
    // Klick auf Label im Panel soll Dropdown offen halten
    ms.querySelector('.multi-select-panel').addEventListener('click', function (e) {
      e.stopPropagation();
    });
  }

  function initAll() {
    document.querySelectorAll('.multi-select').forEach(init);
  }

  // Globale Listener: Klick ausserhalb schliesst, Scroll/Resize repositioniert.
  document.addEventListener('click', function (e) {
    if (!e.target.closest('.multi-select')) {
      closeAll(null);
    }
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') closeAll(null);
  });
  window.addEventListener('resize', function () {
    document.querySelectorAll('.multi-select.open').forEach(positionPanel);
  });
  // Scroll in beliebigem Vorfahren -> Position anpassen (capture, damit wir alle
  // scrollbaren Container mitbekommen).
  window.addEventListener('scroll', function () {
    document.querySelectorAll('.multi-select.open').forEach(positionPanel);
  }, true);

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initAll);
  } else {
    initAll();
  }
})();
