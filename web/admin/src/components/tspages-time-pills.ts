export class TspagesTimePills extends HTMLElement {
  connectedCallback(): void {
    this.render();
  }

  private render(): void {
    const ranges = (this.getAttribute("ranges") ?? "").split(",");
    const labels = (this.getAttribute("labels") ?? "").split(",");
    const current = this.getAttribute("current") ?? "";
    const baseURL = this.getAttribute("base-url") ?? "";
    const preserveParams = (this.getAttribute("preserve-params") ?? "").split(",").filter(Boolean);

    const currentParams = new URLSearchParams(window.location.search);

    const nav = document.createElement("nav");
    nav.setAttribute("aria-label", "Time range");
    nav.className = "flex gap-1";

    ranges.forEach((range, i) => {
      const a = document.createElement("a");
      a.className = "pill";
      a.textContent = labels[i] ?? range;

      const params = new URLSearchParams();
      params.set("range", range);
      for (const p of preserveParams) {
        const val = currentParams.get(p);
        if (val) params.set(p, val);
      }
      a.href = `${baseURL}?${params.toString()}`;

      if (range === current) {
        a.setAttribute("aria-current", "step");
      }

      nav.appendChild(a);
    });

    this.replaceChildren(nav);
  }
}
