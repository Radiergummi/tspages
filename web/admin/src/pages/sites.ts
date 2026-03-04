import {
  CategoryScale,
  Chart,
  Filler,
  LinearScale,
  LineController,
  LineElement,
  PointElement,
} from "chart.js";

function main(): void {
  // region Sparklines

  Chart.register(LineController, LineElement, PointElement, LinearScale, CategoryScale, Filler);

  const accent = getComputedStyle(document.documentElement)
    .getPropertyValue("--color-blue-500")
    .trim();

  document.querySelectorAll<HTMLCanvasElement>(".sparkline").forEach((el) => {
    const counts: number[] = JSON.parse(el.dataset.counts!);
    new Chart(el, {
      type: "line",
      data: {
        labels: counts.map(() => ""),
        datasets: [
          {
            data: counts,
            borderColor: accent,
            borderWidth: 1,
            backgroundColor: accent + "50",
            fill: "start",
            pointRadius: 0,
            pointHitRadius: 0,
            tension: 0.35,
          },
        ],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: { legend: { display: false }, tooltip: { enabled: false } },
        scales: {
          x: { display: false },
          y: {
            display: false,
            min: -5,
            grace: "5%",
            afterFit: (axis) => {
              axis.paddingBottom = 0;
            },
          },
        },
      },
    });
  });

  // endregion

  // region View Toggle (progressive enhancement: persist preference in localStorage)

  const STORAGE_KEY = "tspages:sites-view";
  const toggle = document.querySelector<HTMLElement>("[data-view-toggle]");

  if (toggle) {
    const radios = toggle.querySelectorAll<HTMLInputElement>("input[name='view']");

    radios.forEach((radio) => {
      radio.addEventListener("change", () => {
        try {
          localStorage.setItem(STORAGE_KEY, radio.value);
        } catch {
          // storage unavailable
        }
      });
    });

    // Restore saved preference
    try {
      const saved = localStorage.getItem(STORAGE_KEY);
      if (saved === "grid" || saved === "list") {
        const radio = document.getElementById(`view-${saved}`) as HTMLInputElement | null;
        if (radio) radio.checked = true;
      }
    } catch {
      // storage unavailable
    }
  }

  // endregion
}

document.addEventListener("DOMContentLoaded", main);
