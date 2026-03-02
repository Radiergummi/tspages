import {
  doughnut,
  formatLabel,
  initCharts,
  lineChart,
  pluck,
  reloadOnThemeChange,
  stackedBar,
} from "../lib/charts";

interface TimeBucket {
  time: string;
  count: number;
}

interface HourCount {
  hour: number;
  count: number;
}

interface FormPath {
  path: string;
  count: number;
}

interface SubmissionsData {
  time_series?: TimeBucket[];
  hourly_pattern?: HourCount[];
  paths?: FormPath[];
}

async function main(): Promise<void> {
  const theme = initCharts();

  const response = await fetch(window.location.href, {
    headers: { Accept: "application/json" },
  });

  if (!response.ok) {
    return;
  }

  const data: SubmissionsData = await response.json();

  if (data.time_series?.length) {
    const counts = data.time_series.map(({ count }) => count);
    const max = Math.max(...counts) || 1;

    lineChart(
      document.getElementById("submissions-chart") as HTMLCanvasElement | null,
      data.time_series.map(({ time }) => formatLabel(time, "all")),
      [{ label: "Submissions", data: counts, color: theme.cv("--color-blue-500"), fill: "start" }],
      { yMin: -max * 0.05 },
    );
  }

  if (data.hourly_pattern?.length) {
    const hourMap = new Map(data.hourly_pattern.map((h) => [h.hour, h.count]));
    const labels = Array.from({ length: 24 }, (_, i) => `${String(i).padStart(2, "0")}:00`);
    const counts = Array.from({ length: 24 }, (_, i) => hourMap.get(i) ?? 0);

    stackedBar(
      document.getElementById("hourly-chart") as HTMLCanvasElement | null,
      labels,
      [{ label: "Submissions", data: counts, color: theme.cv("--color-blue-500") }],
      theme,
    );
  }

  if (data.paths && data.paths.length > 1) {
    doughnut(
      document.getElementById("forms-chart") as HTMLCanvasElement | null,
      pluck(data.paths, "path"),
      pluck(data.paths, "count"),
      theme,
      { center: "count" },
    );
  }
}

document.addEventListener("DOMContentLoaded", main);
reloadOnThemeChange();
