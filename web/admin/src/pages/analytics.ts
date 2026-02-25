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
} from "chart.js";

async function main(): Promise<void> {
    Chart.register(
        LineController,
        BarController,
        DoughnutController,
        LineElement,
        BarElement,
        ArcElement,
        PointElement,
        LinearScale,
        CategoryScale,
        Filler,
        Legend,
        Tooltip,
    );

    const dark = window.matchMedia("(prefers-color-scheme: dark)").matches;
    const style = getComputedStyle(document.documentElement);
    const textColor = style.getPropertyValue(dark ? "--color-base-500" : "--color-base-600").trim();
    const gridColor = style.getPropertyValue(dark ? "--color-base-800" : "--color-base-200").trim();
    const accent = style.getPropertyValue("--color-blue-500").trim();
    const mainText = style.getPropertyValue(dark ? "--color-base-200" : "--color-black").trim();
    const tooltipBg = style.getPropertyValue(dark ? "--color-base-950" : "--color-paper").trim();
    const cv = (name: string) => style.getPropertyValue(name).trim();
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

    // Register doughnut center-total plugin
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

    const response = await fetch(window.location.href, {
        headers: {
            Accept: "application/json",
        },
    });
    const {nodes, os, range, sites, status_time_series, time_series}: AnalyticsData = await response.json();

    if (time_series?.length) {
        lineChart(
            document.getElementById("requests-chart") as HTMLCanvasElement | null,
            time_series,
            range,
        );
    }

    if (status_time_series?.length) {
        stackedBar(
            document.getElementById("status-chart") as HTMLCanvasElement | null,
            status_time_series,
            range,
        );
    }

    if (sites?.length) {
        doughnut(
            document.getElementById("sites-chart") as HTMLCanvasElement | null,
            pluck(sites, "site"),
            pluck(sites, "count"),
        );
    }

    if (os?.length) {
        doughnut(
            document.getElementById("os-chart") as HTMLCanvasElement | null,
            pluck(os, "os"),
            pluck(os, "count"),
        );
    }

    if (nodes?.length) {
        doughnut(
            document.getElementById("nodes-chart") as HTMLCanvasElement | null,
            pluck(nodes, "node_name"),
            pluck(nodes, "count"),
        );
    }

    function lineChart(node: HTMLCanvasElement | null, buckets: TimeBucket[], range: string) {
        if (!node) {
            return;
        }

        const counts = buckets.map(({count}) => count);
        const max = Math.max(...counts) || 1;

        return new Chart(node, {
            type: "line",
            data: {
                labels: buckets.map(({time}) => formatLabel(time, range)),
                datasets: [
                    {
                        backgroundColor: accent + "18",
                        borderColor: accent,
                        borderWidth: 1.5,
                        data: counts,
                        fill: "start",
                        pointHitRadius: 8,
                        pointRadius: 0,
                        tension: 0.35,
                    },
                ],
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
                    tooltip: {
                        backgroundColor: tooltipBg,
                        bodyColor: mainText,
                        borderColor: gridColor,
                        borderWidth: 1,
                        cornerRadius: 6,
                        padding: 10,
                        titleColor: mainText,
                    },
                },
                responsive: true,
                scales: {
                    x: {
                        display: false,
                        offset: false,
                        grid: {
                            color: gridColor + "40",
                        },
                    },
                    y: {
                        display: false,
                        min: -max * 0.05,
                        grace: "5%",
                        afterFit: (axis) => {
                            axis.paddingBottom = 0;
                        },
                    },
                },
            },
        });
    }

    function stackedBar(
        node: HTMLCanvasElement | null,
        buckets: StatusBucket[],
        range: string,
    ) {
        if (!node) {
            return;
        }

        const labels = buckets.map(({time}) => formatLabel(time, range));
        const surfaceColor = style
            .getPropertyValue(dark ? "--color-base-900" : "--color-base-50")
            .trim();

        function dataSet(label: string, data: number[], color: string) {
            return {
                label,
                data,
                backgroundColor: color,
                borderRadius: data.length > 0 ? 1 : 0,
                borderSkipped: data.length === 0,
                borderColor: surfaceColor,
                borderWidth: {
                    top: data.length === 0 ? 0 : 2,
                    right: 0,
                    bottom: 0,
                    left: 0,
                },
            };
        }

        return new Chart(node, {
            type: "bar",
            data: {
                labels,
                datasets: [
                    dataSet(
                        "1/2/3xx",
                        buckets.map(({ok}) => ok),
                        cv("--color-base-500"),
                    ),
                    dataSet(
                        "4xx",
                        buckets.map(({client_err}) => client_err),
                        cv("--color-orange-500"),
                    ),
                    dataSet(
                        "5xx",
                        buckets.map(({server_err}) => server_err),
                        cv("--color-red-400"),
                    ),
                ].filter(({data}) => data.some((value) => value > 0)),
            },
            options: {
                interaction: {intersect: false, mode: "index"},
                maintainAspectRatio: false,
                plugins: {
                    legend: {display: false},
                    tooltip: {
                        backgroundColor: tooltipBg,
                        bodyColor: mainText,
                        borderColor: gridColor,
                        borderWidth: 1,
                        cornerRadius: 6,
                        padding: 10,
                        titleColor: mainText,
                    },
                },
                responsive: true,
                scales: {
                    x: {display: false, stacked: true},
                    y: {display: false, stacked: true, beginAtZero: true, grace: "5%"},
                },
            },
        });
    }

    function doughnut(node: HTMLCanvasElement | null, labels: string[], data: number[]) {
        if (!node) {
            return;
        }

        return new Chart(node, {
            type: "doughnut",
            data: {
                labels,
                datasets: [
                    {
                        backgroundColor: palette.slice(0, labels.length),
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
                layout: {padding: 4},
                plugins: {
                    legend: {
                        position: "bottom",
                        labels: {
                            boxWidth: 8,
                            boxHeight: 8,
                            borderRadius: 4,
                            useBorderRadius: true,
                            padding: 12,
                            font: {size: 11, weight: "bold"},
                        },
                    },
                    tooltip: {
                        backgroundColor: tooltipBg,
                        bodyColor: mainText,
                        borderColor: gridColor,
                        borderWidth: 1,
                        cornerRadius: 6,
                        padding: 10,
                        titleColor: mainText,
                    },
                },
            },
        });
    }
}

document.addEventListener("DOMContentLoaded", main);

window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    location.reload();
});

interface TimeBucket {
    time: string;
    count: number;
}

interface StatusBucket {
    time: string;
    ok: number;
    client_err: number;
    server_err: number;
}

interface AnalyticsData {
    range: string;
    time_series?: TimeBucket[];
    status_time_series?: StatusBucket[];
    sites?: { site: string; count: number }[];
    os?: { os: string; count: number }[];
    nodes?: { node_name: string; count: number }[];
}

function pluck<T, K extends keyof T>(items: T[], key: K): T[K][] {
    return items.map((item) => item[key]);
}

function formatNumber(number: number): string {
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

function formatLabel(iso: string, range: string): string {
    const date = new Date(iso);

    if (isShortRange(range)) {
        return date.toLocaleTimeString([], {
            hour: "2-digit",
            minute: "2-digit",
            hour12: false,
        });
    }

    return date.toLocaleDateString([], {
        month: "short",
        day: "numeric",
    });
}
