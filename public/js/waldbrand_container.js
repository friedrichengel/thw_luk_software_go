// waldbrand_container.js - Dashboard-Kachel "Waldbrand": Karte des
// Waldbrandgefahrenindex (WBI) des DWD. Das Bild kommt ueber den Server
// (/api/proxy?type=waldbrand, dort gecacht und stuendlich bedingt beim DWD
// nachgefragt), damit bei Ausfall oder fehlender Sitzung eine Meldung statt
// eines leeren Feldes erscheint.
(function () {
  'use strict';
  var box = document.getElementById('waldbrand-image');
  if (!box) return;

  var img = document.createElement('img');
  img.alt = 'Waldbrandgefahrenindex (DWD)';
  img.className = 'waldbrand-map';
  var note = document.createElement('div');
  note.className = 'waldbrand-note';
  var stand = document.createElement('div');
  stand.className = 'waldbrand-stand';
  box.appendChild(img);
  box.appendChild(note);
  box.appendChild(stand);

  var lastUrl = null;

  function fail(msg) {
    img.hidden = true;
    note.hidden = false;
    note.textContent = 'Waldbrandkarte des DWD derzeit nicht verfügbar: ' + msg;
    stand.textContent = '';
  }

  function formatStand(res) {
    // Stand des Bildes laut DWD (Last-Modified), ersatzweise Abrufzeit des Servers
    var at = res.headers.get('Last-Modified') || res.headers.get('X-Cache-At');
    var d = at ? new Date(at) : null;
    if (!d || isNaN(d.getTime())) return '';
    var text = 'Stand DWD ' + d.toLocaleString('de-DE', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' });
    if (res.headers.get('X-Cache-Stale')) text += ' (älterer Stand, DWD nicht erreichbar)';
    return text;
  }

  async function refresh() {
    try {
      var res = await fetch('/api/proxy?type=waldbrand', { credentials: 'same-origin', cache: 'no-store' });
      if (!res.ok) {
        var code = 'HTTP ' + res.status;
        try { var j = await res.json(); if (j && j.error) code = j.error; } catch (e) { /* kein JSON */ }
        if (res.status === 401) { window.location.replace('/login?redirect=' + encodeURIComponent(location.pathname + location.search)); return; }
        fail(code);
        return;
      }
      var blob = await res.blob();
      var url = URL.createObjectURL(blob);
      img.onload = function () {
        if (lastUrl) URL.revokeObjectURL(lastUrl);
        lastUrl = url;
        img.hidden = false;
        note.hidden = true;
        stand.textContent = formatStand(res);
      };
      img.onerror = function () { URL.revokeObjectURL(url); fail('Bild konnte nicht dargestellt werden'); };
      img.src = url;
    } catch (e) {
      fail('keine Verbindung zum Server');
    }
  }

  refresh();
  // Der DWD aktualisiert die Karte einmal taeglich gegen 05:00 UTC, gelegentlich
  // spaeter. Die Kachel fragt stuendlich beim Server nach; der prueft bedingt
  // beim DWD und liefert sonst aus dem Cache.
  setInterval(refresh, 60 * 60 * 1000);
})();
