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

  const mainNode = document.querySelector<HTMLElement>("main")!;
  const siteName = mainNode.dataset.site!;

  // region Sparkline

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

  // endregion

  // region Deploy modal
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

  // endregion

  // region Copy deploy command

  document
    .querySelector<HTMLButtonElement>("[data-action='copy-cmd']")
    ?.addEventListener("click", () => copyToClipboard("deploy-cmd"));

  // endregion

  // region Activate deployment

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

  // endregion

  // region Delete site

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

  // endregion

  // region Cleanup old deployments

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

  // endregion

  // region Config editing (progressive enhancement)

  const configDialog = document.getElementById("config-dialog");
  if (configDialog) {
    const spaToggle = configDialog.querySelector<HTMLInputElement>("[name='spa_routing'][type='checkbox']");
    const dirToggle = configDialog.querySelector<HTMLInputElement>("[name='directory_listing'][type='checkbox']");

    spaToggle?.addEventListener("change", () => {
      if (dirToggle) dirToggle.disabled = spaToggle.checked;
    });
    dirToggle?.addEventListener("change", () => {
      if (spaToggle) spaToggle.disabled = dirToggle.checked;
    });

    if (spaToggle?.checked && dirToggle) dirToggle.disabled = true;
    if (dirToggle?.checked && spaToggle) spaToggle.disabled = true;

    const webhookURL = configDialog.querySelector<HTMLInputElement>("[name='webhook_url']");
    const webhookEvents = configDialog.querySelectorAll<HTMLInputElement>("[name='webhook_events']");
    const webhookSecret = configDialog.querySelector<HTMLInputElement>("[name='webhook_secret']");

    function updateWebhookFields(): void {
      const hasURL = !!webhookURL?.value.trim();
      webhookEvents.forEach((cb) => (cb.disabled = !hasURL));
      if (webhookSecret) webhookSecret.disabled = !hasURL;
      if (hasURL && !Array.from(webhookEvents).some((cb) => cb.checked)) {
        webhookEvents.forEach((cb) => (cb.checked = true));
      }
    }

    webhookURL?.addEventListener("input", updateWebhookFields);
    updateWebhookFields();
  }

  // endregion
}

document.addEventListener("DOMContentLoaded", main);
