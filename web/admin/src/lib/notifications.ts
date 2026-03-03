/** Initialise push notification toggle for a site's submissions page. */
export async function initNotifications(
  site: string,
  path: string,
  vapidKey: string,
  btn: HTMLButtonElement,
): Promise<void> {
  if (!("serviceWorker" in navigator) || !("PushManager" in window)) return;

  btn.hidden = false;

  const reg = await navigator.serviceWorker.register("/sw.js");
  const label = btn.querySelector<HTMLElement>("#notify-label");

  let sub = await reg.pushManager.getSubscription();

  // Sync visual state with server.
  if (sub) {
    const ok = await checkSubscribed(site, path, sub.endpoint);
    setActive(btn, label, ok);
  }

  btn.addEventListener("click", async () => {
    btn.disabled = true;
    try {
      if (!sub) {
        sub = await reg.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: urlBase64ToUint8Array(vapidKey),
        });
      }
      const wasSubscribed = await checkSubscribed(site, path, sub.endpoint);
      if (wasSubscribed) {
        await serverUnsubscribe(sub.endpoint, site, path);
        setActive(btn, label, false);
      } else {
        await serverSubscribe(site, path, sub);
        setActive(btn, label, true);
      }
    } finally {
      btn.disabled = false;
    }
  });
}

function setActive(
  btn: HTMLButtonElement,
  label: HTMLElement | null,
  on: boolean,
): void {
  btn.setAttribute("aria-pressed", String(on));
  if (label) label.textContent = on ? "Notifications on" : "Notify me";
}

async function checkSubscribed(
  site: string,
  path: string,
  endpoint: string,
): Promise<boolean> {
  const params = new URLSearchParams({ site, path, endpoint });
  const res = await fetch(`/push/subscribe?${params}`);
  if (!res.ok) return false;
  const data: { subscribed: boolean } = await res.json();
  return data.subscribed;
}

async function serverSubscribe(
  site: string,
  path: string,
  sub: PushSubscription,
): Promise<void> {
  const key = sub.getKey("p256dh");
  const auth = sub.getKey("auth");
  await fetch("/push/subscribe", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      site,
      path,
      endpoint: sub.endpoint,
      key_p256dh: key ? btoa(String.fromCharCode(...new Uint8Array(key))) : "",
      key_auth: auth
        ? btoa(String.fromCharCode(...new Uint8Array(auth)))
        : "",
    }),
  });
}

async function serverUnsubscribe(
  endpoint: string,
  site: string,
  path: string,
): Promise<void> {
  await fetch("/push/subscribe", {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ endpoint, site, path }),
  });
}

function urlBase64ToUint8Array(base64String: string): Uint8Array {
  const padding = "=".repeat((4 - (base64String.length % 4)) % 4);
  const base64 = (base64String + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(base64);
  return Uint8Array.from(raw, (c) => c.charCodeAt(0));
}
