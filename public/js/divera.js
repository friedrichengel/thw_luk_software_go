/*
 * divera.js - DiveraMap: Fahrzeugstatus (FMS) und Einsaetze aus Divera 24/7
 * auf einer Leaflet-Karte mit Fahrzeugliste, PDF-Druck und Status-Rueckmeldung.
 *
 * Anbindung an den Server:
 *   - Daten kommen vom Server-Endpunkt /api/divera (Sitzung + Divera-Recht
 *     der Organisation; der Browser sieht nie einen Schluessel):
 *       GET  /api/divera?action=vehicles           (Divera pull/vehicle-status, 1:1)
 *       GET  /api/divera?action=operations         (Divera alarms, 1:1)
 *       GET  /api/divera?action=download&id=N      (Einsatz-PDF)
 *       POST /api/divera?action=set_status&id=N    (JSON-Body inkl. csrf)
 *     Fehler: {error: "divera_disabled" | "divera_forbidden" | "divera_upstream" | "unauthorized"}
 *   - CSRF-Token aus window.STAB.csrf (im JSON-Body, kein X-CSRF-Header).
 *   - Status setzen nur mit window.STAB.divera.write.
 *   - Kacheln/Kartenzentrum aus window.CURRENT_OV (tile_url, lat, lng).
 */

/* --- Konfiguration aus window.STAB / window.CURRENT_OV --- */
const DIVERA_API = "/api/divera";
const DIVERA_CFG = (window.STAB && window.STAB.divera) || {};
const DIVERA_CAN_WRITE = !!DIVERA_CFG.write;

function diveraCsrf() {
  return (window.STAB && window.STAB.csrf) || "";
}

const DIVERA_ERROR_TEXT = {
  unauthorized: "Sitzung abgelaufen. Bitte neu anmelden.",
  divera_disabled: "Divera ist für Ihre Organisation nicht eingerichtet.",
  divera_forbidden: "Keine Berechtigung für Divera.",
  divera_upstream: "Divera antwortet nicht. Daten können veraltet sein.",
  divera_auth_failed: "Divera lehnt den Schlüssel der Organisation ab. Bitte Zugang unter „Organisation verwalten“ prüfen.",
  network: "Keine Verbindung zum Server.",
};

function diveraErrorText(code) {
  return DIVERA_ERROR_TEXT[code] || ("Fehler beim Laden der Divera-Daten (" + code + ").");
}

/* --- Hilfsfunktion: Drittinhalte vor innerHTML escapen (Schutz gegen XSS aus Divera-Daten) --- */
function _esc(s) {
  const d = document.createElement("div");
  d.textContent = String(s == null ? "" : s);
  return d.innerHTML;
}

/* --- Fehlermeldung in der Fahrzeugliste anzeigen --- */
function showDiveraError(code) {
  const listContainer = document.getElementById("vehicleList");
  if (!listContainer) return;
  let el = document.getElementById("divera-error");
  if (!el) {
    el = document.createElement("div");
    el.id = "divera-error";
    el.className = "divera-error";
    listContainer.parentNode.insertBefore(el, listContainer);
  }
  el.textContent = diveraErrorText(code);
  el.style.display = "block";
}

function clearDiveraError() {
  const el = document.getElementById("divera-error");
  if (el) el.style.display = "none";
}

/*
 * diveraFetch: Aufruf an /api/divera mit einheitlicher Fehlerbehandlung.
 * Liefert das JSON (bzw. die Response bei raw=true) oder wirft einen Error
 * mit .code. Bei "unauthorized" wird sofort zum Login umgeleitet.
 */
async function diveraFetch(action, params, opts) {
  opts = opts || {};
  const url = new URL(DIVERA_API, window.location.origin);
  url.searchParams.set("action", action);
  if (params) {
    Object.keys(params).forEach((k) => {
      if (params[k] !== undefined && params[k] !== null) url.searchParams.set(k, String(params[k]));
    });
  }
  const init = { method: opts.method || "GET", credentials: "same-origin" };
  if (opts.body) {
    init.headers = { "Content-Type": "application/json" };
    init.body = JSON.stringify(Object.assign({ csrf: diveraCsrf() }, opts.body));
  }
  let res;
  try {
    res = await fetch(url.toString(), init);
  } catch (e) {
    const err = new Error("network");
    err.code = "network";
    throw err;
  }
  if (opts.raw) {
    if (!res.ok) {
      let code = "http_" + res.status;
      try {
        const data = await res.json();
        if (data && data.error) code = data.error;
      } catch (e) { /* kein JSON */ }
      if (code === "unauthorized") redirectToLogin();
      const err = new Error(code);
      err.code = code;
      throw err;
    }
    return res;
  }
  let data = null;
  try { data = await res.json(); } catch (e) { data = null; }
  if (!res.ok || !data || data.error) {
    const code = (data && data.error) || ("http_" + res.status);
    if (code === "unauthorized") {
      redirectToLogin();
      await new Promise(() => {});
    }
    const err = new Error(code);
    err.code = code;
    throw err;
  }
  return data;
}

function redirectToLogin() {
  window.location.replace("/login?redirect=" + encodeURIComponent(location.pathname + location.search));
}

/* --- Hilfsfunktionen zum Laden/Speichern von Klick-Zuständen in localStorage --- */
function loadClickStates(key) {
  try {
    const data = localStorage.getItem(key);
    return data ? JSON.parse(data) : {};
  } catch (e) {
    return {};
  }
}

function saveClickStates(key, states) {
  try {
    localStorage.setItem(key, JSON.stringify(states));
  } catch (e) { /* privater Modus o. ae. */ }
}

function updateMidAlert(alarm) {
  const midAlert = document.getElementById('mid-alert');
  if (!midAlert) return;
  midAlert.innerHTML = '';
  if (alarm) {
    midAlert.innerHTML = `<img src="/images/Blaulicht_slower.gif" alt="Blaulicht" style="height:84px;">`;
  }
}

/* --- Leaflet-Karte initialisieren --- */
// Heimatstandort der Organisation (window.STAB.divera.home aus den Stammdaten des OV);
// ohne ihn bleibt das Kartenzentrum der Installation.
const DIVERA_HOME = (window.STAB && window.STAB.divera && window.STAB.divera.home
  && isFinite(window.STAB.divera.home.lat) && isFinite(window.STAB.divera.home.lng))
  ? { lat: Number(window.STAB.divera.home.lat), lng: Number(window.STAB.divera.home.lng), name: String(window.STAB.divera.home.name || "") }
  : null;
const DIVERA_CENTER = DIVERA_HOME
  ? [DIVERA_HOME.lat, DIVERA_HOME.lng]
  : (window.CURRENT_OV && isFinite(window.CURRENT_OV.lat) && isFinite(window.CURRENT_OV.lng))
    ? [Number(window.CURRENT_OV.lat), Number(window.CURRENT_OV.lng)]
    : [52.5200, 13.4050];
const DIVERA_TILE_URL = (window.CURRENT_OV && window.CURRENT_OV.tile_url) || "/tiles/{z}/{x}/{y}.png";

var map = L.map("map").setView(DIVERA_CENTER, 11);
L.tileLayer(DIVERA_TILE_URL, {
  maxZoom: 19,
  attribution: "&copy; OpenStreetMap",
}).addTo(map);

// Zoomfaktor beim Anspringen eines Markers
var jumpZoomFactor = 18;

/* --- Globale Variablen --- */
let vehicleMarkers = {};   // Fahrzeugmarker auf der Karte
let vehicleData = [];      // Fahrzeugdaten

let operationMarkers = {}; // Einsatzmarker auf der Karte
let operationsData = [];   // Einsatzdaten

/* --- Mapping der fmsstatus-Zahlen zu Texten --- */
const statusTexts = {
  0: "Notruf",
  1: "Einsatzbereit (Funk)",
  2: "Einsatzbereit (Wache)",
  3: "Einsatz übernommen",
  4: "Einsatzort eingetroffen",
  5: "Sprechwunsch",
  6: "Nicht Einsatzbereit",
  7: "Anmeldung",
  8: "Abmeldung",
  9: "Fremdanmeldung",
};

let activeVehicleOps = {}; // { [vehicleId]: displayId }

/* --- Formatiert die taktische Zeit --- */
function formatTacticalTime(ts) {
  const date = new Date(Number(ts) * 1000);
  if (typeof getTacticalTime === "function") {
    return getTacticalTime("clock", date);
  }
  return isNaN(date.getTime()) ? "" : date.toLocaleString("de-DE");
}

/* --- Blaulicht abhängig von Status 0 ODER Status 5 mit Unread-Stern --- */
function recalcMidAlert() {
  const vehicleClicks = loadClickStates("vehicleClicks");
  const hasAlarm0 = vehicleData.some(v => v.fmsstatus === 0);
  const hasUnreadS5 = vehicleData.some(v => v.fmsstatus === 5 && isVehicleUnread(v, vehicleClicks));

  updateMidAlert(hasAlarm0 || hasUnreadS5);

  // kompletter Seitenhintergrund rot bei Status 0
  if (hasAlarm0) {
    document.body.classList.add('status-0-background');
  } else {
    document.body.classList.remove('status-0-background');
  }
}

/* === Änderungsdetektion via Signatur (persistiert über Reload) === */

function getVehicleSig(v) {
  return JSON.stringify({
    id: v.id,
    fmsstatus: v.fmsstatus,
    note: v.fmsstatus_note,
    ts: v.fmsstatus_ts,
    lat: Math.round(Number(v.lat) * 1e5) / 1e5,
    lng: Math.round(Number(v.lng) * 1e5) / 1e5,
    name: v.name,
    number: v.number
  });
}

function getOperationSig(op) {
  return JSON.stringify({
    id: op.id,
    foreign_id: (op.foreign_id ?? "").toString().trim(),
    closed: !!op.closed,
    date: op.date,
    priority: !!op.priority,
    title: op.title,
    text: op.text,
    address: op.address,
    report: op.report,
    vehicle: Array.isArray(op.vehicle) ? [...op.vehicle].map(x => String(x)).sort() : []
  });
}

function isVehicleUnread(v, vehicleClicks) {
  const state = vehicleClicks[v.id] || {};
  const currentSig = getVehicleSig(v);
  return state.seenSig ? state.seenSig !== currentSig : false;
}

function isOperationUnread(op, operationClicks) {
  const state = operationClicks[op.id] || {};
  const currentSig = getOperationSig(op);
  return state.seenSig ? state.seenSig !== currentSig : false;
}

function markVehicleSeen(vId, vehicleObj) {
  const clicks = loadClickStates("vehicleClicks");
  const st = clicks[vId] || {};
  st.seenSig = getVehicleSig(vehicleObj);
  clicks[vId] = st;
  saveClickStates("vehicleClicks", clicks);
}

function markOperationSeen(opId, opObj) {
  const clicks = loadClickStates("operationClicks");
  const st = clicks[opId] || {};
  st.seenSig = getOperationSig(opObj);
  clicks[opId] = st;
  saveClickStates("operationClicks", clicks);
}

/* Initiale "gesehen"-Baseline für Fahrzeuge: nur setzen, wenn noch nicht vorhanden */
function ensureVehicleSeenBaseline() {
  const clicks = loadClickStates("vehicleClicks");
  let changed = false;
  for (const v of vehicleData) {
    const st = clicks[v.id] || {};
    if (st.seenSig === undefined) {
      st.seenSig = getVehicleSig(v);
      clicks[v.id] = st;
      changed = true;
    }
  }
  if (changed) saveClickStates("vehicleClicks", clicks);
}

/* --- Fahrzeugdaten laden --- */
async function fetchVehicleData() {
  try {
    const json = await diveraFetch("vehicles");
    if (json && json.success && Array.isArray(json.data)) {
      clearDiveraError();
      vehicleData = json.data;
      ensureVehicleSeenBaseline();
      updateVehicles();
    } else {
      console.error("API meldet einen Fehler bei Fahrzeugdaten");
      showDiveraError("divera_upstream");
    }
  } catch (error) {
    console.error("Fehler beim Abrufen der Fahrzeugdaten:", error);
    showDiveraError(error && error.code ? error.code : "network");
  }
}

/* --- Fahrzeugmarker aktualisieren --- */
function updateVehicles() {
  let vehicleClicks = loadClickStates("vehicleClicks");

  for (let id in vehicleMarkers) {
    map.removeLayer(vehicleMarkers[id]);
  }
  vehicleMarkers = {};

  // Gruppiere Fahrzeuge (für räumlichen Offset)
  let groups = {};
  vehicleData.forEach((v) => {
    if (!hasPosition(v)) return;
    const key = Number(v.lat).toFixed(5) + "_" + Number(v.lng).toFixed(5);
    if (!groups[key]) groups[key] = [];
    groups[key].push(v);
  });

  vehicleData.forEach((vehicle) => {
    if (!hasPosition(vehicle)) return;

    if (!vehicleClicks[vehicle.id]) {
      vehicleClicks[vehicle.id] = { status: vehicle.fmsstatus, clicked: false };
      if (vehicle.fmsstatus === 0 || vehicle.fmsstatus === 5) {
        vehicleClicks[vehicle.id].previousStatus = 2;
      }
    } else {
      if (vehicleClicks[vehicle.id].status !== vehicle.fmsstatus) {
        if (vehicle.fmsstatus === 0 || vehicle.fmsstatus === 5) {
          if (vehicleClicks[vehicle.id].previousStatus === undefined) {
            vehicleClicks[vehicle.id].previousStatus = vehicleClicks[vehicle.id].status;
          }
        } else if (vehicleClicks[vehicle.id].previousStatus !== undefined) {
          delete vehicleClicks[vehicle.id].previousStatus;
        }
        vehicleClicks[vehicle.id].status = vehicle.fmsstatus;
      }
    }

    const coords = getOffsetCoordinates(vehicle, groups);

    const marker = L.marker(coords, { icon: getMarkerIcon(vehicle) });
    marker.bindPopup(getPopupContent(vehicle));

    marker.on("click", function () {
      markVehicleSeen(vehicle.id, vehicle);
      marker.setIcon(getMarkerIcon(vehicle));
      marker.openPopup();
      updateVehicleList();
      ensureVehicleSeenBaseline();
      recalcMidAlert();
    });

    marker.addTo(map);
    vehicleMarkers[vehicle.id] = marker;
  });

  saveClickStates("vehicleClicks", vehicleClicks);
  updateVehicleList();
  recalcMidAlert();
}

function hasPosition(v) {
  const lat = Number(v.lat), lng = Number(v.lng);
  if (!isFinite(lat) || !isFinite(lng)) return false;
  return !(lat === 0 && lng === 0);
}

/* --- Erzeugt den Fahrzeugmarker-Icon (Sternchen hinter Statuszahl, falls ungelesen) --- */
function getMarkerIcon(vehicle) {
  let vehicleClicks = loadClickStates("vehicleClicks");
  const star = isVehicleUnread(vehicle, vehicleClicks) ? "*" : "";
  const unique = new Date().getTime();
  const classes = "vehicle-marker-content status-" + _esc(vehicle.fmsstatus);

  const opId = activeVehicleOps[vehicle.id];
  const opBadge = opId ? `<span class="marker-opid">#${_esc(opId)}</span>` : "";

  const html = `
    <div class="marker-with-badge" data-t="${unique}">
      <div class="${classes}">
        <span class="marker-status">${_esc(vehicle.fmsstatus)}${star}</span>
        <span class="marker-name">${_esc(vehicle.name)}</span>
      </div>
      ${opBadge}
    </div>
  `;

  return L.divIcon({
    html: html,
    className: "",
    iconSize: null,
  });
}

/* --- Gruppierung von Fahrzeugen mit nahe beieinanderliegenden Koordinaten --- */
function getOffsetCoordinates(vehicle, groups) {
  const key = Number(vehicle.lat).toFixed(5) + "_" + Number(vehicle.lng).toFixed(5);
  const group = groups[key];
  if (group && group.length > 1) {
    const index = group.findIndex((v) => v.id === vehicle.id);
    const offsetLat = (index - (group.length - 1) / 2) * 0.0001;
    return [Number(vehicle.lat) + offsetLat, Number(vehicle.lng)];
  }
  return [Number(vehicle.lat), Number(vehicle.lng)];
}

/* --- Inhalt des Popup-Overlays für Fahrzeugmarker --- */
function getPopupContent(vehicle) {
  return `
    <div>
      <strong>Funkrufname:</strong> ${_esc(vehicle.name)}<br>
      <strong>Name:</strong> ${_esc(vehicle.fullname)} / ${_esc(vehicle.shortname)}<br>
      <strong>Kennzeichen:</strong> ${_esc(vehicle.number)}<br>
      <strong>FMS-Status:</strong> ${_esc(vehicle.fmsstatus)} - ${_esc(statusTexts[vehicle.fmsstatus] || "")}<br>
      <strong>FMS-Note:</strong> ${_esc(vehicle.fmsstatus_note)}<br>
      <strong>Zeitpunkt:</strong> ${_esc(formatTacticalTime(vehicle.fmsstatus_ts))}
    </div>
  `;
}

/* --- Status setzen (POST /api/divera?action=set_status&id=N) --- */
async function setVehicleStatus(vehicleId, payload) {
  if (!DIVERA_CAN_WRITE) {
    const err = new Error("divera_forbidden");
    err.code = "divera_forbidden";
    throw err;
  }
  return diveraFetch("set_status", { id: vehicleId }, { method: "POST", body: payload });
}

/* --- Zurücksetzen des Fahrzeugstatus auf "Einsatzbereit (Wache)" (Home-Icon) --- */
async function resetVehicleStatus(vehicleId) {
  const data = {
    status: 2,
    status_id: 2,
    status_note: "Rückgesetzt in OV",
  };
  // Heimatstandort: Standort des Ortsverbands aus den Stammdaten der Organisation.
  // Ohne Standort (RSt/LV oder Organisation ohne Stammdaten-Bezug) bleibt die
  // Position in Divera unveraendert statt irgendwohin zu springen.
  if (DIVERA_HOME) {
    data.lat = DIVERA_HOME.lat;
    data.lng = DIVERA_HOME.lng;
  }
  try {
    const result = await setVehicleStatus(vehicleId, data);
    console.log("Status erfolgreich zurückgesetzt:", result);
    return result;
  } catch (error) {
    console.error("Fehler beim Zurücksetzen des Status:", error);
    showDiveraError(error && error.code ? error.code : "network");
    throw error;
  }
}

/* --- Rückmelden des vorherigen Fahrzeugstatus --- */
async function revertVehicleStatus(vehicleId, prevStatus, actStatus) {
  const data = {
    status: prevStatus,
    status_id: prevStatus,
    status_note: "Vorher: " + (statusTexts[actStatus] || actStatus),
  };
  try {
    const result = await setVehicleStatus(vehicleId, data);
    console.log("Fahrzeugstatus erfolgreich auf vorherigen Zustand zurückgesetzt:", result);
    return result;
  } catch (error) {
    console.error("Fehler beim Zurücksetzen des Status:", error);
    showDiveraError(error && error.code ? error.code : "network");
    throw error;
  }
}

/* --- Bestätigungs-Popup für "Heimatstandort zurücksetzen" --- */
let __confirmResetState = { vehicleId: null };

function __ensureConfirmModal() {
  if (document.getElementById('confirmModal')) return;

  const modal = document.createElement('div');
  modal.id = 'confirmModal';
  modal.className = 'confirm-modal';
  modal.style.display = 'none';
  modal.innerHTML = `
    <div class="confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="confirmLabel">
      <div id="confirmError" class="confirm-error" style="display:none;">JA eingeben</div>
      <label id="confirmLabel" for="confirmInput" class="confirm-label">
        Zur Sicherheit bitte <strong>JA</strong> eingeben.
      </label>
      <input id="confirmInput" class="confirm-input" type="text" autocomplete="off" />
      <div class="confirm-actions">
        <button id="confirmCancel" class="confirm-btn cancel">Abbrechen</button>
        <button id="confirmOk" class="confirm-btn ok">Durchführen</button>
      </div>
    </div>
  `;
  document.body.appendChild(modal);

  const input  = modal.querySelector('#confirmInput');
  const ok     = modal.querySelector('#confirmOk');
  const cancel = modal.querySelector('#confirmCancel');
  const error  = modal.querySelector('#confirmError');

  function close() {
    modal.style.display = 'none';
    error.style.display = 'none';
    input.value = '';
    __confirmResetState.vehicleId = null;
  }

  function submit() {
    const val = (input.value || '').trim().toLowerCase();
    if (val !== 'ja') {
      error.style.display = 'block';
      return;
    }
    error.style.display = 'none';
    const vid = __confirmResetState.vehicleId;
    if (!vid) { close(); return; }

    resetVehicleStatus(vid)
      .then(() => fetchVehicleData())
      .catch(err => console.error('Set-Status fehlgeschlagen', err))
      .finally(() => close());
  }

  ok.addEventListener('click', submit);
  cancel.addEventListener('click', close);
  modal.addEventListener('click', (e) => { if (e.target === modal) cancel.click(); });
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') submit();
    if (e.key === 'Escape') cancel.click();
  });
}

function openConfirmResetDialog(vehicleId) {
  __ensureConfirmModal();
  __confirmResetState.vehicleId = vehicleId;

  const modal = document.getElementById('confirmModal');
  const input = document.getElementById('confirmInput');
  const error = document.getElementById('confirmError');

  error.style.display = 'none';
  input.value = '';
  modal.style.display = 'flex';
  setTimeout(() => input.focus(), 0);
}

/* --- Fahrzeugliste aktualisieren --- */
function updateVehicleList() {
  const listContainer = document.getElementById("vehicleList");
  if (!listContainer) return;
  listContainer.innerHTML = "";
  let vehicleClicks = loadClickStates("vehicleClicks");

  // Sortiere Fahrzeuge absteigend nach fmsstatus_ts (neueste zuerst)
  const sortedVehicles = [...vehicleData].sort((a, b) => Number(b.fmsstatus_ts) - Number(a.fmsstatus_ts));

  sortedVehicles.forEach((vehicle) => {
    const unread = isVehicleUnread(vehicle, vehicleClicks);
    const star = unread ? "*" : "";
    const entry = document.createElement("div");
    entry.className = "vehicle-entry status-" + vehicle.fmsstatus;
    entry.id = "vehicle-entry-" + vehicle.id;
    entry.dataset.timestamp = vehicle.fmsstatus_ts;

    const statusDiv = document.createElement("div");
    statusDiv.className = "vehicle-status";
    statusDiv.textContent = vehicle.fmsstatus + star;

    const infoDiv = document.createElement("div");
    infoDiv.className = "vehicle-info";
    infoDiv.innerHTML = `<div>${_esc(vehicle.number)} - ${_esc(vehicle.name)}</div>
                         <div>${_esc(vehicle.fmsstatus_note)}</div>`;

    const opId = activeVehicleOps[vehicle.id];
    let opBadgeEl = null;
    if (opId) {
      opBadgeEl = document.createElement("div");
      opBadgeEl.className = "vehicle-opid";
      opBadgeEl.textContent = `#${opId}`;
    }

    // Home-Icon (Status zuruecksetzen) nur mit Schreibrecht
    let homeIcon = null;
    if (DIVERA_CAN_WRITE) {
      homeIcon = document.createElement("div");
      homeIcon.className = "home-icon";
      homeIcon.title = "Status auf „Einsatzbereit (Wache)“ zurücksetzen";
      homeIcon.innerHTML = "&#8962;";
      homeIcon.addEventListener("click", (e) => {
        e.stopPropagation();
        openConfirmResetDialog(vehicle.id);
      });
    }

    entry.addEventListener("click", () => {
      markVehicleSeen(vehicle.id, vehicle);
      recalcMidAlert();

      const vc2 = loadClickStates("vehicleClicks");
      const unreadNow = isVehicleUnread(vehicle, vc2);
      statusDiv.textContent = vehicle.fmsstatus + (unreadNow ? "*" : "");

      const marker2 = vehicleMarkers[vehicle.id];
      if (marker2) marker2.setIcon(getMarkerIcon(vehicle));

      // Wenn Status 0 oder 5 -> vorherigen Zustand zurückmelden (nur mit Schreibrecht)
      if (DIVERA_CAN_WRITE && (vehicle.fmsstatus === 0 || vehicle.fmsstatus === 5)) {
        const st = vehicleClicks[vehicle.id] || {};
        const prevStatus = st.previousStatus || 2;
        revertVehicleStatus(vehicle.id, prevStatus, vehicle.fmsstatus)
          .then(() => {
            vehicleClicks[vehicle.id] = vehicleClicks[vehicle.id] || {};
            vehicleClicks[vehicle.id].status = prevStatus;
            delete vehicleClicks[vehicle.id].previousStatus;
            saveClickStates("vehicleClicks", vehicleClicks);
            fetchVehicleData();
          })
          .catch((err) => {
            console.error("Fehler beim Zurückmelden des vorherigen Zustands", err);
          });
      }

      const marker = vehicleMarkers[vehicle.id];
      if (marker) {
        marker.setIcon(getMarkerIcon(vehicle));
        map.setView(marker.getLatLng(), jumpZoomFactor);
        marker.openPopup();
      }
    });

    entry.appendChild(statusDiv);
    entry.appendChild(infoDiv);
    if (opBadgeEl) entry.appendChild(opBadgeEl);
    if (homeIcon) entry.appendChild(homeIcon);
    listContainer.appendChild(entry);
  });
}

/* ===================== */
/*     Einsatzzustände   */
/* ===================== */

/* --- Einsätze laden --- */
async function fetchOperationsData() {
  try {
    const json = await diveraFetch("operations");
    if (json && json.success && json.data) {
      const items = json.data.items;
      operationsData = Array.isArray(items) ? items : Object.values(items || {});
      updateOperations();
    } else {
      console.error("API meldet einen Fehler bei Einsätzen");
    }
  } catch (error) {
    console.error("Fehler beim Abrufen der Einsätze:", error);
    showDiveraError(error && error.code ? error.code : "network");
  }
}

/* --- Einsatzmarker aktualisieren --- */
function updateOperations() {
  let operationClicks = loadClickStates("operationClicks");

  operationsData.forEach((operation) => {
    if (!operation || !hasPosition(operation)) return;
    const coords = [Number(operation.lat), Number(operation.lng)];
    if (
      !operationClicks[operation.id] ||
      operationClicks[operation.id].closed !== operation.closed
    ) {
      operationClicks[operation.id] = { closed: operation.closed, clicked: false };
    }

    const isClosed = operation.closed === true;
    const iconOpts = {
      html: getOperationIconHTML(operation),
      className: isClosed ? "operation-marker-closed" : "",
      iconSize: [80, 80],
      iconAnchor: [40, 0],
    };

    if (operationMarkers[operation.id]) {
      const marker = operationMarkers[operation.id];
      marker.setLatLng(coords);
      marker.setPopupContent(getOperationPopupContent(operation));
      marker.setIcon(L.divIcon(iconOpts));
      if (marker.isPopupOpen()) {
        marker.getPopup().setContent(getOperationPopupContent(operation));
      }
    } else {
      const icon = L.divIcon(iconOpts);
      const marker = L.marker(coords, { icon: icon });
      marker.bindPopup(getOperationPopupContent(operation));
      marker.on("click", function () {
        markOperationSeen(operation.id, operation);
        marker.setIcon(
          L.divIcon({
            html: getOperationIconHTML(operation),
            className: operation.closed === true ? "operation-marker-closed" : "",
            iconSize: [80, 80],
            iconAnchor: [40, 0],
          })
        );
      });
      marker.addTo(map);
      operationMarkers[operation.id] = marker;
    }
  });

  // Fahrzeug -> Einsatz-DisplayId neu aufbauen
  activeVehicleOps = {};
  operationsData.forEach((op) => {
    if (op && op.closed !== true && Array.isArray(op.vehicle) && op.vehicle.length > 0) {
      const disp = getDisplayId(op);
      op.vehicle.forEach((vid) => {
        const vIdNum = parseInt(vid, 10);
        if (!Number.isNaN(vIdNum)) {
          activeVehicleOps[vIdNum] = disp;
        }
      });
    }
  });

  for (const vid in vehicleMarkers) {
    const veh = vehicleData.find(v => String(v.id) === String(vid));
    if (veh) {
      vehicleMarkers[vid].setIcon(getMarkerIcon(veh));
    }
  }

  updateVehicleList();
  saveClickStates("operationClicks", operationClicks);

  // Entferne Einsatzmarker, die nicht mehr vorhanden sind
  for (let id in operationMarkers) {
    if (!operationsData.find((op) => op && op.id == id)) {
      map.removeLayer(operationMarkers[id]);
      delete operationMarkers[id];
    }
  }
}

function getDisplayId(op) {
  const fid = op.foreign_id;
  if (fid === undefined || fid === null) return "kA";
  const s = String(fid).trim();
  return s !== "" ? s : "kA";
}

function getOperationIconHTML(operation) {
  let operationClicks = loadClickStates("operationClicks");
  const unread = isOperationUnread(operation, operationClicks);
  const starElement = unread
    ? `<text x="50%" y="45" text-anchor="middle" fill="white" font-size="24" font-weight="bold">*</text>`
    : "";
  const fillColor = operation.closed === true ? "lightcoral" : "red";
  const displayId = _esc(getDisplayId(operation));

  const idElement = `<text x="50%" y="65" text-anchor="middle" fill="white" font-size="16" font-weight="bold">${displayId}</text>`;

  return `<svg width="80" height="80" viewBox="0 0 80 80" xmlns="http://www.w3.org/2000/svg">
    <polygon points="40,0 0,80 80,80" fill="${fillColor}" stroke="black" stroke-width="4"/>
    ${starElement}
    ${idElement}
  </svg>`;
}

/* --- Inhalt des Einsatz-Popups --- */
function getOperationPopupContent(operation) {
  let vehicleId = (Array.isArray(operation.vehicle) && operation.vehicle.length > 0) ? operation.vehicle[0] : null;
  let vehicleName = vehicleId;
  if (vehicleId) {
    const veh = vehicleData.find((v) => v.id == vehicleId);
    if (veh) {
      vehicleName = veh.name;
    }
  }
  const vehicleIdNum = parseInt(vehicleId, 10);

  const displayId = getDisplayId(operation);
  return `
    <div class="operation-popup">
      <strong>Einsatznummer:</strong> ${_esc(displayId)}<br>
      <strong>Alarmzeit:</strong> ${_esc(formatTacticalTime(operation.date))}<br>
      <strong>Sonderrechte:</strong> ${operation.priority ? "Ja" : "Nein"}<br>
      <strong>Fahrzeug:</strong> ${
        (vehicleId && !Number.isNaN(vehicleIdNum))
          ? `<a href="#" onclick="jumpToVehicle(${vehicleIdNum}); return false;">${_esc(vehicleName)}</a>`
          : "n/a"
      }<br>
      <strong>Titel:</strong> ${_esc(operation.title)}<br>
      <strong>Beschreibung:</strong> ${_esc(operation.text)}<br>
      <strong>Adresse:</strong> ${_esc(operation.address)}<br>
      <strong>Lagemeldung:</strong> ${_esc(operation.report)}
    </div>
  `;
}

/* --- Springt zur Fahrzeugposition --- */
function jumpToVehicle(vehicleId) {
  const marker = vehicleMarkers[vehicleId];
  if (marker) {
    map.setView(marker.getLatLng(), jumpZoomFactor);
    marker.openPopup();
  }
}

/* --- Periodische Aktualisierungen --- */
fetchVehicleData();
setInterval(fetchVehicleData, 15000);

fetchOperationsData();
setInterval(fetchOperationsData, 15000);

/* --- Print PDF --- */

// Druck-Funktion für die Karte: Schließt alle Popups, erstellt ein Canvas-Screenshot und speichert als PDF.
function printMap() {
  if (!window.jspdf || !window.html2canvas) {
    console.error("jsPDF/html2canvas nicht geladen");
    return;
  }
  const { jsPDF } = window.jspdf;
  map.closePopup();

  const savedOpClicks = loadClickStates("operationClicks");

  const hiddenMarkerEls = [];
  for (let id in vehicleMarkers) {
    const el = vehicleMarkers[id].getElement();
    if (el) {
      hiddenMarkerEls.push(el);
      el.style.display = "none";
    }
  }

  setTimeout(() => {
    html2canvas(document.getElementById("map"), { useCORS: true })
      .then((canvas) => {
        const imgData = canvas.toDataURL("image/png");
        const pdf = new jsPDF("landscape", "mm", "a4");
        const pdfWidth = pdf.internal.pageSize.getWidth();
        const pdfHeight = (canvas.height * pdfWidth) / canvas.width;
        pdf.addImage(imgData, "PNG", 0, 0, pdfWidth, pdfHeight);
        pdf.save("Karte.pdf");

        hiddenMarkerEls.forEach((el) => { el.style.display = ""; });
        saveClickStates("operationClicks", savedOpClicks);
        updateOperations();
      })
      .catch((err) => {
        console.error("Fehler beim Erstellen des Canvas:", err);
        hiddenMarkerEls.forEach((el) => { el.style.display = ""; });
        saveClickStates("operationClicks", savedOpClicks);
        updateOperations();
      });
  }, 300);
}

async function printOperationList() {
  if (!window.PDFLib) {
    console.error("pdf-lib nicht geladen");
    return;
  }
  const sortedOps = [...operationsData].filter(Boolean).sort((a, b) => a.id - b.id);
  const mergedPdf = await PDFLib.PDFDocument.create();

  for (const op of sortedOps) {
    try {
      const response = await diveraFetch("download", { id: op.id }, { raw: true });
      const pdfBytes = await response.arrayBuffer();
      const opPdfDoc = await PDFLib.PDFDocument.load(pdfBytes);
      const opPages = await mergedPdf.copyPages(opPdfDoc, opPdfDoc.getPageIndices());
      opPages.forEach((page) => mergedPdf.addPage(page));
    } catch (err) {
      console.error(`Fehler beim Abrufen des PDFs für Einsatz ${op.id}:`, err);
    }
  }

  const mergedPdfBytes = await mergedPdf.save();
  const blob = new Blob([mergedPdfBytes], { type: "application/pdf" });
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = "Einsatzliste.pdf";
  link.click();
}

// Event-Listener für die Buttons
(function () {
  const mapBtn = document.getElementById("printMapBtn");
  const opsBtn = document.getElementById("printOpsBtn");
  if (mapBtn) mapBtn.addEventListener("click", printMap);
  if (opsBtn) opsBtn.addEventListener("click", printOperationList);
})();
