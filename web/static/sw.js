// ZZIRA service worker: successful static assets are cached independently from
// authenticated page shells. Login/sign-out responses purge private HTML so a
// later user on the same browser cannot inherit the previous user's workspace.
const STATIC_CACHE = 'zzira-static-v7';
const PAGE_CACHE = 'zzira-pages-v7';
const CURRENT_CACHES = new Set([STATIC_CACHE, PAGE_CACHE]);
const STATIC_PREFIX = '/static/';
const AUTH_PATHS = new Set(['/login', '/signed-out']);

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

  if (req.mode === 'navigate') {
    event.respondWith((async () => {
      try {
        const res = await fetch(req);
        const responsePath = res.url ? new URL(res.url).pathname : url.pathname;
        if (AUTH_PATHS.has(responsePath)) {
          await caches.delete(PAGE_CACHE);
        } else if (res.ok && (res.headers.get('Content-Type') || '').includes('text/html')) {
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
