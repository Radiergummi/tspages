import {
  ArcElement,
  BarController,
  BarElement,
  CategoryScale,
  Chart,
  DoughnutController,
  Filler,
  Legend,
  LinearScale,
  LineController,
  LineElement,
  PointElement,
  Tooltip,
  type TooltipItem,
  type TooltipModel,
} from "chart.js";
import { TreemapController, TreemapElement } from "chartjs-chart-treemap";

// region Theme

export interface Theme {
  dark: boolean;
  textColor: string;
  gridColor: string;
  mainText: string;
  surfaceColor: string;
  palette: string[];
  cv: (name: string) => string;
}

export function initCharts(): Theme {
  Chart.register(
    LineController,
    BarController,
    DoughnutController,
    TreemapController,
    LineElement,
    BarElement,
    ArcElement,
    TreemapElement,
    PointElement,
    Filler,
    LinearScale,
    CategoryScale,
    Legend,
    Tooltip,
  );

  const dark = window.matchMedia("(prefers-color-scheme: dark)").matches;
  const style = getComputedStyle(document.documentElement);
  const cv = (name: string) => style.getPropertyValue(name).trim();

  const textColor = cv(dark ? "--color-base-500" : "--color-base-600");
  const gridColor = cv(dark ? "--color-base-800" : "--color-base-100");
  const mainText = cv(dark ? "--color-base-200" : "--color-black");
  const surfaceColor = cv(dark ? "--color-base-900" : "--color-base-50");

  const palette = [
    cv("--color-blue-500"),
    cv("--color-orange-400"),
    cv("--color-green-400"),
    cv("--color-red-400"),
    cv("--color-purple-400"),
    cv("--color-cyan-400"),
    cv("--color-magenta-400"),
    cv("--color-yellow-400"),
    cv("--color-orange-500"),
    cv("--color-blue-400"),
  ];

  Chart.defaults.color = textColor;
  Chart.defaults.borderColor = gridColor;
  Chart.defaults.font.family = style.getPropertyValue("--font-sans").trim();
  Chart.defaults.font.size = 11;

  Chart.register({
    id: "crosshair",
    beforeDatasetsDraw(chart) {
      if ("type" in chart.config && chart.config.type === "doughnut") {
        return;
      }

      const tooltip = chart.tooltip;

      if (!tooltip?.opacity || !tooltip.dataPoints?.length) {
        return;
      }

      const { ctx, chartArea } = chart;
      const node = tooltip.dataPoints[0].element;

      ctx.save();

      if ("width" in node && typeof node.width === "number") {
        ctx.fillStyle = gridColor;
        ctx.fillRect(
          node.x - node.width / 2,
          chartArea.top,
          node.width,
          chartArea.bottom - chartArea.top,
        );
      } else {
        ctx.beginPath();
        ctx.moveTo(node.x, chartArea.top);
        ctx.lineTo(node.x, chartArea.bottom);

        ctx.lineWidth = 1;
        ctx.strokeStyle = gridColor;

        ctx.stroke();
      }

      ctx.restore();
    },
  });

  Chart.register({
    id: "doughnutCenter",
    afterDraw(chart) {
      if ("type" in chart.config && chart.config.type !== "doughnut") {
        return;
      }

      const { ctx, chartArea } = chart;
      const cx = (chartArea.left + chartArea.right) / 2;
      const cy = (chartArea.top + chartArea.bottom) / 2;
      const rawData = chart.data.datasets[0].data as number[];
      const mode = chart.canvas.dataset.center;
      const value =
        mode === "count" ? rawData.filter((v) => v > 0).length : rawData.reduce((a, b) => a + b, 0);

      ctx.save();
      ctx.textAlign = "center";
      ctx.textBaseline = "middle";
      ctx.font = `600 1.25rem ${Chart.defaults.font.family}`;
      ctx.fillStyle = mainText;
      ctx.fillText(formatNumber(value), cx, cy);
      ctx.restore();
    },
  });

  return { dark, textColor, gridColor, mainText, surfaceColor, palette, cv };
}

// endregion

// region Tooltip

export interface TooltipOptions {
  formatValue?: (value: number) => string;
  total?: boolean;
}

export function tooltipDefaults(options?: TooltipOptions) {
  return {
    enabled: false,
    external: externalTooltip(options),
  };
}

function renderTooltip(chart: Chart): HTMLDivElement {
  const parent = chart.canvas.parentElement!;
  let node = parent.querySelector<HTMLDivElement>(":scope > .ct");

  if (!node) {
    node = document.createElement("div");
    node.className =
      "ct absolute pointer-events-none transition-opacity z-10 bg-paper/80 dark:bg-black/80 ring-1 ring-base-100 dark:ring-base-500/25 backdrop-blur-sm backdrop-saturate-200 rounded-md px-3 py-2.5 font-mono text-xs leading-snug";
    parent.classList.add("relative");
    parent.appendChild(node);
  }

  return node;
}

function resolveColor(chart: Chart, item: TooltipItem<any>): string {
  const backgroundColor = item.dataset.backgroundColor;

  if (Array.isArray(backgroundColor)) {
    return backgroundColor[item.dataIndex] as string;
  }

  if ("type" in chart.config && chart.config.type === "line") {
    return item.dataset.borderColor as string;
  }

  return backgroundColor as string;
}

function renderTooltipRow(
  barColor: string | null,
  label: string,
  value: string,
  rowClass?: string,
  labelClass?: string,
): HTMLDivElement {
  const rowNode = document.createElement("div");
  rowNode.className = `flex items-center gap-2 whitespace-nowrap ${rowClass}`;

  const barNode = document.createElement("span");
  barNode.className = barColor ? "w-1 shrink-0 h-3 rounded-sm" : "w-1 shrink-0";

  if (barColor) {
    barNode.style.background = barColor;
  }

  rowNode.appendChild(barNode);

  const labelNode = document.createElement("span");
  labelNode.className = `text-muted ${labelClass}`.trimEnd();
  labelNode.textContent = label;
  rowNode.appendChild(labelNode);

  const valueNode = document.createElement("span");
  valueNode.className = "font-semibold ms-auto ps-4 text-black dark:text-base-200";
  valueNode.textContent = value;
  rowNode.appendChild(valueNode);

  return rowNode;
}

function externalTooltip(options?: TooltipOptions) {
  return ({ chart, tooltip }: { chart: Chart; tooltip: TooltipModel<any> }) => {
    const node = renderTooltip(chart);

    if (tooltip.opacity === 0) {
      node.style.opacity = "0";

      return;
    }

    const items = tooltip.dataPoints;

    if (!items?.length) {
      node.style.opacity = "0";

      return;
    }

    const format = options?.formatValue ?? String;
    node.replaceChildren();

    if (tooltip.title?.length) {
      const title = document.createElement("div");
      title.className = "font-semibold mb-1.5 whitespace-nowrap text-black dark:text-base-200";
      title.textContent = tooltip.title.join(" ");
      node.appendChild(title);
    }

    let total = 0;

    for (const item of items) {
      const color = resolveColor(chart, item);
      const label = item.dataset.label || item.label || "";
      const value = (item.parsed?.y ?? item.raw) as number;
      total += value;
      node.appendChild(renderTooltipRow(color, label, format(value)));
    }

    if (options?.total !== false && items.length > 1) {
      node.appendChild(
        renderTooltipRow(null, "TOTAL", format(total), "mt-1 pt-1", "font-semibold"),
      );
    }

    node.style.opacity = "1";

    const { offsetLeft: cx, offsetTop: cy } = chart.canvas;
    const { chartArea } = chart;
    const gap = 12;
    const pw = chart.canvas.parentElement!.clientWidth;
    const ph = cy + chart.canvas.clientHeight;
    const isDoughnut = "type" in chart.config && chart.config.type === "doughnut";
    const anchorX = isDoughnut
      ? tooltip.caretX < (chartArea.left + chartArea.right) / 2
        ? chartArea.left
        : chartArea.right
      : tooltip.caretX;
    const caretX = cx + anchorX;
    const rightX = caretX + gap;
    const leftX = caretX - node.offsetWidth - gap;
    const x = rightX + node.offsetWidth <= pw ? rightX : Math.max(0, leftX);
    const midY = cy + (chartArea.top + chartArea.bottom) / 2;
    const y = Math.max(0, Math.min(midY - node.offsetHeight / 2, ph - node.offsetHeight));

    node.style.left = `${x}px`;
    node.style.top = `${y}px`;
  };
}

// endregion

// region Chart builders

export function stackedBar(
  node: HTMLCanvasElement | null,
  labels: string[],
  datasets: { label: string; data: number[]; color: string }[],
  theme: Theme,
): Chart | undefined {
  if (!node) {
    return;
  }

  return new Chart(node, {
    type: "bar",
    data: {
      labels,
      datasets: datasets
        .map(({ label, data, color }) => ({
          label,
          data,
          backgroundColor: color,
          borderRadius: data.length > 0 ? 1 : 0,
          borderSkipped: data.length === 0,
          borderColor: theme.surfaceColor,
          borderWidth: {
            top: data.length === 0 ? 0 : 2,
            right: 0,
            bottom: 0,
            left: 0,
          },
        }))
        .filter(({ data }) => data.some((value) => value > 0)),
    },
    options: {
      interaction: {
        intersect: false,
        mode: "index",
      },
      maintainAspectRatio: false,
      plugins: {
        legend: {
          display: false,
        },
        tooltip: tooltipDefaults(),
      },
      responsive: true,
      scales: {
        x: {
          display: false,
          stacked: true,
        },
        y: {
          display: false,
          stacked: true,
          beginAtZero: true,
          grace: "5%",
        },
      },
    },
  });
}

export function doughnut(
  node: HTMLCanvasElement | null,
  labels: string[],
  data: number[],
  theme: Theme,
  options?: { center?: "total" | "count" },
): Chart | undefined {
  if (!node) {
    return;
  }

  if (options?.center) {
    node.dataset.center = options.center;
  }

  return new Chart(node, {
    type: "doughnut",
    data: {
      labels,
      datasets: [
        {
          backgroundColor: theme.palette.slice(0, labels.length),
          borderWidth: 0,
          borderRadius: 4,
          spacing: 2,
          data,
          hoverOffset: 4,
        },
      ],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      cutout: "60%",
      layout: {
        padding: 4,
      },
      plugins: {
        legend: {
          position: "bottom",
          labels: {
            boxWidth: 8,
            boxHeight: 8,
            borderRadius: 4,
            useBorderRadius: true,
            padding: 12,
            font: {
              size: 11,
              weight: "bold",
            },
          },
        },
        tooltip: tooltipDefaults(),
      },
    },
  });
}

export interface LineDataset {
  label: string;
  data: number[];
  color: string;
  fill?: boolean | string;
  borderDash?: number[];
  borderWidth?: number;
}

export function lineChart(
  node: HTMLCanvasElement | null,
  labels: string[],
  datasets: LineDataset[],
  options?: {
    formatValue?: (value: number) => string;
    total?: boolean;
    yMin?: number;
  },
): Chart | undefined {
  if (!node) {
    return;
  }

  return new Chart(node, {
    type: "line",
    data: {
      labels,
      datasets: datasets.map(({ borderDash, borderWidth, color, data, fill, label }) => ({
        label,
        data,
        borderColor: color,
        backgroundColor: fill ? `${color}18` : undefined,
        borderWidth: borderWidth ?? 1.5,
        borderDash,
        fill: fill ?? false,
        pointRadius: 0,
        pointHitRadius: 8,
        pointBackgroundColor: color,
        tension: 0.3,
      })),
    },
    options: {
      interaction: {
        intersect: false,
        mode: "index",
      },
      maintainAspectRatio: false,
      responsive: true,
      plugins: {
        legend: { display: false },
        tooltip: tooltipDefaults({
          formatValue: options?.formatValue,
          total: options?.total,
        }),
      },
      scales: {
        x: {
          display: false,
        },
        y: {
          display: false,
          grace: "5%",
          ...(options?.yMin !== null && {
            min: options!.yMin,
          }),
        },
      },
    },
  });
}

export function treemap<T extends Record<string, unknown>>(
  node: HTMLCanvasElement | null,
  tree: T[],
  key: keyof T & string,
  label: keyof T & string,
  theme: Theme,
): Chart | undefined {
  if (!node || !tree.length) {
    return;
  }

  return new Chart(node, {
    type: "treemap",
    data: {
      datasets: [
        {
          tree,
          key,
          groups: [label],
          spacing: 2,
          borderWidth: 0,
          borderRadius: 4,
          backgroundColor(ctx: any) {
            return theme.palette[ctx.dataIndex % theme.palette.length];
          },
          labels: { display: false },
        },
      ],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: { display: false },
        tooltip: {
          ...tooltipDefaults(),
          callbacks: {
            title: () => "",
            label: (item: TooltipItem<"treemap">) => {
              const raw = item.raw as { g?: string; v?: number };
              return `${raw.g ?? ""}: ${raw.v ?? 0}`;
            },
          },
        },
      },
    },
    plugins: [
      {
        id: "treemapLabels",
        afterDatasetsDraw(chart: Chart) {
          const meta = chart.getDatasetMeta(0);
          const ctx = chart.ctx;
          const pad = 6;
          const font = Chart.defaults.font.family;
          ctx.save();
          for (const el of meta.data) {
            const { x, y, width: w, height: h } = el as any;
            const raw = (el as any).$context?.raw as { g?: string; v?: number } | undefined;
            if (!raw?.g || w < 16 || h < 16) {
              continue;
            }
            const vertical = h > w;
            ctx.save();
            ctx.beginPath();
            ctx.rect(x, y, w, h);
            ctx.clip();
            if (vertical) {
              ctx.translate(x + pad + 5, y + h - pad);
              ctx.rotate(-Math.PI / 2);
            } else {
              ctx.translate(x + pad, y + pad + 11);
            }
            ctx.textBaseline = "middle";
            ctx.font = "bold 11px " + font;
            ctx.fillStyle = "white";
            ctx.fillText(raw.g, 0, 0);
            ctx.font = "10px " + font;
            ctx.fillStyle = "rgba(255,255,255,0.6)";
            ctx.fillText(formatNumber(raw.v ?? 0), 0, 14);
            ctx.restore();
          }
          ctx.restore();
        },
      },
    ],
  });
}

// endregion

// region Utilities

export function formatNumber(number: number): string {
  if (number >= 1_000_000) {
    const v = number / 1_000_000;

    return `${v === Math.floor(v) ? v.toFixed(0) : v.toFixed(1)}M`;
  }

  if (number >= 1_000) {
    const v = number / 1_000;

    return `${v === Math.floor(v) ? v.toFixed(0) : v.toFixed(1)}k`;
  }

  return String(number);
}

export function formatLabel(iso: string, range: string): string {
  const date = new Date(iso);

  if (isShortRange(range)) {
    return date.toLocaleTimeString(undefined, {
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
    });
  }

  return date.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  });
}

function isShortRange(range: string): boolean {
  const match = range.match(
    /^P(?:(\d+)Y)?(?:(\d+)M)?(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$/,
  );

  if (!match) {
    return false;
  }

  const [, y, mo, w, d, h] = match;

  if (+(y || 0) > 0 || +(mo || 0) > 0 || +(w || 0) > 0 || +(d || 0) > 0) {
    return false;
  }

  return +(h || 0) <= 24;
}

export function pluck<T, K extends keyof T>(items: T[], key: K): T[K][] {
  return items.map((item) => item[key]);
}

export function reloadOnThemeChange(): void {
  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    location.reload();
  });
}

// endregion
