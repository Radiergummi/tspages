export function openModal(id: string): void {
    const node = document.getElementById(id);

    if (!node) {
        return;
    }

    node.classList.remove("hidden");
    node.classList.add("flex");
}

export function closeModal(id: string): void {
    const node = document.getElementById(id);

    if (!node) {
        return;
    }

    node.classList.remove("flex");
    node.classList.add("hidden");
}

/**
 * Set up a modal: close on backdrop click, close on Escape key.
 */
export function initModal(id: string): HTMLElement | undefined {
    const node = document.getElementById(id);

    if (!node) {
        return;
    }

    node.addEventListener("click", (event) => {
        if (event.target === node) {
            closeModal(id);
        }
    });

    document.addEventListener("keydown", (event) => {
        if (event.key === "Escape" && !node.classList.contains("hidden")) {
            closeModal(id);
        }
    });

    return node;
}
