export class TspagesConfirmButton extends HTMLElement {
  connectedCallback(): void {
    if (!this.hasAttribute("role")) {
      this.setAttribute("role", "button");
    }
    if (!this.hasAttribute("tabindex")) {
      this.setAttribute("tabindex", "0");
    }
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
    const message = this.getAttribute("confirm") ?? "Are you sure?";
    if (!confirm(message)) return;

    const action = this.getAttribute("action");
    if (!action) return;

    const method = this.getAttribute("method") ?? "POST";
    const response = await fetch(action, { method });

    if (response.ok) {
      const redirect = this.getAttribute("redirect");
      if (redirect) {
        location.href = redirect;
      } else {
        location.reload();
      }
    } else {
      const body = await response.text();
      alert(`Failed: ${body.trim()}`);
    }
  };
}
