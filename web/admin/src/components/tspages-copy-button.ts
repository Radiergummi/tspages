const COPIED_DURATION = 2000;

export class TspagesCopyButton extends HTMLElement {
    #savedNodes: Node[] = [];

    connectedCallback(): void {
        if (!this.hasAttribute("role")) {
            this.setAttribute("role", "button");
        }

        if (!this.hasAttribute("tabindex")) {
            this.setAttribute("tabindex", "0");
        }

        this.#savedNodes = Array
            .from(this.childNodes)
            .map((node) => node.cloneNode(true));
        this.addEventListener("click", this.#handleClick);
        this.addEventListener("keydown", this.#handleKeydown);
    }

    disconnectedCallback(): void {
        this.removeEventListener("click", this.#handleClick);
        this.removeEventListener("keydown", this.#handleKeydown);
    }

    #handleKeydown = async (event: KeyboardEvent): Promise<void> => {
        if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();

            await this.#handleClick();
        }
    };

    #handleClick = async (): Promise<void> => {
        const text = this.#getValue();

        if (!text) {
            return;
        }

        await navigator.clipboard.writeText(text);
        this.textContent = "Copied!";

        setTimeout(
            () => this.replaceChildren(...this.#savedNodes.map(
                (node) => node.cloneNode(true),
            )),
            COPIED_DURATION,
        );
    };

    #getValue(): string {
        const value = this.getAttribute("value");

        if (value) {
            return value;
        }

        const targetId = this.getAttribute("target");

        if (targetId) {
            return document.getElementById(targetId)?.textContent?.trim() ?? "";
        }

        return "";
    }
}
