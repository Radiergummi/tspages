const COPIED_DURATION = 2000;

export class TspagesCopyButton extends HTMLElement {
  private savedNodes: Node[] = [];

  connectedCallback(): void {
    if (!this.hasAttribute("role")) {
      this.setAttribute("role", "button");
    }
    if (!this.hasAttribute("tabindex")) {
      this.setAttribute("tabindex", "0");
    }
    this.savedNodes = Array.from(this.childNodes).map((n) => n.cloneNode(true));
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
    this.textContent = "Copied!";
    setTimeout(() => {
      this.replaceChildren(...this.savedNodes.map((n) => n.cloneNode(true)));
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
