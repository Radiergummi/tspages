import {confirmAction} from "../lib/api";

function main(): void {
    const main = document.querySelector<HTMLElement>("main")!;
    const siteName = main.dataset.site!;

    // region Activate deployment

    document
        .querySelector<HTMLButtonElement>("[data-action='activate']")
        ?.addEventListener("click", () => {
            const id = main.dataset.deploymentId!;

            return confirmAction({
                message: `Activate deployment "${id}"?`,
                url: `/deploy/${encodeURIComponent(siteName)}/${encodeURIComponent(id)}/activate`,
                method: "POST",
            });
        });

    // endregion

    // region Delete deployment

    document
        .querySelector<HTMLButtonElement>("[data-action='delete']")
        ?.addEventListener("click", () => {
            const id = main.dataset.deploymentId!;

            return confirmAction({
                message: `Delete deployment "${id}"? This cannot be undone.`,
                url: `/deploy/${encodeURIComponent(siteName)}/${encodeURIComponent(id)}`,
                method: "DELETE",
                onSuccess: `/sites/${encodeURIComponent(siteName)}`,
            });
        });

    // endregion
}

document.addEventListener("DOMContentLoaded", main);
