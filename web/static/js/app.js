// SPDX-License-Identifier: Apache-2.0
//
// Small enhancements for every page (docs/DESIGN.md §11):
//   - remembers checked-off ingredients per recipe for this browser session
//     (FR-09, P4.2), with a reset button; on a meal's shopping list, checks
//     are kept on this device until reset (§9.4)
//   - "Copy as text" and "Print" on the shopping list
//   - Screen Wake Lock (FR-09): always on in cook mode, with an indicator;
//     an opt-in "Keep screen on" toggle on the recipe page
//   - registers the service worker (PWA, §11.3) and shows offline status
// Everything works without this script.

const slug = document.body.dataset.recipe || "";
// A shopping trip outlasts a browser session, so meal lists use localStorage.
const checkStore = () => (slug.startsWith("meal-") ? localStorage : sessionStorage);

// --- ingredient check-off -------------------------------------------------

function checkKey() {
  return "grimoire.checks." + slug;
}

function loadChecks() {
  try {
    return new Set(JSON.parse(checkStore().getItem(checkKey())) || []);
  } catch {
    return new Set();
  }
}

function saveChecks(set) {
  try {
    if (set.size) checkStore().setItem(checkKey(), JSON.stringify([...set]));
    else checkStore().removeItem(checkKey());
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

// --- shopping list: copy and print -----------------------------------------

// The text is every [data-line] in page order, leaving out rows already
// checked off, so it can go straight into a notes or reminders app.
function listText() {
  const out = [];
  for (const el of document.querySelectorAll("[data-line]")) {
    const box = el.querySelector("input[type=checkbox]");
    if (box && box.checked) continue;
    out.push(box ? "- " + el.dataset.line : "\n" + el.dataset.line);
  }
  return out.join("\n").trim() + "\n";
}

async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // Older browsers, or no permission: copy through a hidden textarea.
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.className = "visually-hidden";
    document.body.append(ta);
    ta.select();
    let ok = false;
    try {
      ok = document.execCommand("copy");
    } catch {
      ok = false;
    }
    ta.remove();
    return ok;
  }
}

for (const btn of document.querySelectorAll(".js-copy-list")) {
  btn.hidden = false;
  const label = btn.textContent;
  btn.addEventListener("click", async () => {
    const ok = await copyText(listText());
    btn.textContent = ok ? "Copied ✓" : "Couldn't copy";
    setTimeout(() => (btn.textContent = label), 2000);
  });
}

for (const btn of document.querySelectorAll(".js-print")) {
  btn.hidden = false;
  btn.addEventListener("click", () => window.print());
}

// Print the collapsed "Probably on hand" group too.
window.addEventListener("beforeprint", () => {
  for (const d of document.querySelectorAll(".shop-staples")) {
    d.dataset.wasOpen = d.open ? "1" : "";
    d.open = true;
  }
});
window.addEventListener("afterprint", () => {
  for (const d of document.querySelectorAll(".shop-staples")) d.open = d.dataset.wasOpen === "1";
});

// --- screen wake lock ------------------------------------------------------

// screenWake holds a screen wake lock while it is wanted. Browsers drop the
// lock whenever the page is hidden, so it is taken again on return. If the
// browser drops it while the page is still showing (low battery, for
// example), that counts as turning it off. onChange(held) reports each change.
function screenWake(onChange) {
  let lock = null;
  let wanted = false;
  let pending = false;
  async function acquire() {
    if (!wanted || lock || pending || document.visibilityState !== "visible") return;
    pending = true;
    try {
      const l = await navigator.wakeLock.request("screen");
      if (!wanted) {
        l.release();
        return;
      }
      lock = l;
      l.addEventListener("release", () => {
        if (lock !== l) return;
        lock = null;
        if (document.visibilityState === "visible") wanted = false;
        onChange(false);
      });
      onChange(true);
    } catch {
      wanted = false;
      onChange(false);
    } finally {
      pending = false;
    }
  }
  document.addEventListener("visibilitychange", acquire);
  return {
    on() {
      wanted = true;
      acquire();
    },
    off() {
      wanted = false;
      const l = lock;
      lock = null;
      if (l) l.release();
      onChange(false);
    },
  };
}

const canWake = "wakeLock" in navigator;

// Cook mode: hold the lock the whole time, and say whether it's held.
const wakeNote = document.getElementById("wake");
if (wakeNote) {
  wakeNote.hidden = false;
  const show = (held) => {
    wakeNote.textContent = held ? "Screen stays on" : "Screen may sleep";
    wakeNote.classList.toggle("on", held);
  };
  show(false);
  if (canWake) {
    const wake = screenWake(show);
    wake.on();
    // In cook mode the lock is always wanted, even after the browser drops it.
    document.addEventListener("visibilitychange", () => wake.on());
  } else {
    wakeNote.title = "This browser can't keep the screen on. Raise your auto-lock time while cooking.";
  }
}

// Recipe page: an opt-in toggle, shown only where the API exists.
const wakeToggle = document.getElementById("wake-toggle");
if (wakeToggle && canWake) {
  const wake = screenWake((held) => wakeToggle.setAttribute("aria-pressed", String(held)));
  wakeToggle.hidden = false;
  wakeToggle.addEventListener("click", () => {
    if (wakeToggle.getAttribute("aria-pressed") === "true") wake.off();
    else wake.on();
  });
}

// --- PWA: service worker and offline status --------------------------------

// A different person signed in on this device: drop the offline copies of
// pages (they were rendered for someone else).
(function forgetOtherUsersPages() {
  const user = document.body.dataset.user;
  if (!user || !("caches" in window)) return;
  try {
    const last = localStorage.getItem("grimoire.user");
    if (last && last !== user) {
      caches.keys().then((keys) => keys.filter((k) => k.startsWith("grimoire-pages-")).forEach((k) => caches.delete(k)));
      sessionStorage.clear();
    }
    localStorage.setItem("grimoire.user", user);
  } catch {
    /* storage unavailable: nothing was cached for anyone either */
  }
})();

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
