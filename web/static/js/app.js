// SPDX-License-Identifier: Apache-2.0
//
// Small enhancements for every page (docs/DESIGN.md §11):
//   - remembers checked-off ingredients per recipe for this browser session
//     (FR-09, P4.2), with a reset button
//   - registers the service worker (PWA, §11.3) and shows offline status
// Everything works without this script.

const slug = document.body.dataset.recipe || "";

// --- ingredient check-off -------------------------------------------------

function checkKey() {
  return "grimoire.checks." + slug;
}

function loadChecks() {
  try {
    return new Set(JSON.parse(sessionStorage.getItem(checkKey())) || []);
  } catch {
    return new Set();
  }
}

function saveChecks(set) {
  try {
    if (set.size) sessionStorage.setItem(checkKey(), JSON.stringify([...set]));
    else sessionStorage.removeItem(checkKey());
  } catch {
    /* storage unavailable: checks just won't survive a reload */
  }
}

function applyChecks(root = document) {
  if (!slug) return;
  const checked = loadChecks();
  for (const box of root.querySelectorAll(".ingredients input[type=checkbox][name]")) {
    box.checked = checked.has(box.name);
  }
  updateReset(checked);
}

function updateReset(checked) {
  for (const btn of document.querySelectorAll(".js-clear-checks")) btn.hidden = checked.size === 0;
}

if (slug) {
  document.addEventListener("change", (e) => {
    const box = e.target;
    if (!box.matches(".ingredients input[type=checkbox][name]")) return;
    const checked = loadChecks();
    if (box.checked) checked.add(box.name);
    else checked.delete(box.name);
    saveChecks(checked);
    // Keep the same ingredient in sync between the page and the cook drawer.
    for (const twin of document.querySelectorAll(`.ingredients input[name="${CSS.escape(box.name)}"]`)) {
      twin.checked = box.checked;
    }
    updateReset(checked);
  });
  document.addEventListener("click", (e) => {
    if (!e.target.closest(".js-clear-checks")) return;
    saveChecks(new Set());
    applyChecks();
  });
  // The scaler swaps the ingredient list in place; re-apply after it does.
  document.body.addEventListener("htmx:afterSwap", (e) => applyChecks(e.target));
  applyChecks();
}

// --- PWA: service worker and offline status --------------------------------

if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("/sw.js", { scope: "/" }).then(() => {
    markOffline();
    prefetchFavorites();
  }).catch(() => {});
}

// Show whether this recipe can be read offline (§11.3).
async function markOffline() {
  const badge = document.getElementById("offline-badge");
  if (!badge || !("caches" in window)) return;
  try {
    const hit = await caches.match(location.pathname, { ignoreSearch: true });
    badge.hidden = !hit;
  } catch {
    /* ignore */
  }
}

// On the home page, ask the worker to keep favorites available offline.
function prefetchFavorites() {
  const links = [...document.querySelectorAll("[data-favorites] a.card")].map((a) => a.getAttribute("href"));
  if (links.length && navigator.serviceWorker.controller) {
    navigator.serviceWorker.controller.postMessage({ type: "cache-pages", urls: links });
  }
}

// navigator.onLine is unreliable (captive portals, flaky Wi-Fi, and pages
// served from the worker's cache all report "online"), so check that the
// server actually answers. /healthz is never cached by the worker.
async function showConnectivity() {
  const banner = document.getElementById("offline-banner");
  if (!banner) return;
  let reachable = false;
  try {
    const ctrl = new AbortController();
    const t = setTimeout(() => ctrl.abort(), 3000);
    const res = await fetch("/healthz", { cache: "no-store", signal: ctrl.signal });
    clearTimeout(t);
    reachable = res.ok;
  } catch {
    reachable = false;
  }
  banner.hidden = reachable;
}
window.addEventListener("online", showConnectivity);
window.addEventListener("offline", showConnectivity);
showConnectivity();
