import {
  doughnut,
  formatLabel,
  initCharts,
  lineChart,
  pluck,
  reloadOnThemeChange,
} from "../lib/charts";
import { initNotifications } from "../lib/notifications";

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
    const allCounts = forms.flatMap(({ series }) => series.map(({ count }) => count));
    const max = Math.max(...allCounts) || 1;

    const datasets = forms.map(({ name, series }, index) => ({
      label: name,
      data: series.map(({ count }) => count),
      color: theme.palette[index % theme.palette.length],
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
    const nameMap = new Map((data.time_series_by_form ?? []).map(({ name, path }) => [path, name]));

    doughnut(
      document.getElementById("forms-chart") as HTMLCanvasElement | null,
      data.paths.map(({ path }) => nameMap.get(path) ?? path),
      pluck(data.paths, "count"),
      theme,
      { center: "count" },
    );
  }
}

async function initPushNotifications(): Promise<void> {
  const button = document.getElementById("notify-btn") as HTMLButtonElement | null;

  if (!button) {
    return;
  }

  const site = button.dataset.site ?? "";
  const path = button.dataset.path ?? "";
  const vapidKey = button.dataset.vapid ?? "";

  if (site && path && vapidKey) {
    await initNotifications(site, path, vapidKey, button);
  }
}

document.addEventListener("DOMContentLoaded", async () =>
  Promise.all([main(), initPushNotifications()]),
);
reloadOnThemeChange();
