import { zipSync } from "fflate";

type State = "idle" | "dragging" | "uploading" | "success" | "error";

const OVERLAY_BASE =
  "fixed inset-0 z-100 flex flex-col items-center justify-center gap-6 transition-opacity";

export function initDeployDrop(siteName: string): void {
  const overlay = document.getElementById("deploy-overlay")!;
  const overlayText = overlay.querySelector(".deploy-overlay-text") as HTMLElement;
  const progressBar = overlay.querySelector(".deploy-progress-bar") as HTMLElement;
  let dragCount = 0;
  let currentState: State = "idle";

  function setState(state: State, message: string): void {
    currentState = state;

    if (state === "idle") {
      overlay.className = `${OVERLAY_BASE} opacity-0 pointer-events-none`;
    } else if (state === "dragging") {
      overlay.className = `${OVERLAY_BASE} opacity-100 pointer-events-auto bg-[rgba(75,112,229,0.12)] border-3 border-dashed border-[#4b70e5]`;
      overlayText.className = "deploy-overlay-text text-xl font-semibold text-[#4b70e5]";
    } else {
      overlay.className = `${OVERLAY_BASE} opacity-100 pointer-events-auto bg-black/60`;
      overlayText.className = "deploy-overlay-text text-xl font-semibold text-white";
    }

    if (message) {
      overlayText.textContent = message;
    }

    progressBar.hidden = state !== "uploading";
  }

  // Click overlay to dismiss on error/success states
  overlay.addEventListener("click", () => {
    if (currentState === "error" || currentState === "success") {
      setState("idle", "");
    }
  });

  // Modal dropzone: click and drag support
  const modalDropzone = document.getElementById("modal-dropzone");
  const fileInput = document.getElementById("deploy-file-input") as HTMLInputElement | null;

  if (modalDropzone) {
    modalDropzone.addEventListener("click", (event) => {
      const target = event.target as HTMLElement;

      if (target.tagName !== "BUTTON" && target.tagName !== "INPUT") {
        fileInput?.click();
      }
    });

    modalDropzone.addEventListener("dragover", (event) => {
      event.preventDefault();
      modalDropzone.classList.remove("border-base-700");
      modalDropzone.classList.add("border-blue-500", "bg-blue-500/5");
    });

    modalDropzone.addEventListener("dragleave", () => {
      modalDropzone.classList.remove("border-blue-500", "bg-blue-500/5");
      modalDropzone.classList.add("border-base-700");
    });

    modalDropzone.addEventListener("drop", async (event) => {
      event.preventDefault();
      event.stopPropagation();
      modalDropzone.classList.remove("border-blue-500", "bg-blue-500/5");
      modalDropzone.classList.add("border-base-700");
      document.getElementById("deploy-modal")?.classList.remove("open");

      await handleDrop(event.dataTransfer);
    });
  }

  if (fileInput) {
    fileInput.addEventListener("change", async () => {
      if (!fileInput.files?.length) {
        return;
      }

      document.getElementById("deploy-modal")?.classList.remove("open");

      // Single file -- upload directly (server detects format from filename)
      if (fileInput.files.length === 1) {
        await upload(fileInput.files[0], fileInput.files[0].name);

        fileInput.value = "";

        return;
      }

      // Multiple files -- zip them client-side
      setState("uploading", "Zipping files\u2026");

      const fileMap: Record<string, Uint8Array> = {};

      await Promise.all(
        Array.from(fileInput.files).map(async (file) => {
          const path = file.webkitRelativePath || file.name;
          fileMap[path] = new Uint8Array(await file.arrayBuffer());
        }),
      );

      fileInput.value = "";

      await upload(new Blob([zipSync(fileMap) as BlobPart], { type: "application/zip" }));
    });
  }

  document.addEventListener("dragenter", (event) => {
    event.preventDefault();

    if (++dragCount === 1) {
      setState("dragging", `Drop to deploy to ${siteName}`);
    }
  });

  document.addEventListener("dragover", (event) => event.preventDefault());

  document.addEventListener("dragleave", (event) => {
    event.preventDefault();

    if (--dragCount <= 0) {
      dragCount = 0;

      setState("idle", "");
    }
  });

  document.addEventListener("drop", async (event) => {
    event.preventDefault();
    dragCount = 0;

    await handleDrop(event.dataTransfer);
  });

  async function handleDrop(dataTransfer: DataTransfer | null): Promise<void> {
    const items = dataTransfer?.items;

    if (!items?.length) {
      setState("idle", "");

      return;
    }

    // Single file -- upload directly (but not directories)
    if (items.length === 1 && items[0].kind === "file") {
      const entry = items[0].webkitGetAsEntry();

      if (entry && !entry.isDirectory) {
        const file = items[0].getAsFile();

        if (file) {
          await upload(file, file.name);

          return;
        }
      }
    }

    // Folder or multiple files -- collect via webkitGetAsEntry, then zip
    setState("uploading", "Zipping files\u2026");

    const entries: FileSystemEntry[] = [...items]
      .filter((item) => item.kind === "file")
      .map((item) => item.webkitGetAsEntry())
      .filter((entry): entry is FileSystemEntry => entry !== null);

    if (!entries.length) {
      setState("idle", "");

      return;
    }

    try {
      const fileMap = await readAllEntries(entries);

      if (!Object.keys(fileMap).length) {
        setState("error", "No files found");

        return;
      }

      await upload(new Blob([zipSync(fileMap) as BlobPart], { type: "application/zip" }));
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);

      setState("error", `Error: ${message}`);
    }
  }

  async function readAllEntries(entries: FileSystemEntry[]): Promise<Record<string, Uint8Array>> {
    const topEntries =
      entries.length === 1 && entries[0].isDirectory
        ? await readDir(entries[0] as FileSystemDirectoryEntry)
        : entries;

    return collectFiles(topEntries, "");
  }

  function readDir(dirEntry: FileSystemDirectoryEntry): Promise<FileSystemEntry[]> {
    return new Promise((resolve, reject) => {
      const reader = dirEntry.createReader();
      const all: FileSystemEntry[] = [];

      (function read() {
        reader.readEntries((batch) => {
          if (!batch.length) {
            resolve(all);
            return;
          }

          all.push(...batch);
          read();
        }, reject);
      })();
    });
  }

  async function collectFiles(
    entries: FileSystemEntry[],
    prefix: string,
  ): Promise<Record<string, Uint8Array>> {
    const fileMap: Record<string, Uint8Array> = {};

    await Promise.all(
      entries.map(async (entry) => {
        const path = prefix ? `${prefix}/${entry.name}` : entry.name;

        if (entry.isFile) {
          const file = await new Promise<File>((resolve, reject) =>
            (entry as FileSystemFileEntry).file(resolve, reject),
          );
          fileMap[path] = new Uint8Array(await file.arrayBuffer());
        } else if (entry.isDirectory) {
          Object.assign(
            fileMap,
            await collectFiles(await readDir(entry as FileSystemDirectoryEntry), path),
          );
        }
      }),
    );
    return fileMap;
  }

  async function upload(body: Blob, filename?: string): Promise<void> {
    setState("uploading", `Deploying to ${siteName}\u2026`);

    let url = `/deploy/${encodeURIComponent(siteName)}`;

    if (filename) {
      url += `/${encodeURIComponent(filename)}`;
    }

    try {
      const resp = await fetch(url, {
        method: "POST",
        body,
      });

      if (resp.ok) {
        setState("success", "Deployed!");
        setTimeout(() => location.reload(), 800);
      } else {
        setState("error", `Deploy failed: ${(await resp.text()).trim()}`);
      }
    } catch {
      setState("error", "Network error");
    }
  }
}
