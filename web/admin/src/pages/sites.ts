import { openModal, closeModal, initModal } from "../lib/modal";
import {
  CategoryScale,
  Chart,
  Filler,
  LinearScale,
  LineController,
  LineElement,
  PointElement,
} from "chart.js";

Chart.register(LineController, LineElement, PointElement, LinearScale, CategoryScale, Filler);

// Sparklines
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

// New site modal
const modal = document.getElementById("new-site-modal");
if (modal) {
  initModal("new-site-modal");

  document
    .querySelector<HTMLButtonElement>("[data-action='new-site']")
    ?.addEventListener("click", () => {
      openModal("new-site-modal");
      setTimeout(() => document.getElementById("site-name")?.focus(), 0);
    });

  modal
    .querySelector<HTMLButtonElement>(".modal-close")
    ?.addEventListener("click", () => closeModal("new-site-modal"));
}
