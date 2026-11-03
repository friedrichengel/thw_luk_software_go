/*
 * divera_container.js - Divera-Personaluebersicht fuer das Dashboard
 * (Tab "Divera" in Box 1): Verfuegbarkeit je Gruppe und Status mit
 * Qualifikations-Icons, Summen und Detailaufklappung.
 *
 * Anbindung an den Server:
 *   - Daten vom Server-Endpunkt GET /api/divera?action=all (Divera pull/all,
 *     1:1 durchgereicht, Namen serverseitig gekuerzt). Sitzung + Divera-Recht
 *     der Organisation sind Pflicht; Fehler kommen als
 *     {error: "divera_disabled" | "divera_forbidden" | "divera_upstream" | "unauthorized"}.
 *   - Gruppen, Status und Monitor werden aus den Divera-Daten abgeleitet
 *     (cluster.group, cluster.status, monitor); die eingebauten Tabellen bleiben als
 *     Fallback, wenn die Antwort diese Strukturen nicht enthaelt.
 *   - Tut nichts, wenn #divera_entrys nicht in der Seite ist.
 */
(function () {
  "use strict";

  const entrysEl = document.getElementById("divera_entrys");
  if (!entrysEl) return;

  const DIVERA_API = "/api/divera";

  const ERROR_TEXT = {
    unauthorized: "Sitzung abgelaufen. Bitte neu anmelden.",
    divera_disabled: "Divera ist für Ihre Organisation nicht eingerichtet.",
    divera_forbidden: "Keine Berechtigung für Divera.",
    divera_upstream: "Divera antwortet nicht. Daten können veraltet sein.",
    divera_auth_failed: "Divera lehnt den Schlüssel der Organisation ab. Bitte Zugang unter „Organisation verwalten“ prüfen.",
    network: "Keine Verbindung zum Server.",
  };

  /**********************************************
   * Hilfsfunktionen
   **********************************************/
  // Drittinhalte (Gruppen-/Status-Namen, Notizen) muessen escaped werden,
  // bevor sie ueber innerHTML ins DOM kommen. Schutz gegen Stored-XSS
  // aus boesartigen Divera-Antworten.
  function _esc(s) {
    const d = document.createElement("div");
    d.textContent = String(s == null ? "" : s);
    return d.innerHTML;
  }

  function showError(code) {
    let el = document.getElementById("divera_error");
    if (!el) {
      el = document.createElement("div");
      el.id = "divera_error";
      el.className = "divera_error";
      entrysEl.parentNode.insertBefore(el, entrysEl);
    }
    el.textContent = ERROR_TEXT[code] || ("Fehler beim Laden der Divera-Daten (" + code + ").");
    el.style.display = "block";
  }

  function clearError() {
    const el = document.getElementById("divera_error");
    if (el) el.style.display = "none";
  }

  function toQuals(q) {
    if (!Array.isArray(q)) return [];
    return q.map(x => Number(x)).filter(x => !Number.isNaN(x));
  }

  /**********************************************
   * Icon-Symbole für Qualifikationen (Divera-Qualifikations-IDs)
   **********************************************/
  function getVehicleSymbol(quals) {
    if (quals.includes(35))
      return `<span class="divera_qualification-icon" data-qual="B"><i class="fas fa-car-side"></i></span>`;
    else if (quals.includes(1))
      return `<span class="divera_qualification-icon" data-qual="C"><i class="fas fa-truck"></i></span>`;
    else if (quals.includes(38))
      return `<span class="divera_qualification-icon" data-qual="CE"><i class="fas fa-truck-moving"></i></span>`;
    return "";
  }

  function getGasMaskSymbol(quals) {
    return (quals.includes(120) || quals.includes(2))
      ? `<span class="divera_qualification-icon" data-qual="AGT"><i class="fas fa-head-side-mask"></i></span>`
      : "";
  }

  function getBoatSymbol(quals) {
    if (quals.includes(18))
      return `<span class="divera_qualification-icon" data-qual="BoFü"><i class="fas fa-anchor"></i></span>`;
    else if (quals.includes(104))
      return `<span class="divera_qualification-icon" data-qual="Binnen"><i class="fas fa-sailboat"></i></span>`;
    else if (quals.includes(105))
      return `<span class="divera_qualification-icon" data-qual="See"><i class="fas fa-ship"></i></span>`;
    return "";
  }

  function getWalkieTalkieSymbol(quals) {
    return quals.includes(47)
      ? `<span class="divera_qualification-icon" data-qual="SprFu Fü"><i class="fas fa-walkie-talkie"></i></span>`
      : "";
  }

  function getBriefcaseSymbol(quals) {
    return quals.includes(48)
      ? `<span class="divera_qualification-icon" data-qual="SanHe"><i class="fas fa-briefcase-medical"></i></span>`
      : "";
  }

  function getPlugSymbol(quals) {
    if (quals.includes(141))
      return `<span class="divera_qualification-icon" data-qual="SEA"><i class="fas fa-plug-circle-xmark"></i></span>`;
    else if (quals.includes(140))
      return `<span class="divera_qualification-icon" data-qual="NEA"><i class="fas fa-plug-circle-bolt"></i></span>`;
    return "";
  }

  function getFireSymbol(quals) {
    return quals.includes(142)
      ? `<span class="divera_qualification-icon" data-qual="Thermisch trennen"><i class="fas fa-fire"></i></span>`
      : "";
  }

  function getPeopleCarryBoxSymbol(quals) {
    return quals.includes(151)
      ? `<span class="divera_qualification-icon" data-qual="Ldkr"><i class="fas fa-people-carry-box"></i></span>`
      : "";
  }

  function getClipboardSymbol(quals) {
    return (quals.includes(156) || quals.includes(157))
      ? `<span class="divera_qualification-icon" data-qual="Leiter Stab"><i class="fas fa-clipboard"></i></span>`
      : "";
  }

  function getSpoonSymbol(quals) {
    return quals.includes(219)
      ? `<span class="divera_qualification-icon" data-qual="Küche"><i class="fas fa-spoon"></i></span>`
      : "";
  }

  function getTreeSymbol(quals) {
    return (quals.includes(235) || quals.includes(4208) || quals.includes(4209) || quals.includes(4210))
      ? `<span class="divera_qualification-icon" data-qual="MKS"><i class="fas fa-tree"></i></span>`
      : "";
  }

  function getRadiationSymbol(quals) {
    return quals.includes(30313)
      ? `<span class="divera_qualification-icon" data-qual="CBRN"><i class="fas fa-radiation"></i></span>`
      : "";
  }

  function getSymbolColumns(quals) {
    return [
      getVehicleSymbol(quals),        // Stelle 1
      getGasMaskSymbol(quals),        // Stelle 2
      getBoatSymbol(quals),           // Stelle 3
      getWalkieTalkieSymbol(quals),   // Stelle 4
      getBriefcaseSymbol(quals),      // Stelle 5
      getPlugSymbol(quals),           // Stelle 6
      getFireSymbol(quals),           // Stelle 7
      getPeopleCarryBoxSymbol(quals), // Stelle 8
      getClipboardSymbol(quals),      // Stelle 9
      getSpoonSymbol(quals),          // Stelle 10
      getTreeSymbol(quals),           // Stelle 11
      getRadiationSymbol(quals)       // Stelle 12
    ];
  }

  /**********************************************
   * Gruppen- und Statusfilter
   *
   * Gruppen und Status werden aus den Divera-Daten abgeleitet
   * (cluster.group bzw. cluster.status). Die festen Werte dienen
   * nur als Fallback fuer Antworten ohne diese Strukturen.
   **********************************************/
  const FALLBACK_GROUP_MAPPING = {
    "Fachberater": 12057,
    "LUK": 12058,
    "Zugtrupp": 12059,
    "Bergung": 12060,
    "FGr N": 4724,
    "FGr SB": 4725,
    "FGr W": 12061
  };

  const FALLBACK_STATUS_ORDER = [23536, 9868, 9869, 23518, 9870];
  const FALLBACK_STATUS_NAMES = {
    "23536": "Wache",
    "9868": "30 Minuten",
    "9869": "60 Minuten",
    "23518": "90 Minuten",
    "9870": "nicht einsatzbereit"
  };

  // Aktuell gueltige Zuordnungen (werden bei jedem Datenabruf neu abgeleitet)
  let groupMapping = Object.assign({}, FALLBACK_GROUP_MAPPING);
  let statusOrder = FALLBACK_STATUS_ORDER.slice();
  let statusNames = Object.assign({}, FALLBACK_STATUS_NAMES);
  let monitorKey = null;

  // Divera liefert Cluster-Listen als Objekt id -> {name, sorting, ...}
  // oder als Array von Objekten mit id. Beides in [{id, name, sorting}] wandeln.
  function clusterEntries(obj) {
    const out = [];
    if (!obj || typeof obj !== "object") return out;
    if (Array.isArray(obj)) {
      obj.forEach(e => {
        if (e && typeof e === "object" && e.name != null && e.id != null) {
          out.push({ id: Number(e.id), name: String(e.name), sorting: Number(e.sorting) });
        }
      });
      return out;
    }
    for (const key of Object.keys(obj)) {
      const e = obj[key];
      if (!e || typeof e !== "object" || e.name == null) continue;
      const id = Number(e.id != null ? e.id : key);
      if (Number.isNaN(id)) continue;
      out.push({ id: id, name: String(e.name), sorting: Number(e.sorting) });
    }
    return out;
  }

  function sortBySorting(list) {
    return list.sort((a, b) => {
      const sa = Number.isNaN(a.sorting) ? Number.MAX_SAFE_INTEGER : a.sorting;
      const sb = Number.isNaN(b.sorting) ? Number.MAX_SAFE_INTEGER : b.sorting;
      if (sa !== sb) return sa - sb;
      return a.name.localeCompare(b.name, "de");
    });
  }

  function deriveMappings(data) {
    const cluster = data && data.cluster ? data.cluster : null;

    // Gruppen
    const groups = sortBySorting(clusterEntries(cluster && cluster.group));
    if (groups.length > 0) {
      groupMapping = {};
      groups.forEach(g => {
        // Namen doppelt? ID anhaengen, damit kein Eintrag verloren geht.
        let name = g.name;
        if (groupMapping[name] !== undefined) name = name + " (" + g.id + ")";
        groupMapping[name] = g.id;
      });
    } else {
      groupMapping = Object.assign({}, FALLBACK_GROUP_MAPPING);
    }

    // Status
    const stati = sortBySorting(clusterEntries(cluster && cluster.status));
    if (stati.length > 0) {
      statusOrder = stati.map(s => s.id);
      statusNames = {};
      stati.forEach(s => { statusNames[String(s.id)] = s.name; });
    } else {
      statusOrder = FALLBACK_STATUS_ORDER.slice();
      statusNames = Object.assign({}, FALLBACK_STATUS_NAMES);
    }

    // Monitor: "2" bevorzugen, sonst erster vorhandener Schluessel
    monitorKey = null;
    if (data && data.monitor && typeof data.monitor === "object") {
      const keys = Object.keys(data.monitor);
      if (keys.includes("2")) monitorKey = "2";
      else if (keys.length > 0) monitorKey = keys[0];
    }
  }

  function getUserCategory(userQuals) {
    if (userQuals.some(q => [136, 101, 8710].includes(q))) return "Führungskräfte";
    if (userQuals.some(q => [193].includes(q))) return "Einsatzkräfte";
    if (userQuals.some(q => [135, 134, 8709].includes(q))) return "Unterführende";
    return "Einsatzkräfte";
  }

  /**********************************************
   * Daten abrufen und UI aktualisieren
   **********************************************/
  let dataCache = null;

  async function fetchData() {
    let res;
    try {
      res = await fetch(DIVERA_API + "?action=all", { credentials: "same-origin" });
    } catch (error) {
      console.error("Fehler beim Abrufen der Daten:", error);
      showError("network");
      return;
    }
    let json = null;
    try { json = await res.json(); } catch (e) { json = null; }
    if (!res.ok || !json || json.error) {
      const code = (json && json.error) || ("http_" + res.status);
      if (code === "unauthorized") {
        window.location.replace("/login?redirect=" + encodeURIComponent(location.pathname + location.search));
        return;
      }
      console.error("Divera-Daten nicht ladbar:", code);
      showError(code);
      return;
    }
    if (!json.data || typeof json.data !== "object") {
      showError("divera_upstream");
      return;
    }
    clearError();
    dataCache = json.data;
    deriveMappings(dataCache);
    updateFilters();
    updateUI();
  }

  /**********************************************
   * Filter-Dropdowns einrichten
   **********************************************/
  // Auswahl ueber Datenaktualisierungen hinweg merken (Wert -> checked)
  let selectedGroups = null;   // null = noch nie gesetzt -> alles an
  let selectedStatus = null;

  function rememberSelection() {
    const g = document.getElementById("divera_filter-group-dropdown");
    const s = document.getElementById("divera_filter_status-dropdown");
    if (g && g.querySelector("input")) selectedGroups = getSelectedValuesFromDropdown("divera_filter-group-dropdown");
    if (s && s.querySelector("input")) selectedStatus = getSelectedValuesFromDropdown("divera_filter_status-dropdown");
  }

  function buildOptions(dropdownId, labelText, allLabel, entries, remembered) {
    const dropdown = document.getElementById(dropdownId);
    if (!dropdown) return;
    const label = dropdown.querySelector(".divera_dropdown-label");
    const options = dropdown.querySelector(".divera_dropdown-options");
    if (!label || !options) return;
    label.textContent = labelText;
    options.innerHTML = "";
    const allOption = document.createElement("label");
    allOption.innerHTML = `<input type="checkbox" value="all"> ${_esc(allLabel)}`;
    options.appendChild(allOption);
    entries.forEach(([value, name]) => {
      const option = document.createElement("label");
      option.innerHTML = `<input type="checkbox" value="${_esc(value)}"> ${_esc(name)}`;
      options.appendChild(option);
    });
    // Auswahl wiederherstellen (neue Eintraege: an, wenn vorher "alle" an war)
    const checkboxes = options.querySelectorAll("input[type='checkbox']");
    if (!remembered) {
      checkboxes.forEach(cb => { cb.checked = true; });
    } else {
      const allWasOn = remembered.includes("all");
      checkboxes.forEach(cb => {
        if (cb.value === "all") cb.checked = allWasOn;
        else cb.checked = allWasOn || remembered.includes(cb.value);
      });
    }
    checkboxes.forEach(cb => cb.addEventListener("change", onFilterChange));
  }

  function onFilterChange(e) {
    const dropdown = e.target.closest(".divera_dropdown");
    const checkboxes = dropdown.querySelectorAll("input[type='checkbox']");
    const allBox = dropdown.querySelector("input[value='all']");
    if (e.target.value === "all") {
      checkboxes.forEach(chk => { chk.checked = e.target.checked; });
    } else {
      if (!e.target.checked) {
        if (allBox) allBox.checked = false;
      } else {
        let allIndividualChecked = true;
        checkboxes.forEach(chk => {
          if (chk.value !== "all" && !chk.checked) { allIndividualChecked = false; }
        });
        if (allIndividualChecked && allBox) allBox.checked = true;
      }
    }
    rememberSelection();
    updateUI();
  }

  function updateFilters() {
    rememberSelection();
    buildOptions("divera_filter-group-dropdown", "Gruppe", "Alle Gruppen",
      Object.entries(groupMapping).map(([name, id]) => [String(id), name]), selectedGroups);
    buildOptions("divera_filter_status-dropdown", "Status", "Alle Status",
      statusOrder.map(sid => [String(sid), statusNames[String(sid)] || String(sid)]), selectedStatus);
  }

  function getSelectedValuesFromDropdown(dropdownId) {
    const dropdown = document.getElementById(dropdownId);
    if (!dropdown) return ["all"];
    const checkboxes = dropdown.querySelectorAll("input[type='checkbox']");
    const values = [];
    checkboxes.forEach(cb => {
      if (cb.checked) { values.push(cb.value); }
    });
    return values;
  }

  function bindDropdownToggle(dropdownId) {
    const dropdown = document.getElementById(dropdownId);
    if (!dropdown) return;
    const label = dropdown.querySelector(".divera_dropdown-label");
    const options = dropdown.querySelector(".divera_dropdown-options");
    if (!label || !options) return;
    label.addEventListener("click", (e) => {
      e.stopPropagation();
      options.style.display = (options.style.display === "block") ? "none" : "block";
    });
    // Klicks in der Optionsliste schliessen das Fenster nicht
    options.addEventListener("click", (e) => e.stopPropagation());
  }

  // Starte beide Dropdowns geschlossen
  document.querySelectorAll(".divera_dropdown-options").forEach(opt => {
    opt.style.display = "none";
  });

  bindDropdownToggle("divera_filter-group-dropdown");
  bindDropdownToggle("divera_filter_status-dropdown");

  window.addEventListener("click", () => {
    document.querySelectorAll(".divera_dropdown-options").forEach(opt => {
      opt.style.display = "none";
    });
  });

  /**********************************************
   * UI aktualisieren – Gruppen und Detailzeilen
   **********************************************/
  function updateUI() {
    const entrys = entrysEl;
    entrys.innerHTML = "";
    if (!dataCache) return;

    let groupFilter = getSelectedValuesFromDropdown("divera_filter-group-dropdown");
    let statusFilter = getSelectedValuesFromDropdown("divera_filter_status-dropdown");

    if (groupFilter.includes("all")) { groupFilter = null; }
    if (statusFilter.includes("all")) { statusFilter = null; }

    // Falls bei Gruppen keine Checkbox ausgewählt ist, leere Liste anzeigen
    if (groupFilter !== null && groupFilter.length === 0) {
      entrys.innerHTML = "";
      return;
    }

    let sumFuehrung = 0, sumUnter = 0, sumEinsatz = 0, sumTotal = 0;
    let groupsAdded = 0;

    // Nutzer je Status aus dem Monitor (Schluessel aus deriveMappings)
    const monitor = (monitorKey !== null && dataCache.monitor) ? dataCache.monitor[monitorKey] : null;
    let usersByStatus = {};
    if (monitor && typeof monitor === "object") {
      for (const statusId in monitor) {
        if (isNaN(statusId)) continue;
        if (statusFilter !== null && !statusFilter.includes(statusId)) continue;
        const bucket = monitor[statusId];
        usersByStatus[statusId] = bucket && Array.isArray(bucket.all) ? bucket.all : [];
      }
    }
    let userEntries = [];
    for (const arr of Object.values(usersByStatus)) {
      if (Array.isArray(arr)) { userEntries = userEntries.concat(arr); }
    }
    const uniqueUsers = {};
    userEntries.forEach(entry => {
      if (entry && entry.id != null) uniqueUsers[entry.id] = entry;
    });

    const consumers = (dataCache.cluster && dataCache.cluster.consumer && typeof dataCache.cluster.consumer === "object")
      ? dataCache.cluster.consumer : {};

    // Für jede Gruppe
    for (const [groupName, groupId] of Object.entries(groupMapping)) {
      if (groupFilter !== null && !groupFilter.includes(String(groupId))) continue;

      let groupUsers = [];
      for (const userId in consumers) {
        const consumer = consumers[userId];
        if (!consumer || !uniqueUsers[userId]) continue;
        const groups = toQuals(consumer.groups);
        if (groups.includes(Number(groupId))) {
          groupUsers.push({ id: userId, consumer, quals: toQuals(consumer.qualifications) });
        }
      }
      let counts = { "Führungskräfte": 0, "Unterführende": 0, "Einsatzkräfte": 0 };
      let sortedUsers = { "Führungskräfte": [], "Unterführende": [], "Einsatzkräfte": [] };
      groupUsers.forEach(userObj => {
        const cat = getUserCategory(userObj.quals);
        counts[cat]++;
        sortedUsers[cat].push(userObj);
      });
      const total = groupUsers.length;
      if (statusFilter !== null && statusFilter.length === 0) {
        counts = { "Führungskräfte": 0, "Unterführende": 0, "Einsatzkräfte": 0 };
      }
      sumFuehrung += counts["Führungskräfte"];
      sumUnter += counts["Unterführende"];
      sumEinsatz += counts["Einsatzkräfte"];
      sumTotal += total;
      groupsAdded++;

      const container = document.createElement("div");
      container.className = "divera_group-container";

      const row = document.createElement("div");
      row.className = "divera_group-row";
      row.innerHTML = `<span style="color: blue;">${_esc(groupName)}</span>
                       <span>${counts["Führungskräfte"]} / ${counts["Unterführende"]} / ${counts["Einsatzkräfte"]} // <u style="text-decoration: underline double;">${total}</u></span>`;

      // Klick auf die Gruppenzeile: Toggle der Detailanzeige
      row.addEventListener("click", (e) => {
        e.stopPropagation();
        let details = container.querySelector(".divera_details");
        if (details) {
          details.style.display = (details.style.display === "none") ? "block" : "none";
        } else {
          details = createDetailContainer(sortedUsers);
          details.style.display = "block";
          container.appendChild(details);
        }
      });

      container.appendChild(row);
      entrys.appendChild(container);
    }

    if (groupsAdded > 0) {
      const spacer = document.createElement("div");
      spacer.style.height = "10px";
      entrys.appendChild(spacer);
    }

    const totalContainer = document.createElement("div");
    totalContainer.className = "divera_group-container";
    const gesamtRow = document.createElement("div");
    gesamtRow.className = "divera_group-row";
    gesamtRow.style.fontWeight = "bold";
    gesamtRow.innerHTML = `<span>Gesamt</span>
                           <span>${sumFuehrung} / ${sumUnter} / ${sumEinsatz} // <u style="text-decoration: underline double;">${sumTotal}</u></span>`;
    totalContainer.appendChild(gesamtRow);
    entrys.appendChild(totalContainer);
  }

  // Erzeugt Detailanzeige (divera_details) für eine Gruppe – initial geschlossen
  function createDetailContainer(sortedUsers) {
    const detailContainer = document.createElement("div");
    detailContainer.className = "divera_details";
    detailContainer.style.display = "none";
    const allNameCells = [];

    ["Führungskräfte", "Unterführende", "Einsatzkräfte"].forEach(category => {
      if (sortedUsers[category].length > 0) {
        const catHeader = document.createElement("div");
        catHeader.style.fontWeight = "bold";
        catHeader.textContent = category;
        detailContainer.appendChild(catHeader);

        const table = document.createElement("div");
        table.className = "divera_detail-table";
        sortedUsers[category].forEach(userObj => {
          const detailRow = document.createElement("div");
          detailRow.className = "divera_detail-row";

          const nameCell = document.createElement("div");
          nameCell.className = "divera_person-name";
          const c = userObj.consumer;
          const personName = c.stdformat_name || ((c.firstname || "") + " " + (c.lastname || "")).trim();
          nameCell.textContent = personName;
          detailRow.appendChild(nameCell);
          allNameCells.push(nameCell);

          const symbols = getSymbolColumns(userObj.quals);
          const filteredSymbols = symbols.filter(s => s.trim() !== "");
          const iconHTML = filteredSymbols.length > 0 ? "(" + filteredSymbols.join(", ") + ")" : "";
          const iconCell = document.createElement("div");
          iconCell.className = "divera_person-symbol-cell";
          iconCell.innerHTML = iconHTML;
          detailRow.appendChild(iconCell);

          iconCell.querySelectorAll(".divera_qualification-icon").forEach(iconEl => {
            iconEl.addEventListener("mouseenter", () => {
              const rect = iconEl.getBoundingClientRect();
              showTooltip(rect.left + rect.width / 2, rect.top, iconEl.getAttribute("data-qual"));
            });
            iconEl.addEventListener("mouseleave", hideTooltip);
          });

          nameCell.addEventListener("click", (e) => {
            e.stopPropagation();
            showTooltip(e.clientX, e.clientY, getQualificationNames(userObj.quals));
          });
          nameCell.addEventListener("mouseenter", (e) => {
            showTooltip(e.clientX, e.clientY, getQualificationNames(userObj.quals));
          });
          nameCell.addEventListener("mouseleave", hideTooltip);

          table.appendChild(detailRow);
        });
        detailContainer.appendChild(table);
      }
    });

    // Einheitliche Namensbreite
    let maxNameWidth = 0;
    allNameCells.forEach(cell => {
      if (cell.offsetWidth > maxNameWidth) maxNameWidth = cell.offsetWidth;
    });
    allNameCells.forEach(cell => {
      cell.style.width = maxNameWidth + "px";
    });

    return detailContainer;
  }

  function getQualificationNames(qualIds) {
    const names = [];
    const q = dataCache && dataCache.cluster ? dataCache.cluster.qualification : null;
    if (q && typeof q === "object") {
      for (const qid of qualIds) {
        const entry = q[qid];
        if (entry && entry.name != null) names.push(String(entry.name));
      }
    }
    return names.join(", ");
  }

  /**********************************************
   * Tooltip – fixed position, direkt am Mauszeiger
   **********************************************/
  function showTooltip(x, y, text) {
    const tooltip = document.getElementById("divera_tooltip");
    if (!tooltip) return;
    tooltip.textContent = text || "";
    tooltip.style.position = "fixed";
    tooltip.style.left = (x + 10) + "px";
    tooltip.style.top = (y - 20) + "px";
    tooltip.style.display = text ? "block" : "none";
  }

  function hideTooltip() {
    const tooltip = document.getElementById("divera_tooltip");
    if (tooltip) tooltip.style.display = "none";
  }

  /**********************************************
   * Start und periodische Aktualisierung (3 min)
   **********************************************/
  fetchData();
  setInterval(fetchData, 180000);
})();
