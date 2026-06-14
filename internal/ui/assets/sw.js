/**
 * Service Worker for goyt
 * Provides offline functionality and caching
 */

const CACHE_NAME = 'goyt-v4';
const STATIC_CACHE_URLS = [
  '/',
  '/assets/css/main.css',
  '/assets/js/app.js',
  '/assets/js/modules/api-client.js',
  '/assets/js/modules/icons.js',
  '/assets/js/modules/download-manager.js',
  '/assets/js/modules/settings-manager.js',
  '/assets/js/modules/ui-manager.js'
];

// Install event - cache static assets
self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME)
      .then((cache) => {
        console.log('SW: Caching static assets');
        // Cache each URL independently and tolerate failures. addAll() is
        // all-or-nothing: a single missing or renamed asset would reject the
        // whole install and leave a stale worker in control, serving outdated
        // JS. allSettled keeps the install resilient so the new worker always
        // takes over.
        return Promise.allSettled(
          STATIC_CACHE_URLS.map((url) =>
            cache.add(url).catch((err) => {
              console.warn('SW: Skipped caching', url, err);
            })
          )
        );
      })
      .then(() => {
        console.log('SW: Installation complete');
        return self.skipWaiting();
      })
      .catch((error) => {
        console.error('SW: Installation failed:', error);
      })
  );
});

// Activate event - clean up old caches
self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((cacheNames) => {
        return Promise.all(
          cacheNames.map((cacheName) => {
            if (cacheName !== CACHE_NAME) {
              console.log('SW: Deleting old cache:', cacheName);
              return caches.delete(cacheName);
            }
          })
        );
      })
      .then(() => {
        console.log('SW: Activation complete');
        return self.clients.claim();
      })
  );
});

// Fetch event - serve cached content when offline
self.addEventListener('fetch', (event) => {
  // Only handle GET requests
  if (event.request.method !== 'GET') {
    return;
  }

  // Skip API requests and external resources
  if (event.request.url.includes('/api/') || 
      !event.request.url.startsWith(self.location.origin)) {
    return;
  }

  // Network-first so UI updates land immediately; fall back to cache offline.
  event.respondWith(
    fetch(event.request)
      .then((response) => {
        if (response && response.status === 200 && response.type === 'basic') {
          const responseToCache = response.clone();
          caches.open(CACHE_NAME).then((cache) => {
            cache.put(event.request, responseToCache);
          });
          return response;
        }
        // A non-OK response (e.g. 429 rate limit, 5xx) would otherwise render a
        // broken page. Serve the last cached copy when one exists so transient
        // server errors degrade gracefully instead of stripping styling.
        if (response && !response.ok) {
          return caches.match(event.request).then(
            (cachedResponse) => cachedResponse || response
          );
        }
        return response;
      })
      .catch(() => {
        return caches.match(event.request).then((cachedResponse) => {
          if (cachedResponse) {
            return cachedResponse;
          }
          if (event.request.mode === 'navigate') {
            return caches.match('/');
          }
          return Response.error();
        });
      })
  );
});

// Message event - handle commands from main app
self.addEventListener('message', (event) => {
  if (event.data && event.data.type === 'SKIP_WAITING') {
    self.skipWaiting();
  }
});

// Background sync for download operations (when network becomes available)
self.addEventListener('sync', (event) => {
  if (event.tag === 'download-retry') {
    event.waitUntil(
      // Notify main app that network is available
      self.clients.matchAll().then((clients) => {
        clients.forEach((client) => {
          client.postMessage({ type: 'NETWORK_AVAILABLE' });
        });
      })
    );
  }
});