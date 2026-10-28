# Information Page for Dietenbach parkrun

https://parkrun.freiburg.run


## Technical details:

- An interactive Leaflet map previews the parkrun course and shows amenities in the
  park and nearby cafes.
- The Saturday weather forecast is retrieved from the Open-Meteo API.
- Past event data is read from the Google Sheet and displayed with participant
  counts and links to results, event reports, and photo albums.
- The site generator names `style.css` and `main.js` using their SHA-256 checksums.
  When either file changes, the generated HTML references a new filename so browsers
  fetch the updated asset on the next page load.
