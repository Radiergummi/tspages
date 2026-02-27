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
        LineElement,
        BarElement,
        ArcElement,
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
    const gridColor = cv(dark ? "--color-base-900" : "--color-base-100");
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
        id: "doughnutCenter",
        afterDraw(chart) {
            if ("type" in chart.config && chart.config.type !== "doughnut") {
                return;
            }

            const {ctx, chartArea} = chart;
            const cx = (
                           chartArea.left + chartArea.right
                       ) / 2;
            const cy = (
                           chartArea.top + chartArea.bottom
                       ) / 2;
            const rawData = chart.data.datasets[0].data as number[];
            const total = rawData.reduce((a, b) => a + b, 0);

            ctx.save();
            ctx.textAlign = "center";
            ctx.textBaseline = "middle";
            ctx.font = "600 1.25rem " + Chart.defaults.font.family;
            ctx.fillStyle = mainText;
            ctx.fillText(formatNumber(total), cx, cy);
            ctx.restore();
        },
    });

    return {dark, textColor, gridColor, mainText, surfaceColor, palette, cv};
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

function getOrCreateTooltip(chart: Chart): HTMLDivElement {
    const parent = chart.canvas.parentElement!;
    let el = parent.querySelector<HTMLDivElement>(":scope > .ct");
    if (!el) {
        el = document.createElement("div");
        el.className = "ct absolute pointer-events-none transition-opacity z-10 bg-paper dark:bg-base-950 border border-base-100 dark:border-base-900 rounded-md px-3 py-2.5 font-mono text-xs leading-snug";
        parent.classList.add("relative");
        parent.appendChild(el);
    }
    return el;
}

function resolveColor(chart: Chart, item: TooltipItem<any>): string {
    const bg = item.dataset.backgroundColor;
    if (Array.isArray(bg)) return bg[item.dataIndex] as string;
    if ("type" in chart.config && chart.config.type === "line") return item.dataset.borderColor as string;
    return bg as string;
}

function makeRow(barColor: string | null, label: string, value: string, rowClass?: string, labelClass?: string): HTMLDivElement {
    const row = document.createElement("div");
    row.className = "flex items-center gap-2 whitespace-nowrap" + (rowClass ? " " + rowClass : "");

    const bar = document.createElement("span");
    bar.className = barColor ? "w-1 shrink-0 h-3.5 rounded-sm" : "w-1 shrink-0";
    if (barColor) bar.style.background = barColor;
    row.appendChild(bar);

    const lbl = document.createElement("span");
    lbl.className = "text-muted" + (labelClass ? " " + labelClass : "");
    lbl.textContent = label;
    row.appendChild(lbl);

    const val = document.createElement("span");
    val.className = "font-semibold ml-auto pl-4 text-black dark:text-base-200";
    val.textContent = value;
    row.appendChild(val);

    return row;
}

function externalTooltip(options?: TooltipOptions) {
    return ({chart, tooltip}: {chart: Chart; tooltip: TooltipModel<any>}) => {
        const el = getOrCreateTooltip(chart);
        if (tooltip.opacity === 0) {
            el.style.opacity = "0";
            return;
        }

        const items = tooltip.dataPoints;
        if (!items?.length) {
            el.style.opacity = "0";
            return;
        }

        const fmt = options?.formatValue ?? String;
        el.replaceChildren();

        if (tooltip.title?.length) {
            const title = document.createElement("div");
            title.className = "font-semibold mb-1.5 whitespace-nowrap text-black dark:text-base-200";
            title.textContent = tooltip.title.join(" ");
            el.appendChild(title);
        }

        let total = 0;
        for (let i = 0; i < items.length; i++) {
            const item = items[i];
            const color = resolveColor(chart, item);
            const label = item.dataset.label || item.label || "";
            const value = (item.parsed?.y ?? item.raw) as number;
            total += value;
            el.appendChild(makeRow(color, label, fmt(value)));
        }

        if (options?.total !== false && items.length > 1) {
            el.appendChild(makeRow(null, "TOTAL", fmt(total),
                "mt-1 pt-1 border-t border-base-100 dark:border-base-900", "font-semibold"));
        }

        el.style.opacity = "1";

        const {offsetLeft: cx, offsetTop: cy} = chart.canvas;
        let x = cx + tooltip.caretX - el.offsetWidth / 2;
        const maxX = chart.canvas.parentElement!.clientWidth - el.offsetWidth;
        x = Math.max(0, Math.min(x, maxX));
        const y = Math.max(0, cy + tooltip.caretY - el.offsetHeight - 12);

        el.style.left = x + "px";
        el.style.top = y + "px";
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
                .map(({label, data, color}) => (
                    {
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
                    }
                ))
                .filter(({data}) => data.some((value) => value > 0)),
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
): Chart | undefined {
    if (!node) {
        return;
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
            datasets: datasets.map(({borderDash, borderWidth, color, data, fill, label}) => (
                {
                    label,
                    data,
                    borderColor: color,
                    backgroundColor: fill ? color + "18" : undefined,
                    borderWidth: borderWidth ?? 1.5,
                    borderDash,
                    fill: fill ?? false,
                    pointRadius: 0,
                    pointHitRadius: 8,
                    pointBackgroundColor: color,
                    tension: 0.3,
                }
            )),
        },
        options: {
            interaction: {
                intersect: false,
                mode: "index",
            },
            maintainAspectRatio: false,
            responsive: true,
            plugins: {
                legend: {display: false},
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
                    ...(
                        options?.yMin != null && {
                            min: options.yMin,
                        }
                    ),
                },
            },
        },
    });
}

// endregion

// region Utilities

export function formatNumber(number: number): string {
    if (number >= 1_000_000) {
        const v = number / 1_000_000;

        return (
                   v === Math.floor(v) ? v.toFixed(0) : v.toFixed(1)
               ) + "M";
    }

    if (number >= 1_000) {
        const v = number / 1_000;

        return (
                   v === Math.floor(v) ? v.toFixed(0) : v.toFixed(1)
               ) + "k";
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

    if (+(
        y || 0
    ) > 0 || +(
        mo || 0
    ) > 0 || +(
        w || 0
    ) > 0 || +(
        d || 0
    ) > 0) {
        return false;
    }

    return +(
        h || 0
    ) <= 24;
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
