// js/steinmap.js
// escHtml: Text fuer innerHTML/Template-Literale entschaerfen (STEIN-, Nominatim- und Stammdaten sind Fremddaten).
function escHtml(s) { const d = document.createElement("div"); d.textContent = String(s == null ? "" : s); return d.innerHTML; }

// === Konfiguration ===
// Stammdaten (Ortsverbaende mit Koordinaten, Einheiten, Nachbar-OVs anderer
// Landesverbaende) kommen ueber api/kraefte?action=stamm - gemeinsam mit dem
// STEINboard und den Einsatz-Seiten (internal/kraefte) - und werden
// beim Start per KraefteStamm.load() in config.stein uebernommen. Der
// Abschnitt "bezirke" der Stammdaten (optional) nennt einen Landkreis, der
// in Bezirke gegliedert ist, samt GeoJSON unter /geo/.
const config = {
  stein: {
    units: [],
    units_nachbar: [],
    filter: [],
    aktiv: [],
    proxyUrl: "/api/proxy?type=stein"
  }
};

// Gruppennamen wie in stein.js
const groupNames = {
  "1": "Fahrzeug",
  "2": "Gerät",
  "3": "Person",
  "4": "Einheit",
  "5": "Anhänger"
};

// Farben nach Status
const stateColors = {
  ready:     "rgb(40, 167, 69)",
  notready:  "rgb(220, 53, 69)",
  semiready: "rgb(255, 193, 7)",
  inuse:     "rgb(0, 51, 153)",
  maint:     "rgb(108, 117, 125)",
  dummy:     "rgb(138, 43, 226)"
};

// Hintergrund-Style je nach Status und Einsatzreservierung
function getBackground(state, operationReservation) {
  const baseColor = stateColors[state] || "rgb(128,128,128)";
  if (operationReservation) {
    const nums = baseColor.match(/\d+/g);
    return `repeating-linear-gradient(
      45deg,
      ${baseColor},
      ${baseColor} 10px,
      rgba(${nums[0]}, ${nums[1]}, ${nums[2]}, 0.882) 10px,
      rgba(${nums[0]}, ${nums[1]}, ${nums[2]}, 0.882) 20px
    )`;
  } else {
    return baseColor;
  }
}

// Banner-Setup für Fehlermeldungen
(function setupErrorBanner() {
  // CSS für Banner
  const style = document.createElement('style');
  style.innerHTML = `
    #error-banner {
      position: absolute;
      top: 10px;
      left: 50%;
      transform: translateX(-50%);
      background-color: #fff3cd;
      color: #856404;
      padding: 8px 12px;
      border-radius: 4px;
      z-index: 1000;
      text-align: center;
      display: none;
      font-weight: bold;
    }
  `;
  document.head.appendChild(style);

  // Banner-Element
  const banner = document.createElement('div');
  banner.id = 'error-banner';
  document.addEventListener('DOMContentLoaded', () => {
    const mapContainer = document.getElementById('map');
    if (mapContainer) mapContainer.appendChild(banner);
  });

  // Exponiere Funktionen global
  window.showErrorBanner = function(message, seconds = null) {
    clearErrorBanner();
    banner.textContent = message;
    banner.style.display = 'block';
    if (typeof seconds === 'number') {
      let remaining = seconds;
      banner.textContent = `${message} (${remaining}s)`;
      const interval = setInterval(() => {
        remaining -= 1;
        if (remaining <= 0) {
          clearInterval(interval);
          banner.style.display = 'none';
        } else {
          banner.textContent = `${message} (${remaining}s)`;
        }
      }, 1000);
    }
  };

  window.clearErrorBanner = function() {
    banner.style.display = 'none';
    banner.textContent = '';
  };
})();

// Globals für Karte, Marker und GeoJSON-Layer
let map;
const unitMarkers = {};
let visibleUnitMarkers = [];
let nachbarMarkers = [];
let globalAssets = [];
let geocodeMarker = null;
const overlays = {};
let gemeindenData = null;
let currentUnitForList = null;

// Globals für Messwerkzeug
let measureMode = 'none';
let measurePoints = [];
let measureLayerGroup = null;
let measureInfoPopup = null;
let measureAreaClosed = false;

// Timestamp des letzten erlaubten Aufrufs (in ms seit Epoch)
let lastGeocodeTime = 0;

// Timestamp des letzten erlaubten Routing-Calls
let lastRoutingTime = 0;

// Default-Style für Landkreise und Highlight-Tracking
const defaultGKStyle    = { color: '#444', weight: 1, fill: false };
let landkreisLayers     = {};
// Bezirksebene (Stammdaten "bezirke": {landkreis, geo, titel, prefix,
// attribution}): innerhalb dieses Landkreises ist der Bezirk die
// Zustaendigkeitsebene; der zustaendige OV traegt den Bezirksnamen
// (OV "<prefix> Neustadt" -> Neustadt). Ohne Abschnitt gibt es keine Ebene.
let bezirkeData         = null;
let bezirkeCfg          = null;
let bezirkLayers        = {};
const bezirkStyle       = { color: '#555', weight: 1, dashArray: '4 3', fill: true, fillOpacity: 0 };
let highlightedLayers    = [];

/**
 * Entfernt jede bisherige Hervorhebung.
 */
function clearHighlightedCounties() {
  highlightedLayers.forEach(layer => {
    layer.setStyle(layer.isBezirk ? bezirkStyle : defaultGKStyle);
    if (layer.options.fillPattern) {
      layer.options.fillPattern.remove();
    }
  });
  highlightedLayers = [];
}

//Markiert die gegebenen Landkreise schraffiert.
//Benötigt Leaflet.pattern-Plugin.
function highlightCounties(names) {
  names.forEach(name => {
    const layer = landkreisLayers[name];
    if (!layer) return;

    const pattern = new L.StripePattern({
      weight:  2,         // Linienbreite
      space:   6,         // Abstand zwischen Linien
      color:   '#FF8C00', // Linienfarbe
      opacity: 1,
      angle:   45
    });
    pattern.addTo(map);

    layer.setStyle({
      fill:        true,          // <-- hier aktivieren
      fillPattern: pattern,
      // Optional: zusätzlich eine Grundfarbe unterlegen:
      fillColor:   '#FFA500',
      fillOpacity: 0.3,           // Transparenz des Musters / der Grundfarbe
      color:       '#FF8C00',     // Randfarbe
      weight:      2
    });

    highlightedLayers.push(layer);
  });
}

// Karte initialisieren
function initMap() {
  map = L.map('map').setView([51.1657, 10.4515], 6);
  L.tileLayer(((window.CURRENT_OV && window.CURRENT_OV.tile_url) || '/tiles/{z}/{x}/{y}.png'), {
    attribution: '&copy; OpenStreetMap-Mitwirkende'
  }).addTo(map);

  L.control.scale({ position: 'bottomright', imperial: false }).addTo(map);

  map.on('click', onMapClick);
  addUnitMarkers();
  addNachbarMarkers();
  addGemeindenLayer();
  addBezirkeLayer();

  measureLayerGroup = L.layerGroup().addTo(map);
  addMeasureToolsControl();
}

// Hilfsfunktion: Popup für Geocode-Resultate und Klicks vereinheitlichen
function showGeocodePopup(lat, lng, displayName) {
  // Input-Feld aktualisieren
  const input = document.getElementById('geocode-input');
  input.value = displayName;

  // Landkreis ermitteln, im Landkreis mit Bezirksebene zusaetzlich den Bezirk
  const landkreis = getLandkreis(lat, lng);
  const bezirk = bezirkeCfg && landkreis === bezirkeCfg.landkreis ? getBezirk(lat, lng) : null;

  // Basis-HTML
  const coordsHtml = `<strong>Koordinaten:</strong> Lat: ${lat.toFixed(6)}, Long: ${lng.toFixed(6)}`;
  const lkHtml = landkreis ? `<strong>Landkreis:</strong> ${landkreis}` : '';
  const bzHtml = bezirk ? `<strong>Bezirk:</strong> ${bezirk}` : '';

  // Zuständige Ortsverbände und Regionalstelle
  let ortsverbande = [], regionalstellen = [];
  if (bezirk) {
    // Im Landkreis mit Bezirksebene ist genau der OV mit dem Bezirksnamen zustaendig
    ortsverbande = config.stein.units
      .filter(u => u.zust_landkreis === bezirkeCfg.landkreis && ovMatchesBezirk(u.name, bezirk))
      .map(u => u.name);
    ortsverbande = [...new Set(ortsverbande)];
  } else if (landkreis) {
    ortsverbande = config.stein.units
      .filter(u => u.zust_landkreis === landkreis && u.art !== 'lv')
      .map(u => u.name);
    ortsverbande = [...new Set(ortsverbande)];
  }
  if (ortsverbande.length) {

    regionalstellen = ortsverbande
      .map(name => {
        const u = config.stein.units.find(v => v.name === name);
        return u && u.sammel.includes('Regionalstelle') ? u.sammel : null;
      })
      .filter(r => r);
    regionalstellen = [...new Set(regionalstellen)];
  }

  const ortsHtml = ortsverbande.length ? `<strong>Ortsverband:</strong> ${escHtml(ortsverbande.join(', '))}` : '';
  const regHtml = regionalstellen.length ? `<strong>Regionalstelle:</strong> ${escHtml(regionalstellen.join(', '))}` : '';

  const popupParts = [escHtml(displayName), coordsHtml, lkHtml, bzHtml, ortsHtml, regHtml].filter(s => s);
  const popupHtml = popupParts.join('<br>');

  // Marker setzen bzw. erneuern
  if (geocodeMarker) map.removeLayer(geocodeMarker);
  geocodeMarker = L.circleMarker([lat, lng], { color: 'red', radius: 8, fillOpacity: 0.8 })
    .addTo(map)
    .bindPopup(popupHtml)
    .openPopup();
}


// Geocoder-Click-Handler (Kartenklick)
async function onMapClick(e) {
  if (measureMode !== 'none') {
    addMeasurePoint(e.latlng);
    return;
  }

  const { lat, lng } = e.latlng;
  let displayName = `${lat.toFixed(6)}, ${lng.toFixed(6)}`;

  const now = Date.now();
  const diff = now - lastGeocodeTime;
  // Doppelklick-Schutz; die eigentliche Bremse (1 Anfrage/s) sitzt im Server
  if (diff < 2000) {
    const secs = Math.round((2000 - diff) / 1000);
    console.warn('Zu schnell hintereinander angefragt – bitte warten.');
    showErrorBanner('Zu schnell angefragt — bitte warten.', secs);
    return;
  }
  // Merke dir den Zeitstempel dieses Aufrufs
  lastGeocodeTime = now;

  // Reverse-Geocode
  try {
    // Ueber den Server (Nominatim direkt laesst die CSP nicht zu; der Server
    // bremst auf eine Anfrage je Sekunde und cacht).
    const url = `/api/proxy?type=geocode-reverse&lat=${lat}&lon=${lng}`;
    const res = await fetch(url);
    if (res.status === 429) showErrorBanner('Adresssuche ausgelastet — bitte kurz warten.', 5);
    const data = await res.json();
    if (data.display_name) displayName = data.display_name;
  } catch {/* ignore */}

  showGeocodePopup(lat, lng, displayName);
}

// Gemeinden/Landkreise aus der lokalen GeoJSON
function addGemeindenLayer() {
  // Liegt in assets/geo/ ausserhalb von public/ (nginx-alias auf /geo/)
  fetch('/geo/landkreise_simplify20.geojson')
    .then(res => res.json())
    .then(geojson => {
      gemeindenData = geojson;
      const gemeindenLayer = L.geoJSON(geojson, {
        style: defaultGKStyle,
        onEachFeature: (feature, layer) => {
          const name = feature.properties.NAME || feature.properties.GEN || '–';
          layer.bindTooltip(name, { sticky: true });
          landkreisLayers[name] = layer;
        }
      });
      overlays['Landkreise'] = gemeindenLayer;
      gemeindenLayer.addTo(map);
      if (!map.layerControl) {
        map.layerControl = L.control.layers(null, overlays, { collapsed: false }).addTo(map);
      } else {
        map.layerControl.addOverlay(gemeindenLayer, 'Landkreise');
      }
    })
    .catch(err => {
      console.error('Fehler beim Laden von Landkreisdaten:', err);
      showErrorBanner('Fehler beim Laden der Landkreisdaten.');
    });
}

// Bezirke als eigene Ebene (gestrichelt, zuschaltbar), nur wenn die
// Stammdaten einen Abschnitt "bezirke" mit GeoJSON-Pfad nennen.
function addBezirkeLayer() {
  const st = (window.KraefteStamm && KraefteStamm.state && KraefteStamm.state.stamm) || {};
  const cfg = st.bezirke && typeof st.bezirke === 'object' ? st.bezirke : null;
  if (!cfg || !cfg.landkreis || !cfg.geo || !/^\/geo\/[\w.-]+\.geojson$/.test(String(cfg.geo))) return;
  bezirkeCfg = cfg;
  const titel = cfg.titel || ('Bezirke ' + cfg.landkreis);
  fetch(cfg.geo)
    .then(res => res.json())
    .then(geojson => {
      bezirkeData = geojson;
      const layerGroup = L.geoJSON(geojson, {
        style: bezirkStyle,
        onEachFeature: (feature, layer) => {
          const name = feature.properties.name || feature.properties.GEN || '–';
          layer.isBezirk = true;
          layer.bindTooltip('Bezirk ' + name, { sticky: true });
          bezirkLayers[name] = layer;
        },
        attribution: cfg.attribution || ''
      });
      overlays[titel] = layerGroup;
      layerGroup.addTo(map);
      if (!map.layerControl) {
        map.layerControl = L.control.layers(null, overlays, { collapsed: false }).addTo(map);
      } else {
        map.layerControl.addOverlay(layerGroup, titel);
      }
    })
    .catch(err => {
      console.error('Fehler beim Laden der Bezirksgrenzen:', err);
    });
}

// Normalisiert OV- und Bezirksnamen fuer den Vergleich ("<prefix>-Mitte" -> "mitte").
function normBezirk(s) {
  let v = String(s || '').toLowerCase();
  const prefix = bezirkeCfg && bezirkeCfg.prefix ? String(bezirkeCfg.prefix).toLowerCase() : '';
  if (prefix && v.startsWith(prefix)) v = v.slice(prefix.length).replace(/^[\s-]*/, '');
  return v
    .replace(/[\s-]+/g, '')
    .replace(/ä/g, 'ae').replace(/ö/g, 'oe').replace(/ü/g, 'ue').replace(/ß/g, 'ss');
}
function ovMatchesBezirk(ovName, bezirk) {
  return normBezirk(ovName) === normBezirk(bezirk);
}

// Bestimmt den Bezirk eines Punkts (null ausserhalb des Landkreises mit Bezirksebene)
function getBezirk(lat, lng) {
  if (!bezirkeData) return null;
  const pt = [lng, lat];
  for (const feature of bezirkeData.features) {
    const geom = feature.geometry;
    const polys = geom.type === 'Polygon' ? [geom.coordinates] : (geom.type === 'MultiPolygon' ? geom.coordinates : []);
    for (const polygon of polys) {
      if (polygon.length && pointInPolygon(pt, polygon[0])) return feature.properties.name || feature.properties.GEN;
    }
  }
  return null;
}

// Hebt den Bezirk eines OV hervor (statt des ganzen Landkreises), falls bekannt.
function highlightBezirkForUnit(unit) {
  if (!unit || !bezirkeCfg || unit.zust_landkreis !== bezirkeCfg.landkreis) return false;
  const name = Object.keys(bezirkLayers).find(b => ovMatchesBezirk(unit.name, b));
  if (!name) return false;
  const layer = bezirkLayers[name];
  layer.setStyle({ fill: true, fillColor: '#FFA500', fillOpacity: 0.35, color: '#FF8C00', weight: 2, dashArray: null });
  highlightedLayers.push(layer);
  return true;
}

// Punkt-in-Polygon (Ray-Casting)
function pointInPolygon(point, vs) {
  const x = point[0], y = point[1];
  let inside = false;
  for (let i = 0, j = vs.length - 1; i < vs.length; j = i++) {
    const xi = vs[i][0], yi = vs[i][1];
    const xj = vs[j][0], yj = vs[j][1];
    const intersect = ((yi > y) !== (yj > y)) &&
      (x < (xj - xi) * (y - yi) / (yj - yi) + xi);
    if (intersect) inside = !inside;
  }
  return inside;
}

// Bestimmt den Landkreis eines Punkts
function getLandkreis(lat, lng) {
  if (!gemeindenData) return null;
  const pt = [lng, lat];
  for (const feature of gemeindenData.features) {
    const geom = feature.geometry;
    const name = feature.properties.NAME || feature.properties.GEN;
    if (geom.type === 'Polygon') {
      for (const ring of geom.coordinates) {
        if (pointInPolygon(pt, ring)) return name;
      }
    } else if (geom.type === 'MultiPolygon') {
      for (const polygon of geom.coordinates) {
        for (const ring of polygon) {
          if (pointInPolygon(pt, ring)) return name;
        }
      }
    }
  }
  return null;
}

function clearUnitMarkers() {
  visibleUnitMarkers.forEach(marker => map.removeLayer(marker));
  visibleUnitMarkers = [];
  Object.keys(unitMarkers).forEach(key => delete unitMarkers[key]);
}

function clearNachbarMarkers() {
  nachbarMarkers.forEach(marker => map.removeLayer(marker));
  nachbarMarkers = [];
}

// Marker für jede Einheit mit Offset bei gleichen Positionen
function addUnitMarkers(units = config.stein.units) {
  clearUnitMarkers();

  const posMap = {};
  units.forEach(unit => {
    if (unit.lat == null || unit.lng == null) return;
    const key = `${unit.lat.toFixed(6)}:${unit.lng.toFixed(6)}`;
    (posMap[key] = posMap[key] || []).push(unit);
  });

  const offsetDeg = 0.0005;
  Object.values(posMap).forEach(unitsAtPos => {
    const count = unitsAtPos.length;
    unitsAtPos.forEach((unit, i) => {
      let lat = unit.lat, lng = unit.lng;
      if (count > 1) {
        const angle = (2 * Math.PI * i) / count;
        lat += offsetDeg * Math.cos(angle);
        lng += offsetDeg * Math.sin(angle);
      }
      const marker = L.marker([lat, lng]).addTo(map);
      visibleUnitMarkers.push(marker);
      unitMarkers[unit.name] = marker;
      marker.on('click', () => onUnitClick(unit));
    });
  });
}

// --- NEU: Marker für benachbarte Einheiten ---
// Definiere ein orangefarbenes Icon für den Marker.
const orangeIcon = L.icon({
  iconUrl: 'images/marker-icon-nachbar.png',
  shadowUrl: 'images/marker-shadow.png',
  iconSize: [25, 41],
  iconAnchor: [12, 41],
  popupAnchor: [1, -34],
  shadowSize: [41, 41]
});


function addNachbarMarkers() {
  clearNachbarMarkers();

  config.stein.units_nachbar.forEach(unit => {
    const marker = L.marker([unit.lat, unit.lng], { icon: orangeIcon }).addTo(map);
    nachbarMarkers.push(marker);
    marker.on('click', () => onNachbarUnitClick(unit));
  });
}

// Handler für Klick auf benachbarte Einheit
function onNachbarUnitClick(unit) {
  clearHighlightedCounties();
  if (unit.zust_landkreis) {
    highlightCounties([unit.zust_landkreis]);
  }

  // Basis Popup-Inhalt
  let content = `<strong>${escHtml(unit.name)}</strong>`;
  content += `<br><br><strong>LV:</strong> ${escHtml(unit.lv)}`;
  content += `<br><br><strong>Landkreis:</strong> ${escHtml(unit.zust_landkreis)}`;

  // Routing-/Distanz-Infos nur, wenn Geocode-Marker existiert
  if (geocodeMarker) {
    const now = Date.now();
    const diff = now - lastRoutingTime;
    const pos = geocodeMarker.getLatLng();
    const hav = getDistance(unit.lat, unit.lng, pos.lat, pos.lng).toFixed(1);
    content += `<br><br><strong>Entfernung zum Marker:</strong>`;
    content += `<br>Luftlinie: ${hav} km`;

    // Doppelklick-Schutz; die eigentliche Bremse (1 Anfrage/s) sitzt im Server
    if (diff < 2000) {
      const secs = Math.round((2000 - diff) / 1000);
      console.warn('Routing-Anfrage zu schnell – zeige nur Basis-Popup.');
      showErrorBanner('Routing-Anfrage zu schnell — bitte warten.', secs);
      L.popup({ offset: L.point(0, -10) })
        .setLatLng([unit.lat, unit.lng])
        .setContent(content)
        .openOn(map);
    } else {
      lastRoutingTime = now;
      // Ueber den Server (OSRM direkt laesst die CSP nicht zu).
      const url = `/api/proxy?type=route&from=${unit.lat},${unit.lng}&to=${pos.lat},${pos.lng}`;
      fetch(url)
        .then(r => r.json())
        .then(data => {
          if (data.routes?.length) {
            const r = data.routes[0];
            const km = (r.distance / 1000).toFixed(1);
            const dur = r.duration;
            const h = Math.floor(dur / 3600);
            const m = Math.round((dur % 3600) / 60);
            content += `<br>Route: ${km} km, ${h ? h + 'h ' : ''}${m}min`;
          }
          L.popup({ offset: L.point(0, -10) })
            .setLatLng([unit.lat, unit.lng])
            .setContent(content)
            .openOn(map);
        })
        .catch(() => {
          L.popup({ offset: L.point(0, -10) })
            .setLatLng([unit.lat, unit.lng])
            .setContent(content)
            .openOn(map);
        });
    }
  } else {
    // Kein Geocode-Marker: nur Basis-Popup
    L.popup({ offset: L.point(0, -10) })
      .setLatLng([unit.lat, unit.lng])
      .setContent(content)
      .openOn(map);
  }

  L.popup({ offset: L.point(0, -10) })
    .setLatLng([unit.lat, unit.lng])
    .setContent(content)
    .openOn(map);

  showNeighborUnits(unit);
}




// Sidebar-Anzeige für benachbarte Einheiten aus dem units-Array
function showNeighborUnits(unit) {
  currentUnitForList = unit;
  const list = document.getElementById('vehicleList');
  list.innerHTML = '';

  const header = document.createElement('div');
  header.className = 'group-header';
  header.textContent = `Einheiten (${unit.units.length})`;
  list.appendChild(header);

  const groupDiv = document.createElement('div');
  groupDiv.style.display = 'flex';
  groupDiv.style.flexWrap = 'wrap';
  groupDiv.style.gap = '8px';

  unit.units.forEach((label, i) => {
    const box = document.createElement('div');
    box.className = 'asset-box';
    box.textContent = label;
    // Keine Live-Daten: lila wie im STEINboard (Status unbekannt, nicht "einsatzbereit")
    box.style.background = getBackground('dummy', false);
    box.style.color = 'white';
    const e = (unit.einheiten || [])[i];
    if (e) {
      const badge = KraefteStamm.bindBadge([e.key]);
      if (badge) box.appendChild(badge);
      const take = KraefteStamm.takeLink([e.key]);
      if (take) box.appendChild(take);
    }
    groupDiv.appendChild(box);
  });

  list.appendChild(groupDiv);
}

// Haversine-Distanz in km
function getDistance(lat1, lon1, lat2, lon2) {
  const toRad = d => d * Math.PI/180;
  const R = 6371;
  const dLat = toRad(lat2 - lat1);
  const dLon = toRad(lon2 - lon1);
  const a = Math.sin(dLat/2)**2 +
            Math.cos(toRad(lat1)) *
            Math.cos(toRad(lat2)) *
            Math.sin(dLon/2)**2;
  const c = 2 * Math.atan2(Math.sqrt(a), Math.sqrt(1 - a));
  return R * c;
}

// Handler für Marker-Klick: Popup, Sidebar und Highlight, inkl. Routing-Throttle
function onUnitClick(unit) {
  clearHighlightedCounties();
  showVehiclesForUnit(unit);

  // Basis Popup-Inhalt
  let content = `<strong>${escHtml(unit.name)}</strong>`;
  if (unit.sammel !== unit.name && unit.sammel.includes('Regionalstelle')) {
    content += `<br><br><strong>Regionalstelle:</strong> ${escHtml(unit.sammel)}`;
  }

  // Routing-/Distanz-Infos nur, wenn Geocode-Marker existiert
  if (geocodeMarker) {
    const now = Date.now();
    const diff = now - lastRoutingTime;
    const pos = geocodeMarker.getLatLng();
    const hav = getDistance(unit.lat, unit.lng, pos.lat, pos.lng).toFixed(1);
    content += `<br><br><strong>Entfernung zum Marker:</strong>`;
    content += `<br>Luftlinie: ${hav} km`;

    // Doppelklick-Schutz; die eigentliche Bremse (1 Anfrage/s) sitzt im Server
    if (diff < 2000) {
      const secs = Math.round((2000 - diff) / 1000);
      console.warn('Routing-Anfrage zu schnell – zeige nur Basis-Popup.');
      showErrorBanner('Routing-Anfrage zu schnell — bitte warten.', secs);
      L.popup({ offset: L.point(0, -10) })
        .setLatLng([unit.lat, unit.lng])
        .setContent(content)
        .openOn(map);
    } else {
      lastRoutingTime = now;
      // Ueber den Server (OSRM direkt laesst die CSP nicht zu).
      const url = `/api/proxy?type=route&from=${unit.lat},${unit.lng}&to=${pos.lat},${pos.lng}`;
      fetch(url)
        .then(r => r.json())
        .then(data => {
          if (data.routes?.length) {
            const r = data.routes[0];
            const km = (r.distance / 1000).toFixed(1);
            const dur = r.duration;
            const h = Math.floor(dur / 3600);
            const m = Math.round((dur % 3600) / 60);
            content += `<br>Route: ${km} km, ${h ? h + 'h ' : ''}${m}min`;
          }
          L.popup({ offset: L.point(0, -10) })
            .setLatLng([unit.lat, unit.lng])
            .setContent(content)
            .openOn(map);
        })
        .catch(() => {
          L.popup({ offset: L.point(0, -10) })
            .setLatLng([unit.lat, unit.lng])
            .setContent(content)
            .openOn(map);
        });
    }
  } else {
    // Kein Geocode-Marker: nur Basis-Popup
    L.popup({ offset: L.point(0, -10) })
      .setLatLng([unit.lat, unit.lng])
      .setContent(content)
      .openOn(map);
  }

  // Highlight-Logik: die LV-Dienststelle (art "lv", Name = Region) umfasst alle Landkreise
  if (unit.art === 'lv' && unit.name === unit.sammel) {
    const all = config.stein.units
      .map(u => u.zust_landkreis)
      .filter(Boolean);
    highlightCounties([...new Set(all)]);
  } else if (unit.name === unit.sammel) {
    const kids = config.stein.units
      .filter(u => u.sammel === unit.name)
      .map(u => u.zust_landkreis)
      .filter(Boolean);
    highlightCounties([...new Set(kids)]);
  } else if (highlightBezirkForUnit(unit)) {
    // OV mit Bezirksebene: nur sein Bezirk
  } else if (unit.zust_landkreis) {
    highlightCounties([unit.zust_landkreis]);
  }
}

// Hilfsfunktion: liest Filter-Checkboxen aus und gibt Array von groupIds oder null (für "all")
function getSelectedGroups() {
  const allCb = document.getElementById('filter_all');
  const others = Array.from(document.querySelectorAll('.filter-checkbox'));
  if (allCb.checked) return null;
  const sel = others.filter(cb => cb.checked).map(cb => cb.value);
  return sel.length ? sel : null;
}

// Fahrzeuge einer Einheit in der Seitenleiste anzeigen
function showVehiclesForUnit(unit) {
  // Merken, welche Einheit aktuell angezeigt wird
  currentUnitForList = unit;

  const list = document.getElementById('vehicleList');
  list.innerHTML = '';

  // Alle Assets zur Einheit herausfiltern
  const vehicles = globalAssets.filter(a => a.buname === unit.name);

  // Wenn keine Daten von STEIN vorhanden und unit.units definiert, Fallback auf units-Array wie bei Nachbar-Einheiten
  if (vehicles.length === 0 && unit.units) {
    showNeighborUnits(unit);
    return;
  }

  // Ausgewählte Gruppen abfragen (null = „Alles anzeigen“)
  const selectedGroups = getSelectedGroups();

  // In Gruppen nach groupId sortieren
  const groups = {};
  vehicles.forEach(asset => {
    const g = String(asset.groupId);
    (groups[g] = groups[g] || []).push(asset);
  });

  // Pro definierter Gruppe (1–5) Header + Items anlegen,
  // falls im Filter ausgewählt oder „Alles anzeigen“
  Object.keys(groupNames).forEach(gId => {
    // Wenn gefiltert, aber diese Gruppe nicht drin, überspringen
    if (selectedGroups && !selectedGroups.includes(gId)) return;

    const items = groups[gId];
    if (!items) return;

    // Gruppen-Überschrift mit Zähler
    const header = document.createElement('div');
    header.className = 'group-header';
    header.textContent = `${groupNames[gId]} (${items.length})`;
    list.appendChild(header);

    // Container für die Asset-Boxen
    const groupDiv = document.createElement('div');
    groupDiv.style.display = 'flex';
    groupDiv.style.flexWrap = 'wrap';
    groupDiv.style.gap = '8px';

    // Einzelne Asset-Boxen
    items.forEach(asset => {
      const text = asset.label + (asset.name ? ' – ' + asset.name : '');
      const diffDays = Math.floor((Date.now() - new Date(asset.lastModified)) / (1000*60*60*24));
      const box = document.createElement('div');
      box.className = 'asset-box';
      box.innerHTML = `
        <div style="font-weight:bold;">${escHtml(asset.buname)}</div>
        <div>${escHtml(text)}</div>
        <div>Tage seit Änderung: ${diffDays}</div>
      `;
      box.style.background = getBackground(asset.status, asset.operationReservation);
      if (asset.status === "inuse") box.style.color = "white";
      // Kraeftekatalog: in einem offenen Einsatz gebunden? Im Einsatz-Kontext: Uebernahme-Link
      if (String(asset.groupId) === "4") {
        const keys = KraefteStamm.keysFor(asset.buId, asset.category);
        const badge = KraefteStamm.bindBadge(keys);
        if (badge) box.appendChild(badge);
        const take = KraefteStamm.takeLink(keys, { bez: asset.label, ov: asset.buname, funkruf: asset.radioName });
        if (take) box.appendChild(take);
      }

      // Tooltip bei Klick in der Liste
      box.addEventListener('click', e => {
        e.stopPropagation();
        const tooltip = document.getElementById('tooltip');
        tooltip.innerHTML = `
          <strong>Label:</strong> ${escHtml(asset.label)}<br>
          <strong>Name:</strong> ${escHtml(asset.name)}<br>
          <strong>RadioName:</strong> ${escHtml(asset.radioName)}<br>
          <strong>ISSI:</strong> ${escHtml(asset.issi)}<br>
          <strong>Comment:</strong> ${escHtml(asset.comment)}<br>
          <strong>Category:</strong> ${escHtml(asset.category)}<br>
          <strong>Last Modified:</strong> ${escHtml(asset.lastModified)}<br>
          <strong>Last Modified By:</strong> ${escHtml(asset.lastModifiedBy)}
        `;
        let top = e.pageY + 5, left = e.pageX + 5;
        tooltip.style.display = 'block';
        tooltip.style.top = top + 'px';
        tooltip.style.left = left + 'px';
        const rect = tooltip.getBoundingClientRect();
        if (rect.right > window.innerWidth) tooltip.style.left = (window.innerWidth - rect.width - 5) + 'px';
        if (rect.bottom > window.innerHeight) tooltip.style.top = (window.innerHeight - rect.height - 5) + 'px';
      });

      groupDiv.appendChild(box);
    });

    list.appendChild(groupDiv);
  });
}


// Holt Assets vom STEIN-Proxy (nur aktiv gefiltert)
async function fetchAllAssets() {
  const { proxyUrl, units, aktiv } = config.stein;
  const activeUnits = units.filter(u => aktiv.includes(u.sammel));
  const buids = activeUnits.map(u => u.buid).join(',');
  const url = `${proxyUrl}&buIds=${encodeURIComponent(buids)}`;

  const resp = await fetch(url);
  if (!resp.ok) throw new Error('Fehler beim Abrufen der Assets');
  // Cache-Stand-Header auswerten (falls Element vorhanden)
  try {
    const oldest = resp.headers.get('X-Cache-Oldest-At');
    const standEl = document.getElementById('dataStand');
    if (oldest && standEl) {
      const d = new Date(oldest);
      if (!isNaN(d.getTime())) {
        const hh = String(d.getHours()).padStart(2, '0');
        const mm = String(d.getMinutes()).padStart(2, '0');
        const ss = String(d.getSeconds()).padStart(2, '0');
        standEl.textContent = `Stand: ${hh}:${mm}:${ss}`;
      }
    }
  } catch (e) { /* Header evtl. nicht sichtbar - ignorieren */ }
  const assets = await resp.json();

  const mapping = {};
  activeUnits.forEach(u => mapping[Number(u.buid)] = u.name);
  assets.forEach(a => a.buname = mapping[a.buId] || String(a.buId));
  return assets;
}

// Assets speichern und ggf. Sidebar aktualisieren
async function fetchAndStoreAssets() {
  try {
    await KraefteStamm.bindungen();   // Bindung in offenen Einsaetzen (Band "im Einsatz")
    globalAssets = await fetchAllAssets();
    // Refresh der Sidebar anhand der zuletzt angeklickten Einheit
    if (currentUnitForList) {
      if (currentUnitForList.units) {
        // Benachbarte Einheit: eigene Sidebar-Funktion
        showNeighborUnits(currentUnitForList);
      } else {
        // Normale STEIN-Einheit
        showVehiclesForUnit(currentUnitForList);
      }
    }
  } catch (e) {
    console.error(e);
  }
}

// Adresse geokodieren und auf der Karte markieren (Suchfeld)
async function geocodeAddress() {
  const mode = document.getElementById('search-mode')?.value || 'address';
  const searchValue = document.getElementById('geocode-input').value.trim();
  if (!searchValue) return;

  if (mode === 'stein') {
    applySteinSearch(searchValue);
    return;
  }

  const now = Date.now();
  const diff = now - lastGeocodeTime;
  // Doppelklick-Schutz; die eigentliche Bremse (1 Anfrage/s) sitzt im Server
  if (diff < 2000) {
    const secs = Math.round((2000 - diff) / 1000);
    console.warn('Zu schnell hintereinander angefragt – bitte warten.');
    showErrorBanner('Zu schnell angefragt — bitte warten.', secs);
    return;
  }
  // Merke dir den Zeitstempel dieses Aufrufs
  lastGeocodeTime = now;

  const addr = document.getElementById('geocode-input').value.trim();
  if (!addr) return;

  try {
    // Ueber den Server (Nominatim direkt laesst die CSP nicht zu).
    const res = await fetch('/api/proxy?type=geocode&limit=1&q=' + encodeURIComponent(addr));
    if (res.status === 429) {
      showErrorBanner('Adresssuche ausgelastet — bitte kurz warten.', 5);
      return;
    }
    const data = await res.json();

    if (!Array.isArray(data) || !data.length) {
      showErrorBanner('Keine Ergebnisse gefunden.', 5);
      return;
    }

    const { lat, lon, display_name } = data[0];
    showGeocodePopup(parseFloat(lat), parseFloat(lon), display_name);
    map.setView([lat, lon], 14);
  } catch (err) {
    console.error(err);
    showErrorBanner('Fehler beim Geokodieren.', 5);
  }
}

// Event-Listener für Checkbox-Änderungen
function initFilterControls() {
  const allCb = document.getElementById('filter_all');
  const others = Array.from(document.querySelectorAll('.filter-checkbox'));
  const allCbs  = [ allCb, ...others ];

  allCbs.forEach(cb => {
    cb.addEventListener('change', () => {
      // Wenn "Alles anzeigen" angeklickt, alle anderen aus
      if (cb.id === 'filter_all' && cb.checked) {
        others.forEach(o => o.checked = false);
      }
      // Wenn eine Unterkategorie angeklickt, "Alles anzeigen" aus
      if (cb !== allCb && cb.checked) {
        allCb.checked = false;
      }
      // Wenn kein Kästchen aktiv, "Alles anzeigen" wieder an
      if (!allCbs.some(x => x.checked)) {
        allCb.checked = true;
      }
      // Falls bereits eine Liste offen, neu rendern
      if (currentUnitForList) {
        showVehiclesForUnit(currentUnitForList);
      }

      // Stein-Suche bei Gruppenänderung sofort neu anwenden
      const isSteinMode = document.getElementById('search-mode')?.value === 'stein';
      if (isSteinMode) {
        const searchValue = document.getElementById('geocode-input')?.value.trim();
        if (searchValue) applySteinSearch(searchValue);
      }
    });
  });
}

// === Messwerkzeug-Funktionen ===

function formatDistance(km) {
  return km < 1 ? `${Math.round(km * 1000)} m` : `${km.toFixed(2)} km`;
}

function formatArea(areaSqm) {
  if (areaSqm >= 1000000) return `${(areaSqm / 1000000).toFixed(2)} qkm`;
  if (areaSqm >= 10000) return `${(areaSqm / 10000).toFixed(2)} ha`;
  return `${Math.round(areaSqm)} qm`;
}

function calculateGeodesicArea(latLngs) {
  const d2r = Math.PI / 180;
  const radius = 6378137;
  let area = 0;

  if (latLngs.length > 2) {
    for (let i = 0; i < latLngs.length; i++) {
      const p1 = latLngs[i];
      const p2 = latLngs[(i + 1) % latLngs.length];
      area += ((p2.lng - p1.lng) * d2r) * (2 + Math.sin(p1.lat * d2r) + Math.sin(p2.lat * d2r));
    }
    area = Math.abs((area * radius * radius) / 2);
  }

  return area;
}

function setMeasureMode(mode) {
  measureMode = mode;
  measurePoints = [];
  measureAreaClosed = false;

  if (measureInfoPopup) {
    map.closePopup(measureInfoPopup);
    measureInfoPopup = null;
  }

  updateMeasureOverlay();

  document.querySelectorAll('.measure-tools a').forEach(btn => {
    btn.classList.toggle('active', mode !== 'none' && btn.dataset.mode === mode);
  });
}

function updateMeasureOverlay() {
  if (!measureLayerGroup) return;
  measureLayerGroup.clearLayers();

  if (measureInfoPopup) {
    map.closePopup(measureInfoPopup);
    measureInfoPopup = null;
  }

  if (measureMode === 'none' || measurePoints.length === 0) return;

  const pointClass = measureMode === 'area' ? 'measure-point-marker area' : 'measure-point-marker distance';

  measurePoints.forEach((p, idx) => {
    const marker = L.marker(p, {
      draggable: true,
      icon: L.divIcon({ className: pointClass, iconSize: [12, 12], iconAnchor: [6, 6] })
    }).addTo(measureLayerGroup);

    marker.on('drag', (ev) => {
      measurePoints[idx] = ev.target.getLatLng();
      updateMeasureOverlay();
    });

    if (measureMode === 'area' && idx === 0 && measurePoints.length >= 3 && !measureAreaClosed) {
      marker.on('click', (ev) => {
        L.DomEvent.stopPropagation(ev);
        measureAreaClosed = true;
        updateMeasureOverlay();
      });
    }
  });

  if (measureMode === 'distance') {
    if (measurePoints.length > 1) {
      L.polyline(measurePoints, { color: '#0b5ed7', weight: 3 }).addTo(measureLayerGroup);
    }

    let totalKm = 0;
    for (let i = 1; i < measurePoints.length; i++) {
      const a = measurePoints[i - 1];
      const b = measurePoints[i];
      totalKm += getDistance(a.lat, a.lng, b.lat, b.lng);
    }

    const last = measurePoints[measurePoints.length - 1];
    const text = `Distanz: ${formatDistance(totalKm)} (${measurePoints.length} Punkte)`;
    measureInfoPopup = L.popup({ closeButton: false, autoClose: false, closeOnClick: false })
      .setLatLng(last)
      .setContent(text)
      .openOn(map);
  }

  if (measureMode === 'area') {
    if (measurePoints.length > 1) {
      const polyOpts = { color: '#198754', weight: 2 };

      if (measureAreaClosed && measurePoints.length > 2) {
        L.polygon(measurePoints, {
          ...polyOpts,
          fillColor: '#198754',
          fillOpacity: 0.2
        }).addTo(measureLayerGroup);

        const areaSqm = calculateGeodesicArea(measurePoints);
        const last = measurePoints[measurePoints.length - 1];
        const text = `Fl&auml;che: ${formatArea(areaSqm)} (${measurePoints.length} Punkte)`;
        measureInfoPopup = L.popup({ closeButton: false, autoClose: false, closeOnClick: false })
          .setLatLng(last)
          .setContent(text)
          .openOn(map);
      } else {
        L.polyline(measurePoints, { ...polyOpts, dashArray: '5,5' }).addTo(measureLayerGroup);

        if (measurePoints.length > 2) {
          L.polyline([measurePoints[measurePoints.length - 1], measurePoints[0]], {
            color: '#198754',
            weight: 1,
            dashArray: '2,6'
          }).addTo(measureLayerGroup);
        }

        const last = measurePoints[measurePoints.length - 1];
        measureInfoPopup = L.popup({ closeButton: false, autoClose: false, closeOnClick: false })
          .setLatLng(last)
          .setContent('Fl&auml;che: Punkte setzen, dann letzten Punkt auf den ersten klicken zum Schliessen.')
          .openOn(map);
      }
    }
  }
}

function addMeasurePoint(latlng) {
  if (measureMode === 'area') {
    if (measureAreaClosed) {
      updateMeasureOverlay();
      return;
    }

    if (measurePoints.length >= 3) {
      const first = measurePoints[0];
      const firstPx = map.latLngToContainerPoint(first);
      const clickPx = map.latLngToContainerPoint(latlng);
      const closeDistPx = firstPx.distanceTo(clickPx);

      if (closeDistPx <= 14) {
        measureAreaClosed = true;
        updateMeasureOverlay();
        return;
      }
    }
  }

  measurePoints.push(latlng);
  updateMeasureOverlay();
}

function addMeasureToolsControl() {
  const MeasureControl = L.Control.extend({
    options: { position: 'topleft' },
    onAdd: function () {
      const container = L.DomUtil.create('div', 'leaflet-bar measure-tools');

      container.innerHTML = [
        '<a href="#" data-mode="distance" title="Distanz messen">D</a>',
        '<a href="#" data-mode="area" title="Fl&auml;che messen">F</a>',
        '<a href="#" data-mode="none" title="Messung zuruecksetzen">X</a>'
      ].join('');

      L.DomEvent.disableClickPropagation(container);
      L.DomEvent.disableScrollPropagation(container);

      container.querySelectorAll('a').forEach(link => {
        L.DomEvent.on(link, 'click', (ev) => {
          L.DomEvent.preventDefault(ev);
          const mode = link.dataset.mode;
          if (mode === 'none') {
            setMeasureMode('none');
          } else {
            setMeasureMode(measureMode === mode ? 'none' : mode);
          }
        });
      });

      return container;
    }
  });

  map.addControl(new MeasureControl());
}

// === Stein-Suche-Funktionen ===

function normalizeSteinToken(text) {
  return String(text || '').toUpperCase().replace(/\s+/g, ' ').trim();
}

function matchesSteinToken(entry, searchToken) {
  const item = normalizeSteinToken(entry);
  const token = normalizeSteinToken(searchToken);
  if (!item || !token) return false;
  return (
    item === token ||
    item.startsWith(`${token} `) ||
    item.startsWith(`${token}(`) ||
    item.startsWith(`${token}-`) ||
    item.includes(token)
  );
}

function steinUnitEntryMatchesSelectedGroups() {
  const selectedGroups = getSelectedGroups();
  if (!selectedGroups) return true;
  return selectedGroups.includes('4');
}

function steinAssetMatchesSelectedGroups(asset) {
  const selectedGroups = getSelectedGroups();
  if (!selectedGroups) return true;
  return selectedGroups.includes(String(asset?.groupId ?? ''));
}

function assetMatchesSteinSearch(asset, token) {
  const fields = [asset?.label, asset?.name, asset?.radioName, asset?.issi, asset?.comment, asset?.category];
  return fields.some(value => matchesSteinToken(value, token));
}

function renderSteinSearchResults(searchToken, units, matchedAssets = []) {
  const list = document.getElementById('vehicleList');
  if (!list) return;
  list.innerHTML = '';

  const assetsByUnit = {};
  matchedAssets.forEach(asset => {
    const key = asset.buname || '';
    if (!key) return;
    (assetsByUnit[key] = assetsByUnit[key] || []).push(asset);
  });
  const hasAssetMatches = matchedAssets.length > 0;

  const header = document.createElement('div');
  header.className = 'group-header';
  header.textContent = hasAssetMatches
    ? `Stein (${units.length} Einheit${units.length === 1 ? '' : 'en'}, ${matchedAssets.length} Treffer)`
    : `Stein (${units.length})`;
  list.appendChild(header);

  const token = normalizeSteinToken(searchToken);
  units.forEach(unit => {
    if (hasAssetMatches) {
      const assets = assetsByUnit[unit.name] || [];
      if (!assets.length) return;

      const unitHeader = document.createElement('div');
      unitHeader.className = 'group-header';
      unitHeader.textContent = `${unit.name} (${assets.length})`;
      list.appendChild(unitHeader);

      const groupDiv = document.createElement('div');
      groupDiv.style.display = 'flex';
      groupDiv.style.flexWrap = 'wrap';
      groupDiv.style.gap = '8px';

      assets.forEach(asset => {
        const text = asset.label + (asset.name ? ' - ' + asset.name : '');
        const diffDays = Math.floor((Date.now() - new Date(asset.lastModified)) / (1000 * 60 * 60 * 24));

        const box = document.createElement('div');
        box.className = 'asset-box';
        box.innerHTML = `
          <div style="font-weight:bold;">${escHtml(asset.buname)}</div>
          <div>${escHtml(text)}</div>
          <div>Tage seit Änderung: ${diffDays}</div>
        `;
        box.style.background = getBackground(asset.status, asset.operationReservation);
        if (asset.status === 'inuse') box.style.color = 'white';

        groupDiv.appendChild(box);
      });

      list.appendChild(groupDiv);
      return;
    }

    const matches = (unit.units || []).filter(item =>
      steinUnitEntryMatchesSelectedGroups() && matchesSteinToken(item, token)
    );
    if (!matches.length) return;

    const box = document.createElement('div');
    box.className = 'asset-box';
    box.innerHTML = `
      <div style="font-weight:bold;">${escHtml(unit.name)}</div>
      <div>${escHtml(matches.join(', '))}</div>
    `;
    box.style.background = getBackground('dummy', false);
    box.style.color = 'white';
    list.appendChild(box);
  });
}

function applySteinSearch(searchToken) {
  const token = normalizeSteinToken(searchToken);
  if (!token) {
    addUnitMarkers();
    addNachbarMarkers();
    const list = document.getElementById('vehicleList');
    if (list) list.innerHTML = '';
    return;
  }

  const hasLiveAssets = Array.isArray(globalAssets) && globalAssets.length > 0;
  let matchedAssets = [];
  let matchedUnits = [];

  if (hasLiveAssets) {
    matchedAssets = globalAssets.filter(asset =>
      steinAssetMatchesSelectedGroups(asset) && assetMatchesSteinSearch(asset, token)
    );

    const matchedNames = new Set(matchedAssets.map(asset => asset.buname).filter(Boolean));
    matchedUnits = config.stein.units.filter(unit =>
      unit.lat != null && unit.lng != null && matchedNames.has(unit.name)
    );
  } else {
    matchedUnits = config.stein.units.filter(unit => {
      if (!Array.isArray(unit.units) || unit.lat == null || unit.lng == null) return false;
      return unit.units.some(item =>
        steinUnitEntryMatchesSelectedGroups() && matchesSteinToken(item, token)
      );
    });
  }

  addUnitMarkers(matchedUnits);
  clearNachbarMarkers();
  renderSteinSearchResults(token, matchedUnits, matchedAssets);

  if (!matchedUnits.length) {
    showErrorBanner('Keine STEIN-Treffer gefunden.', 5);
    return;
  }

  const bounds = L.latLngBounds(matchedUnits.map(u => [u.lat, u.lng]));
  if (bounds.isValid()) {
    map.fitBounds(bounds, { padding: [30, 30] });
  }
}

function initSearchModeControl() {
  const modeSelect = document.getElementById('search-mode');
  const input = document.getElementById('geocode-input');
  if (!modeSelect || !input) return;

  modeSelect.addEventListener('change', () => {
    const isSteinMode = modeSelect.value === 'stein';
    input.placeholder = isSteinMode
      ? 'Stein suchen (z.B. ZTr, I)'
      : 'Adresse eingeben...';

    if (!isSteinMode) {
      addUnitMarkers();
      addNachbarMarkers();
      const list = document.getElementById('vehicleList');
      if (list) list.innerHTML = '';
    }
  });
}

// ---- Druck-Modal ----

function openPrintModal() {
  var dialog = document.getElementById('printDialog');
  var filenameInput = document.getElementById('printFilename');
  var titleInput = document.getElementById('printTitle');

  // Dateiname vorbelegen (taktische Zeit wie beim Stein-Export)
  if (typeof getTacticalTime === 'function') {
    filenameInput.value = getTacticalTime('export');
  } else {
    var d = new Date();
    filenameInput.value = 'Karte_' + d.toISOString().slice(0,10);
  }
  titleInput.value = '';

  dialog.showModal();
  titleInput.focus();
}

function closePrintModal() {
  var dialog = document.getElementById('printDialog');
  if (dialog.open) dialog.close();
}

// Event-Listener fuer Modal-Buttons werden im DOMContentLoaded-Block unten registriert.

function getMarkerInfoText() {
  if (!geocodeMarker) return '';
  const popup = geocodeMarker.getPopup();
  if (!popup) return '';
  const content = popup.getContent();
  if (!content) return '';
  // HTML-Tags entfernen, <br> durch Zeilenumbruch, Leerzeilen komprimieren
  return content
    .replace(/<br\s*\/?>/gi, '\n')
    .replace(/<[^>]+>/g, '')
    .replace(/&amp;/g, '&')
    .replace(/\n{2,}/g, '\n')
    .trim();
}

function executePrint() {
  const titleText = document.getElementById('printTitle').value.trim();
  let filename = document.getElementById('printFilename').value.trim();
  if (!filename) filename = 'Karte';
  if (!filename.toLowerCase().endsWith('.pdf')) filename += '.pdf';

  closePrintModal();

  const mapContainer = document.getElementById('map');

  // Massstab unten links hinzufuegen
  const scaleControl = L.control.scale({ position: 'bottomleft', imperial: false }).addTo(map);

  // Nordpfeil oben rechts als DOM-Overlay
  const northDiv = L.DomUtil.create('div', 'north-arrow');
  northDiv.style.position = 'absolute';
  northDiv.style.top = '10px';
  northDiv.style.right = '10px';
  northDiv.style.zIndex = '1000';
  northDiv.innerHTML = '<img src="/images/Nordpfeil.png" style="width:40px;opacity:0.8;" />';
  mapContainer.appendChild(northDiv);

  var showMarkerInfo = document.getElementById('printShowMarkerInfo') && document.getElementById('printShowMarkerInfo').checked;
  const markerInfo = showMarkerInfo ? getMarkerInfoText() : '';

  setTimeout(() => {
    // Temporaer Zoom-, Layer- und Messwerkzeug-Kontrollen ausblenden
    const hiddenControls = [];
    document.querySelectorAll('.leaflet-control-zoom, .leaflet-control-layers, .measure-tools').forEach(el => {
      hiddenControls.push({ el, display: el.style.display });
      el.style.display = 'none';
    });

    domtoimage.toPng(mapContainer, {
        width: mapContainer.offsetWidth,
        height: mapContainer.offsetHeight,
        style: { transform: 'none', width: mapContainer.offsetWidth + 'px', height: mapContainer.offsetHeight + 'px' }
    })
      .then(dataUrl => {
        const { jsPDF } = window.jspdf;
        const pdf = new jsPDF('landscape', 'mm', 'a4');
        const pdfWidth = pdf.internal.pageSize.getWidth();
        const pdfHeight = pdf.internal.pageSize.getHeight();
        const margin = 10;

        let cursorY = margin;

        // Ueberschrift mit taktischer Zeit (falls Checkbox aktiv)
        var showStand = document.getElementById('printShowStand') && document.getElementById('printShowStand').checked;
        var standzeit = (showStand && typeof getTacticalTime === 'function') ? getTacticalTime('export') : '';
        var headerText = titleText || '';
        if (headerText && standzeit) {
          headerText += ' (Stand: ' + standzeit + ')';
        } else if (!headerText && standzeit) {
          headerText = 'Stand: ' + standzeit;
        }
        if (headerText) {
          pdf.setFont('helvetica', 'bold');
          pdf.setFontSize(16);
          pdf.text(headerText, pdfWidth / 2, cursorY + 6, { align: 'center' });
          cursorY += 14;
        }

        // Kartenbild
        const img = new Image();
        img.onload = () => {
          const availableWidth = pdfWidth - 2 * margin;
          const ratio = img.height / img.width;
          let imgWidth = availableWidth;
          let imgHeight = availableWidth * ratio;

          // Platz fuer Marker-Info reservieren
          const infoLines = markerInfo ? pdf.setFontSize(8).splitTextToSize(markerInfo, availableWidth) : [];
          const infoHeight = infoLines.length > 0 ? infoLines.length * 3.2 + 8 : 0;
          const maxImgHeight = pdfHeight - cursorY - margin - infoHeight;

          if (imgHeight > maxImgHeight) {
            imgHeight = maxImgHeight;
            imgWidth = imgHeight / ratio;
          }

          const imgX = (pdfWidth - imgWidth) / 2;
          pdf.addImage(dataUrl, 'PNG', imgX, cursorY, imgWidth, imgHeight);
          cursorY += imgHeight + 4;

          // Marker-Informationen unter dem Bild (kompakt)
          if (infoLines.length > 0) {
            cursorY += 2;
            pdf.setFont('helvetica', 'bold');
            pdf.setFontSize(8);
            pdf.text('Marker-Information:', margin, cursorY);
            cursorY += 3.5;
            pdf.setFont('helvetica', 'normal');
            pdf.setFontSize(8);
            pdf.text(infoLines, margin, cursorY);
          }

          pdf.save(filename);

          // Aufraeumen
          map.removeControl(scaleControl);
          document.querySelector('.north-arrow')?.remove();
          hiddenControls.forEach(({ el, display }) => { el.style.display = display; });
        };
        img.src = dataUrl;
      })
      .catch(err => {
        console.error('Fehler beim Erstellen des Bilds:', err);
        map.removeControl(scaleControl);
        document.querySelector('.north-arrow')?.remove();
        hiddenControls.forEach(({ el, display }) => { el.style.display = display; });
      });
  }, 500);
}

// DOMContentLoaded: Karte initialisieren & Daten holen
document.addEventListener('DOMContentLoaded', async () => {
  initFilterControls();
  initSearchModeControl();
  // Stammdaten laden, bevor die Marker gesetzt werden
  try {
    Object.assign(config.stein, await KraefteStamm.load());
  } catch (e) {
    console.error('Stammdaten der Einheiten konnten nicht geladen werden:', e);
    showErrorBanner('Stammdaten der Einheiten konnten nicht geladen werden.');
  }
  initMap();
  fetchAndStoreAssets();
  setInterval(fetchAndStoreAssets, 300000);
  document.getElementById('geocode-btn').addEventListener('click', geocodeAddress);
  document.getElementById('geocode-input').addEventListener('keydown', e => {
    if (e.key === 'Enter') {
      e.preventDefault();
      geocodeAddress();
    }
  });
  document.addEventListener('click', () => {
    const t = document.getElementById('tooltip'); if (t) t.style.display = 'none';
  });

  // Druck-Dialog: Button + Abbrechen + Bestaetigen + ESC
  var printBtn = document.getElementById('printBtn');
  if (printBtn) printBtn.addEventListener('click', openPrintModal);
  var cancelBtn = document.getElementById('cancelPrintBtn');
  if (cancelBtn) cancelBtn.addEventListener('click', closePrintModal);
  var confirmBtn = document.getElementById('confirmPrintBtn');
  if (confirmBtn) confirmBtn.addEventListener('click', executePrint);
  var dialog = document.getElementById('printDialog');
  if (dialog) dialog.addEventListener('cancel', function(e) { e.preventDefault(); closePrintModal(); });
});

// Fallback: Falls DOMContentLoaded schon gefeuert hat, Listener sofort registrieren
if (document.readyState !== 'loading') {
  var pb = document.getElementById('printBtn');
  if (pb && !pb._printBound) { pb.addEventListener('click', openPrintModal); pb._printBound = true; }
  var cb = document.getElementById('cancelPrintBtn');
  if (cb) cb.addEventListener('click', closePrintModal);
  var cfb = document.getElementById('confirmPrintBtn');
  if (cfb) cfb.addEventListener('click', executePrint);
  var dlg = document.getElementById('printDialog');
  if (dlg) dlg.addEventListener('cancel', function(e) { e.preventDefault(); closePrintModal(); });
}
