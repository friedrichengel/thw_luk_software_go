// Erweiterte WMS-Kachel mit JSONP-Unterstützung für GetFeatureInfo-Anfragen
L.TileLayer.BetterWMS = L.TileLayer.WMS.extend({
onAdd: function(map) {
    // Wenn die Layer zur Karte hinzugefügt wird, registriere einen Klick-Listener
    L.TileLayer.WMS.prototype.onAdd.call(this, map);
    map.on("click", this.getFeatureInfoJsonp, this);
},

onRemove: function(map) {
    // Entferne den Klick-Listener, wenn die Layer von der Karte entfernt wird
    L.TileLayer.WMS.prototype.onRemove.call(this, map);
    map.off("click", this.getFeatureInfoJsonp, this);
},

getFeatureInfo: function(evt) {
    // Standard-AJAX-Anfrage (nicht JSONP)
    var url = this.getFeatureInfoUrl(evt.latlng),
        showResults = L.Util.bind(this.showGetFeatureInfo, this);
    $.ajax({
    url: url,
    success: function(data, status, xhr) {
        var err = typeof data === "string" ? null : data;
        showResults(err, evt.latlng, data);
    },
    error: function(xhr, status, error) {
        showResults(error);
    }
    });
},

// JSONP-Version der GetFeatureInfo-Anfrage
getFeatureInfoJsonp: function(evt) {
    var url = this.getFeatureInfoUrl(evt.latlng),
        showResultsJson = L.Util.bind(this.showGetFeatureInfoJson, this);
    $.ajax({
    url: url,
    dataType: "jsonp",
    jsonpCallback: "parseResponse",
    success: function(data) {
        showResultsJson(evt.latlng, data);
    }
    });
},

getFeatureInfoUrl: function(latlng) {
    // Erstelle die URL für die GetFeatureInfo-Anfrage
    var point = this._map.latLngToContainerPoint(latlng, this._map.getZoom()),
        size = this._map.getSize(),
        params = {
        request: "GetFeatureInfo",
        service: "WMS",
        srs: "EPSG:4326",
        styles: this.wmsParams.styles,
        transparent: this.wmsParams.transparent,
        version: this.wmsParams.version,
        format: this.wmsParams.format,
        bbox: this._map.getBounds().toBBoxString(),
        height: size.y,
        width: size.x,
        layers: this.wmsParams.layers,
        query_layers: this.wmsParams.layers,
        info_format: "text/javascript",
        // Es werden nur ausgewählte Eigenschaften abgefragt
        propertyName: "EVENT,ONSET,EXPIRES,SENT",
        // Bei überlappenden Warnungen werden bis zu 50 Features abgefragt
        FEATURE_COUNT: 50
        };

    params[params.version === "1.3.0" ? "i" : "x"] = point.x;
    params[params.version === "1.3.0" ? "j" : "y"] = point.y;

    return this._url + L.Util.getParamString(params, this._url, true);
},

showGetFeatureInfo: function(err, latlng, content) {
    if (err) {
    return;
    }
    // Zeige die rohe Antwort als Popup an
    L.popup({ maxWidth: 800 })
    .setLatLng(latlng)
    .setContent(content)
    .openOn(this._map);
},

showGetFeatureInfoJson: function(latlng, data) {
    // Falls keine Features zurückgegeben wurden, tue nichts
    if (!data.features || data.features.length === 0) {
    return;
    }
    var content = "<h2>Amtliche Warnung</h2>";
    $.each(data.features, function(i, item) {
    var o = new Date(item.properties.ONSET);
    var e = new Date(item.properties.EXPIRES);
    var onset =
        ("0" + o.getDate()).slice(-2) + "." +
        ("0" + (o.getMonth() + 1)).slice(-2) + ". - " +
        ("0" + o.getHours()).slice(-2) + ":" +
        ("0" + o.getMinutes()).slice(-2) + " Uhr";
    var end =
        ("0" + e.getDate()).slice(-2) + "." +
        ("0" + (e.getMonth() + 1)).slice(-2) + ". - " +
        ("0" + e.getHours()).slice(-2) + ":" +
        ("0" + e.getMinutes()).slice(-2) + " Uhr";
    content +=
        "<p><table><tr><td>Ereignis:</td><td><b>" +
        item.properties.EVENT +
        "</b></td></tr>" +
        "<tr><td>Beginn:</td><td>" + onset + "</td></tr>" +
        "<tr><td>Ende:</td><td>" + end + "</td></tr></table></p>";
    });
    content += "<a target='blank' href='https://www.dwd.de/warnungen'>www.dwd.de/warnungen</a>";

    L.popup({ maxWidth: 800 })
    .setLatLng(latlng)
    .setContent(content)
    .openOn(this._map);
}
});

// Hilfsfunktion, um die neue Layer-Klasse zu instanziieren
L.tileLayer.betterWms = function(url, options) {
return new L.TileLayer.BetterWMS(url, options);
};

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
            .bindPopup('<strong>' + (window.CURRENT_OV.name || '') + '</strong>');
    } catch (e) { /* OV-Marker nicht setzbar, still ignorieren */ }
}

// OSM-Hintergrundlayer
var osmlayer = L.tileLayer(((window.CURRENT_OV && window.CURRENT_OV.tile_url) || '/tiles/{z}/{x}/{y}.png'), {
attribution:
    'Map data: &copy; <a href="https://openstreetmap.org">OpenStreetMap</a> contributors',
maxZoom: 18
});

// Warnungs-Layer vom DWD-Geoserver (mit der erweiterten WMS-Funktionalität)
var warnlayer = L.tileLayer.betterWms("https://maps.dwd.de/geoproxy_warnungen/service/", {
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