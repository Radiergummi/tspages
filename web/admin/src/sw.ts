/// <reference lib="webworker" />

const sw = self as unknown as ServiceWorkerGlobalScope & typeof globalThis;

interface PushData {
  title: string;
  body: string;
  url: string;
}

sw.addEventListener("push", (event) => {
  const data: PushData | undefined = event.data?.json();
  if (!data) return;
  event.waitUntil(
    sw.registration.showNotification(data.title, {
      body: data.body,
      data: { url: data.url },
    }),
  );
});

sw.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url: string = event.notification.data?.url ?? "/";
  event.waitUntil(sw.clients.openWindow(url));
});
