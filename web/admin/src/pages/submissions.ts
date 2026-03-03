import {
  doughnut,
  formatLabel,
  initCharts,
  lineChart,
  pluck,
  reloadOnThemeChange,
} from "../lib/charts";

interface TimeBucket {
  time: string;
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

  if (data.paths && data.paths.length > 1) {
    const nameMap = new Map(
      (data.time_series_by_form ?? []).map((f) => [f.path, f.name]),
    );
    doughnut(
      document.getElementById("forms-chart") as HTMLCanvasElement | null,
      data.paths.map((p) => nameMap.get(p.path) ?? p.path),
      pluck(data.paths, "count"),
      theme,
      { center: "count" },
    );
  }
}

document.addEventListener("DOMContentLoaded", main);
reloadOnThemeChange();
