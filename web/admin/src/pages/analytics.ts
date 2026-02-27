import {doughnut, formatLabel, initCharts, lineChart, pluck, reloadOnThemeChange, stackedBar} from "../lib/charts";

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

async function main(): Promise<void> {
    const theme = initCharts();

    const response = await fetch(window.location.href, {
        headers: {Accept: "application/json"},
    });
    const {nodes, os, range, sites, status_time_series, time_series}: AnalyticsData =
        await response.json();

    if (time_series?.length) {
        const counts = time_series.map(({count}) => count);
        const max = Math.max(...counts) || 1;

        lineChart(
            document.getElementById("requests-chart") as HTMLCanvasElement | null,
            time_series.map(({time}) => formatLabel(time, range)),
            [{label: "Requests", data: counts, color: theme.cv("--color-blue-500"), fill: "start"}],
            {yMin: -max * 0.05},
        );
    }

    if (status_time_series?.length) {
        stackedBar(
            document.getElementById("status-chart") as HTMLCanvasElement | null,
            status_time_series.map(({time}) => formatLabel(time, range)),
            [
                {
                    label: "1/2/3xx",
                    data: status_time_series.map(({ok}) => ok),
                    color: theme.cv("--color-base-500"),
                },
                {
                    label: "4xx",
                    data: status_time_series.map(({client_err}) => client_err),
                    color: theme.cv("--color-orange-500"),
                },
                {
                    label: "5xx",
                    data: status_time_series.map(({server_err}) => server_err),
                    color: theme.cv("--color-red-400"),
                },
            ],
            theme,
        );
    }

    if (sites?.length) {
        doughnut(
            document.getElementById("sites-chart") as HTMLCanvasElement | null,
            pluck(sites, "site"),
            pluck(sites, "count"),
            theme,
        );
    }

    if (os?.length) {
        doughnut(
            document.getElementById("os-chart") as HTMLCanvasElement | null,
            pluck(os, "os"),
            pluck(os, "count"),
            theme,
        );
    }

    if (nodes?.length) {
        doughnut(
            document.getElementById("nodes-chart") as HTMLCanvasElement | null,
            pluck(nodes, "node_name"),
            pluck(nodes, "count"),
            theme,
        );
    }
}

document.addEventListener("DOMContentLoaded", main);
reloadOnThemeChange();
