import { useEffect, useMemo, useState } from 'react';
import { errorMessage, type Station, type StationUpdate } from '../api';
import { fieldMeta, formatField, unitLabel, type UnitSystem } from '../lib/units';
import { Alert } from './Alert';
import { Button } from './Button';
import { Card } from './Card';
import { CheckboxField } from './fields';

interface DataToUseProps {
  station: Station;
  /** Latest value per canonical field, in SI, from the recent readings. */
  latest: Record<string, { value: number; ts: string }>;
  system: UnitSystem;
  onSave: (patch: StationUpdate) => Promise<void>;
}

/** Order fields the way the registry lists them, unknown names last. */
function sortFields(names: string[]): string[] {
  return [...names].sort((a, b) => a.localeCompare(b));
}

/**
 * The "Data to use" checklist: which fields of this station are stored and
 * uploaded. Defaults come from the gateway, per model.
 */
export function DataToUse({ station, latest, system, onSave }: DataToUseProps) {
  const savedKey = JSON.stringify([station.fields, station.include_raw]);
  const [checked, setChecked] = useState<Set<string>>(() => new Set(station.fields));
  const [includeRaw, setIncludeRaw] = useState(station.include_raw);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  // Pick up the server's view after a save or a reload, not on every poll.
  useEffect(() => {
    setChecked(new Set(station.fields));
    setIncludeRaw(station.include_raw);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [savedKey]);

  const rows = useMemo(
    () => sortFields([...new Set([...station.fields_available, ...station.fields])]),
    [station.fields_available, station.fields],
  );
  const defaults = useMemo(() => new Set(station.fields_default), [station.fields_default]);
  const isDefault = rows.every((f) => checked.has(f) === defaults.has(f));

  function toggle(f: string, on: boolean) {
    setSaved(false);
    setChecked((prev) => {
      const next = new Set(prev);
      if (on) next.add(f);
      else next.delete(f);
      return next;
    });
  }

  function resetToDefaults() {
    setSaved(false);
    setChecked(new Set(station.fields_default));
  }

  async function save() {
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      const fields: Record<string, boolean> = {};
      for (const f of rows) fields[f] = checked.has(f);
      const patch: StationUpdate = { fields: isDefault ? null : fields };
      // Leave include_raw alone unless it was changed, so the default rule
      // (on once the station is selected) keeps applying.
      if (includeRaw !== station.include_raw) patch.include_raw = includeRaw;
      await onSave(patch);
      setSaved(true);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Data to use" id="data-to-use">
      <p className="muted">
        Only the fields ticked here are stored and uploaded for this station.
        {!station.selected &&
          ' This station is not selected yet, so nothing is stored or uploaded until you select it.'}
      </p>
      {rows.length === 0 ? (
        <p className="muted">No fields have been reported by this station yet.</p>
      ) : (
        <ul className="field-list" aria-label="Fields to use">
          {rows.map((f) => {
            const meta = fieldMeta(f);
            const unit = unitLabel(meta.quantity, system);
            const v = latest[f];
            return (
              <li key={f} className="field-row">
                <input
                  id={`use-${f}`}
                  type="checkbox"
                  checked={checked.has(f)}
                  onChange={(e) => toggle(f, e.target.checked)}
                />
                <label htmlFor={`use-${f}`}>
                  {meta.label}
                  {unit && <span className="muted"> ({unit})</span>}
                </label>
                <span className="field-value">
                  {v ? (
                    formatField(f, v.value, system)
                  ) : (
                    <span className="muted">no value yet</span>
                  )}
                </span>
              </li>
            );
          })}
        </ul>
      )}
      <CheckboxField
        label="Include other decoder fields"
        checked={includeRaw}
        onChange={(e) => {
          setSaved(false);
          setIncludeRaw(e.target.checked);
        }}
        hint={
          station.raw_fields.length > 0
            ? `Sends the values this sensor reports that have no standard name (${station.raw_fields.join(', ')}) as extra data.`
            : 'Sends any values this sensor reports that have no standard name as extra data.'
        }
      />
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
        <Button variant="primary" onClick={save} busy={busy}>
          Save
        </Button>
        <Button onClick={resetToDefaults} disabled={busy || isDefault}>
          Reset to defaults
        </Button>
      </div>
    </Card>
  );
}
