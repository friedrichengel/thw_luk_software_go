function getTacticalTime(mode, dateInput) {
  // Falls kein Datum übergeben wurde, verwende die aktuelle Zeit
  const now = dateInput ? new Date(dateInput) : new Date();
  const options = {
    timeZone: "Europe/Berlin",
    day: "2-digit",
    month: "short",
    year: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false
  };

  // Formatieren des Datums (z.B. "01 Jan 25, 08:15")
  let formattedDate = new Intl.DateTimeFormat("en-GB", options).format(now);

  // Entferne das Komma und teile den String in einzelne Bestandteile
  let cleanedDate = formattedDate.replace(",", "");
  let parts = cleanedDate.split(" ");

  // Extrahiere Tag, Monat und Jahr
  let day = parts[0];
  let month = parts[1].toLowerCase();
  let year = parts[2];

  // Uhrzeit robust per Regex ziehen (funktioniert überall)
  const timeMatch = formattedDate.match(/\b(\d{2}):(\d{2})\b/);
  let hour = timeMatch ? timeMatch[1] : "00";
  let minute = timeMatch ? timeMatch[2] : "00";

  // Mapping-Tabelle für Monatskürzel
  const monthMap = {
    jan: "jan",
    feb: "feb",
    mar: "mar",
    apr: "apr",
    may: "may",
    jun: "jun",
    jul: "jul",
    aug: "aug",
    sept: "sep", // Safari liefert "Sept"
    sep: "sep",
    oct: "oct",
    nov: "nov",
    dec: "dec"
  };

  // Normalisiere Monat
  let monthCode = monthMap[month] || month;

  // Farbe des Stunden-/Minuten-Teils: schwarz, wenn mode "clock" ist, sonst gelb
  if (mode === "export") {
    return `${day}${hour}${minute}${monthCode}${year}`;
  } else {
    let color = (mode === "clock") ? "black" : "rgb(255,255,0)";
    return `${day}<span style="color: ${color}">${hour}${minute}</span>${monthCode}${year}`;
  }
}

function updateTacticalTime() {
  document.getElementById("tacticalClock").innerHTML = getTacticalTime();
}

setInterval(updateTacticalTime, 10000);

document.addEventListener("DOMContentLoaded", () => {
  updateTacticalTime();
});
