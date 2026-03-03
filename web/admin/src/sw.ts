/// <reference lib="webworker" />
declare const self: ServiceWorkerGlobalScope;

interface PushData {
  title: string;
  body: string;
  url: string;
}

self.addEventListener("push", (event: PushEvent) => {
  const data: PushData | undefined = event.data?.json();
  if (!data) return;
  event.waitUntil(
    self.registration.showNotification(data.title, {
      body: data.body,
      data: { url: data.url },
    }),
  );
});

self.addEventListener("notificationclick", (event: NotificationEvent) => {
  event.notification.close();
  const url: string = event.notification.data?.url ?? "/";
  event.waitUntil(self.clients.openWindow(url));
});
