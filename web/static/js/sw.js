// SPDX-License-Identifier: Apache-2.0
//
// Service worker (docs/DESIGN.md §11.3):
//   - pages: network first, falling back to the cached copy when offline
//   - static assets and photos: cache first (their URLs are content-hashed)
//   - recipe pages you open (and your favorites) are kept for offline reading
// No offline editing: only GET requests are ever cached.

const VERSION = "v1";
const PAGES = "grimoire-pages-" + VERSION;
const ASSETS = "grimoire-assets-" + VERSION;
const MAX_PAGES = 60;
const NETWORK_TIMEOUT_MS = 4000;

self.addEventListener("install", () => self.skipWaiting());

self.addEventListener("activate", (event) => {
  event.waitUntil((async () => {
    for (const key of await caches.keys()) {
      if (key !== PAGES && key !== ASSETS) await caches.delete(key);
    }
    await self.clients.claim();
  })());
});

// Pages worth keeping offline: home, recipe views, and cook mode.
function cacheablePage(url) {
  return url.pathname === "/" || /^\/r\/[^/]+(\/cook)?$/.test(url.pathname);
}

self.addEventListener("fetch", (event) => {
  const req = event.request;
  const url = new URL(req.url);
  if (req.method !== "GET" || url.origin !== self.location.origin) return;
  if (url.pathname.startsWith("/static/") || url.pathname.startsWith("/img/")) {
    event.respondWith(cacheFirst(req));
    return;
  }
  if (req.mode === "navigate") {
    event.respondWith(networkFirst(req, url));
  }
});

async function cacheFirst(req) {
  const cache = await caches.open(ASSETS);
  const hit = await cache.match(req);
  if (hit) return hit;
  const res = await fetch(req);
  if (res.ok && res.type === "basic") cache.put(req, res.clone());
  return res;
}

async function networkFirst(req, url) {
  const cache = await caches.open(PAGES);
  try {
    const res = await withTimeout(fetch(req), NETWORK_TIMEOUT_MS);
    // Only keep real pages from our own origin: never a redirect to the
    // sign-in page (an expired SSO session) in place of a recipe.
    if (res.ok && !res.redirected && cacheablePage(url)) {
      await cache.put(url.pathname, res.clone());
      trim(cache);
    }
    return res;
  } catch {
    const hit = await cache.match(url.pathname);
    return hit || offlinePage();
  }
}

function withTimeout(promise, ms) {
  return new Promise((resolve, reject) => {
    const t = setTimeout(() => reject(new Error("timeout")), ms);
    promise.then((v) => { clearTimeout(t); resolve(v); }, (e) => { clearTimeout(t); reject(e); });
  });
}

async function trim(cache) {
  const keys = await cache.keys();
  for (const k of keys.slice(0, Math.max(0, keys.length - MAX_PAGES))) await cache.delete(k);
}

function offlinePage() {
  return new Response(
    "<!doctype html><meta charset=utf-8><meta name=viewport content='width=device-width'>" +
      "<title>Offline · Kitchen Grimoire</title><link rel=stylesheet href='/static/css/app.css'>" +
      "<main class=page><h1>You're offline</h1><p>This page hasn't been saved for offline reading. " +
      "Recipes you've opened recently are still available.</p><p><a href='/'>Home</a></p></main>",
    { status: 503, headers: { "Content-Type": "text/html; charset=utf-8" } },
  );
}

// The page asks us to keep favorites available offline.
self.addEventListener("message", (event) => {
  if (event.data?.type !== "cache-pages") return;
  event.waitUntil((async () => {
    const cache = await caches.open(PAGES);
    for (const u of event.data.urls.slice(0, 30)) {
      const url = new URL(u, self.location.origin);
      if (url.origin !== self.location.origin || !cacheablePage(url)) continue;
      if (await cache.match(url.pathname)) continue;
      try {
        const res = await fetch(url.pathname, { credentials: "same-origin" });
        if (res.ok && !res.redirected) await cache.put(url.pathname, res);
      } catch {
        /* offline now: try again next visit */
      }
    }
    trim(cache);
  })());
});
