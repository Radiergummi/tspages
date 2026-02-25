export function openModal(id: string): void {
  const el = document.getElementById(id);
  if (!el) return;
  el.classList.remove("hidden");
  el.classList.add("flex");
}

export function closeModal(id: string): void {
  const el = document.getElementById(id);
  if (!el) return;
  el.classList.remove("flex");
  el.classList.add("hidden");
}

/**
 * Set up a modal: close on backdrop click, close on Escape key.
 */
export function initModal(id: string): void {
  const el = document.getElementById(id);
  if (!el) return;

  el.addEventListener("click", (e) => {
    if (e.target === el) closeModal(id);
  });

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !el.classList.contains("hidden")) {
      closeModal(id);
    }
  });
}
