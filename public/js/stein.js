// Konfiguration: Stammdaten (Ortsverbaende, Einheiten, Regionalstellen) kommen
// ueber api/kraefte?action=stamm - gemeinsam mit STEINmap und den Einsatz-Seiten
// (lib/KraefteKatalog.php) - und werden in steinInit() per KraefteStamm.load()
// in config.stein uebernommen. Live-Abruf nur fuer Regionalstellen mit
// "live": true in der Stammdatei; alle anderen erscheinen als Dummy (lila).
const config = {
  stein: {
    units: [],
    filter: [],
    aktiv: [],
    proxyUrl: "/api/proxy?type=stein"
  }
};

let globalAssets = [];

let currentlyDisplayedAssets = [];

const stateColors = {
  ready: "rgb(40, 167, 69)",
  notready: "rgb(220, 53, 69)",
  semiready: "rgb(255, 193, 7)",
  inuse: "rgb(0, 51, 153)",
  maint: "rgb(108, 117, 125)",
  dummy: "rgb(138, 43, 226)"
};

const groupNames = {
  "1": "Fahrzeug",
  "2": "Gerät",
  "3": "Person",
  "4": "Einheit",
  "5": "Anhänger"
};

let searchTerm;

function getGroupNamefromShort(value) {
  switch (value) {
    case '-':
      return '-';
    case 'B':
    case 'B1':
    case 'B ASH':
    case 'B EGS':
    case 'B1 ASH':
      return 'Bergungsgruppe';
    case 'Bel':
      return 'Fachgruppe Beleuchtung';
    case 'BrB':
      return 'Fachgruppe Brückenbau';
    case 'BT':
      return 'BT';
    case 'E':
      return 'Fachgruppe Elektroversorgung';
    case 'ENT':
      return 'ENT';
    case 'F':
    case 'FK (F)':
      return 'Fachgruppe Führungsunterstützung';
    case 'FK':
      return 'Fachgruppe Führung und Kommunikation';
    case 'FK (FK)':
      return 'FK (FK)';
    case 'FK (FM)':
      return 'FK (FM)';
    case 'Gesamter OV':
      return 'Gesamter OV';
    case 'HCP':
      return 'HCP';
    case 'HV':
      return 'HV';
    case 'I':
      return 'Fachgruppe Infrastruktur';
    case 'K (A)':
    case 'K (B)':
      return 'Fachgruppe Kommunikation';
    case 'Lkr':
      return 'Lkr';
    case 'Log-MW':
      return 'Fachgruppe Logistik-Materialwirtschaft';
    case 'Log-V':
      return 'Fachgruppe Logistik-Verpflegung';
    case 'MT':
      return 'MT';
    case 'N':
      return 'Fachgruppe Notversorgung und Notinstandsetzung';
    case 'O (A)':
    case 'O (B)':
    case 'O (C)':
      return 'Fachgruppe Ortung';
    case 'ÖGA':
      return 'ÖGA';
    case 'Öl (A)':
    case 'Öl (B)':
    case 'Öl (C)':
      return 'Fachgruppe Ölschaden';
    case 'OV-Stab':
      return 'OV-Stab';
    case 'Prüfteam':
      return 'Prüfteam';
    case 'rescEU CBRN':
      return 'rescEU CBRN';
    case 'R (A)':
    case 'R (B)':
    case 'R (C)':
      return 'Fachgruppe Räumen';
    case 'SB (A)':
    case 'SB (B)':
      return 'Fachgruppe Schwere Bergung';
    case 'SEEBA':
      return 'SEEBA';
    case 'SEELift':
      return 'SEELift';
    case 'SEEWA':
      return 'SEEWA';
    case 'Sp':
      return 'Fachgruppe Sprengen';
    case 'Stab':
      return 'Stab';
    case 'Sys BR500':
      return 'Sys BR500';
    case 'Tr ErLo':
      return 'Tr ErLo';
    case 'Tr ESS':
      return 'Tr Einsatzstellensicherungssystem';
    case 'Tr Log-M':
      return 'Tr Logistik-Material';
    case 'Tr Log-VG':
      return 'Tr Logistik-Verbrauchsgüter';
    case 'Tr MHP':
      return 'Tr Mobiler Hochwasserpegel';
    case 'Tr TS':
      return 'Trupp Schwerer Transport';
    case 'Tr UL':
      return 'Trupp Unbemannte Luftfahrtsysteme';
    case 'TW':
      return 'Fachgruppe Trinkwasserversorgung';
    case 'VOST':
      return 'VOST';
    case 'W':
    case 'W (A)':
    case 'W (B)':
      return 'Fachgruppe Wassergefahren';
    case 'WP (A)':
    case 'WP (B)':
    case 'WP (C)':
      return 'Fachgruppe Wasserschaden/Pumpen';
    case 'ZTr':
    case 'ZTr TZ':
      return 'Zugtrupp';
    case 'ZTr FZ-FK':
      return 'Zugtrupp FZ Führung und Kommunikation';
    case 'ZTr FZ-Log':
      return 'Zugtrupp FZ Logistik';
    default:
      return value;
  }
}

function saveAllFilterStates() {
  const toJSON = cbList =>
    cbList.map(cb => ({ value: cb.value, checked: cb.checked }));
  
  // Organisationseinheiten
  const bunes = Array.from(document.querySelectorAll("#buname-dropdown input[type=checkbox]"));
  localStorage.setItem("bunameState", JSON.stringify(toJSON(bunes)));
  
  // Typen
  const types = Array.from(document.querySelectorAll("#group-dropdown input[type=checkbox]"));
  localStorage.setItem("groupState", JSON.stringify(toJSON(types)));
  
  // Zustand
  const states = Array.from(document.querySelectorAll("#state-dropdown input[type=checkbox]"));
  localStorage.setItem("stateState", JSON.stringify(toJSON(states)));
  
  // Kategorien (Gruppen)
  const cats = Array.from(document.querySelectorAll("#category-dropdown input[type=checkbox]"));
  localStorage.setItem("catState", JSON.stringify(toJSON(cats)));
}

function loadAllFilterStates() {
  const apply = (key, selector) => {
    const raw = localStorage.getItem(key);
    if (!raw) return;
    const arr = JSON.parse(raw);
    arr.forEach(item => {
      const cb = document.querySelector(`${selector}[value="${item.value}"]`);
      if (cb) cb.checked = item.checked;
    });
  };
  apply("bunameState",   "#buname-dropdown input[type=checkbox]");
  apply("groupState",    "#group-dropdown input[type=checkbox]");
  apply("stateState",    "#state-dropdown input[type=checkbox]");
  apply("catState",      "#category-dropdown input[type=checkbox]");
}


function getBackground(state, operationReservation) {
  const baseColor = stateColors[state] || "rgb(128,128,128)";
  if (operationReservation) {
    const nums = baseColor.match(/\d+/g);
    const rgba = `rgba(${nums[0]}, ${nums[1]}, ${nums[2]}, 0.882)`;
    return `repeating-linear-gradient(45deg, ${baseColor}, ${baseColor} 10px, ${rgba} 10px, ${rgba} 20px)`;
  } else {
    return baseColor;
  }
}

// BU-Dropdown befüllen (basierend auf config.stein.units und config.stein.filter)
function populateBunameDropdown() {
  const dropdown = document.getElementById("buname-dropdown");
  const content = dropdown.querySelector(".dropdown-content");
  content.innerHTML = "";

  // "Alle anzeigen"-Eintrag
  const allLabel = document.createElement("label");
  const allInput = document.createElement("input");
  allInput.type = "checkbox";
  allInput.className = "buname-all";
  allInput.value = "all";
  allInput.checked = true;
  allLabel.appendChild(allInput);
  allLabel.appendChild(document.createTextNode(" Alle anzeigen"));
  content.appendChild(allLabel);

  // Horizontaler Strich
  const hr1 = document.createElement("hr");
  content.appendChild(hr1);

  // Filter-Einträge aus config.stein.filter
  config.stein.filter.forEach(filterValue => {
    const label = document.createElement("label");
    const input = document.createElement("input");
    input.type = "checkbox";
    input.className = "buname-filter-group";
    input.value = filterValue;
    input.checked = true;
    label.appendChild(input);
    label.appendChild(document.createTextNode(" " + filterValue));
    content.appendChild(label);
  });

  // Erneut horizontaler Strich
  const hr2 = document.createElement("hr");
  content.appendChild(hr2);

  // BU-Einträge (die einzelnen Einheiten)
  config.stein.units.forEach(unit => {
    const label = document.createElement("label");
    const input = document.createElement("input");
    input.type = "checkbox";
    input.className = "buname-unit";
    input.value = unit.name;
    input.checked = true;
    // setze den zugehörigen sammel-Wert als Datenattribut
    input.setAttribute("data-sammel", unit.sammel);
    label.appendChild(input);
    label.appendChild(document.createTextNode(" " + unit.name));
    content.appendChild(label);
  });

  // Setup der Event-Listener für das BU-Dropdown
  setupBunameDropdownListeners();
}

  // Spezifische Event-Listener für das BU-Dropdown
function setupBunameDropdownListeners() {
  const dropdown = document.getElementById("buname-dropdown");
  // "Alle anzeigen" Checkbox
  const allCheckbox = dropdown.querySelector("input.buname-all");
  allCheckbox.addEventListener("change", function(){
    const newState = allCheckbox.checked;
    // Setze alle Filter- und Unit-Checkboxen auf neuen Zustand
    const filterCheckboxes = dropdown.querySelectorAll("input.buname-filter-group");
    const unitCheckboxes = dropdown.querySelectorAll("input.buname-unit");
    filterCheckboxes.forEach(cb => cb.checked = newState);
    unitCheckboxes.forEach(cb => cb.checked = newState);
    displayAssets();
  });

  // Filter-Checkboxen: Beim Wechsel aller zugehörigen BU-Einträge an/abwählen
  const filterCheckboxes = dropdown.querySelectorAll("input.buname-filter-group");
  filterCheckboxes.forEach(filterCb => {
    filterCb.addEventListener("change", function(){
      const newState = filterCb.checked;
      const unitCheckboxes = dropdown.querySelectorAll("input.buname-unit");
      unitCheckboxes.forEach(unitCb => {
        if(unitCb.getAttribute("data-sammel") === filterCb.value) {
          unitCb.checked = newState;
        }
      });
      // Aktualisiere den "Alle anzeigen" Status (falls alle untergeordneten Checkboxen gleich gesetzt sind)
      const allSubCheckboxes = dropdown.querySelectorAll("input.buname-filter-group, input.buname-unit");
      let allChecked = true;
      allSubCheckboxes.forEach(cb => {
        if (!cb.checked) { allChecked = false; }
      });
      allCheckbox.checked = allChecked;
      displayAssets();
    });
  });

  // Einzelne BU-Einträge: Beim Wechsel prüfen, ob "Alle anzeigen" (und evtl. der zugehörige Filter) angepasst werden muss
  const unitCheckboxes = dropdown.querySelectorAll("input.buname-unit");
  unitCheckboxes.forEach(unitCb => {
    unitCb.addEventListener("change", function(){
      const allSubCheckboxes = dropdown.querySelectorAll("input.buname-filter-group, input.buname-unit");
      let allChecked = true;
      allSubCheckboxes.forEach(cb => {
        if (!cb.checked) { allChecked = false; }
      });
      allCheckbox.checked = allChecked;
      displayAssets();
    });
  });
}

// Aktualisierte Funktion zum Auslesen der ausgewählten BU’s: Hier werden nur die Unit-Checkboxen berücksichtigt!
function getSelectedBunames() {
  const dropdown = document.getElementById("buname-dropdown");
  const allCheckbox = dropdown.querySelector("input.buname-all");
  let selectedBunames = [];
  if(allCheckbox && allCheckbox.checked) {
    selectedBunames = config.stein.units.map(unit => unit.name);
  } else {
    const unitCheckboxes = Array.from(dropdown.querySelectorAll("input.buname-unit"));
    unitCheckboxes.forEach(cb => {
      if(cb.checked) { selectedBunames.push(cb.value); }
    });
  }
  return selectedBunames;
}

// Einfügen eines „clearing“ Trenner-Divs mit sichtbarer Linie
function appendSeparator(parent) {
  const hr = document.createElement("hr");
  // Lässt das <hr> über alle Grid-Spalten laufen
  hr.style.gridColumn = "1 / -1";
  hr.style.width      = "100%";
  hr.style.margin     = "12px 0";  // Abstand oben/unten
  parent.appendChild(hr);
}

function displayAssets() {
  // 1. Ermittlung der Filterauswahl für BU (nur die Unit-Checkboxen)
  const selectedBunames = getSelectedBunames();

  // 2. Ermittlung der Filterauswahl für Gruppen
  const groupCheckboxes = Array.from(document.querySelectorAll("#group-dropdown input[type='checkbox']"));
  let selectedGroups = [];
  const selectAllGroup = groupCheckboxes.find(cb => cb.value === "all");
  if (selectAllGroup && selectAllGroup.checked) {
    selectedGroups = Object.keys(groupNames);
  } else {
    groupCheckboxes.forEach(cb => {
      if (cb.checked && cb.value !== "all") {
        selectedGroups.push(cb.value);
      }
    });
  }

  // 3. Filterauswahl Zustände
  const stateCheckboxes = Array.from(document.querySelectorAll("#state-dropdown input[type='checkbox']"));
  let selectedStates = [];
  const selectAllState = stateCheckboxes.find(cb => cb.value === "all");

  if (selectAllState && selectAllState.checked) {
    // Wenn "Alle anzeigen" angehakt ist, nimm alle Status-Werte außer "all"
    selectedStates = stateCheckboxes
      .map(cb => cb.value)
      .filter(val => val !== "all");
  } else {
    // Ansonsten nur die angehakten (ohne "all")
    stateCheckboxes.forEach(cb => {
      if (cb.checked && cb.value !== "all") {
        selectedStates.push(cb.value);
      }
    });
  }

  // 4. Filterauswahl Kategorien
  const categoryCheckboxes = Array.from(document.querySelectorAll("#category-dropdown input[type='checkbox']"));
  let selectedCategories = [];
  const selectAllCategory = categoryCheckboxes.find(cb => cb.value === "all");
  if (selectAllCategory && selectAllCategory.checked) {
    selectedCategories = categoryCheckboxes
      .filter(cb => cb.value !== "all")
      .map(cb => cb.value);
  } else {
    categoryCheckboxes.forEach(cb => {
      if (cb.checked && cb.value !== "all") {
        selectedCategories.push(cb.value);
      }
    });
  }

  // 5. Filterung globalAssets
  let filteredAssets = globalAssets.filter(asset => {
    const matchesBuname   = selectedBunames.includes(asset.buname);
    const matchesGroup    = selectedGroups.includes(String(asset.groupId));

    // Kategorie‐Filter: Dummy überspringen, alle anderen normal
    const cat             = asset.category === null ? "-" : asset.category;
    const matchesCategory = selectedCategories.includes(cat);

    // Zustand‐Filter: Standard‐Logik, Dummy erscheint nur wenn in selectedStates
    const matchesState    = selectedStates.includes(asset.status)
      || (selectedStates.includes("ev") && asset.operationReservation);

    return matchesBuname && matchesGroup && matchesCategory && matchesState;
  });

  // 6. Suche
  searchTerm = document.getElementById("search-input").value.trim();
  if (searchTerm) {
    const safe = searchTerm
      .replace(/[-[\]{}()+?.,\\^$|#\s]/g, '\\$&')   // Regex‐Escape
      .replace(/\*/g, ".*");                       // Wildcard * → .*
    const regex = new RegExp(safe, "i");
  
    filteredAssets = filteredAssets.filter(asset => {
      // Hier den buname mit ins „Suchfeld“ packen:
      const haystack = [
        asset.buname,                 // <-- NEU
        asset.label,
        asset.name || ""
      ].join(" ");
      return regex.test(haystack);
    });
  }

  // 7. Zusammenfassung
  const groupOrder = ["1", "2", "3", "4", "5"];
  const groupCounts = {};
  groupOrder.forEach(g => groupCounts[g] = 0);
  filteredAssets.forEach(a => { groupCounts[String(a.groupId)]++; });

  const container = document.getElementById("asset-container");
  container.innerHTML = "";

  // Summary
  const summaryDiv = document.createElement("div");
  summaryDiv.className = "summary-field";
  summaryDiv.innerHTML = groupOrder
    .filter(g => groupCounts[g] > 0)
    .map(g => `${groupNames[g]||"Gruppe "+g}: ${groupCounts[g]}`)
    .join(" &nbsp;&nbsp; ");
  container.appendChild(summaryDiv);

  // 8. Rendering pro Gruppe
  groupOrder.forEach(groupId => {
    const groupAssets = filteredAssets.filter(a => String(a.groupId) === groupId);
    if (!groupAssets.length) return;

    // Sortieren
    if (currentSortOption === "organisationseinheit") {
      groupAssets.sort((a,b) => a.buname.localeCompare(b.buname, 'de'));
    } else if (currentSortOption === "zustand") {
      groupAssets.sort((a,b) => b.status.localeCompare(a.status));
    } else if (currentSortOption === "tage") {
      groupAssets.sort((a,b) =>
        (Date.now() - new Date(b.lastModified)) - (Date.now() - new Date(a.lastModified))
      );
    } else if (currentSortOption === "regionalstelle") {
      groupAssets.sort((a,b) => {
        const cmp = a.sammel.localeCompare(b.sammel, 'de');
        return cmp !== 0 ? cmp : a.buname.localeCompare(b.buname, 'de');
      });
    }

    // Gruppen-Überschrift
    const hdr = document.createElement("div");
    hdr.className = "group-header";
    hdr.textContent = `${groupNames[groupId]||"Gruppe "+groupId} (${groupAssets.length})`;
    container.appendChild(hdr);

    // Wenn regionalstelle-Sortierung, dann zusätzlich nach sammel aufteilen
    if (currentSortOption === "regionalstelle") {
      // 1) Assets nach Regionalstelle+BU-Namen sortieren …
      groupAssets.sort((a, b) => {
        const cmp = a.sammel.localeCompare(b.sammel, 'de');
        return cmp !== 0
          ? cmp
          : a.buname.localeCompare(b.buname, 'de');
      });

      // 2) Einzigartige Regionalstellen extrahieren **und** sortieren
      const sammels = Array.from(new Set(groupAssets.map(a => a.sammel)))
                          .sort((x, y) => x.localeCompare(y, 'de'));

      // 3) In sauberer Reihenfolge rendern
      sammels.forEach((sammel, idx) => {
        groupAssets
          .filter(a => a.sammel === sammel)
          .forEach(asset => appendAssetBox(asset, container));
        if (idx < sammels.length - 1) appendSeparator(container);
      });
    }else {
      // In allen anderen Sortier-Modi: nach jedem buname einen Break & HR einfügen
      let lastBuname = null;
      groupAssets.forEach(asset => {
        if (lastBuname !== null && asset.buname !== lastBuname) {
          //appendSeparator(container);
        }
        lastBuname = asset.buname;
        appendAssetBox(asset, container);
      });
    }
  });

  // 9. Speichern
  currentlyDisplayedAssets = filteredAssets;
}

/**
 * Hilfsfunktion: rendert ein asset-box DIV und hängt es an parent an
 */
function appendAssetBox(asset, parent) {
  let text = asset.label;
  if (asset.name) text += " – " + asset.name;
  const diffDays = Math.floor((Date.now() - new Date(asset.lastModified)) / (1000*60*60*24));

  const box = document.createElement("div");
  box.className = "asset-box";
  box.innerHTML = `
    <div style="font-weight:bold;margin-bottom:4px;">${asset.buname}</div>
    <div>${text}</div>
    <div>Tage seit Änderung: ${diffDays}</div>
  `;
  box.style.background = getBackground(asset.status, asset.operationReservation);
  if ((asset.status === "inuse")||(asset.status === "dummy")) box.style.color = "white";
  // Kraeftekatalog: in einem offenen Einsatz gebunden? Im Einsatz-Kontext: Uebernahme-Link
  if (String(asset.groupId) === "4") {
    const keys = KraefteStamm.keysFor(asset.buId, asset.category);
    const badge = KraefteStamm.bindBadge(keys);
    if (badge) box.appendChild(badge);
    const take = KraefteStamm.takeLink(keys, { bez: asset.label, ov: asset.buname, funkruf: asset.radioName });
    if (take) box.appendChild(take);
  }

  box.addEventListener("click", e => {
    e.stopPropagation();
    const tooltip = document.getElementById("tooltip");
    tooltip.innerHTML = `
      <strong>Label:</strong> ${asset.label || ""}<br>
      <strong>Name:</strong> ${asset.name || ""}<br>
      <strong>RadioName:</strong> ${asset.radioName || ""}<br>
      <strong>ISSI:</strong> ${asset.issi || ""}<br>
      <strong>Kommentar:</strong> ${asset.comment || ""}<br>
      <strong>Kategorie:</strong> ${asset.category || ""}<br>
      <strong>Zuletzt geändert:</strong> ${asset.lastModified || ""}<br>
      <strong>Von:</strong> ${asset.lastModifiedBy || ""}
    `;
    tooltip.style.top = (e.pageY + 5) + "px";
    tooltip.style.left = (e.pageX + 5) + "px";
    tooltip.style.display = "block";
  });

  parent.appendChild(box);
}


// Registrierung der Event-Listener für die exklusiven Sortier-Checkboxen
document.querySelectorAll(".sort-checkbox").forEach(checkbox => {
  checkbox.addEventListener("change", function(e) {
    if (this.checked) {
      document.querySelectorAll(".sort-checkbox").forEach(other => {
        if (other !== this) {
          other.checked = false;
        }
      });
      currentSortOption = this.getAttribute("data-sort");
      document.querySelector("#sort-dropdown .dropbtn").textContent = "Sortierung";
      displayAssets();
    } else {
      this.checked = true;
    }
  });
});

// Globale Variable für die Sortierreihenfolge (Standard: Organisationseinheit)
let currentSortOption = "regionalstelle";

// Setup der Dropdowns (außer buname, da hier nun eine eigene Logik genutzt wird)
setupDropdown("group-dropdown");
setupDropdown("state-dropdown");
setupDropdown("category-dropdown");
setupDropdown("sort-dropdown");
setupDropdown("buname-dropdown");

// Stammdaten laden, dann BU-Dropdown fuellen und Assets abrufen (alle 5 Minuten neu)
async function steinInit() {
  try {
    Object.assign(config.stein, await KraefteStamm.load());
  } catch (e) {
    console.error("Stammdaten der Einheiten konnten nicht geladen werden:", e);
  }
  populateBunameDropdown();
  // Filterzustand speichern wie bei den statischen Checkboxen (siehe unten)
  document.querySelectorAll("#buname-dropdown input[type=checkbox]").forEach(cb => cb.addEventListener("change", saveAllFilterStates));
  loadAllFilterStates();
  fetchAndStoreAssets();
  setInterval(fetchAndStoreAssets, 300000);
}
steinInit();

// Assets-Daten abrufen
async function fetchAllAssets() {
  const { proxyUrl, units, aktiv } = config.stein;
  // nur aktive Regionalstellen-Units berücksichtigen für den echten Abruf
  const activeUnits = units.filter(unit => aktiv.includes(unit.sammel));
  const buids = activeUnits.map(unit => unit.buid).join(",");
  const url = `${proxyUrl}&buIds=${encodeURIComponent(buids)}`;

  // 1) echte Daten abrufen
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error("Fehler beim Abrufen der Assets");
  }
  // Cache-Stand-Header auswerten (falls Element vorhanden)
  try {
    const oldest = response.headers.get("X-Cache-Oldest-At");
    const standEl = document.getElementById("dataStand");
    if (oldest && standEl) {
      const d = new Date(oldest);
      if (!isNaN(d.getTime())) {
        const hh = String(d.getHours()).padStart(2, "0");
        const mm = String(d.getMinutes()).padStart(2, "0");
        const ss = String(d.getSeconds()).padStart(2, "0");
        standEl.textContent = `Stand: ${hh}:${mm}:${ss}`;
      }
    }
  } catch (e) { /* Header evtl. nicht sichtbar - ignorieren */ }
  const assets = await response.json();  // Rohdaten

  // 2) Name- und Sammel-Mapping vorbereiten
  const unitMapping = {};
  const sammelMapping = {};
  units.forEach(unit => {
    const key = Number(unit.buid);
    unitMapping[key] = unit.name.trim();
    sammelMapping[key] = unit.sammel.trim();
  });

  // 3) Echte Assets anreichern
  assets.forEach(asset => {
    const key = Number(asset.buId);
    asset.buname = unitMapping[key]   || asset.buId;
    asset.sammel = sammelMapping[key] || "";
  });

  // 4) Zähle, wie viele echte Assets es pro buId gab
  const realCount = assets.reduce((acc, a) => {
    const k = Number(a.buId);
    acc[k] = (acc[k] || 0) + 1;
    return acc;
  }, {});

  // 5) Dummy-Assets für Units ohne echte Daten oder inaktiver Regionalstelle
  units.forEach(unit => {
    const buid = Number(unit.buid);
    const isActiveRegion = aktiv.includes(unit.sammel);
    const hasAnyReal = (realCount[buid] || 0) > 0;

    // wenn NICHT aktiv ODER keine echten Datensätze
    if (!isActiveRegion || !hasAnyReal) {
      (unit.stein_cats || unit.units || []).forEach(subUnitName => {
        assets.push({
          buId: buid,
          buname: unit.name,
          sammel: unit.sammel,
          label: getGroupNamefromShort(subUnitName),
          name: "",
          radioName: "",
          issi: "",
          comment: "",
          category: subUnitName,
          groupId: 4,                
          status: "dummy",          
          operationReservation: false, 
          lastModified: new Date().toISOString(),
          lastModifiedBy: ""
        });
      });
    }
  });

  return assets;
}

async function fetchAndStoreAssets() {
  try {
    await KraefteStamm.bindungen();   // Bindung in offenen Einsaetzen (Band "im Einsatz")
    globalAssets = await fetchAllAssets();
    displayAssets();
  } catch (error) {
    console.error("Fehler beim Laden der Daten: ", error);
  }
}

// Dropdown-Setup (gemeinsame Funktionalität)
function setupDropdown(dropdownId) {
  const dropdown = document.getElementById(dropdownId);
  const btn = dropdown.querySelector(".dropbtn");
  btn.addEventListener("click", e => {
    e.stopPropagation();
    document.querySelectorAll(".dropdown").forEach(d => {
      if (d.id !== dropdownId) {
        d.classList.remove("show");
      }
    });
    dropdown.classList.toggle("show");
  });
  dropdown.querySelector(".dropdown-content").addEventListener("click", function(e) {
    e.stopPropagation();
  });
  document.addEventListener("click", () => {
    dropdown.classList.remove("show");
  });
}

document.getElementById("search-input").addEventListener("keyup", displayAssets);
document.addEventListener("click", () => {
  document.getElementById("tooltip").style.display = "none";
});

function setupSelectAllForDropdown(dropdownId) {
  const dropdown = document.getElementById(dropdownId);
  const content = dropdown.querySelector(".dropdown-content");
  const checkboxes = Array.from(content.querySelectorAll("input[type='checkbox']"));
  checkboxes.forEach(cb => {
    cb.addEventListener("change", function () {
      if (this.value === "all") {
        // Wenn "Alle anzeigen" geändert wird, setze alle anderen Checkboxen entsprechend
        checkboxes.forEach(item => {
          if (item.value !== "all") {
            item.checked = this.checked;
          }
        });
      } else {
        // Wenn eine Einzelfeld-Checkbox abgewählt wird, deaktiviere auch "Alle anzeigen"
        if (!this.checked) {
          const allCb = content.querySelector("input[value='all']");
          if (allCb) {
            allCb.checked = false;
          }
        } else {
          // Wenn alle anderen Checkboxen (außer "Alle anzeigen") ausgewählt sind,
          // setze "Alle anzeigen" automatisch auf true.
          const nonAllCheckboxes = checkboxes.filter(item => item.value !== "all");
          if (nonAllCheckboxes.every(item => item.checked)) {
            const allCb = content.querySelector("input[value='all']");
            if (allCb) {
              allCb.checked = true;
            }
          }
        }
      }
      
      // Setze ggf. den Button-Text zurück (optional)
      let btnText = "";
      if (dropdown.id === "group-dropdown") {
        btnText = "Typen";
      } else if (dropdown.id === "state-dropdown") {
        btnText = "Zustand";
      } else if (dropdown.id === "category-dropdown") {
        btnText = "Gruppen";
      } 
      dropdown.querySelector(".dropbtn").textContent = btnText;
      
      // Aktualisiere die Anzeige der Assets
      displayAssets();
    });
  });
}     

setupSelectAllForDropdown("group-dropdown");
setupSelectAllForDropdown("state-dropdown");
setupSelectAllForDropdown("category-dropdown");

document.querySelectorAll("input[type=checkbox]").forEach(cb => {
  cb.addEventListener("change", () => {
    // erst Filtern, dann Speichern
    displayAssets();
    saveAllFilterStates();
  });
});