const COPY_ICON = `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>`;
const CHECK_ICON = `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>`;

function initCopyButtons(): void {
  document.querySelectorAll<HTMLPreElement>(".prose pre").forEach((pre) => {
    const code = pre.querySelector("code");
    if (!code) {
      return;
    }

    const btn = document.createElement("button");
    btn.className = "copy-btn";
    btn.setAttribute("aria-label", "Copy code");
    btn.innerHTML = COPY_ICON;

    btn.addEventListener("click", async () => {
      await navigator.clipboard.writeText(code.textContent ?? "");
      btn.innerHTML = CHECK_ICON;
      btn.classList.add("copied");
      setTimeout(() => {
        btn.innerHTML = COPY_ICON;
        btn.classList.remove("copied");
      }, 2000);
    });

    pre.appendChild(btn);
  });
}

function initTOCHighlight(): void {
  const tocLinks = document.querySelectorAll<HTMLAnchorElement>("[data-toc-link]");
  if (tocLinks.length === 0) {
    return;
  }

  const headings = Array.from(tocLinks)
    .map((link) => document.getElementById(link.getAttribute("href")!.slice(1)))
    .filter((el): el is HTMLElement => el !== null);

  if (headings.length === 0) {
    return;
  }

  const scrollParent = document.getElementById("main-content")!;
  let ticking = false;

  scrollParent.addEventListener(
    "scroll",
    () => {
      if (ticking) {
        return;
      }
      ticking = true;
      requestAnimationFrame(() => {
        let current = "";
        for (const heading of headings) {
          if (heading.getBoundingClientRect().top <= 100) {
            current = heading.id;
          }
        }

        tocLinks.forEach((link) => {
          const id = link.getAttribute("href")!.slice(1);
          if (id === current) {
            link.setAttribute("aria-current", "true");
          } else {
            link.removeAttribute("aria-current");
          }
        });
        ticking = false;
      });
    },
    { passive: true },
  );
}

initCopyButtons();
initTOCHighlight();
