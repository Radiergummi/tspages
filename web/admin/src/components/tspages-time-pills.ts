export class TspagesTimePills extends HTMLElement {
    connectedCallback(): void {
        this.render();
    }

    private render(): void {
        const ranges = this.getAttribute("ranges")?.split(",") ?? [];
        const labels = this.getAttribute("labels")?.split(",") ?? [];
        const current = this.getAttribute("current") ?? "";
        const baseUrl = this.getAttribute("base-url") ?? "";
        const preserveParams = this.getAttribute("preserve-params")
                                   ?.split(",")
                                   .filter(Boolean) ?? [];

        const currentParams = new URLSearchParams(window.location.search);

        const nav = document.createElement("nav");
        nav.setAttribute("aria-label", "Time range");
        nav.className = "flex gap-1";

        ranges.forEach((range, index) => {
            const node = document.createElement("a");
            node.className = "pill";
            node.textContent = labels[index] ?? range;

            const params = new URLSearchParams();
            params.set("range", range);

            for (const name of preserveParams) {
                const value = currentParams.get(name);

                if (value) {
                    params.set(name, value);
                }
            }

            node.href = `${baseUrl}?${params.toString()}`;

            if (range === current) {
                node.setAttribute("aria-current", "step");
            }

            nav.appendChild(node);
        });

        this.replaceChildren(nav);
    }
}
