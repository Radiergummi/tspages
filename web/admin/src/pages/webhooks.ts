import {
    ArcElement,
    BarController,
    BarElement,
    CategoryScale,
    Chart,
    DoughnutController,
    Legend,
    LinearScale,
    Tooltip,
} from "chart.js";

async function main(): Promise<void> {
    Chart.register(
        BarController,
        DoughnutController,
        BarElement,
        ArcElement,
        LinearScale,
        CategoryScale,
        Legend,
        Tooltip,
    );

    const dark = window.matchMedia("(prefers-color-scheme: dark)").matches;
    const style = getComputedStyle(document.documentElement);
    const textColor = style.getPropertyValue(dark ? "--color-base-500" : "--color-base-600").trim();
    const gridColor = style.getPropertyValue(dark ? "--color-base-800" : "--color-base-200").trim();
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
            const cx = (chartArea.left + chartArea.right) / 2;
            const cy = (chartArea.top + chartArea.bottom) / 2;
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
        headers: {Accept: "application/json"},
    });
    const data: WebhookData = await response.json();

    if (data.time_series?.length) {
        stackedBar(
            document.getElementById("deliveries-chart") as HTMLCanvasElement | null,
            data.time_series,
            data.range,
        );
    }

    if (data.events?.length) {
        doughnut(
            document.getElementById("events-chart") as HTMLCanvasElement | null,
            data.events.map((e) => e.event),
            data.events.map((e) => e.count),
        );
    }

    function stackedBar(
        node: HTMLCanvasElement | null,
        buckets: DeliveryBucket[],
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
                        "Succeeded",
                        buckets.map(({succeeded}) => succeeded),
                        cv("--color-green-500"),
                    ),
                    dataSet(
                        "Failed",
                        buckets.map(({failed}) => failed),
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

interface DeliveryBucket {
    time: string;
    succeeded: number;
    failed: number;
}

interface WebhookData {
    range: string;
    total: number;
    succeeded: number;
    failed: number;
    time_series?: DeliveryBucket[];
    events?: { event: string; count: number }[];
}

function formatNumber(number: number): string {
    if (number >= 1_000_000) {
        const v = number / 1_000_000;
        return (v === Math.floor(v) ? v.toFixed(0) : v.toFixed(1)) + "M";
    }

    if (number >= 1_000) {
        const v = number / 1_000;
        return (v === Math.floor(v) ? v.toFixed(0) : v.toFixed(1)) + "k";
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

    if (+(y || 0) > 0 || +(mo || 0) > 0 || +(w || 0) > 0 || +(d || 0) > 0) {
        return false;
    }
    return +(h || 0) <= 24;
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
