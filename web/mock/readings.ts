// Deterministic fake readings. A value at a given timestamp is always the same,
// so polling with "since" appends consistently and reloads look stable.

import type { MockStation } from './state';

const STEP_MS = 16_000; // the WH65 family reports roughly every 16 seconds

function hash(n: number): number {
  // Small integer hash mapped to [0, 1). Good enough for plausible jitter.
  let x = (n ^ 0x9e3779b9) >>> 0;
  x = Math.imul(x ^ (x >>> 16), 0x45d9f3b) >>> 0;
  x = Math.imul(x ^ (x >>> 16), 0x45d9f3b) >>> 0;
  x = (x ^ (x >>> 16)) >>> 0;
  return x / 4294967296;
}

function jitter(slot: number, salt: number, amplitude: number): number {
  return (hash(slot * 31 + salt) - 0.5) * 2 * amplitude;
}

export interface Reading {
  ts: string;
  f: Record<string, number>;
}

export function readingAt(station: MockStation, tsMs: number): Reading {
  const slot = Math.floor(tsMs / STEP_MS);
  const dayFrac = (((tsMs / 86_400_000) % 1) + 1) % 1; // UTC fraction of day
  const diurnal = Math.sin((dayFrac - 0.3) * 2 * Math.PI); // peaks mid afternoon
  const stationSalt = station.id * 7;

  const f: Record<string, number> = {};
  const has = (name: string) => station.fields.includes(name);

  if (has('temp_c')) f.temp_c = round(14 + 7 * diurnal + jitter(slot, stationSalt + 1, 0.15), 1);
  if (has('humidity_pct')) {
    f.humidity_pct = round(
      clamp(68 - 22 * diurnal + jitter(slot, stationSalt + 2, 1.5), 15, 100),
      0,
    );
  }
  if (has('pressure_station_hpa')) {
    f.pressure_station_hpa = round(1012 + 3 * Math.sin(tsMs / 7_200_000) + jitter(slot, 3, 0.1), 1);
  }
  if (has('wind_speed_ms')) {
    const base = Math.max(0, 2.5 + 2 * diurnal + jitter(slot, stationSalt + 4, 1.4));
    f.wind_speed_ms = round(base, 1);
    if (has('wind_gust_ms'))
      f.wind_gust_ms = round(base + Math.abs(jitter(slot, stationSalt + 5, 2.2)), 1);
  }
  if (has('wind_dir_deg')) {
    f.wind_dir_deg = round(
      (225 + 40 * Math.sin(tsMs / 3_600_000) + jitter(slot, stationSalt + 6, 25) + 360) % 360,
      0,
    );
  }
  if (has('rain_mm')) {
    // Cumulative counter that steps up during a "shower" window each day.
    const showers = Math.floor(tsMs / 86_400_000) * 6.4;
    const inShower = dayFrac > 0.55 && dayFrac < 0.62;
    const partial = inShower ? ((dayFrac - 0.55) / 0.07) * 6.4 : dayFrac >= 0.62 ? 6.4 : 0;
    f.rain_mm = round(showers + partial, 1);
  }
  if (has('uv_index')) f.uv_index = round(Math.max(0, 6 * diurnal), 1);
  if (has('light_lux'))
    f.light_lux = round(Math.max(0, 52_000 * diurnal + jitter(slot, 7, 800)), 0);
  if (has('solar_wm2')) f.solar_wm2 = round(Math.max(0, 640 * diurnal), 0);
  if (has('lightning_count')) f.lightning_count = 0;
  if (has('battery_ok')) f.battery_ok = station.battery_ok ? 1 : 0;

  return { ts: new Date(slot * STEP_MS).toISOString(), f };
}

export function readingsSince(station: MockStation, sinceMs: number, nowMs: number): Reading[] {
  const out: Reading[] = [];
  const first = Math.floor(sinceMs / STEP_MS) * STEP_MS + STEP_MS;
  const last = Math.floor(nowMs / STEP_MS) * STEP_MS;
  // Cap the size so a bad "since" cannot blow up the dev server.
  const maxPoints = 20_000;
  let t = Math.max(first, last - maxPoints * STEP_MS);
  for (; t <= last; t += STEP_MS) out.push(readingAt(station, t));
  return out;
}

function round(n: number, digits: number): number {
  const p = 10 ** digits;
  return Math.round(n * p) / p;
}

function clamp(n: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, n));
}
