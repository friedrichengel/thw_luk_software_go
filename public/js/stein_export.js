/**
 * Erzeugt ein Excel-Dokument, in dem in der ersten Zeile die Überschrift steht
 * und anschließend (jeweils durch Leerzeilen getrennt) die einzelnen Gruppen-Tabellen enthalten sind.
 */
// Standard-Konfiguration: Spalten pro Kategorie (änderbar)
const exportConfig = {
  "1": ["Regionalstelle", "OE", "Fahrzeug", "Funkrufname", "THW-Kennzeichen", "Gruppe", "Status", "Kommentar", "ISSI", "Änderung"],
  "2": ["Regionalstelle", "OE", "Name", "Gruppe", "Status", "Kommentar", "Änderung"],
  "3": ["Regionalstelle", "OE", "Funktion", "Funkrufname", "Gruppe", "Status", "Kommentar", "ISSI", "Änderung"],
  "4": ["Regionalstelle", "OE", "Gruppe", "Status", "Kommentar", "ISSI", "Änderung"],
  "5": ["Regionalstelle", "OE", "Anhänger", "Funkrufname", "THW-Kennzeichen", "Gruppe", "Status", "Kommentar", "ISSI", "Änderung"]
};

// Standardfelder die aktiv gehakt sind
const defaultSelections = {
  "1": ["Regionalstelle", "OE", "Fahrzeug", "Funkrufname", "THW-Kennzeichen", "Gruppe", "Status", "Kommentar", "Änderung"],
  "2": ["Regionalstelle", "OE", "Name", "Gruppe", "Status", "Kommentar", "Änderung"],                        
  "3": ["Regionalstelle", "OE", "Funktion", "Funkrufname", "Gruppe", "Status", "Kommentar", "Änderung"],
  "4": ["Regionalstelle", "OE", "Gruppe", "Status", "Kommentar", "Änderung"],    
  "5": ["Regionalstelle", "OE", "Anhänger", "Funkrufname", "THW-Kennzeichen", "Gruppe", "Status", "Kommentar", "Änderung"]
};


// holt die taktische Zeit
function defaultFilename() {
  return getTacticalTime("export");
}

// Modal anzeigen
function openExportModal() {
  const overlay       = document.getElementById('exportOverlay');
  const catContainer  = document.getElementById('exportCategories');
  const filenameInput = document.getElementById('exportFilename');
  catContainer.innerHTML = '';

  Object.entries(exportConfig).forEach(([catId, fields], idx) => {
    const catBlock = document.createElement('div');
    catBlock.className = 'export-category-block';

    // Master-Checkbox + fetter Kategoriename
    const masterLabel = document.createElement('label');
    const masterCb    = document.createElement('input');
    masterCb.type     = 'checkbox';
    masterCb.checked  = true;
    masterCb.className     = 'export-category-master';
    masterCb.dataset.catId = catId;

    const strong = document.createElement('strong');
    strong.textContent = groupNames[catId] || `Kategorie ${catId}`;

    masterLabel.append(masterCb, ' ', strong);
    catBlock.appendChild(masterLabel);

    // Feld-Checkboxen
    const fieldRow = document.createElement('div');
    fieldRow.className = 'export-fields';
    fields.forEach(field => {
      const label = document.createElement('label');
      const cb    = document.createElement('input');
      cb.type     = 'checkbox';
      cb.value    = field;
      cb.checked = (defaultSelections[catId] || []).includes(field);
      label.append(cb, ' ', field);
      fieldRow.appendChild(label);
    });
    catBlock.appendChild(fieldRow);

    // Master-Toggle
    masterCb.addEventListener('change', () => {
      fieldRow.querySelectorAll('input').forEach(i => i.checked = masterCb.checked);
    });

    catContainer.appendChild(catBlock);
    if (idx < Object.keys(exportConfig).length - 1) {
      const divider = document.createElement('div');
      divider.className = 'export-divider';
      catContainer.appendChild(divider);
    }
  });

  filenameInput.value = defaultFilename();

  // nur EIN handler hier
  const confirmBtn = document.getElementById('confirmExportBtn');
  confirmBtn.type = 'button';
  confirmBtn.onclick = () => {
    const selections = {};
    // über jeden Block iterieren
    document.querySelectorAll('.export-category-block').forEach(block => {
      const masterCb = block.querySelector('input.export-category-master');
      const id       = masterCb.dataset.catId;
      if (!masterCb.checked) {
        selections[id] = [];
      } else {
        const vals = Array.from(
          block.querySelectorAll('.export-fields input:checked')
        ).map(cb => cb.value);
        selections[id] = vals;
      }
    });

    const fname = filenameInput.value.trim() || defaultFilename();
    closeExportModal();
    exportToExcel(selections, fname).catch(err => {
      console.error('Export fehlgeschlagen:', err);
      alert('Export fehlgeschlagen: ' + (err && err.message ? err.message : err));
    });
  };

  document.getElementById('cancelExportBtn').onclick = closeExportModal;
  overlay.style.display = 'flex';
}


// Modal schließen
function closeExportModal() {
  document.getElementById('exportOverlay').style.display = 'none';
}

// Export-Button, der nur das Modal öffnet:
const exportBtn = document.getElementById('exportBtn');
exportBtn.setAttribute('type', 'button');
exportBtn.addEventListener('click', openExportModal);

async function exportToExcel(selections, filename) {
  // Abbruch, wenn keine Kategorie aktiv mit >0 Spalten
  const hasAnyColumn = Object.values(selections).some(arr => arr.length > 0);
  if (!hasAnyColumn) {
    alert('Bitte aktiviere mindestens eine Kategorie und wähle mindestens eine Spalte.');
    return;
  }
  if (typeof ExcelJS === 'undefined') {
    throw new Error('Excel-Bibliothek nicht geladen (Seite neu laden).');
  }

  // 1. Header-Werte
  const filterCriteria = currentSortOption;
  const suchBegriff    = searchTerm || '';
  const taktischeZeit = getTacticalTime("export");
  const week           = getCalendarWeek(new Date()).toString();

  // 2. Assets gruppieren
  const grouped = {"1":[],"2":[],"3":[],"4":[],"5":[]};
  currentlyDisplayedAssets.forEach(a => { const gid = String(a.groupId); if (grouped[gid]) grouped[gid].push(a); });

  // 3. Sortierung je Gruppe
  Object.values(grouped).forEach(groupAssets => {
    if (filterCriteria === "organisationseinheit") {
      groupAssets.sort((a,b) => String(a.buname||'').localeCompare(String(b.buname||''),'de',{sensitivity:'base'}));
    }
    else if (filterCriteria === "zustand") {
      groupAssets.sort((a,b) => String(b.status||'').localeCompare(String(a.status||'')));
    }
    else if (filterCriteria === "tage") {
      groupAssets.sort((a,b) =>
        Math.floor((Date.now()-new Date(b.lastModified))/(86400000))
        - Math.floor((Date.now()-new Date(a.lastModified))/(86400000))
      );
    }
    else if (filterCriteria === "regionalstelle") {
      groupAssets.sort((a,b) => {
        const cmp = String(a.sammel||'').localeCompare(String(b.sammel||''), 'de');
        return cmp !== 0
          ? cmp
          : String(a.buname||'').localeCompare(String(b.buname||''), 'de');
      });
    }
  });

  // 4. Workbook
  const wb = new ExcelJS.Workbook();
  const ws = wb.addWorksheet("Export");

  // State-Farben und Namen definieren
  const stateColors = {
    ready:    "28A745",
    notready: "DC3545",
    semiready:"FFC107",
    inuse:    "003399",
    maint:    "6C757D",
    dummy:   "EA45CC"
  };

  const stateNames = {
    ready:    "Einsatzbereit",
    notready: "Nicht Einsatzbereit",
    semiready:"Bedingt Einsatzbereit",
    inuse:    "im Einsatz",
    maint:    "In der Werkstatt",
    dummy:   "Dummy"
  };

  // Header
  ws.addRow(["Status Einsatzbereitschaftsmeldung"]);
  ws.addRow([]);
  ws.addRow(["Filterkriterien:",filterCriteria]);
  ws.addRow(["Suchbegriff:",suchBegriff]);
  ws.addRow(["Datum:",taktischeZeit]);
  ws.addRow(["Zeitraum:","KW",week]);
  ws.addRow(["Verfasser:","LVBE-LuK"]);
  ws.addRow([]);

  // Hilfs-Map für Feldname → Asset-Property
  const fieldMap = {
    'Regionalstelle': a =>
      (a.sammel || '')
        .replace(/Regionalstelle\s*/g, '')  // entfernt alle Vorkommen von "Regionalstelle" und anschließende Leerzeichen
        .trim(),                            // räumt führende/abschließende Leerzeichen weg
    'OE':             a => a.buname || '',
    'Fahrzeug':       a => a.label || '',
    'Funkrufname':    a => a.radioName || '',
    'THW-Kennzeichen':a => a.name || '',
    'Gruppe':         a => a.category || '',
    'Status':         a => a.status || '',
    'Kommentar':      a => a.comment || '',
    'ISSI':           a => a.issi || '',
    'Änderung':       a => (a.lastModified||'').split('T')[0] || ''
  };

  function addGroup(id, name, headers) {
    const assets = grouped[id] || [];
    // Skip, wenn keine Zeilen oder keine Spalten
    if (!assets.length || headers.length === 0) return;

    ws.addRow([name]);
    ws.addRow(headers);

    const idxStatus = headers.indexOf('Status'); // 0-based

    assets.forEach(asset => {
      // Nur die gewählten Spalten in exakt dieser Reihenfolge aus dem Asset ziehen
      const rowValues = headers.map(h => (fieldMap[h] || (() => ''))(asset));

      const newRow = ws.addRow(rowValues);

      // Status-Zelle formatieren, wenn vorhanden
      if (idxStatus >= 0) {
        const cell = newRow.getCell(idxStatus + 1);
        const key  = (asset.status || '').toLowerCase();
        const hex  = stateColors[key] || 'FFFFFF';
        const fill = asset.operationReservation
          ? { type:'pattern', pattern:'darkTrellis', fgColor:{argb:hex}, bgColor:{argb:'FFFFFFFF'} }
          : { type:'pattern', pattern:'solid',     fgColor:{argb:hex} };
        cell.value = stateNames[key] || key;
        cell.fill  = fill;
        cell.font  = { color:{ argb: key === 'inuse' ? 'FFFFFFFF' : 'FF000000' } };
      }
    });

    ws.addRow([]);
  }

  // Pro Kategorie aufrufen
  addGroup('1','Fahrzeug',  selections['1']);
  addGroup('2','Gerät',     selections['2']);
  addGroup('3','Person',    selections['3']);
  addGroup('4','Einheit',   selections['4']);
  addGroup('5','Anhänger',  selections['5']);

  // Download
  const buf = await wb.xlsx.writeBuffer();
  const blob = new Blob([buf],{type:"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"});
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a"); a.href=url; a.download=filename+".xlsx";
  document.body.appendChild(a); a.click(); document.body.removeChild(a); URL.revokeObjectURL(url);
}

/**
 * Kalenderwoche (ISO)
 */
function getCalendarWeek(date) {
  const target = new Date(date.valueOf());
  const dayNr = (date.getDay() + 6) % 7;
  target.setDate(target.getDate() - dayNr + 3);
  const firstThursday = target.valueOf();
  target.setMonth(0, 1);
  if (target.getDay() !== 4) {
    target.setMonth(0, 1 + ((4 - target.getDay()) + 7) % 7);
  }
  return 1 + Math.ceil((firstThursday - target) / (7 * 24 * 3600 * 1000));
}