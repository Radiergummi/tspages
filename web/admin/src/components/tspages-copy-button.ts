const COPIED_DURATION = 2000;

export class TspagesCopyButton extends HTMLElement {
  private originalContent = "";

  connectedCallback(): void {
    if (!this.hasAttribute("role")) {
      this.setAttribute("role", "button");
    }
    if (!this.hasAttribute("tabindex")) {
      this.setAttribute("tabindex", "0");
    }
    this.originalContent = this.textContent ?? "";
    this.addEventListener("click", this.handleClick);
    this.addEventListener("keydown", this.handleKeydown);
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.handleClick);
    this.removeEventListener("keydown", this.handleKeydown);
  }

  private handleKeydown = (e: KeyboardEvent): void => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      this.handleClick();
    }
  };

  private handleClick = async (): Promise<void> => {
    const text = this.getValue();
    if (!text) return;

    await navigator.clipboard.writeText(text);
    const saved = this.textContent;
    this.textContent = "Copied!";
    setTimeout(() => {
      this.textContent = saved;
    }, COPIED_DURATION);
  };

  private getValue(): string {
    const value = this.getAttribute("value");
    if (value) return value;

    const targetId = this.getAttribute("target");
    if (targetId) {
      return document.getElementById(targetId)?.textContent?.trim() ?? "";
    }
    return "";
  }
}
