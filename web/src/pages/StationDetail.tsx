import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { api, errorMessage, type Reading, type Station } from '../api';
import { Alert } from '../components/Alert';
import { Badge } from '../components/Badge';
import { Card } from '../components/Card';
import { Chart, type ChartSeries } from '../components/Chart';
import { Stat, StatGrid } from '../components/Stat';
import { formatDateTime, formatRelative } from '../lib/format';
import { convert, fieldMeta, formatField, unitLabel, type UnitSystem } from '../lib/units';
import { useUnits } from '../lib/UnitsContext';
import { usePoll } from '../lib/usePoll';

const RANGES = [
  { label: '1 h', seconds: 3600 },
  { label: '6 h', seconds: 6 * 3600 },
  { label: '24 h', seconds: 24 * 3600 },
  { label: '7 d', seconds: 7 * 24 * 3600 },
];

const LIVE_POLL_MS = 10_000;

/** Keep the readings list sorted and free of duplicates. */
function mergeReadings(existing: Reading[], incoming: Reading[], cutoffMs: number): Reading[] {
  if (incoming.length === 0) return existing.filter((r) => Date.parse(r.ts) >= cutoffMs);
  const seen = new Set(existing.map((r) => r.ts));
  const merged = existing.slice();
  for (const r of incoming) {
    if (!seen.has(r.ts)) {
      merged.push(r);
      seen.add(r.ts);
    }
  }
  merged.sort((a, b) => Date.parse(a.ts) - Date.parse(b.ts));
  return merged.filter((r) => Date.parse(r.ts) >= cutoffMs);
}

function useReadings(key: string | undefined, rangeSeconds: number) {
  const [readings, setReadings] = useState<Reading[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const lastTsRef = useRef<number | null>(null);

  // Full reload when the station or the range changes.
  useEffect(() => {
    if (!key) return;
    const ac = new AbortController();
    setLoading(true);
    setReadings([]);
    lastTsRef.current = null;
    const since = new Date(Date.now() - rangeSeconds * 1000);
    api
      .readings(key, since, { signal: ac.signal })
      .then((rows) => {
        if (ac.signal.aborted) return;
        rows.sort((a, b) => Date.parse(a.ts) - Date.parse(b.ts));
        setReadings(rows);
        lastTsRef.current = rows.length ? Date.parse(rows[rows.length - 1].ts) : since.getTime();
        setError(null);
      })
      .catch((err) => {
        if (!ac.signal.aborted) setError(err);
      })
      .finally(() => {
        if (!ac.signal.aborted) setLoading(false);
      });
    return () => ac.abort();
  }, [key, rangeSeconds]);

  // Incremental poll for anything newer than what we have.
  const pollLatest = useCallback(
    async (signal: AbortSignal) => {
      if (!key || lastTsRef.current === null) return null;
      const since = new Date(lastTsRef.current);
      const rows = await api.readings(key, since, { signal });
      if (rows.length) {
        const cutoff = Date.now() - rangeSeconds * 1000;
        setReadings((prev) => mergeReadings(prev, rows, cutoff));
        lastTsRef.current = Math.max(lastTsRef.current, ...rows.map((r) => Date.parse(r.ts)));
      }
      return rows.length;
    },
    [key, rangeSeconds],
  );
  usePoll(pollLatest, LIVE_POLL_MS, [pollLatest], Boolean(key));

  return { readings, error, loading };
}

interface ChartSpec {
  title: string;
  fields: { key: string; colorVar: string; rightAxis?: boolean; stepped?: boolean }[];
}

const CHARTS: ChartSpec[] = [
  {
    title: 'Temperature and humidity',
    fields: [
      { key: 'temp_c', colorVar: '--chart-2' },
      { key: 'humidity_pct', colorVar: '--chart-1', rightAxis: true },
    ],
  },
  {
    title: 'Wind',
    fields: [
      { key: 'wind_speed_ms', colorVar: '--chart-3' },
      { key: 'wind_gust_ms', colorVar: '--chart-5' },
    ],
  },
  { title: 'Pressure', fields: [{ key: 'pressure_station_hpa', colorVar: '--chart-4' }] },
  { title: 'Rain total', fields: [{ key: 'rain_mm', colorVar: '--chart-1', stepped: true }] },
  {
    title: 'Sun',
    fields: [
      { key: 'solar_wm2', colorVar: '--chart-5' },
      { key: 'light_lux', colorVar: '--chart-5' },
      { key: 'uv_index', colorVar: '--chart-4', rightAxis: true },
    ],
  },
];

function buildSeries(
  readings: Reading[],
  spec: ChartSpec,
  system: UnitSystem,
): { times: number[]; series: ChartSeries[] } | null {
  const present = spec.fields.filter((f) => readings.some((r) => f.key in r.f));
  if (present.length === 0) return null;
  const times = readings.map((r) => Math.floor(Date.parse(r.ts) / 1000));
  const series = present.map<ChartSeries>((f) => {
    const meta = fieldMeta(f.key);
    return {
      label: meta.label,
      colorVar: f.colorVar,
      unit: unitLabel(meta.quantity, system),
      rightAxis: f.rightAxis,
      stepped: f.stepped,
      values: readings.map((r) =>
        f.key in r.f ? convert(r.f[f.key], meta.quantity, system) : null,
      ),
    };
  });
  return { times, series };
}

export function StationDetailPage() {
  const { key } = useParams<{ key: string }>();
  const { system } = useUnits();
  const [rangeIdx, setRangeIdx] = useState(2);
  const range = RANGES[rangeIdx];
  const stations = usePoll((s) => api.stations.list({ signal: s }), 15000);
  const station: Station | null = stations.data?.find((s) => s.key === key) ?? null;
  const { readings, error, loading } = useReadings(key, range.seconds);

  useEffect(() => {
    document.title = `${station?.name || station?.model || 'Station'} | PWS Gateway`;
  }, [station]);

  const latest = readings.length ? readings[readings.length - 1] : null;

  // Latest value per field, falling back to the most recent reading that has it.
  const latestByField = useMemo(() => {
    const out: Record<string, { value: number; ts: string }> = {};
    for (let i = readings.length - 1; i >= 0; i--) {
      for (const [k, v] of Object.entries(readings[i].f)) {
        if (!(k in out)) out[k] = { value: v, ts: readings[i].ts };
      }
    }
    return out;
  }, [readings]);

  const charts = useMemo(
    () =>
      CHARTS.map((spec) => ({ spec, data: buildSeries(readings, spec, system) })).filter(
        (c) => c.data !== null,
      ),
    [readings, system],
  );

  if (!key) return <Alert tone="bad">No station given.</Alert>;

  return (
    <>
      <div className="page-header">
        <h1>{station ? station.name || station.model : key}</h1>
        {station?.selected && <Badge tone="info">Selected for upload</Badge>}
        <span className="spacer" />
        <div className="range-tabs" role="group" aria-label="Chart time range">
          {RANGES.map((r, i) => (
            <button
              key={r.label}
              type="button"
              aria-pressed={i === rangeIdx}
              onClick={() => setRangeIdx(i)}
            >
              {r.label}
            </button>
          ))}
        </div>
      </div>

      <p className="muted">
        <Link to="/stations">All stations</Link>
        {station && (
          <>
            {' '}
            | {station.model} id {station.id}
            {station.channel !== null ? ` ch ${station.channel}` : ''} | last heard{' '}
            {formatRelative(station.last_seen)} | signal {station.rssi_db.toFixed(0)} dB, SNR{' '}
            {station.snr_db.toFixed(0)} dB
          </>
        )}
      </p>

      {stations.data && !station && (
        <Alert tone="warn">This station has not been heard recently and is no longer listed.</Alert>
      )}
      {error && <Alert tone="bad">{errorMessage(error)}</Alert>}

      <div className="stack">
        <Card
          title="Latest readings"
          id="latest"
          actions={
            <span className="muted" role="status" aria-live="polite">
              {latest
                ? `at ${formatDateTime(latest.ts)}`
                : loading
                  ? 'Loading...'
                  : 'no readings in this range'}
            </span>
          }
        >
          <div className="readings-grid">
            {Object.entries(latestByField)
              .sort(([a], [b]) => a.localeCompare(b))
              .map(([k, v]) => (
                <Stat key={k} label={fieldMeta(k).label} value={formatField(k, v.value, system)} />
              ))}
          </div>
          {!loading && Object.keys(latestByField).length === 0 && (
            <p className="muted">Nothing recorded for this station in the selected range.</p>
          )}
        </Card>

        {charts.map(({ spec, data }) => (
          <Card
            key={spec.title}
            title={spec.title}
            id={spec.title.replace(/\s+/g, '-').toLowerCase()}
          >
            {data && data.times.length > 1 ? (
              <Chart
                times={data.times}
                series={data.series}
                title={`${spec.title} over the last ${range.label}`}
              />
            ) : (
              <p className="muted">Not enough points to draw yet.</p>
            )}
          </Card>
        ))}

        <StatGrid>
          <Stat
            label="Points loaded"
            value={readings.length.toLocaleString()}
            hint={`last ${range.label}, refreshes every ${LIVE_POLL_MS / 1000}s`}
          />
        </StatGrid>
      </div>
    </>
  );
}
