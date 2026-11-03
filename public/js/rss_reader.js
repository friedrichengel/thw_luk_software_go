      document.addEventListener("DOMContentLoaded", function () {
        // Drittinhalte (RSS-Titel, Description, Link) muessen escaped werden,
        // bevor sie ins HTML interpoliert werden. Sonst koennte boeser
        // Feed-Content (Stored-XSS aus dem Quell-RSS) Skripte einschmuggeln.
        function _esc(s) {
          const d = document.createElement("div");
          d.textContent = String(s == null ? "" : s);
          return d.innerHTML;
        }
        function _safeUrl(u) {
          // Erlaubt nur http/https, sonst leeren Anchor.
          const s = String(u == null ? "" : u).trim();
          return /^https?:\/\//i.test(s) ? _esc(s) : "#";
        }
        // Feeds kommen vom Server (window.LUK_FEEDS: eigene Feeds der
        // Organisation oder Standard); die feste Liste darunter ist nur
        // Rueckfall, wenn das Skript ohne diese Angabe laeuft.
        function feedsFromServer() {
          const list = Array.isArray(window.LUK_FEEDS) ? window.LUK_FEEDS : null;
          if (!list || !list.length) return null;
          return list.map(function (f) {
            const btn = document.querySelector('.tab-btn[onclick*="content' + f.slot + '"]');
            if (btn && f.title) btn.textContent = f.title;
            return { urls: (f.urls || []).slice(), containerId: "rss-feed-" + f.slot, maxItems: 0, sortDescending: true };
          });
        }
        // Rueckfall: nur ueberregionale Quellen; die regionalen Kacheln 6-1/6-2
        // kommen aus den Stammdaten (Abschnitt "feeds") bzw. der Organisation.
        const feeds = feedsFromServer() || [
          {
              url: "https://gdacs.org/xml/rss_24h.xml",
              containerId: "rss-feed-4-2",
              maxItems: 0,
              sortDescending: true
          },
          {
              url: "https://www.tagesschau.de/infoservices/alle-meldungen-100~rss2.xml",
              containerId: "rss-feed-4-1",
              maxItems: 0,
              sortDescending: true
          }
        ];

        const updateInterval = 180000; // Aktualisierungsintervall in Millisekunden (3 Min)

        // Antwort des Servers lesen: JSON oder, bei Fehlern (z. B. HTML-Fehlerseite
        // eines Proxys), eine verstaendliche Meldung mit HTTP-Status.
        async function fetchFeedJson(url) {
          const response = await fetch(url, { credentials: "same-origin" });
          const text = await response.text();
          let data = null;
          try { data = JSON.parse(text); } catch (e) { data = null; }
          if (!response.ok || !data) {
            const code = data && data.error ? data.error : ("HTTP " + response.status + (data ? "" : ", keine JSON-Antwort"));
            throw new Error("Server meldet " + code);
          }
          return data;
        }

        async function fetchRSS(feed) {
          const container = document.getElementById(feed.containerId);
          if (!container) {
            console.error(
              `Container mit ID ${feed.containerId} nicht gefunden.`
            );
            return;
          }

          try {
            let items = [];

            if (feed.urls && Array.isArray(feed.urls)) {
              // Mehrere Feeds einlesen
              const promises = feed.urls.map((url) =>
                fetchFeedJson(`/api/proxy?type=rss&url=${encodeURIComponent(url)}`)
              );
              const results = await Promise.all(promises);
              results.forEach((data) => {
                if (data.status === "ok") {
                  items = items.concat(data.items);
                } else {
                  console.error("RSS-Feed konnte nicht geladen werden.");
                }
              });
            } else if (feed.url) {
              // Ein einzelner Feed
              const data = await fetchFeedJson(`/api/proxy?type=rss&url=${encodeURIComponent(feed.url)}`);

              if (data.status !== "ok") {
                throw new Error("RSS-Feed konnte nicht geladen werden.");
              }

              items = data.items;
            }

            // Filter: Keine Einträge mit "Video |" am Anfang des Titels
            items = items.filter(item => !item.title.startsWith("Video |") && !item.title.startsWith("Video:"));

            // Zeitzonen-Korrektur: Falls ein Datum fehlt oder ungültig ist, ans Ende setzen
            items.forEach((item) => {
              const parsedDate = new Date(item.pubDate);
              if (isNaN(parsedDate.getTime())) {
                item.dateObject = new Date(0); // Falls Datum ungültig ist, auf frühestes mögliches Datum setzen
              } else {
                item.dateObject = new Date(parsedDate.toISOString()); // UTC-Sicherung
              }
            });

            // Duplikate entfernen basierend auf Kombination von link und title
            const seen = new Set();
            items = items.filter((item) => {
              const key = item.link + item.title;
              if (seen.has(key)) {
                return false;
              }
              seen.add(key);
              return true;
            });

            // Zuerst aufsteigend nach Zeit sortieren
            items.sort((a, b) => a.dateObject - b.dateObject);

            // Falls absteigende Sortierung gewünscht, Liste umdrehen
            if (feed.sortDescending) {
              items.reverse();
            }

            // Maximale Anzahl von Einträgen begrenzen
            if (feed.maxItems > 0) {
                items = items.slice(0, feed.maxItems);
            }

            displayFeed(items, container);
          } catch (error) {
            container.innerHTML = `<p>Fehler beim Laden des Feeds: ${_esc(error.message)}</p>`;
          }
        }

        function displayFeed(items, container) {
          container.innerHTML = items
            .map((item) => {
              const date = item.dateObject;
              const formattedDate = date.toLocaleDateString("de-DE", {
                weekday: "long",
                year: "numeric",
                month: "long",
                day: "numeric",
              });
              const formattedTime = date.toLocaleTimeString("de-DE", {
                hour: "2-digit",
                minute: "2-digit",
                second: "2-digit",
              });
              return `
                <div class="rss-item">
                  <h3><a href="${_safeUrl(item.link)}" target="_blank" rel="noopener noreferrer">${_esc(item.title)}</a></h3>
                  <p><strong>${_esc(formattedDate)} - ${_esc(formattedTime)} Uhr</strong></p>
                  <p>${_esc(item.description)}</p>
                </div>
                <hr>
              `;
            })
            .join("");
        }

        function updateFeeds() {
          feeds.forEach((feed) => fetchRSS(feed));
        }

        updateFeeds();
        setInterval(updateFeeds, updateInterval);
      });
