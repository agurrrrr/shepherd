// PWA shell + Web Push for task-complete notifications.
// Shepherd still needs a live daemon; we only cache the app shell for install.

const CACHE_NAME = 'shepherd-v2';

self.addEventListener('install', () => {
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((names) =>
      Promise.all(names.filter((n) => n !== CACHE_NAME).map((n) => caches.delete(n)))
    )
  );
  self.clients.claim();
});

self.addEventListener('fetch', (event) => {
  if (event.request.mode === 'navigate') {
    event.respondWith(
      fetch(event.request).catch(() =>
        caches.match(event.request).then((r) => r || caches.match('/'))
      )
    );
  }
});

self.addEventListener('push', (event) => {
  let data = {
    title: 'Shepherd',
    body: '작업이 끝났습니다',
    url: '/',
    tag: 'shepherd'
  };
  try {
    if (event.data) {
      data = { ...data, ...event.data.json() };
    }
  } catch {
    try {
      const text = event.data && event.data.text();
      if (text) data.body = text;
    } catch {
      // keep defaults
    }
  }

  event.waitUntil(
    self.registration.showNotification(data.title || 'Shepherd', {
      body: data.body || '',
      icon: '/icon-192.png',
      badge: '/icon-192.png',
      tag: data.tag || 'shepherd',
      renotify: true,
      data: { url: data.url || '/' }
    })
  );
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const target = new URL(event.notification.data?.url || '/', self.location.origin).href;

  event.waitUntil(
    (async () => {
      const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
      for (const client of windows) {
        if (client.url === target && 'focus' in client) {
          return client.focus();
        }
      }
      if (windows[0]) {
        if ('focus' in windows[0]) await windows[0].focus();
        if ('navigate' in windows[0]) return windows[0].navigate(target);
      }
      if (self.clients.openWindow) {
        return self.clients.openWindow(target);
      }
    })()
  );
});
