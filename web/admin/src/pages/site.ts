import { confirmAction, copyToClipboard } from "../lib/api";
import { initDeployDrop } from "../lib/deploy-drop";
import { closeModal, initModal, openModal } from "../lib/modal";
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
  Chart.register(LineController, LineElement, PointElement, LinearScale, CategoryScale, Filler);

  const main = document.querySelector<HTMLElement>("main")!;
  const siteName = main.dataset.site!;

  // Sparkline
  const sparkLineElement = document.getElementById("sparkline") as HTMLCanvasElement | null;

  if (sparkLineElement) {
    const counts: number[] = JSON.parse(sparkLineElement.dataset.counts!);
    const accent = getComputedStyle(document.documentElement)
      .getPropertyValue("--color-blue-500")
      .trim();

    new Chart(sparkLineElement, {
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
        plugins: {
          legend: {
            display: false,
          },
          tooltip: {
            enabled: false,
          },
        },
        scales: {
          x: {
            display: false,
          },
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
  }

  // Deploy modal
  const deployModal = document.getElementById("deploy-modal");

  if (deployModal) {
    initModal("deploy-modal");

    document
      .querySelector<HTMLButtonElement>("[data-action='deploy']")
      ?.addEventListener("click", () => openModal("deploy-modal"));

    deployModal
      .querySelector<HTMLButtonElement>(".modal-close")
      ?.addEventListener("click", () => closeModal("deploy-modal"));

    initDeployDrop(siteName);
  }

  // Copy deploy command
  document
    .querySelector<HTMLButtonElement>("[data-action='copy-cmd']")
    ?.addEventListener("click", () => copyToClipboard("deploy-cmd"));

  // Activate deployment
  document.querySelectorAll<HTMLButtonElement>("[data-action='activate']").forEach((button) => {
    button.addEventListener("click", () => {
      const id = button.dataset.deploymentId!;

      return confirmAction({
        message: `Activate deployment "${id}"?`,
        url: `/deploy/${encodeURIComponent(siteName)}/${encodeURIComponent(id)}/activate`,
        method: "POST",
      });
    });
  });

  // Delete site
  document
    .querySelector<HTMLButtonElement>("[data-action='delete-site']")
    ?.addEventListener("click", () =>
      confirmAction({
        message: `Delete site "${siteName}" and all its deployments?`,
        url: `/deploy/${encodeURIComponent(siteName)}`,
        method: "DELETE",
        onSuccess: "/sites",
      }),
    );

  // Cleanup old deployments
  document
    .querySelector<HTMLButtonElement>("[data-action='cleanup']")
    ?.addEventListener("click", async () => {
      if (!confirm("Delete all inactive deployments? This cannot be undone.")) {
        return;
      }

      const response = await fetch(`/deploy/${encodeURIComponent(siteName)}/deployments`, {
        method: "DELETE",
      });

      if (response.ok) {
        const data = await response.json();

        alert(`Deleted ${data.deleted} deployment(s).`);
        location.reload();
      } else {
        const text = await response.text();

        alert(`Cleanup failed: ${text.trim()}`);
      }
    });
}

document.addEventListener("DOMContentLoaded", main);
