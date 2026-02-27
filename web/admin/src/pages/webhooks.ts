import {doughnut, formatLabel, initCharts, lineChart, reloadOnThemeChange, stackedBar} from "../lib/charts";

interface DeliveryBucket {
    time: string;
    succeeded: number;
    failed: number;
}

interface LatencyBucket {
    time: string;
    p50: number;
    p95: number;
    max: number;
}

interface WebhookData {
    range: string;
    total: number;
    succeeded: number;
    failed: number;
    time_series?: DeliveryBucket[];
    events?: { event: string; count: number }[];
    latency?: LatencyBucket[];
}

async function main(): Promise<void> {
    const theme = initCharts();

    const response = await fetch(window.location.href, {
        headers: {Accept: "application/json"},
    });
    const data: WebhookData = await response.json();

    if (data.time_series?.length) {
        stackedBar(
            document.getElementById("deliveries-chart") as HTMLCanvasElement | null,
            data.time_series.map(({time}) => formatLabel(time, data.range)),
            [
                {
                    label: "Succeeded",
                    data: data.time_series.map(({succeeded}) => succeeded),
                    color: theme.cv("--color-green-500"),
                },
                {
                    label: "Failed",
                    data: data.time_series.map(({failed}) => failed),
                    color: theme.cv("--color-red-400"),
                },
            ],
            theme,
        );
    }

    if (data.events?.length) {
        doughnut(
            document.getElementById("events-chart") as HTMLCanvasElement | null,
            data.events.map(({event}) => event),
            data.events.map(({count}) => count),
            theme,
        );
    }

    if (data.latency?.length) {
        lineChart(
            document.getElementById("latency-chart") as HTMLCanvasElement | null,
            data.latency.map(({time}) => formatLabel(time, data.range)),
            [
                {
                    label: "Avg",
                    data: data.latency.map(({p50}) => Math.round(p50)),
                    color: theme.cv("--color-blue-500"),
                    fill: true,
                },
                {
                    label: "p95",
                    data: data.latency.map(({p95}) => Math.round(p95)),
                    color: theme.cv("--color-yellow-400"),
                    borderDash: [4, 3],
                },
                {
                    label: "Max",
                    data: data.latency.map(({max}) => Math.round(max)),
                    color: theme.cv("--color-red-400"),
                    borderDash: [2, 2],
                    borderWidth: 1,
                },
            ],
            {formatValue: (v) => `${v}ms`, total: false},
        );
    }
}

document.addEventListener("DOMContentLoaded", main);
reloadOnThemeChange();
