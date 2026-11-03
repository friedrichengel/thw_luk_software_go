/* THW LuK Stabsansicht - Service Worker
 *
 * Aufgabe: Macht das Dashboard als PWA installierbar (Tablet-/Home-Screen)
 * und cached statische Assets (CSS, JS, Bilder, Fonts), damit das Dashboard
 * auch beim ersten Reload nach Verbindungsabbruch noch das Layout aufbaut.
 *
 * Wichtig:
 *  - Seitenaufrufe (Navigationen), Seiten und API-Antworten werden NIE
 *    gecached und auch nicht abgefangen: Die Login-Pruefung laeuft immer
 *    ueber den Server. Gecached werden nur Dateien mit bekannter statischer
 *    Endung (CSS, JS, Bilder, Fonts, GeoJSON).
 *  - Cross-Origin-Anfragen (Tiles, Karten-APIs) werden vom SW ignoriert.
 *  - Beim Versionswechsel (CACHE_NAME aendern) werden alte Caches geloescht.
 */
const CACHE_NAME = 'thw-luk-v5';

// Nur diese Endungen gelten als statisch und duerfen in den Cache.
const STATIC_EXT = /\.(css|js|mjs|png|jpg|jpeg|gif|svg|webp|ico|woff2?|ttf|otf|eot|geojson|json)$/i;

// Nur diese Pfade liefern statische Dateien (siehe httpx.RegisterStatic); alles
// andere (Seiten, /api/) laeuft immer ueber den Server und die Login-Pruefung.
const STATIC_PREFIX = /^\/(css|js|images|fonts|geo)\//;

const PRECACHE = [
  '/css/pagelayout.css',
  '/css/tablet.css',
  '/css/leaflet.css',
  '/css/luk.css',
  '/images/favicon_dashboard.png',
  '/images/favicon_steinmap.png',
  '/images/favicon_steinboard.png',
  '/images/favicon_diveramap.png',
  '/images/THW.svg',
  '/images/image.png'
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) => {
      // einzeln cachen, damit ein 404 nicht den ganzen install kippt
      return Promise.all(PRECACHE.map((url) =>
        cache.add(url).catch(() => null)
      ));
    })
  );
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(
        keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k))
      ))
      .then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;

  let url;
  try {
    url = new URL(req.url);
  } catch (e) {
    return;
  }

  // Cross-Origin: nicht eingreifen (Karten-Tiles, Proxys, externe APIs).
  if (url.origin !== self.location.origin) return;

  // Seitenaufrufe immer zum Server durchreichen: Nur so greift die
  // Login-Umleitung (requirePage/requireLogin). Betrifft auch "/" und
  // Pfade ohne Endung, die sonst als Asset gecached wuerden.
  if (req.mode === 'navigate' || req.destination === 'document') return;

  // Niemals dynamische/auth-pflichtige Antworten cachen: nur Dateien unter den
  // statischen Pfaden mit bekannter Endung kommen in den Cache.
  if (!STATIC_PREFIX.test(url.pathname)) return;
  if (!STATIC_EXT.test(url.pathname)) return;

  // Cache-first fuer statische Assets, Netzwerk-Update im Hintergrund.
  event.respondWith(
    caches.match(req).then((cached) => {
      const networkFetch = fetch(req).then((res) => {
        // Umgeleitete Antworten (z. B. auf login) nie unter der
        // Asset-URL ablegen.
        if (res && res.ok && !res.redirected && (res.type === 'basic' || res.type === 'default')) {
          const copy = res.clone();
          caches.open(CACHE_NAME).then((cache) => cache.put(req, copy)).catch(() => {});
        }
        return res;
      }).catch(() => cached);
      return cached || networkFetch;
    })
  );
});
