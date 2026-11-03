// Erstelle die Karte; Variablenname mit "3" am Ende, wie gewünscht.
const __mowasCenter = [51.163375, 10.447683]; // Deutschlandmitte
const __mowasZoom = 5;
const map3 = L.map('map-mowas').setView(__mowasCenter, __mowasZoom);

// OpenStreetMap-Tiles hinzufügen
L.tileLayer(((window.CURRENT_OV && window.CURRENT_OV.tile_url) || '/tiles/{z}/{x}/{y}.png'), {
    maxZoom: 18,
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> Mitwirkende'
}).addTo(map3);

// Erstelle eine LayerGroup, um die GeoJSON-Features und Marker einfach aktualisieren zu können.
const featuresLayer3 = L.layerGroup().addTo(map3);

// Definiere ein orangefarbenes Icon für den Marker.
const orangeIcon = L.icon({
    iconUrl: '/images/marker-icon-orange.png',
    shadowUrl: '/images/marker-shadow.png',
    iconSize: [25, 41],
    iconAnchor: [12, 41],
    popupAnchor: [1, -34],
    shadowSize: [41, 41]
});

// OV-Marker einzeichnen (bleibt permanent, unabhaengig von featuresLayer3)
if (window.CURRENT_OV && window.CURRENT_OV.lat) {
    try {
        const __ovIconBlue = L.icon({
            iconUrl: '/images/marker-icon.png',
            shadowUrl: '/images/marker-shadow.png',
            iconSize: [25, 41],
            iconAnchor: [12, 41],
            popupAnchor: [1, -34],
            shadowSize: [41, 41]
        });
        L.marker([window.CURRENT_OV.lat, window.CURRENT_OV.lng], { icon: __ovIconBlue, title: window.CURRENT_OV.name })
            .addTo(map3)
            .bindPopup('<strong>' + _esc(window.CURRENT_OV.name) + '</strong>');
    } catch (e) { console.warn('OV-Marker MOWAS:', e); }
}

// _esc: Fremddaten (Warnmeldungen, Pegel, Organisationsname) vor dem Einsetzen
// in HTML entschaerfen.
function _esc(s) { const d = document.createElement('div'); d.textContent = String(s == null ? '' : s); return d.innerHTML; }
// _safeUrl: nur http(s)-Adressen als Link, alles andere wird nicht verlinkt.
function _safeUrl(u) { return /^https?:\/\//i.test(String(u || '')) ? String(u) : ''; }
// Funktion zum Abrufen und Anzeigen der Warnungs- und GeoJSON-Daten.
function updateMapData() {
    // Vor dem Neuladen alle vorhandenen Layer entfernen.
    featuresLayer3.clearLayers();

    // URL der ersten API (Warnungenliste)
    const warningsUrl = '/api/proxy?type=nina';

    fetch(warningsUrl)
    .then(response => response.json())
    .then(warnings => {
        // Für jeden Warnungseintrag:
        warnings.forEach(warning => {
            const warningId = warning.id; // z.B. "mow.DE-HE-DA-W184-20240723-000"
            // Erstelle den Popup-Inhalt basierend auf den Warnungsdaten.
            const popupContent = `
                <a href="https://warnung.bund.de/meldungen/${encodeURIComponent(warningId)}" target="_blank" rel="noopener">
                    <strong>${_esc(warning.i18nTitle && warning.i18nTitle.de)}</strong>
                </a><br>
                Typ: ${_esc(warning.type)} (${_esc(warning.severity)})<br>
                Datum: ${_esc(warning.startDate)}<br>
                Event: ${_esc(warning.transKeys && warning.transKeys.event)}
            `;
            // URL der GeoJSON-Daten, hier wird die Warnungs-ID eingesetzt.
            const geojsonUrl = `/api/proxy?type=nina-geo&id=${encodeURIComponent(warningId)}`;

            // GeoJSON-Daten abrufen
            fetch(geojsonUrl)
                .then(response => response.json())
                .then(geojsonData => {
                    // GeoJSON-Layer erstellen
                    const geojsonLayer = L.geoJSON(geojsonData, {
                        // Style anhand der GeoJSON-Properties definieren.
                        style: function(feature) {
                            return {
                                color: feature.properties.strokeColor,
                                weight: feature.properties.strokeWeight,
                                opacity: feature.properties.strokeOpacity,
                                fillColor: feature.properties.fillColor,
                                fillOpacity: feature.properties.fillOpacity
                            };
                        },
                        // Für jedes Feature ein Popup binden.
                        onEachFeature: function(feature, layer) {
                            layer.on('click', function(e) {
                                layer.bindPopup(popupContent).openPopup();
                            });
                        }
                    });
                    // GeoJSON-Layer der LayerGroup hinzufügen.
                    featuresLayer3.addLayer(geojsonLayer);

                    // Falls die GeoJSON-Geometrie einen gültigen Bereich hat, Marker im Zentrum hinzufügen.
                    if (geojsonLayer.getBounds().isValid()) {
                        const center = geojsonLayer.getBounds().getCenter();
                        const marker = L.marker(center, { icon: orangeIcon });
                        marker.bindPopup(popupContent);
                        featuresLayer3.addLayer(marker);
                    }
                })
                .catch(error => console.error('Fehler beim Laden der GeoJSON-Daten:', error));
        });
    })
    .catch(error => console.error('Fehler beim Laden der Warnungen:', error));
}

// Initiales Laden der Daten.
updateMapData();

// Aktualisiere die Map alle 3 Minuten (180.000 ms).
setInterval(updateMapData, 180000);