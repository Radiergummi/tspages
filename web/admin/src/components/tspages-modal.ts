export class TspagesModal extends HTMLElement {
  connectedCallback(): void {
    this.classList.add("hidden");

    this.addEventListener("click", this.handleBackdropClick);
    document.addEventListener("keydown", this.handleKeydown);

    this.querySelectorAll<HTMLElement>(".modal-close").forEach((btn) =>
      btn.addEventListener("click", () => this.close()),
    );
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.handleBackdropClick);
    document.removeEventListener("keydown", this.handleKeydown);
  }

  show(): void {
    this.classList.remove("hidden");
    this.classList.add("flex");

    const focusable = this.querySelector<HTMLElement>(
      "input:not([type=hidden]), select, textarea, button:not(.modal-close), [tabindex]",
    );
    focusable?.focus();
  }

  close(): void {
    this.classList.remove("flex");
    this.classList.add("hidden");
  }

  private handleBackdropClick = (e: MouseEvent): void => {
    if (e.target === this) {
      this.close();
    }
  };

  private handleKeydown = (e: KeyboardEvent): void => {
    if (e.key === "Escape" && !this.classList.contains("hidden")) {
      this.close();
    }
  };
}
