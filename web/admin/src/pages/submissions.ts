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

interface FormSeries {
  path: string;
  name: string;
  series: TimeBucket[];
}

interface SubmissionsData {
  time_series_by_form?: FormSeries[];
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

  if (data.time_series_by_form?.length) {
    const forms = data.time_series_by_form;
    const labels = forms[0].series.map(({ time }) => formatLabel(time, "all"));
    const allCounts = forms.flatMap((f) => f.series.map((b) => b.count));
    const max = Math.max(...allCounts) || 1;

    const datasets = forms.map((f, i) => ({
      label: f.name,
      data: f.series.map(({ count }) => count),
      color: theme.palette[i % theme.palette.length],
      fill: forms.length === 1 ? ("start" as const) : false,
    }));

    lineChart(
      document.getElementById("submissions-chart") as HTMLCanvasElement | null,
      labels,
      datasets,
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
