const comparison = document.querySelector(".comparison");
const range = comparison.querySelector(".comparison-range");
const darkImage = comparison.querySelector(".comparison-dark");

function updateSplit() {
  const light = range.valueAsNumber;
  comparison.style.setProperty("--split", light + "%");
  range.setAttribute("aria-valuetext", "浅色 " + light + "%，深色 " + (100 - light) + "%");
}

function moveSplit(event) {
  const bounds = comparison.getBoundingClientRect();
  const percent = 100 * (event.clientX - bounds.left) / bounds.width;
  range.valueAsNumber = Math.round(Math.min(100, Math.max(0, percent)));
  updateSplit();
}

range.addEventListener("input", updateSplit);
range.addEventListener("pointerdown", (event) => {
  if (!event.isPrimary || event.button !== 0) return;
  event.preventDefault();
  range.focus({ preventScroll: true });
  range.setPointerCapture(event.pointerId);
  moveSplit(event);
});
range.addEventListener("pointermove", (event) => {
  if (range.hasPointerCapture(event.pointerId)) moveSplit(event);
});

darkImage.decode().then(() => {
  updateSplit();
  for (const element of comparison.querySelectorAll("[hidden]")) element.hidden = false;
}).catch(() => {
  // Keep the light screenshot as a fallback if the other image fails to load.
});
