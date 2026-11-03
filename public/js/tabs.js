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