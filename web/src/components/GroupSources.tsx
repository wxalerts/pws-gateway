import { useEffect, useMemo, useState } from 'react';
import { errorMessage, type Station } from '../api';
import { Alert } from './Alert';
import { Button } from './Button';
import { Card } from './Card';
import { SelectField } from './fields';

/**
 * The field groups of the combined record the gateway uploads, in display
 * order. A group is supplied by one station as a whole, so wind speed, gust and
 * direction never come from different anemometers. Must match
 * `config.FieldGroups` in the Go daemon.
 */
export const FIELD_GROUPS: { id: string; label: string; fields: string[] }[] = [
  { id: 'temperature', label: 'Temperature', fields: ['temp_c'] },
  { id: 'humidity', label: 'Humidity', fields: ['humidity_pct'] },
  { id: 'pressure', label: 'Pressure', fields: ['pressure_station_hpa'] },
  { id: 'wind', label: 'Wind', fields: ['wind_speed_ms', 'wind_gust_ms', 'wind_dir_deg'] },
  { id: 'rain', label: 'Rain', fields: ['rain_mm'] },
  { id: 'solar_uv', label: 'Solar and UV', fields: ['uv_index', 'light_lux', 'solar_wm2'] },
  { id: 'lightning', label: 'Lightning', fields: ['lightning_count', 'lightning_km'] },
];

export type Composite = Record<string, string>;

/** Selected stations that report at least one field of the group. */
export function candidates(stations: Station[], fields: string[]): Station[] {
  return stations.filter((s) => s.selected && s.fields.some((f) => fields.includes(f)));
}

interface GroupSourcesProps {
  stations: Station[];
  /** The saved group to station key map (config section `composite`). */
  composite: Composite;
  onSave: (next: Composite) => Promise<void>;
}

const AUTO = '';

function stationLabel(s: Station): string {
  return s.name || `${s.model} id ${s.id}${s.channel ? ` ch ${s.channel}` : ''}`;
}

/**
 * "Which station supplies each group". The gateway sends WxAlerts one combined
 * record, so when two selected stations report the same group the owner picks
 * one here. Automatic means the only station that reports it; with several
 * candidates and no choice the group is left out, so it is flagged.
 */
export function GroupSources({ stations, composite, onSave }: GroupSourcesProps) {
  const savedKey = JSON.stringify(composite);
  const [draft, setDraft] = useState<Composite>(composite);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    setDraft(composite);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [savedKey]);

  const rows = useMemo(
    () =>
      FIELD_GROUPS.map((g) => ({ ...g, options: candidates(stations, g.fields) })).filter(
        (g) => g.options.length > 0,
      ),
    [stations],
  );
  // Nothing to choose until a group has two candidates.
  if (!rows.some((g) => g.options.length > 1)) return null;

  const dirty = JSON.stringify(draft) !== savedKey;

  async function save() {
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      const next: Composite = {};
      for (const g of rows) {
        const key = draft[g.id];
        // Only a station that is still a candidate is kept.
        if (key && g.options.some((s) => s.key === key)) next[g.id] = key;
      }
      await onSave(next);
      setSaved(true);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Which station supplies each reading" id="group-sources">
      <p className="muted">
        WxAlerts receives one combined weather report from this gateway. When more than one selected
        station reports the same kind of reading, choose which one to use. Wind speed, gust and
        direction always come from the same station.
      </p>
      <div className="form-narrow">
        {rows.map((g) => {
          const chosen = draft[g.id] ?? AUTO;
          const ambiguous = g.options.length > 1 && chosen === AUTO;
          return (
            <SelectField
              key={g.id}
              label={g.label}
              value={chosen}
              onChange={(v) => {
                setSaved(false);
                setDraft((d) => ({ ...d, [g.id]: v }));
              }}
              options={[
                {
                  value: AUTO,
                  label:
                    g.options.length > 1
                      ? 'Not chosen'
                      : `Automatic (${stationLabel(g.options[0])})`,
                },
                ...g.options.map((s) => ({ value: s.key, label: stationLabel(s) })),
              ]}
              hint={
                ambiguous
                  ? `${g.options.length} stations report this. Until you choose one, ${g.label.toLowerCase()} is not sent.`
                  : undefined
              }
              error={undefined}
            />
          );
        })}
      </div>
      {error && (
        <Alert tone="bad" live>
          {error}
        </Alert>
      )}
      {saved && (
        <Alert tone="ok" live>
          Saved.
        </Alert>
      )}
      <div className="btn-row">
        <Button variant="primary" onClick={save} busy={busy} disabled={!dirty}>
          Save choices
        </Button>
      </div>
    </Card>
  );
}
