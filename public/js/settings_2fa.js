// 2FA-Einrichtung: QR-Code lokal im Browser erzeugen. Das TOTP-Geheimnis
// verlaesst damit nie den eigenen Server (frueher lieferte ein externer
// Dienst das Bild und bekam dafuer die komplette otpauth-URI).
(function () {
  'use strict';
  document.addEventListener('DOMContentLoaded', function () {
    var box = document.querySelector('.qr-code[data-otpauth]');
    if (!box || typeof qrcode !== 'function') return;
    try {
      if (qrcode.stringToBytesFuncs && qrcode.stringToBytesFuncs['UTF-8']) {
        qrcode.stringToBytes = qrcode.stringToBytesFuncs['UTF-8'];
      }
      var qr = qrcode(0, 'M');
      qr.addData(box.getAttribute('data-otpauth'));
      qr.make();
      // createSvgTag liefert ausschliesslich Markup aus den berechneten Modulen.
      box.innerHTML = qr.createSvgTag({ cellSize: 4, margin: 8, scalable: true });
      box.removeAttribute('data-otpauth');
    } catch (e) {
      box.textContent = 'QR-Code konnte nicht erzeugt werden. Bitte den Schluessel von Hand eingeben.';
    }
  });
})();
