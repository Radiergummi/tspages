export async function confirmAction(opts: {
  message: string;
  url: string;
  method: "POST" | "DELETE";
  onSuccess?: string;
}): Promise<void> {
  if (!confirm(opts.message)) return;

  const res = await fetch(opts.url, { method: opts.method });
  if (res.ok) {
    if (opts.onSuccess) {
      location.href = opts.onSuccess;
    } else {
      location.reload();
    }
  } else {
    alert(`Failed: ${(await res.text()).trim()}`);
  }
}

export function copyToClipboard(elementId: string): void {
  const el = document.getElementById(elementId);
  if (el?.textContent) {
    navigator.clipboard.writeText(el.textContent);
  }
}
