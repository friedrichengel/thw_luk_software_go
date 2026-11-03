// DWD-Warnkarte: WMS-Ebenen des DWD-Geoproxys ueber Leaflet. Die fruehere
// GetFeatureInfo-Abfrage per JSONP (Fremdscript von maps.dwd.de) ist entfernt:
// die Content-Security-Policy laesst kein fremdes Script zu.
// _esc: Fremddaten (Warnmeldungen, Pegel, Organisationsname) vor dem Einsetzen
// in HTML entschaerfen.
function _esc(s) { const d = document.createElement('div'); d.textContent = String(s == null ? '' : s); return d.innerHTML; }
// _safeUrl: nur http(s)-Adressen als Link, alles andere wird nicht verlinkt.
function _safeUrl(u) { return /^https?:\/\//i.test(String(u || '')) ? String(u) : ''; }
// Initialisierung der Leaflet-Karte im DIV mit der ID "kartencontainer"
// Zentrum: Koordinaten des eingeloggten OV, sonst Deutschlandmitte
var __dwdCenter = [51.163, 10.447]; // Deutschlandmitte
var __dwdZoom = 5;
var karte = L.map("kartencontainer", {
center: __dwdCenter,
zoom: __dwdZoom,
zoomControl: true,
dragging: true,
attributionControl: true
});

// OV-Marker einzeichnen
if (window.CURRENT_OV && window.CURRENT_OV.lat) {
    try {
        var __ovIconDwd = L.icon({
            iconUrl: '/images/marker-icon.png',
            shadowUrl: '/images/marker-shadow.png',
            iconSize: [25, 41],
            iconAnchor: [12, 41],
            popupAnchor: [1, -34],
            shadowSize: [41, 41]
        });
        L.marker([window.CURRENT_OV.lat, window.CURRENT_OV.lng], { icon: __ovIconDwd, title: window.CURRENT_OV.name })
            .addTo(karte)
            .bindPopup('<strong>' + _esc(window.CURRENT_OV.name) + '</strong>');
    } catch (e) { /* OV-Marker nicht setzbar, still ignorieren */ }
}

// OSM-Hintergrundlayer
var osmlayer = L.tileLayer(((window.CURRENT_OV && window.CURRENT_OV.tile_url) || '/tiles/{z}/{x}/{y}.png'), {
attribution:
    'Map data: &copy; <a href="https://openstreetmap.org">OpenStreetMap</a> contributors',
maxZoom: 18
});

// Warnungs-Layer vom DWD-Geoserver (mit der erweiterten WMS-Funktionalität)
var warnlayer = L.tileLayer.wms("https://maps.dwd.de/geoproxy_warnungen/service/", {
layers: "Warnungen_Gemeinden_vereinigt",
format: "image/png",
transparent: true,
opacity: 0.8,
attribution: 'Warndaten: &copy; <a href="https://www.dwd.de">DWD</a>'
});

// Layer mit neutraler Darstellung der Gemeindegrenzen
var gemeindelayer = L.tileLayer.wms("https://maps.dwd.de/geoproxy_warnungen/service/", {
layers: "Warngebiete_Gemeinden",
format: "image/png",
styles: "",
transparent: true,
opacity: 0.6,
attribution:
    'Geobasisdaten Gemeinden: &copy; <a href="https://www.bkg.bund.de">BKG</a> 2015 (Daten verändert)'
});

// Layer-Kontrolle mit den Basis- und Overlay-Layern
var baseLayers = {
"OpenStreetMap": osmlayer.addTo(karte)
};
var overLayers = {
"<span title='Wetter- und Unwetterwarnungen einblenden'>Warnungen einblenden</span>": warnlayer.addTo(karte),
"<span title='Gemeindegrenzen auf Warnkarte einblenden'>Gemeindegrenzen einblenden</span>": gemeindelayer
};

L.control.layers(baseLayers, overLayers).addTo(karte);

// Funktion zur Aktualisierung der dynamischen WMS-Layer, um z. B. Caching zu umgehen
function aktualisiereLayer() {
// Neuen Zeitstempel als Parameter setzen
var timestamp = new Date().getTime();
warnlayer.setParams({ time: timestamp });
gemeindelayer.setParams({ time: timestamp });

// Die Layer neu zeichnen, sodass neue Tiles geladen werden
warnlayer.redraw();
gemeindelayer.redraw();
}

// Aktualisierung alle 10 Minuten (600000 Millisekunden)
setInterval(aktualisiereLayer, 600000);