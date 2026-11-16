import { useEffect, useRef } from 'react';
import uPlot from 'uplot';
import 'uplot/dist/uPlot.min.css';

export interface ChartSeries {
  label: string;
  /** Display values, already converted. Length must match `times`. */
  values: (number | null)[];
  /** CSS custom property name holding the stroke colour, for example --chart-1. */
  colorVar: string;
  /** Unit label appended in the legend and axis. */
  unit?: string;
  /** Plot on the right-hand scale instead of the left. */
  rightAxis?: boolean;
  /** Step line for cumulative counters like rain. */
  stepped?: boolean;
}

interface ChartProps {
  /** Unix seconds. */
  times: number[];
  series: ChartSeries[];
  height?: number;
  /** Accessible description of what the chart shows. */
  title: string;
}

function cssVar(name: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback;
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return v || fallback;
}

function buildOptions(series: ChartSeries[], width: number, height: number): uPlot.Options {
  const text = cssVar('--fg-muted', '#777');
  const grid = cssVar('--border', '#ccc');
  const hasRight = series.some((s) => s.rightAxis);
  const leftUnit = series.find((s) => !s.rightAxis)?.unit ?? '';
  const rightUnit = series.find((s) => s.rightAxis)?.unit ?? '';

  const axes: uPlot.Axis[] = [
    {
      stroke: text,
      grid: { stroke: grid, width: 1 },
      ticks: { stroke: grid, width: 1 },
      font: '12px system-ui, sans-serif',
    },
    {
      scale: 'left',
      stroke: text,
      grid: { stroke: grid, width: 1 },
      ticks: { stroke: grid, width: 1 },
      font: '12px system-ui, sans-serif',
      size: 56,
      values: (_u, vals) => vals.map((v) => `${formatTick(v)}${leftUnit ? ` ${leftUnit}` : ''}`),
    },
  ];
  if (hasRight) {
    axes.push({
      scale: 'right',
      side: 1,
      stroke: text,
      grid: { show: false },
      ticks: { stroke: grid, width: 1 },
      font: '12px system-ui, sans-serif',
      size: 56,
      values: (_u, vals) => vals.map((v) => `${formatTick(v)}${rightUnit ? ` ${rightUnit}` : ''}`),
    });
  }

  return {
    width,
    height,
    cursor: { drag: { x: true, y: false } },
    legend: { live: true },
    scales: {
      x: { time: true },
      left: { auto: true },
      right: { auto: true },
    },
    axes,
    series: [
      { label: 'Time' },
      ...series.map<uPlot.Series>((s) => ({
        label: s.label,
        scale: s.rightAxis ? 'right' : 'left',
        stroke: cssVar(s.colorVar, '#1f6feb'),
        width: 2,
        spanGaps: false,
        paths: s.stepped ? uPlot.paths.stepped?.({ align: 1 }) : undefined,
        value: (_u, v) =>
          v === null || v === undefined ? '--' : `${v.toFixed(1)}${s.unit ? ` ${s.unit}` : ''}`,
      })),
    ],
  };
}

function formatTick(v: number): string {
  if (Math.abs(v) >= 1000) return v.toFixed(0);
  if (Number.isInteger(v)) return String(v);
  return v.toFixed(1);
}

/**
 * uPlot wrapper. Re-creates the plot when the series definition or the colour
 * scheme changes and only calls setData when values update, which keeps the
 * live chart cheap on a Pi-sized browser tab too.
 */
export function Chart({ times, series, height = 220, title }: ChartProps) {
  const hostRef = useRef<HTMLDivElement>(null);
  const plotRef = useRef<uPlot | null>(null);
  const seriesSig = series
    .map((s) => `${s.label}|${s.colorVar}|${s.rightAxis ? 'r' : 'l'}|${s.unit ?? ''}`)
    .join(';');

  // Create and destroy the plot. Depends on the series layout, not the values.
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const width = host.clientWidth || 600;
    const plot = new uPlot(
      buildOptions(series, width, height),
      [times, ...series.map((s) => s.values)],
      host,
    );
    plotRef.current = plot;

    const ro = new ResizeObserver(() => {
      const w = host.clientWidth;
      if (w > 0) plot.setSize({ width: w, height });
    });
    ro.observe(host);

    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const onScheme = () => {
      // Colours are read from CSS variables at build time, so rebuild.
      plot.destroy();
      const p2 = new uPlot(
        buildOptions(series, host.clientWidth || width, height),
        [times, ...series.map((s) => s.values)],
        host,
      );
      plotRef.current = p2;
    };
    mq.addEventListener('change', onScheme);

    return () => {
      mq.removeEventListener('change', onScheme);
      ro.disconnect();
      plotRef.current?.destroy();
      plotRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seriesSig, height]);

  // Push new data without rebuilding.
  useEffect(() => {
    plotRef.current?.setData([times, ...series.map((s) => s.values)]);
  }, [times, series]);

  return (
    <figure className="chart">
      <figcaption className="visually-hidden">{title}</figcaption>
      <div ref={hostRef} className="chart-host" role="img" aria-label={title} />
    </figure>
  );
}
