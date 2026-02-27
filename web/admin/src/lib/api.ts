export async function confirmAction({
  message,
  method,
  onSuccess,
  url,
}: {
  message: string;
  url: string;
  method: "POST" | "DELETE";
  onSuccess?: string;
}): Promise<void> {
  if (!confirm(message)) {
    return;
  }

  const response = await fetch(url, { method });

  if (response.ok) {
    if (onSuccess) {
      location.href = onSuccess;
    } else {
      location.reload();
    }
  } else {
    const body = await response.text();

    alert(`Failed: ${body.trim()}`);
  }
}

export function copyToClipboard(id: string): void {
  const node = document.getElementById(id);

  if (node?.textContent) {
    void navigator.clipboard.writeText(node.textContent);
  }
}
