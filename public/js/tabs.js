function switchTab(button, contentId) {
    let parentBox = button.closest('.box');
    let tabs = parentBox.querySelectorAll('.tab-btn');
    let contents = parentBox.querySelectorAll('.tab-content');

    // Alle Tabs und Inhalte deaktivieren
    tabs.forEach(tab => tab.classList.remove('active'));
    contents.forEach(content => content.classList.remove('active'));

    // Den angeklickten Tab und dessen Inhalt aktivieren
    button.classList.add('active');
    document.getElementById(contentId).classList.add('active');

    // Falls der Karten-Tab (ID "mapContent") aktiviert wird, 
    // muss die Karte neu gezeichnet werden.
    if (contentId === 'mapContent' && window.map3) {
      setTimeout(() => {
        window.map3.invalidateSize();
      }, 200);
    }
  }

// Tab-Schaltflaechen und externe Links ohne Inline-Handler (CSP ohne
// 'unsafe-inline'): data-tab="content-id" bzw. data-href="https://...".
document.addEventListener('click', function (e) {
  const tab = e.target.closest('.tab-btn[data-tab]');
  if (tab) { switchTab(tab, tab.dataset.tab); return; }
  const link = e.target.closest('[data-href]');
  if (link && /^https?:\/\//i.test(link.dataset.href)) window.open(link.dataset.href, '_blank', 'noopener');
});
document.addEventListener('keydown', function (e) {
  if (e.key !== 'Enter' && e.key !== ' ') return;
  const link = e.target.closest ? e.target.closest('[data-href]') : null;
  if (link && /^https?:\/\//i.test(link.dataset.href)) { e.preventDefault(); window.open(link.dataset.href, '_blank', 'noopener'); }
});
