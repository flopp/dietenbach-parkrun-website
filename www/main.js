const themeButton = document.querySelector(".theme-toggle");
themeButton.addEventListener("click", () => {
  const isDark = document.documentElement.dataset.theme !== "dark";
  document.documentElement.dataset.theme = isDark ? "dark" : "light";
  themeButton.setAttribute("aria-pressed", String(isDark));
  themeButton.textContent = isDark ? "Helles Design" : "Dunkles Design";
});

const visitorCounts = {
  daily: document.querySelector("#visitor-count-daily"),
  monthly: document.querySelector("#visitor-count-monthly"),
  yearly: document.querySelector("#visitor-count-yearly"),
  total: document.querySelector("#visitor-count-total")
};
if (visitorCounts.total) {
  fetch("counter.php", { cache: "no-store" })
    .then((response) => {
      if (!response.ok) {
        throw new Error(`Besucherzähler konnte nicht geladen werden (HTTP ${response.status}).`);
      }
      return response.json();
    })
    .then((counts) => {
      if (Object.entries(visitorCounts).some(([key, element]) =>
        !element || !Number.isSafeInteger(counts[key]) || counts[key] < 0
      )) {
        throw new Error("Der Besucherzähler hat eine ungültige Antwort geliefert.");
      }
      Object.entries(visitorCounts).forEach(([key, element]) => {
        element.textContent = counts[key].toLocaleString("de-DE");
      });
    })
    .catch((error) => {
      console.error(error);
      Object.values(visitorCounts).forEach((element) => {
        if (element) {
          element.textContent = "nicht verfügbar";
        }
      });
    });
}

const fitMap = (map, markers) => {
  const parkBounds = L.latLngBounds(
    [47.999593, 7.798834],
    [48.005662, 7.809992]
  );
  map.fitBounds(parkBounds.extend(markers), { padding: [20, 20] });
};

if (window.L) {
  const meetingPoint = [48.001265, 7.806430];
  const map = L.map("map");
  L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>'
  }).addTo(map);
  L.marker(meetingPoint).addTo(map).bindPopup("Treffpunkt · Dietenbachpark");

  fitMap(map, [meetingPoint]);
  map.whenReady(() => {
    document.querySelector(".map-fallback")?.remove();
  });

  const parkAmenitiesMap = L.map("park-amenities-map");
  L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>'
  }).addTo(parkAmenitiesMap);
  const parkAmenities = [
    ["Dietenbachsee-Strand", [48.000907, 7.802943]],
    ["Skatepark", [48.002710, 7.804445]],
    ["Calisthenics-Park", [48.002924, 7.803361]],
    ["Beach-Volleyball", [48.000483, 7.803726]],
    ["Toiletten", [48.003555, 7.802739]],
    ["Friedenskreuz", [48.000253, 7.801870]],
    ["Diskgolf", [48.000756, 7.805378]],
    ["Spielplatz", [48.001632, 7.806376]],
    ["Parkplatz", [48.000950, 7.808822]],
    ["Baseballfeld", [48.001804, 7.807792]],
    ["Fußballplatz (FFC)", [48.004209, 7.803190]],
    ["Tischtennis", [48.002163, 7.804931]]
  ];
  parkAmenities.forEach(([name, coordinates]) => {
    L.marker(coordinates).addTo(parkAmenitiesMap).bindPopup(name);
  });
  fitMap(parkAmenitiesMap, parkAmenities.map(([, coordinates]) => coordinates));
  parkAmenitiesMap.whenReady(() => {
    document.querySelector("#park-amenities-map .map-fallback")?.remove();
  });

  const cafeLocations = [
    [47.994334, 7.810644],
    [48.001289, 7.817808],
    [47.994523, 7.811184],
    [48.000878, 7.803262],
    [47.998665, 7.792977]
  ];
  const cafeMap = L.map("cafe-map");
  L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>'
  }).addTo(cafeMap);
  [
    ["Bäckerei Heitzmann in Weingarten", cafeLocations[0]],
    ["Küchenschelle", cafeLocations[1]],
    ["Café Odessa in Weingarten", cafeLocations[2]],
    ["Picknick am Dietenbachsee · erster Samstag im Monat, nur im Sommer", cafeLocations[3]],
    ["Kaisers Gute Backstube „artisane“ in Rieselfeld", cafeLocations[4]]
  ].forEach(([name, coordinates]) => {
    L.marker(coordinates).addTo(cafeMap).bindPopup(name);
  });
  fitMap(cafeMap, cafeLocations);
  cafeMap.whenReady(() => {
    document.querySelector("#cafe-map .map-fallback")?.remove();
  });

  const courseMap = L.map("course-map");
  L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>'
  }).addTo(courseMap);
  fitMap(courseMap, []);
  fetch("course.geojson")
    .then((response) => {
      if (!response.ok) {
        throw new Error(`Streckendaten konnten nicht geladen werden (HTTP ${response.status}).`);
      }
      return response.json();
    })
    .then((course) => {
      const route = L.geoJSON(course, {
        style: { color: "#d98200", weight: 5, opacity: 0.9 },
        onEachFeature: (feature, layer) => {
          if (feature.properties?.name) {
            layer.bindPopup(feature.properties.name);
          }
        }
      }).addTo(courseMap);
      fitMap(courseMap, [route.getBounds()]);
      document.querySelector("#course-map .map-fallback")?.remove();
    })
    .catch((error) => {
      console.error(error);
      const status = document.querySelector("#course-map .map-fallback");
      if (status) {
        status.textContent = "Die Streckenkarte konnte nicht geladen werden. Öffne die Originalkarte in Google My Maps.";
      }
    });
} else {
  document.querySelector("#course-map .map-fallback").textContent =
    "Die Kartenbibliothek konnte nicht geladen werden. Öffne die Originalkarte in Google My Maps.";
  document.querySelector("#park-amenities-map .map-fallback").textContent =
    "Die Karte des Dietenbachparks konnte nicht geladen werden.";
}