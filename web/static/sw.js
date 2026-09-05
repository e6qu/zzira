// ZZIRA service worker: successful static assets are cached independently from
// authenticated page shells. Navigations into the Shauth SSO flow (/login,
// /signed-out, and everything under /auth/, including the RP-initiated
// logout bridge) bypass this worker entirely rather than going through
// fetch()-and-inspect: fetch() follows redirects internally, so intercepting
// those navigations collapses the SSO provider's redirect chain into a
// single opaque response and hides every intermediate hop from the browser's
// own navigation history and network observability. The private page cache
// is instead purged by app.js posting CLEAR_PRIVATE_CACHE once it detects it
// landed on /login or /signed-out, independent of how that page was reached.
const STATIC_CACHE = 'zzira-static-v11';
const PAGE_CACHE = 'zzira-pages-v11';
const CURRENT_CACHES = new Set([STATIC_CACHE, PAGE_CACHE]);
const STATIC_PREFIX = '/static/';

function bypassesServiceWorker(pathname) {
  return pathname === '/dashboards' || pathname.startsWith('/dashboards/') || pathname === '/login' || pathname === '/signed-out' || pathname.startsWith('/auth/');
}

self.addEventListener('install', (event) => {
  event.waitUntil(self.skipWaiting());
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((key) => !CURRENT_CACHES.has(key)).map((key) => caches.delete(key)))
    ).then(() => self.clients.claim())
  );
});

self.addEventListener('message', (event) => {
  if (event.origin !== self.location.origin) return;
  if (event.data && event.data.type === 'CLEAR_PRIVATE_CACHE') {
    event.waitUntil(caches.delete(PAGE_CACHE));
  }
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;

  if (url.pathname.startsWith(STATIC_PREFIX)) {
    event.respondWith((async () => {
      const hit = await caches.match(req);
      if (hit) return hit;
      const res = await fetch(req);
      if (res.ok) {
        const cache = await caches.open(STATIC_CACHE);
        await cache.put(req, res.clone());
      }
      return res;
    })());
    return;
  }

  if (req.mode === 'navigate' && !bypassesServiceWorker(url.pathname)) {
    event.respondWith((async () => {
      try {
        const res = await fetch(req);
        if (res.ok && (res.headers.get('Content-Type') || '').includes('text/html')) {
          const cache = await caches.open(PAGE_CACHE);
          await cache.put(req, res.clone());
        }
        return res;
      } catch (_) {
        const hit = await caches.match(req, { cacheName: PAGE_CACHE });
        return hit || new Response(
          '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Offline · ZZIRA</title></head><body><main><h1>ZZIRA is offline</h1><p>This page is not available in the local cache yet.</p></main></body></html>',
          { status: 503, headers: { 'Content-Type': 'text/html; charset=utf-8' } }
        );
      }
    })());
  }
});
