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

  const mainNode = document.querySelector<HTMLElement>("[data-site]")!;
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

  // region Config editing

  const configView = document.getElementById("config-view");
  const configEdit = document.getElementById("config-edit");
  const editBtn = document.querySelector<HTMLButtonElement>("[data-action='edit-config']");
  const cancelBtn = document.querySelector<HTMLButtonElement>("[data-action='cancel-config']");
  const saveBtn = document.querySelector<HTMLButtonElement>("[data-action='save-config']");

  editBtn?.addEventListener("click", () => {
    configView?.classList.add("hidden");
    configEdit?.classList.remove("hidden");
    editBtn.classList.add("hidden");
  });

  cancelBtn?.addEventListener("click", () => {
    configEdit?.classList.add("hidden");
    configView?.classList.remove("hidden");
    editBtn?.classList.remove("hidden");
  });

  // Toggle buttons: click toggles aria-checked
  function isToggleOn(el: HTMLElement | null): boolean {
    return el?.getAttribute("aria-checked") === "true";
  }

  function setToggle(el: HTMLElement | null, on: boolean): void {
    el?.setAttribute("aria-checked", String(on));
  }

  const toggles = configEdit?.querySelectorAll<HTMLButtonElement>(".toggle");
  toggles?.forEach((btn) => {
    btn.addEventListener("click", () => {
      if (btn.disabled) return;
      setToggle(btn, !isToggleOn(btn));
      btn.dispatchEvent(new Event("change"));
    });
  });

  const spaToggle = document.getElementById("cfg-spa") as HTMLButtonElement | null;
  const dirToggle = document.getElementById("cfg-dirlist") as HTMLButtonElement | null;

  spaToggle?.addEventListener("change", () => {
    if (dirToggle) dirToggle.disabled = isToggleOn(spaToggle);
  });
  dirToggle?.addEventListener("change", () => {
    if (spaToggle) spaToggle.disabled = isToggleOn(dirToggle);
  });

  // Set initial disabled state
  if (isToggleOn(spaToggle) && dirToggle) dirToggle.disabled = true;
  if (isToggleOn(dirToggle) && spaToggle) spaToggle.disabled = true;

  const webhookURL = document.getElementById("cfg-webhook-url") as HTMLInputElement | null;
  const webhookEvents = document.querySelectorAll<HTMLInputElement>("[name='webhook_events']");
  const webhookSecret = document.getElementById("cfg-webhook-secret") as HTMLInputElement | null;

  function updateWebhookFields(): void {
    const hasURL = !!webhookURL?.value.trim();
    webhookEvents.forEach((cb) => (cb.disabled = !hasURL));
    if (webhookSecret) webhookSecret.disabled = !hasURL;
    // If URL just got a value and no events are checked, check all
    if (hasURL && !Array.from(webhookEvents).some((cb) => cb.checked)) {
      webhookEvents.forEach((cb) => (cb.checked = true));
    }
  }

  webhookURL?.addEventListener("input", updateWebhookFields);
  updateWebhookFields(); // set initial state

  saveBtn?.addEventListener("click", async () => {
    const body: Record<string, unknown> = {
      public: isToggleOn(document.getElementById("cfg-public")),
      spa_routing: isToggleOn(spaToggle),
      html_extensions: !isToggleOn(document.getElementById("cfg-cleanurls")),
      analytics: isToggleOn(document.getElementById("cfg-analytics")),
      directory_listing: isToggleOn(dirToggle),
      index_page: (document.getElementById("cfg-index") as HTMLInputElement)?.value.trim() ?? "",
      not_found_page: (document.getElementById("cfg-notfound") as HTMLInputElement)?.value.trim() ?? "",
      trailing_slash: (document.getElementById("cfg-trailing") as HTMLSelectElement)?.value ?? "",
      webhook_url: webhookURL?.value.trim() ?? "",
      webhook_events: Array.from(webhookEvents).filter((cb) => cb.checked).map((cb) => cb.value),
      webhook_secret: webhookSecret?.value ?? "",
    };

    const response = await fetch(`/sites/${encodeURIComponent(siteName)}/config`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });

    if (response.ok) {
      location.reload();
    } else {
      const text = await response.text();
      alert(`Failed to save: ${text.trim()}`);
    }
  });

  // endregion
}

document.addEventListener("DOMContentLoaded", main);
