// _esc: Fremddaten (Warnmeldungen, Pegel, Organisationsname) vor dem Einsetzen
// in HTML entschaerfen.
function _esc(s) { const d = document.createElement('div'); d.textContent = String(s == null ? '' : s); return d.innerHTML; }
// _safeUrl: nur http(s)-Adressen als Link, alles andere wird nicht verlinkt.
function _safeUrl(u) { return /^https?:\/\//i.test(String(u || '')) ? String(u) : ''; }

// Karte initialisieren – Mittelpunkt: eingeloggter OV oder Default Deutschland
const __lhpCenter = [51.163, 10.447]; // Deutschlandmitte
const __lhpZoom = 5;
const map = L.map('map-stations').setView(__lhpCenter, __lhpZoom);

// OV-Marker einzeichnen (sobald die Karte existiert)
if (window.CURRENT_OV && window.CURRENT_OV.lat) {
    try {
        const __ovIcon = L.icon({
            iconUrl: '/images/marker-icon.png',
            shadowUrl: '/images/marker-shadow.png',
            iconSize: [25, 41],
            iconAnchor: [12, 41],
            popupAnchor: [1, -34],
            shadowSize: [41, 41]
        });
        L.marker([window.CURRENT_OV.lat, window.CURRENT_OV.lng], { icon: __ovIcon, title: window.CURRENT_OV.name })
            .addTo(map)
            .bindPopup('<strong>' + _esc(window.CURRENT_OV.name) + '</strong>');
    } catch (e) { console.warn('OV-Marker LHP:', e); }
}
map.createPane("stationsPane");
map.getPane("stationsPane").style.zIndex = 420;
map.createPane("alertsPane");
map.getPane("alertsPane").style.zIndex = 410;

// Basis-Layer: OpenStreetMap
const osm = L.tileLayer(((window.CURRENT_OV && window.CURRENT_OV.tile_url) || '/tiles/{z}/{x}/{y}.png'), {
    maxZoom: 19,
    attribution: '&copy; OpenStreetMap'
});
osm.addTo(map);

// Overlay-Layer: Alerts und Stations
const alertsLayer = L.layerGroup();
const stationsLayer = L.layerGroup();
const stationsNoGreenLayer = L.layerGroup();

// Beide Layer initial zur Karte hinzufügen (können dann über die Layer-Kontrolle ein- oder ausgeschaltet werden)
alertsLayer.addTo(map);
//stationsLayer.addTo(map);
stationsNoGreenLayer.addTo(map);

// Layer-Kontrolle (über "Baselayer" und "Overlay")
const overlays = {
    "Warnungen": alertsLayer,
    "alle Pegel": stationsLayer,
    "auff&auml;llige Pegel": stationsNoGreenLayer
};
L.control.layers(null, overlays, { collapsed: false }).addTo(map);

// Funktion zum Abrufen und Anzeigen der Alerts
async function updateAlerts() {
    try {
    const response = await fetch('/api/proxy?type=lhp&kind=alerts');
    const data = await response.json();
    // Je nach API-Struktur: Array in data.features oder data.alerts
    const alerts = Array.isArray(data.features)
        ? data.features
        : Array.isArray(data.alerts)
        ? data.alerts
        : [];

    // Bestehende Inhalte im Layer löschen
    alertsLayer.clearLayers();

    alerts.forEach(alert => {
        // Popup-Inhalt: Anzeige der gewünschten Felder aus den Properties
        const popupContent = `
        <div>
            <h3>${_esc(alert.properties?.alertHeadline || 'Hochwasserwarnung')}</h3>
            <p>Beschreibung: ${_esc(alert.properties?.areaDesc || 'Keine Angabe')}</p>
            <p>Typ: ${_esc(alert.properties?.areaType || 'Keine Angabe')}</p>
            <p>Klasse: ${_esc(alert.properties?.lhpClassName || 'Keine Angabe')}</p>
            ${_safeUrl(alert.properties?.alertLink)
            ? `<p>Link: <a href="${_esc(_safeUrl(alert.properties.alertLink))}" target="_blank" rel="noopener">${_esc(alert.properties.alertLink)}</a></p>`
            : ''}
        </div>
        `;

        // Farbe aus dem Datensatz übernehmen (entweder als Objekt mit .color oder als direkter String)
        const styleColor = (alert.style && typeof alert.style === 'object' && alert.style.color)
        ? alert.style.color
        : (alert.style || '#FF0000'); // Fallback: Rot

        let layer = null;
        if (alert.geometry) {
        const type = alert.geometry.type;
        const coords = alert.geometry.coordinates;
        if (type === 'Point') {
            // GeoJSON: [longitude, latitude] → Umkehrung!
            layer = L.circleMarker([coords[1], coords[0]], {
            pane: "alertsPane",
            color: styleColor,
            radius: 8,
            fillOpacity: 0.8
            });
        } else if (type === 'LineString') {
            const latlngs = coords.map(coord => [coord[1], coord[0]]);
            layer = L.polyline(latlngs, { pane: "alertsPane", color: styleColor });
        } else if (type === 'Polygon') {
            const latlngs = coords[0].map(coord => [coord[1], coord[0]]);
            layer = L.polygon(latlngs, { pane: "alertsPane", color: styleColor });
        } else if (type === 'MultiLineString') {
            const latlngs = coords.map(line => line.map(coord => [coord[1], coord[0]]));
            layer = L.polyline(latlngs, { pane: "alertsPane", color: styleColor });
        } else if (type === 'MultiPolygon') {
            const latlngs = coords.map(polygon =>
            polygon[0].map(coord => [coord[1], coord[0]])
            );
            layer = L.polygon(latlngs, { pane: "alertsPane", color: styleColor });
        }
        if (layer) {
            layer.bindPopup(popupContent);
            layer.addTo(alertsLayer);
        }
        } else {
        // Fallback: Falls keine GeoJSON-Geometrie vorhanden, versuche alternative Koordinatenfelder
        let lat = alert.lat || (alert.coordinates && alert.coordinates.latitude);
        let lon = alert.lon || (alert.coordinates && alert.coordinates.longitude);
        if (lat && lon) {
            layer = L.circleMarker([lat, lon], {
            pane: "alertsPane",
            color: styleColor,
            radius: 8,
            fillOpacity: 0.8
            });
            layer.bindPopup(popupContent);
            layer.addTo(alertsLayer);
        }
        }
    });
    } catch (error) {
    console.error('Fehler beim Laden der Alerts:', error);
    }
}

// Funktion zum Abrufen und Anzeigen der Stations
async function updateStations() {
    try {
    const response = await fetch('/api/proxy?type=lhp&kind=stations');
    const data = await response.json();
    // Die Stations-Daten liegen als FeatureCollection vor – Array in data.features
    const stations = data.features || [];
    stationsLayer.clearLayers();
    stationsNoGreenLayer.clearLayers();

    stations.forEach(station => {
        const props = station.properties;
        const popupContent = `
        <div>
            <h3>${_esc(props.name || 'Station')}</h3>
            <p>Gewässer: ${_esc(props.water || 'Keine Angabe')}</p>
            ${_safeUrl(props.stationLink)
            ? `<p>Link: <a href="${_esc(_safeUrl(props.stationLink))}" target="_blank" rel="noopener">${_esc(props.stationLink)}</a></p>`
            : ''}
            <p>Zeitpunkt: ${_esc(props.timestamp || 'Keine Angabe')}</p>
        </div>
        `;

        // Farbe aus dem station.style übernehmen
        const styleColor = (station.style && typeof station.style === 'object' && station.style.color)
        ? station.style.color
        : (station.style || '#0000FF'); // Fallback: Blau

        let layer = null;
        if (station.geometry && station.geometry.type === 'Point') {
        const coords = station.geometry.coordinates;
        layer = L.circleMarker([coords[1], coords[0]], {
            pane: "stationsPane",
            color: styleColor,
            radius: 2,
            fillOpacity: 0.8
        });
        layer.bindPopup(popupContent);
        layer.addTo(stationsLayer);
        if (styleColor !== "#7CBD5C") {
            const nonGreenLayer = L.circleMarker([coords[1], coords[0]], {
            pane: "stationsPane",
            color: styleColor,
            radius: 4,
            fillOpacity: 0.8
            });
            nonGreenLayer.bindPopup(popupContent);
            nonGreenLayer.addTo(stationsNoGreenLayer);
        }
        } else {
        // Fallback: alternative Koordinatenfelder
        let lat = station.lat || (station.coordinates && station.coordinates.latitude);
        let lon = station.lon || (station.coordinates && station.coordinates.longitude);
        if (lat && lon) {
            layer = L.circleMarker([lat, lon], {
            pane: "stationsPane",
            color: styleColor,
            radius: 2,
            fillOpacity: 0.8
            });
            layer.bindPopup(popupContent);
            layer.addTo(stationsLayer);
            if (styleColor !== "#7CBD5C") {
            const nonGreenLayer = L.circleMarker([lat, lon], {
                pane: "stationsPane",
                color: styleColor,
                radius: 4,
                fillOpacity: 0.8
            });
            nonGreenLayer.bindPopup(popupContent);
            nonGreenLayer.addTo(stationsNoGreenLayer);
            }
        }
        }
    });
    } catch (error) {
    console.error('Fehler beim Laden der Stations:', error);
    }
}

// Initiale Aktualisierung beider Layer
updateStations();
updateAlerts();

// Automatische Aktualisierung alle 10 Minuten
setInterval(() => {
    updateStations();
    updateAlerts();
}, 600000);
