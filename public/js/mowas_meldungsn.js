async function fetchWarnings() {
  try {
    const response = await fetch('/api/proxy?type=nina');
    if (!response.ok) {
      throw new Error(`HTTP-Fehler! Status: ${response.status}`);
    }
    const data = await response.json();
    displayWarnings(data);  // Zeigt alle Warnmeldungen an (textlich)
    updateMidAlert(data);   // Blendet das Bild ein, wenn Warnmeldung < 5 Minuten alt ist
  } catch (error) {
    console.error('Fehler beim Abrufen der Daten:', error);
    document.getElementById('feed-container-mowas').innerText = 'Fehler beim Laden der Warnmeldungen.';
    document.getElementById('mid-alert').innerText = 'Fehler beim Laden der Warnmeldungen.';
  }
}

// Zeigt alle Warnmeldungen (ohne Zeitfilter) an
function displayWarnings(data) {
  const container = document.getElementById('feed-container-mowas');
  container.innerHTML = ''; // Alten Inhalt löschen

  if (!data || !Array.isArray(data) || data.length === 0) {
      container.innerText = 'Keine aktuellen Warnmeldungen.';
      return;
  }

  // Optional: Sortiere die Warnmeldungen (neueste zuerst)
  data.sort((a, b) => new Date(b.startDate) - new Date(a.startDate));

  data.forEach(warning => {
      const item = document.createElement('div');
      item.classList.add('feed-item');

      // Erstelle den Titel als klickbares Element
      const title = document.createElement('div');
      title.classList.add('feed-title');
      title.textContent = warning.i18nTitle.de || 'Kein Titel verfügbar';
      // Beim Klick auf den Titel öffnet sich ein neuer Tab mit der URL
      title.addEventListener('click', () => {
          window.open(`https://warnung.bund.de/meldungen/${warning.id}`, '_blank');
      });

      // Erstelle weitere Elemente für Datum und Beschreibung
      const dateDiv = document.createElement('div');
      dateDiv.textContent = warning.startDate ? new Date(warning.startDate).toLocaleString() : 'Unbekanntes Datum';
      const typeDiv = document.createElement('div');
      typeDiv.textContent = warning.type || 'Keine Beschreibung verfügbar';

      // Füge die Elemente zum Eintrag hinzu
      item.appendChild(title);
      item.appendChild(dateDiv);
      item.appendChild(typeDiv);
      container.appendChild(item);
  });
}

// Prüft, ob es Warnmeldungen gibt, die jünger als 5 Minuten sind,
// und zeigt in diesem Fall ein Bild im Div "mid-alert" an.
function updateMidAlert(data) {
  const midAlert = document.getElementById('mid-alert');
  midAlert.innerHTML = ''; // Inhalt zurücksetzen

  if (!data || !Array.isArray(data) || data.length === 0) {
      return;
  }

  const now = new Date();
  // Filter: Warnmeldungen, die jünger als 5 Minuten (5 * 60 * 1000 Millisekunden) sind
  const recentWarnings = data.filter(warning => {
      if (!warning.startDate) return false;
      const warningTime = new Date(warning.startDate);
      return (now - warningTime) < 5 * 60 * 1000;
  });

  // Falls mindestens eine Warnmeldung aktuell ist, Bild anzeigen
  if (recentWarnings.length > 0) {
      midAlert.innerHTML = `<img src="images/Blaulicht_slower.gif" alt="Blaulicht">`;
  }
}

// Initialer Abruf und regelmäßige Aktualisierung alle 3 Minuten
fetchWarnings();
setInterval(fetchWarnings, 180000);
